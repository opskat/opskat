import { describe, expect, it } from "vitest";
import {
  buildImportIntent,
  detectDelimiter,
  parseDelimitedText,
  parseImportSourceText,
  parseTabSeparatedRows,
} from "@/lib/tableImport";

describe("parseTabSeparatedRows", () => {
  it("parses a pasted block without treating the first row as a header", () => {
    expect(parseTabSeparatedRows("1\tAlice\n2\tBob")).toEqual([
      ["1", "Alice"],
      ["2", "Bob"],
    ]);
  });

  it("keeps a value containing a comma in one cell", () => {
    expect(parseTabSeparatedRows("1\tAlice, A.")).toEqual([["1", "Alice, A."]]);
  });

  it("keeps empty cells and drops a trailing blank line", () => {
    expect(parseTabSeparatedRows("1\t\t3\n")).toEqual([["1", "", "3"]]);
  });

  it("preserves all-empty rows inside a pasted block", () => {
    expect(parseTabSeparatedRows("1\tAlice\n\t\n3\tCarol")).toEqual([
      ["1", "Alice"],
      ["", ""],
      ["3", "Carol"],
    ]);
  });

  it("keeps quotes that occur inside an unquoted clipboard value", () => {
    expect(parseTabSeparatedRows('1\tO"Reilly')).toEqual([["1", 'O"Reilly']]);
  });

  it("keeps an unmatched leading quote in a clipboard value", () => {
    expect(parseTabSeparatedRows('1\t"draft')).toEqual([["1", '"draft']]);
  });

  it("still parses complete quoted clipboard fields", () => {
    expect(parseTabSeparatedRows('1\t"line\nbreak"\t"say ""hi"""')).toEqual([["1", "line\nbreak", 'say "hi"']]);
  });
});

describe("table import helpers", () => {
  it("keeps the existing qualifier handling for file imports", () => {
    expect(parseDelimitedText('name\nO"Reilly', ",")).toEqual({
      headers: ["name"],
      rows: [["OReilly"]],
    });
  });

  it("parses quoted CSV cells with commas, quotes, and embedded newlines", () => {
    expect(parseDelimitedText('id,name,note\n1,"Alice, A.","line\nbreak"\n2,"say ""hi""",ok', ",")).toEqual({
      headers: ["id", "name", "note"],
      rows: [
        ["1", "Alice, A.", "line\nbreak"],
        ["2", 'say "hi"', "ok"],
      ],
    });
  });

  it("parses TSV without treating commas as separators", () => {
    expect(parseDelimitedText("id\tname\tnote\n1\tAlice, A.\t中文", "\t")).toEqual({
      headers: ["id", "name", "note"],
      rows: [["1", "Alice, A.", "中文"]],
    });
  });

  it("detects TSV when tabs outnumber commas in the header", () => {
    expect(detectDelimiter("id\tname\tnote\n1\tAlice\tok")).toBe("\t");
    expect(detectDelimiter("id,name,note\n1,Alice,ok")).toBe(",");
  });

  it("parses JSON arrays of objects into headers and rows", () => {
    expect(
      parseImportSourceText({
        text: JSON.stringify([
          { id: 1, name: "Alice", active: true },
          { id: 2, name: "Bob", note: { tier: "gold" } },
        ]),
        format: "json",
      })
    ).toEqual({
      headers: ["id", "name", "active", "note"],
      rows: [
        ["1", "Alice", "true", ""],
        ["2", "Bob", "", '{"tier":"gold"}'],
      ],
    });
  });

  it("parses XML repeated elements into headers and rows", () => {
    expect(
      parseImportSourceText({
        text: "<users><user><id>1</id><name>Alice</name></user><user><id>2</id><name>Bob</name></user></users>",
        format: "xml",
      })
    ).toEqual({
      headers: ["id", "name"],
      rows: [
        ["1", "Alice"],
        ["2", "Bob"],
      ],
    });
  });

  it("defaults to auto record-delimiter detection so Windows CSVs do not leak \\r into cells", () => {
    expect(
      parseImportSourceText({
        text: "id,name\r\n1,Alice\r\n2,Bob\n",
        format: "csv",
        fieldDelimiter: ",",
      })
    ).toEqual({
      headers: ["id", "name"],
      rows: [
        ["1", "Alice"],
        ["2", "Bob"],
      ],
    });
  });

  it("respects an explicit record delimiter so opposite line endings stay inside cells", () => {
    expect(
      parseImportSourceText({
        text: "id,note\r1,line\nstill-one\r2,bob",
        format: "csv",
        fieldDelimiter: ",",
        recordDelimiter: "cr",
      })
    ).toEqual({
      headers: ["id", "note"],
      rows: [
        ["1", "line\nstill-one"],
        ["2", "bob"],
      ],
    });

    expect(
      parseImportSourceText({
        text: "id,note\r\n1,line\nstill-one\r\n",
        format: "csv",
        fieldDelimiter: ",",
        recordDelimiter: "crlf",
      })
    ).toEqual({
      headers: ["id", "note"],
      rows: [["1", "line\nstill-one"]],
    });
  });

  it("supports single-quote text qualifiers and disabling qualifiers entirely", () => {
    expect(
      parseImportSourceText({
        text: "id,name\n1,'Alice, A.'\n2,'say ''hi'''",
        format: "csv",
        fieldDelimiter: ",",
        textQualifier: "'",
      })
    ).toEqual({
      headers: ["id", "name"],
      rows: [
        ["1", "Alice, A."],
        ["2", "say 'hi'"],
      ],
    });

    expect(
      parseImportSourceText({
        text: 'id,name\n1,"Alice"',
        format: "csv",
        fieldDelimiter: ",",
        textQualifier: "none",
      })
    ).toEqual({
      headers: ["id", "name"],
      rows: [["1", '"Alice"']],
    });
  });
});

// 源文件里的值怎么转换留在前端（规格决定 5）：按驱动给出的列类别转换日期、小数点、二进制编码与空值，
// 结构化地交给驱动（文本、空值、二进制字节）；语句由驱动生成，见 internal/dbdriver/*/import_test.go。
describe("buildImportIntent", () => {
  const text = (value: string) => ({ kind: "text", text: value });
  const NULL = { kind: "null" };

  it("sends the mapped columns in source order, the match keys, the mode and the multi-row options", () => {
    expect(
      buildImportIntent({
        headers: ["name", "skip", "id"],
        rows: [["Alice", "x", "1"]],
        mapping: { name: "name", id: "id" },
        nullStrategy: "literal-null",
        mode: "append-update",
        keys: ["id"],
        advancedOptions: { extendedInsert: true, maxStatementSizeKb: 64 },
      })
    ).toEqual({
      mode: "append-update",
      columns: ["name", "id"],
      keys: ["id"],
      rows: [[text("Alice"), text("1")]],
      extendedInsert: true,
      maxStatementSizeKb: 64,
    });
  });

  it("keeps empty cells as empty strings unless the null strategy marks them NULL", () => {
    const parsed = parseDelimitedText("id,name,note\n1,,NULL\n2,Bob,", ",");
    const intent = (nullStrategy: "literal-null" | "empty-is-null", emptyStringAsNull = false) =>
      buildImportIntent({
        headers: parsed.headers,
        rows: parsed.rows,
        mapping: { id: "id", name: "name", note: "note" },
        nullStrategy,
        advancedOptions: { emptyStringAsNull },
      }).rows;

    expect(intent("literal-null")).toEqual([
      [text("1"), text(""), NULL],
      [text("2"), text("Bob"), text("")],
    ]);
    expect(intent("empty-is-null")).toEqual([
      [text("1"), NULL, text("NULL")],
      [text("2"), text("Bob"), NULL],
    ]);
    expect(intent("literal-null", true)).toEqual([
      [text("1"), NULL, text("NULL")],
      [text("2"), text("Bob"), NULL],
    ]);
  });

  it("normalizes decimal symbols only for columns the driver reports as numbers", () => {
    expect(
      buildImportIntent({
        headers: ["id", "score", "name"],
        rows: [["1,5", "1,5", "alice,bob"]],
        mapping: { id: "id", score: "score", name: "name" },
        nullStrategy: "literal-null",
        columnCategories: { id: "number", score: "number", name: "text" },
        conversionOptions: { decimalSymbol: "," },
      }).rows
    ).toEqual([[text("1.5"), text("1.5"), text("alice,bob")]]);
  });

  it("converts source dates and datetimes to canonical text for date and datetime columns", () => {
    expect(
      buildImportIntent({
        headers: ["id", "occurred_on", "occurred_at", "bad"],
        rows: [["1", "24/8/23", "15:30:38 24/Aug/2023", "not a date"]],
        mapping: { id: "id", occurred_on: "occurred_on", occurred_at: "occurred_at", bad: "bad" },
        nullStrategy: "literal-null",
        columnCategories: { id: "number", occurred_on: "date", occurred_at: "datetime", bad: "date" },
        conversionOptions: { dateOrder: "dmy", dateDelimiter: "/", dateTimeOrder: "time-date", timeDelimiter: ":" },
      }).rows
    ).toEqual([[text("1"), text("2023-08-24"), text("2023-08-24 15:30:38"), text("not a date")]]);
  });

  it("supports a separate year delimiter when enabled", () => {
    expect(
      buildImportIntent({
        headers: ["d"],
        rows: [["24/8-2023"]],
        mapping: { d: "d" },
        nullStrategy: "literal-null",
        columnCategories: { d: "date" },
        conversionOptions: { dateOrder: "dmy", dateDelimiter: "/", yearDelimiter: "-" },
      }).rows
    ).toEqual([[text("2023-08-24")]]);
  });

  it("decodes base64 and hex into binary bytes for binary columns, and keeps undecodable cells as text", () => {
    const rows = (binaryEncoding: "base64" | "hex", cells: string[]) =>
      buildImportIntent({
        headers: cells.map((_, i) => `c${i}`),
        rows: [cells],
        mapping: Object.fromEntries(cells.map((_, i) => [`c${i}`, `c${i}`])),
        nullStrategy: "literal-null",
        columnCategories: Object.fromEntries(cells.map((_, i) => [`c${i}`, "binary"])),
        conversionOptions: { binaryEncoding },
      }).rows[0];

    expect(rows("base64", ["SGVsbG8=", "", "not base64!"])).toEqual([
      { kind: "binary", hex: "48656c6c6f" },
      { kind: "binary", hex: "" },
      text("not base64!"),
    ]);
    expect(rows("hex", ["48656C6C6F", "abc", "xyz"])).toEqual([
      { kind: "binary", hex: "48656c6c6f" },
      { kind: "binary", hex: "0abc" },
      text("xyz"),
    ]);
  });

  it("sends columns the driver did not categorize as text", () => {
    expect(
      buildImportIntent({
        headers: ["d", "payload"],
        rows: [["24/8/23", "SGVsbG8="]],
        mapping: { d: "d", payload: "payload" },
        nullStrategy: "literal-null",
        conversionOptions: { dateOrder: "dmy", dateDelimiter: "/", binaryEncoding: "base64" },
      }).rows
    ).toEqual([[text("24/8/23"), text("SGVsbG8=")]]);
  });
});
