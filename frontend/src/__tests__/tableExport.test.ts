import { describe, expect, it } from "vitest";
import { buildTableExportContent, toCsv, toTsv, toTsvData, toTsvFields } from "@/lib/tableExport";

const columns = ["id", "name", "note", "missing"];
const rows = [
  { id: 1, name: "Alice", note: "hello, world", missing: null },
  { id: 2, name: "O'Reilly", note: "line\nbreak", missing: "" },
  { id: 3, name: "中文", note: 'say "hi"', missing: undefined },
];

describe("table export helpers", () => {
  it("exports CSV with headers, quotes, newlines, Chinese text, and empty NULL cells", () => {
    expect(toCsv(columns, rows)).toBe(
      ["id,name,note,missing", '1,Alice,"hello, world",', '2,O\'Reilly,"line\nbreak",', '3,中文,"say ""hi""",'].join(
        "\n"
      )
    );
  });

  it("exports TSV with tab/newline escaping and empty NULL cells", () => {
    expect(
      toTsv(
        ["id", "note"],
        [
          { id: 1, note: "tab\tvalue" },
          { id: 2, note: "line\nbreak" },
        ]
      )
    ).toBe(["id\tnote", '1\t"tab\tvalue"', '2\t"line\nbreak"'].join("\n"));
  });

  it("exports Copy As TSV variants", () => {
    expect(toTsvData(["id", "name"], [rows[0]])).toBe("1\tAlice");
    expect(toTsvFields(["id", "name"])).toBe("id\tname");
    expect(toTsv(["id", "name"], [rows[0]])).toBe("id\tname\n1\tAlice");
  });

  it("can omit column titles for delimited exports", () => {
    expect(toCsv(["id", "name"], [rows[0]], { includeHeaders: false })).toBe("1,Alice");
    expect(toTsv(["id", "name"], [rows[0]], { includeHeaders: false })).toBe("1\tAlice");
  });

  it("exports delimited text with custom delimiters and text qualifiers", () => {
    expect(
      buildTableExportContent({
        format: "csv",
        columns: ["id", "name", "note"],
        rows: [{ id: 1, name: "Alice", note: "left;right" }],
        includeHeaders: true,
        options: {
          fieldDelimiter: "semicolon",
          recordDelimiter: "crlf",
          textQualifier: "single",
        },
      })
    ).toBe("id;name;note\r\n1;Alice;'left;right'");
  });

  it("applies data formatting options for zero, date, decimal, and binary values", () => {
    expect(
      buildTableExportContent({
        format: "csv",
        columns: ["amount", "created_at", "ratio", "payload"],
        rows: [
          {
            amount: 0,
            created_at: "2026-04-03 05:06:07",
            ratio: 12.5,
            payload: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
          },
        ],
        includeHeaders: false,
        options: {
          blankIfZero: true,
          dateOrder: "dmy",
          dateDelimiter: "/",
          timeDelimiter: ".",
          decimalSymbol: ",",
          binaryDataEncoding: "hex",
        },
      })
    ).toBe(',03/04/2026 05.06.07,"12,5",deadbeef');
  });
});
