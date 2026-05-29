import { describe, expect, it } from "vitest";
import {
  buildDeleteStatement,
  buildFilterByCellValueClause,
  buildPagedSelect,
  buildSingleRowUpdate,
  buildStarterSelectSql,
  quoteIdent,
  quoteTableRef,
} from "@/lib/tableSql";

describe("table SQL helpers", () => {
  it("escapes embedded quotes in identifiers", () => {
    expect(quoteIdent("name`with`backtick", "mysql")).toBe("`name``with``backtick`");
    expect(quoteIdent('name"with"quote', "postgresql")).toBe('"name""with""quote"');
    expect(quoteIdent('name"with"quote', "sqlite")).toBe('"name""with""quote"');
    expect(quoteIdent("plain", "mysql")).toBe("`plain`");
    expect(quoteIdent("plain", "postgresql")).toBe('"plain"');
    expect(quoteTableRef("main", "users", "sqlite")).toBe('"main"."users"');
  });

  it("quotes MSSQL identifiers with brackets and ignores the database in table refs", () => {
    expect(quoteIdent("plain", "mssql")).toBe("[plain]");
    expect(quoteIdent("a]b", "mssql")).toBe("[a]]b]");
    // 不能拼成两段式 [appdb].[users]——MSSQL 会把它当成 schema.object
    expect(quoteTableRef("appdb", "users", "mssql")).toBe("[users]");
    expect(quoteTableRef("appdb", "dbo.users", "mssql")).toBe("[dbo].[users]");
  });

  it("builds paged SELECT per dialect (MSSQL OFFSET/FETCH, others LIMIT)", () => {
    // 其它 driver 行为不变
    expect(
      buildPagedSelect({ tableRef: "`t`", wherePart: "", orderByExpr: "", pageSize: 50, offset: 100, driver: "mysql" })
    ).toBe("SELECT * FROM `t` LIMIT 50 OFFSET 100");
    expect(
      buildPagedSelect({
        tableRef: '"main"."t"',
        wherePart: " WHERE \"a\" = '1'",
        orderByExpr: '"id" ASC',
        pageSize: 50,
        offset: 0,
        driver: "sqlite",
      })
    ).toBe('SELECT * FROM "main"."t" WHERE "a" = \'1\' ORDER BY "id" ASC LIMIT 50 OFFSET 0');
    // MSSQL：OFFSET/FETCH，需要 ORDER BY；无排序键时用 (SELECT NULL) 占位
    expect(
      buildPagedSelect({
        tableRef: "[dbo].[t]",
        wherePart: " WHERE [a] = '1'",
        orderByExpr: "[id] ASC",
        pageSize: 50,
        offset: 100,
        driver: "mssql",
      })
    ).toBe("SELECT * FROM [dbo].[t] WHERE [a] = '1' ORDER BY [id] ASC OFFSET 100 ROWS FETCH NEXT 50 ROWS ONLY");
    expect(
      buildPagedSelect({
        tableRef: "[dbo].[t]",
        wherePart: "",
        orderByExpr: "",
        pageSize: 50,
        offset: 0,
        driver: "mssql",
      })
    ).toBe("SELECT * FROM [dbo].[t] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 50 ROWS ONLY");
  });

  it("builds starter SELECT per dialect", () => {
    expect(buildStarterSelectSql("`appdb`.`users`", "mysql", 100)).toBe("SELECT * FROM `appdb`.`users` LIMIT 100");
    expect(buildStarterSelectSql("[dbo].[users]", "mssql", 100)).toBe("SELECT TOP 100 * FROM [dbo].[users]");
  });

  it("assembles single-row UPDATE per dialect (locks existing behavior + MSSQL TOP)", () => {
    const args = { tableRef: "[dbo].[users]", setSql: "[name] = 'x'", whereSql: "[id] = '7'" };
    // MSSQL：无 LIMIT，用 UPDATE TOP (1)
    expect(buildSingleRowUpdate({ ...args, hasPrimaryKey: true, driver: "mssql" })).toBe(
      "UPDATE TOP (1) [dbo].[users] SET [name] = 'x' WHERE [id] = '7';"
    );
    expect(buildSingleRowUpdate({ ...args, hasPrimaryKey: false, driver: "mssql" })).toBe(
      "UPDATE TOP (1) [dbo].[users] SET [name] = 'x' WHERE [id] = '7';"
    );
    // 其它 driver 行为不变
    const my = { tableRef: "`db`.`t`", setSql: "`n` = 'x'", whereSql: "`id` = '7'" };
    expect(buildSingleRowUpdate({ ...my, hasPrimaryKey: true, driver: "mysql" })).toBe(
      "UPDATE `db`.`t` SET `n` = 'x' WHERE `id` = '7' LIMIT 1;"
    );
    const pg = { tableRef: '"t"', setSql: "\"n\" = 'x'", whereSql: "\"id\" = '7'" };
    expect(buildSingleRowUpdate({ ...pg, hasPrimaryKey: true, driver: "postgresql" })).toBe(
      'UPDATE "t" SET "n" = \'x\' WHERE "id" = \'7\';'
    );
    expect(buildSingleRowUpdate({ ...pg, hasPrimaryKey: false, driver: "postgresql" })).toBe(
      'UPDATE "t" SET "n" = \'x\' WHERE ctid = (SELECT ctid FROM "t" WHERE "id" = \'7\' LIMIT 1);'
    );
    const lite = { tableRef: '"main"."t"', setSql: "\"n\" = 'x'", whereSql: "\"id\" = '7'" };
    expect(buildSingleRowUpdate({ ...lite, hasPrimaryKey: false, driver: "sqlite" })).toBe(
      'UPDATE "main"."t" SET "n" = \'x\' WHERE rowid = (SELECT rowid FROM "main"."t" WHERE "id" = \'7\' LIMIT 1);'
    );
  });

  it("builds MSSQL DELETE with TOP (1) instead of LIMIT", () => {
    expect(
      buildDeleteStatement({
        database: "appdb",
        table: "users",
        columns: ["id", "name"],
        row: { id: 7, name: "alice" },
        primaryKeys: ["id"],
        driver: "mssql",
      }).sql
    ).toBe("DELETE TOP (1) FROM [users] WHERE [id] = '7';");

    expect(
      buildDeleteStatement({
        database: "appdb",
        table: "users",
        columns: ["id", "name"],
        row: { id: 7, name: "alice" },
        primaryKeys: [],
        driver: "mssql",
      }).sql
    ).toBe("DELETE TOP (1) FROM [users] WHERE [id] = '7' AND [name] = 'alice';");
  });

  it("builds filter clauses for NULL and quoted values", () => {
    expect(buildFilterByCellValueClause("deleted_at", null)).toBe("`deleted_at` IS NULL");
    expect(buildFilterByCellValueClause("name", "O'Reilly")).toBe("`name` = 'O''Reilly'");
    expect(buildFilterByCellValueClause("age", 42)).toBe("`age` = '42'");
    expect(buildFilterByCellValueClause("name", "bob", "mysql", "!=")).toBe("`name` <> 'bob'");
    expect(buildFilterByCellValueClause("name", "bob", "mysql", "like")).toBe("`name` LIKE '%bob%'");
    expect(buildFilterByCellValueClause("name", "bob", "mysql", "not_like")).toBe("`name` NOT LIKE '%bob%'");
    expect(buildFilterByCellValueClause("name", "Al", "mysql", "begins_with")).toBe("`name` LIKE 'Al%'");
    expect(buildFilterByCellValueClause("name", "ce", "mysql", "not_ends_with")).toBe("`name` NOT LIKE '%ce'");
    expect(buildFilterByCellValueClause("deleted_at", "ignored", "mysql", "is_null")).toBe("`deleted_at` IS NULL");
    expect(buildFilterByCellValueClause("deleted_at", "ignored", "mysql", "is_not_null")).toBe(
      "`deleted_at` IS NOT NULL"
    );
  });

  it("builds DELETE SQL using primary keys when available", () => {
    const result = buildDeleteStatement({
      database: "appdb",
      table: "users",
      columns: ["id", "name", "deleted_at"],
      row: { id: 7, name: "alice", deleted_at: null },
      primaryKeys: ["id"],
      driver: "mysql",
    });

    expect(result.sql).toBe("DELETE FROM `appdb`.`users` WHERE `id` = '7' LIMIT 1;");
    expect(result.usesPrimaryKey).toBe(true);
  });

  it("escapes postgresql table names in DELETE SQL", () => {
    const result = buildDeleteStatement({
      database: "appdb",
      table: 'audit"logs',
      columns: ['id"part', "name"],
      row: { 'id"part': 7, name: "alice" },
      primaryKeys: ['id"part'],
      driver: "postgresql",
    });

    expect(result.sql).toBe(`DELETE FROM "audit""logs" WHERE "id""part" = '7';`);
    expect(result.usesPrimaryKey).toBe(true);
  });

  it("falls back to all columns when deleting without a primary key", () => {
    const result = buildDeleteStatement({
      database: "appdb",
      table: "users",
      columns: ["id", "name", "deleted_at"],
      row: { id: 7, name: "O'Reilly", deleted_at: null },
      primaryKeys: [],
      driver: "mysql",
    });

    expect(result.sql).toBe(
      "DELETE FROM `appdb`.`users` WHERE `id` = '7' AND `name` = 'O''Reilly' AND `deleted_at` IS NULL LIMIT 1;"
    );
    expect(result.usesPrimaryKey).toBe(false);
  });

  it("builds SQLite DELETE SQL without unsupported LIMIT syntax", () => {
    expect(
      buildDeleteStatement({
        database: "main",
        table: "users",
        columns: ["id", "name"],
        row: { id: 7, name: "alice" },
        primaryKeys: ["id"],
        driver: "sqlite",
      }).sql
    ).toBe(`DELETE FROM "main"."users" WHERE "id" = '7';`);

    expect(
      buildDeleteStatement({
        database: "main",
        table: "users",
        columns: ["id", "name"],
        row: { id: 7, name: "alice" },
        primaryKeys: [],
        driver: "sqlite",
      }).sql
    ).toBe(
      `DELETE FROM "main"."users" WHERE rowid = (SELECT rowid FROM "main"."users" WHERE "id" = '7' AND "name" = 'alice' LIMIT 1);`
    );
  });
});
