import { describe, expect, it } from "vitest";
import { buildDeleteStatement, buildFilterByCellValueClause, quoteIdent, quoteTableRef } from "@/lib/tableSql";

describe("table SQL helpers", () => {
  it("escapes embedded quotes in identifiers", () => {
    expect(quoteIdent("name`with`backtick", "mysql")).toBe("`name``with``backtick`");
    expect(quoteIdent('name"with"quote', "postgresql")).toBe('"name""with""quote"');
    expect(quoteIdent('name"with"quote', "sqlite")).toBe('"name""with""quote"');
    expect(quoteIdent("plain", "mysql")).toBe("`plain`");
    expect(quoteIdent("plain", "postgresql")).toBe('"plain"');
    expect(quoteTableRef("main", "users", "sqlite")).toBe('"main"."users"');
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
