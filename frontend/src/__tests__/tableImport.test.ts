import { describe, expect, it } from "vitest";
import {
  buildImportInsertSql,
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

  it("keeps empty cells as empty strings unless the null strategy marks them NULL", () => {
    const parsed = parseDelimitedText("id,name,note\n1,,NULL\n2,Bob,", ",");

    expect(
      buildImportInsertSql({
        tableName: "appdb.users",
        headers: parsed.headers,
        rows: parsed.rows,
        mapping: { id: "id", name: "name", note: "note" },
        nullStrategy: "literal-null",
        driver: "mysql",
      })
    ).toEqual([
      "INSERT INTO `appdb`.`users` (`id`, `name`, `note`) VALUES ('1', '', NULL);",
      "INSERT INTO `appdb`.`users` (`id`, `name`, `note`) VALUES ('2', 'Bob', '');",
    ]);
  });

  it("can map empty cells to SQL NULL", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.users",
        headers: ["id", "name"],
        rows: [["1", ""]],
        mapping: { id: "id", name: "name" },
        nullStrategy: "empty-is-null",
        driver: "mysql",
      })
    ).toEqual(["INSERT INTO `appdb`.`users` (`id`, `name`) VALUES ('1', NULL);"]);
  });

  it("builds SQL for primary-key based import modes", () => {
    const base = {
      tableName: "appdb.users",
      headers: ["id", "name", "email"],
      rows: [["1", "Alice", "alice@example.test"]],
      mapping: { id: "id", name: "name", email: "email" },
      nullStrategy: "literal-null" as const,
      primaryKeys: ["id"],
      driver: "mysql",
    };

    expect(buildImportInsertSql({ ...base, mode: "update" })).toEqual([
      "UPDATE `appdb`.`users` SET `name` = 'Alice', `email` = 'alice@example.test' WHERE `id` = '1';",
    ]);
    expect(buildImportInsertSql({ ...base, mode: "append-update" })).toEqual([
      "INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test') ON DUPLICATE KEY UPDATE `name` = VALUES(`name`), `email` = VALUES(`email`);",
    ]);
    expect(buildImportInsertSql({ ...base, mode: "append-skip" })).toEqual([
      "INSERT IGNORE INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');",
    ]);
    expect(buildImportInsertSql({ ...base, mode: "delete" })).toEqual([
      "DELETE FROM `appdb`.`users` WHERE `id` = '1';",
    ]);
    expect(buildImportInsertSql({ ...base, mode: "copy" })).toEqual([
      "DELETE FROM `appdb`.`users`;",
      "INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');",
    ]);
  });

  it("builds SQLite upsert and skip SQL without MySQL-only syntax", () => {
    const base = {
      tableName: "main.users",
      headers: ["id", "name"],
      rows: [["1", "Alice"]],
      mapping: { id: "id", name: "name" },
      nullStrategy: "literal-null" as const,
      primaryKeys: ["id"],
      driver: "sqlite",
    };

    expect(buildImportInsertSql({ ...base, mode: "append-update" })).toEqual([
      'INSERT INTO "main"."users" ("id", "name") VALUES (\'1\', \'Alice\') ON CONFLICT ("id") DO UPDATE SET "name" = excluded."name";',
    ]);
    expect(buildImportInsertSql({ ...base, mode: "append-skip" })).toEqual([
      'INSERT OR IGNORE INTO "main"."users" ("id", "name") VALUES (\'1\', \'Alice\');',
    ]);
  });

  describe("MSSQL import", () => {
    // 期望的包装：同一批里由数据库判断映射列是否含自增列，含则临时 IDENTITY_INSERT ON，
    // 语句失败时也要 OFF 并原样 THROW；不含则不触碰 IDENTITY_INSERT。
    const wrapped = (core: string, columns: string[], table = "[dbo].[users]") => {
      const hasIdentity =
        "EXISTS (SELECT 1 FROM sys.identity_columns " +
        `WHERE object_id = OBJECT_ID(N'${table}') AND name IN (${columns.map((c) => `N'${c}'`).join(", ")}))`;
      return (
        `IF ${hasIdentity} SET IDENTITY_INSERT ${table} ON; ` +
        `BEGIN TRY ${core} END TRY ` +
        `BEGIN CATCH IF ${hasIdentity} SET IDENTITY_INSERT ${table} OFF; THROW; END CATCH; ` +
        `IF ${hasIdentity} SET IDENTITY_INSERT ${table} OFF;`
      );
    };

    const base = {
      tableName: "dbo.users",
      headers: ["id", "name", "email"],
      rows: [["1", "Alice", "alice@example.test"]],
      mapping: { id: "id", name: "name", email: "email" },
      nullStrategy: "literal-null" as const,
      primaryKeys: ["id"],
      driver: "mssql",
    };
    const cols = ["id", "name", "email"];

    it("wraps MERGE for append-update and append-skip so identity columns can be written", () => {
      expect(buildImportInsertSql({ ...base, mode: "append-update" })).toEqual([
        wrapped(
          "MERGE [dbo].[users] AS t USING (SELECT '1' AS [id], 'Alice' AS [name], 'alice@example.test' AS [email]) AS s " +
            "ON (t.[id] = s.[id]) " +
            "WHEN MATCHED THEN UPDATE SET t.[name] = s.[name], t.[email] = s.[email] " +
            "WHEN NOT MATCHED THEN INSERT ([id], [name], [email]) VALUES (s.[id], s.[name], s.[email]);",
          cols
        ),
      ]);

      expect(buildImportInsertSql({ ...base, mode: "append-skip" })).toEqual([
        wrapped(
          "MERGE [dbo].[users] AS t USING (SELECT '1' AS [id], 'Alice' AS [name], 'alice@example.test' AS [email]) AS s " +
            "ON (t.[id] = s.[id]) " +
            "WHEN NOT MATCHED THEN INSERT ([id], [name], [email]) VALUES (s.[id], s.[name], s.[email]);",
          cols
        ),
      ]);
    });

    it("wraps plain INSERT for append, and keeps copy's DELETE as its own unwrapped element", () => {
      const insert = "INSERT INTO [dbo].[users] ([id], [name], [email]) VALUES ('1', 'Alice', 'alice@example.test');";
      expect(buildImportInsertSql({ ...base, mode: "append" })).toEqual([wrapped(insert, cols)]);
      expect(buildImportInsertSql({ ...base, mode: "copy" })).toEqual([
        "DELETE FROM [dbo].[users];",
        wrapped(insert, cols),
      ]);
    });

    it("wraps every extended-insert chunk", () => {
      const out = buildImportInsertSql({
        ...base,
        rows: [
          ["1", "A", "a@x"],
          ["2", "B", "b@x"],
        ],
        mode: "append",
        advancedOptions: { extendedInsert: true, maxStatementSizeKb: 1 },
      });
      expect(out).toEqual([
        wrapped("INSERT INTO [dbo].[users] ([id], [name], [email]) VALUES ('1', 'A', 'a@x'), ('2', 'B', 'b@x');", cols),
      ]);
    });

    it("adds no row count of its own around the import statement", () => {
      // 导入摘要的「新增 / 更新」取自每个元素的受影响行数（go-mssqldb 把一批里所有带计数的
      // DONE 相加）。SQL Server 把变量赋值（DECLARE @x = … / SET @x = … / SELECT @x = …）
      // 计为 1 行，真库上每条包装后的语句会多算 1 行，没有自增列的表也一样。
      for (const mode of ["append", "append-update", "append-skip", "copy"] as const) {
        for (const statement of buildImportInsertSql({ ...base, mode })) {
          expect(statement).not.toMatch(/\bDECLARE\b|\b(SET|SELECT)\s+@/i);
        }
      }
    });

    it("escapes quotes in the object name and column names inside N'' literals", () => {
      const [sql] = buildImportInsertSql({
        tableName: "dbo.o'brien",
        headers: ["i'd"],
        rows: [["1"]],
        mapping: { "i'd": "i'd" },
        nullStrategy: "literal-null",
        mode: "append",
        driver: "mssql",
      });
      expect(sql).toContain("OBJECT_ID(N'[dbo].[o''brien]')");
      expect(sql).toContain("name IN (N'i''d')");
    });

    it("leaves update and delete untouched (they do not insert)", () => {
      expect(buildImportInsertSql({ ...base, mode: "update" })).toEqual([
        "UPDATE [dbo].[users] SET [name] = 'Alice', [email] = 'alice@example.test' WHERE [id] = '1';",
      ]);
      expect(buildImportInsertSql({ ...base, mode: "delete" })).toEqual([
        "DELETE FROM [dbo].[users] WHERE [id] = '1';",
      ]);
    });

    it("does not leak IDENTITY_INSERT handling into other drivers", () => {
      for (const driver of ["mysql", "postgresql", "sqlite"]) {
        for (const mode of ["append", "append-update", "append-skip", "copy"] as const) {
          const out = buildImportInsertSql({ ...base, mode, driver });
          expect(out.join("\n")).not.toMatch(/IDENTITY_INSERT|sys\.identity_columns/);
        }
      }
    });
  });

  it("keeps foreign-key toggling out of generated row statements", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.users",
        headers: ["id"],
        rows: [["1"]],
        mapping: { id: "id" },
        nullStrategy: "literal-null",
        mode: "copy",
        advancedOptions: { ignoreForeignKeyConstraint: true },
        driver: "mysql",
      })
    ).toEqual(["DELETE FROM `appdb`.`users`;", "INSERT INTO `appdb`.`users` (`id`) VALUES ('1');"]);
  });

  it("uses ON CONFLICT DO NOTHING for postgresql append-update with no value columns", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.users",
        headers: ["id"],
        rows: [["1"]],
        mapping: { id: "id" },
        nullStrategy: "literal-null",
        primaryKeys: ["id"],
        mode: "append-update",
        driver: "postgresql",
      })
    ).toEqual([`INSERT INTO "appdb"."users" ("id") VALUES ('1') ON CONFLICT ("id") DO NOTHING;`]);

    expect(
      buildImportInsertSql({
        tableName: "appdb.users",
        headers: ["id"],
        rows: [["1"]],
        mapping: { id: "id" },
        nullStrategy: "literal-null",
        primaryKeys: ["id"],
        mode: "append-update",
        driver: "mysql",
      })
    ).toEqual(["INSERT IGNORE INTO `appdb`.`users` (`id`) VALUES ('1');"]);
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

  it("normalizes decimal symbols for numeric target columns", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.metrics",
        headers: ["id", "score", "name"],
        rows: [["1", "1,5", "alice,bob"]],
        mapping: { id: "id", score: "score", name: "name" },
        nullStrategy: "literal-null",
        driver: "mysql",
        columnTypes: { id: "int", score: "decimal(10,2)", name: "varchar(64)" },
        conversionOptions: { decimalSymbol: "," },
      })
    ).toEqual(["INSERT INTO `appdb`.`metrics` (`id`, `score`, `name`) VALUES ('1', '1.5', 'alice,bob');"]);
  });

  it("converts source dates and datetimes to canonical SQL literals", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.events",
        headers: ["id", "occurred_on", "occurred_at"],
        rows: [["1", "24/8/23", "15:30:38 24/Aug/2023"]],
        mapping: { id: "id", occurred_on: "occurred_on", occurred_at: "occurred_at" },
        nullStrategy: "literal-null",
        driver: "mysql",
        columnTypes: { id: "int", occurred_on: "date", occurred_at: "datetime" },
        conversionOptions: {
          dateOrder: "dmy",
          dateDelimiter: "/",
          dateTimeOrder: "time-date",
          timeDelimiter: ":",
        },
      })
    ).toEqual([
      "INSERT INTO `appdb`.`events` (`id`, `occurred_on`, `occurred_at`) VALUES ('1', '2023-08-24', '2023-08-24 15:30:38');",
    ]);
  });

  it("supports a separate year delimiter when enabled", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.events",
        headers: ["d"],
        rows: [["24/8-2023"]],
        mapping: { d: "d" },
        nullStrategy: "literal-null",
        driver: "mysql",
        columnTypes: { d: "date" },
        conversionOptions: {
          dateOrder: "dmy",
          dateDelimiter: "/",
          yearDelimiter: "-",
        },
      })
    ).toEqual(["INSERT INTO `appdb`.`events` (`d`) VALUES ('2023-08-24');"]);
  });

  it("emits driver-aware binary literals from base64 and hex encodings", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.files",
        headers: ["id", "payload"],
        rows: [["1", "SGVsbG8="]],
        mapping: { id: "id", payload: "payload" },
        nullStrategy: "literal-null",
        driver: "mysql",
        columnTypes: { id: "int", payload: "blob" },
        conversionOptions: { binaryEncoding: "base64" },
      })
    ).toEqual(["INSERT INTO `appdb`.`files` (`id`, `payload`) VALUES ('1', X'48656c6c6f');"]);

    expect(
      buildImportInsertSql({
        tableName: "appdb.files",
        headers: ["id", "payload"],
        rows: [["1", "48656C6C6F"]],
        mapping: { id: "id", payload: "payload" },
        nullStrategy: "literal-null",
        driver: "postgresql",
        columnTypes: { id: "int", payload: "bytea" },
        conversionOptions: { binaryEncoding: "hex" },
      })
    ).toEqual([`INSERT INTO "appdb"."files" ("id", "payload") VALUES ('1', '\\x48656c6c6f'::bytea);`]);
  });

  it("uses converted literals in WHERE clauses for primary-key based modes", () => {
    expect(
      buildImportInsertSql({
        tableName: "appdb.events",
        headers: ["id", "name"],
        rows: [["1,5", "Alice"]],
        mapping: { id: "id", name: "name" },
        nullStrategy: "literal-null",
        primaryKeys: ["id"],
        mode: "update",
        driver: "mysql",
        columnTypes: { id: "decimal(10,2)", name: "varchar(64)" },
        conversionOptions: { decimalSymbol: "," },
      })
    ).toEqual(["UPDATE `appdb`.`events` SET `name` = 'Alice' WHERE `id` = '1.5';"]);
  });
});
