import { describe, expect, it } from "vitest";
import {
  buildTableExportContent,
  buildTableExportSelectSql,
  toCsv,
  toInsertSql,
  toTsv,
  toTsvData,
  toTsvFields,
  toUpdateSql,
} from "@/lib/tableExport";

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

  it("exports INSERT SQL with identifier quoting, value quoting, and SQL NULL", () => {
    expect(toInsertSql("appdb.users", ["id", "name", "missing"], rows, "mysql")).toBe(
      [
        "INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('1', 'Alice', NULL);",
        "INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('2', 'O''Reilly', '');",
        "INSERT INTO `appdb`.`users` (`id`, `name`, `missing`) VALUES ('3', '中文', NULL);",
      ].join("\n")
    );
  });

  it("exports Copy As TSV variants", () => {
    expect(toTsvData(["id", "name"], [rows[0]])).toBe("1\tAlice");
    expect(toTsvFields(["id", "name"])).toBe("id\tname");
    expect(toTsv(["id", "name"], [rows[0]])).toBe("id\tname\n1\tAlice");
  });

  it("exports UPDATE SQL using primary keys when available", () => {
    expect(toUpdateSql("appdb.users", ["id", "name"], rows[1], ["id"], "mysql")).toBe(
      "UPDATE `appdb`.`users` SET `id` = '2', `name` = 'O''Reilly' WHERE `id` = '2' LIMIT 1;"
    );
    expect(toUpdateSql("main.users", ["id", "name"], rows[1], ["id"], "sqlite")).toBe(
      'UPDATE "main"."users" SET "id" = \'2\', "name" = \'O\'\'Reilly\' WHERE "id" = \'2\';'
    );
    expect(toUpdateSql("dbo.users", ["id", "name"], rows[1], ["id"], "mssql")).toBe(
      "UPDATE TOP (1) [dbo].[users] SET [id] = '2', [name] = 'O''Reilly' WHERE [id] = '2';"
    );
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
        tableName: "appdb.users",
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
        tableName: "appdb.users",
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

  it("builds all-data export SQL without pagination while preserving filters and sorting", () => {
    expect(
      buildTableExportSelectSql({
        database: "appdb",
        table: "users",
        driver: "mysql",
        scope: "all",
        whereClause: "amount > 10",
        orderByClause: "created_at DESC",
        sortColumn: null,
        sortDir: null,
        page: 2,
        pageSize: 100,
      })
    ).toBe("SELECT * FROM `appdb`.`users` WHERE amount > 10 ORDER BY created_at DESC");
  });

  it("builds current-page export SQL with pagination and header-click sort precedence", () => {
    expect(
      buildTableExportSelectSql({
        database: "appdb",
        table: "users",
        driver: "mysql",
        scope: "page",
        whereClause: "",
        orderByClause: "created_at DESC",
        sortColumn: "name",
        sortDir: "asc",
        page: 1,
        pageSize: 50,
      })
    ).toBe("SELECT * FROM `appdb`.`users` ORDER BY `name` ASC LIMIT 50 OFFSET 50");
  });

  it("builds MSSQL current-page export SQL with OFFSET/FETCH", () => {
    expect(
      buildTableExportSelectSql({
        database: "appdb",
        table: "dbo.users",
        driver: "mssql",
        scope: "page",
        whereClause: "[active] = '1'",
        orderByClause: "",
        sortColumn: "name",
        sortDir: "desc",
        page: 2,
        pageSize: 100,
      })
    ).toBe(
      "SELECT * FROM [dbo].[users] WHERE [active] = '1' ORDER BY [name] DESC OFFSET 200 ROWS FETCH NEXT 100 ROWS ONLY"
    );
  });

  it("builds MSSQL current-page export SQL with a fallback ORDER BY", () => {
    expect(
      buildTableExportSelectSql({
        database: "appdb",
        table: "dbo.users",
        driver: "mssql",
        scope: "page",
        whereClause: "",
        orderByClause: "",
        sortColumn: null,
        sortDir: null,
        page: 0,
        pageSize: 100,
      })
    ).toBe("SELECT * FROM [dbo].[users] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 100 ROWS ONLY");
  });
});
