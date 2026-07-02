import { describe, it, expect } from "vitest";
import { parseTableRefs, dotPrefixAt, resolveTableName, resolveScopeTables, buildSqlCompletions } from "../sql-context";

describe("parseTableRefs", () => {
  it("extracts a single unaliased table from FROM", () => {
    expect(parseTableRefs("SELECT * FROM users")).toEqual([{ table: "users" }]);
  });

  it("extracts alias written without AS", () => {
    expect(parseTableRefs("SELECT * FROM users u")).toEqual([{ table: "users", alias: "u" }]);
  });

  it("extracts alias written with AS (case-insensitive)", () => {
    expect(parseTableRefs("select * from users as u")).toEqual([{ table: "users", alias: "u" }]);
  });

  it("extracts tables from FROM + JOIN with aliases", () => {
    const sql = "SELECT * FROM users u JOIN orders o ON u.id = o.user_id";
    expect(parseTableRefs(sql)).toEqual([
      { table: "users", alias: "u" },
      { table: "orders", alias: "o" },
    ]);
  });

  it("extracts comma-separated tables in FROM", () => {
    expect(parseTableRefs("SELECT * FROM a, b")).toEqual([{ table: "a" }, { table: "b" }]);
  });

  it("keeps schema-qualified table names", () => {
    expect(parseTableRefs("SELECT * FROM public.users u")).toEqual([{ table: "public.users", alias: "u" }]);
  });

  it("strips surrounding quotes/backticks/brackets", () => {
    expect(parseTableRefs("SELECT * FROM `users`")).toEqual([{ table: "users" }]);
    expect(parseTableRefs('SELECT * FROM "users"')).toEqual([{ table: "users" }]);
    expect(parseTableRefs("SELECT * FROM [dbo].[Orders]")).toEqual([{ table: "dbo.Orders" }]);
  });

  it("does not treat a following clause keyword as an alias", () => {
    expect(parseTableRefs("SELECT * FROM users WHERE id = 1")).toEqual([{ table: "users" }]);
  });

  it("does not treat a JOIN modifier keyword as an alias", () => {
    expect(parseTableRefs("SELECT * FROM a LEFT JOIN b ON a.id = b.a_id")).toEqual([{ table: "a" }, { table: "b" }]);
  });

  it("returns empty when there is no FROM/JOIN", () => {
    expect(parseTableRefs("SELECT 1")).toEqual([]);
  });
});

describe("dotPrefixAt", () => {
  it("returns the identifier when cursor is right after a dot", () => {
    expect(dotPrefixAt("SELECT u.")).toBe("u");
  });

  it("returns the identifier when a partial column is typed after the dot", () => {
    expect(dotPrefixAt("SELECT u.na")).toBe("u");
  });

  it("returns the table name for a bare-table dot", () => {
    expect(dotPrefixAt("SELECT users.")).toBe("users");
  });

  it("returns the last segment for a schema-qualified dot", () => {
    expect(dotPrefixAt("SELECT * FROM public.users.")).toBe("users");
  });

  it("returns null when there is no trailing dot expression", () => {
    expect(dotPrefixAt("SELECT * FROM u")).toBeNull();
    expect(dotPrefixAt("SELECT 1")).toBeNull();
  });
});

describe("resolveTableName", () => {
  const pgTables = ["public.users", "public.orders"];

  it("matches an exact table name", () => {
    expect(resolveTableName("users", ["users", "orders"])).toBe("users");
  });

  it("matches case-insensitively but returns the canonical casing", () => {
    expect(resolveTableName("Users", ["users"])).toBe("users");
  });

  it("resolves an unqualified name to a schema-qualified table by last segment", () => {
    expect(resolveTableName("users", pgTables)).toBe("public.users");
  });

  it("matches a fully schema-qualified reference", () => {
    expect(resolveTableName("public.users", pgTables)).toBe("public.users");
  });

  it("strips quotes before matching", () => {
    expect(resolveTableName("`users`", ["users"])).toBe("users");
  });

  it("returns null for an unknown table", () => {
    expect(resolveTableName("missing", ["users"])).toBeNull();
  });
});

describe("resolveScopeTables", () => {
  it("returns the canonical tables referenced in FROM/JOIN", () => {
    const sql = "SELECT * FROM users u JOIN orders o ON u.id = o.user_id";
    expect(resolveScopeTables(sql, ["users", "orders"]).sort()).toEqual(["orders", "users"]);
  });

  it("dedupes a table referenced more than once", () => {
    expect(resolveScopeTables("SELECT * FROM users u, users u2", ["users"])).toEqual(["users"]);
  });

  it("resolves unqualified names to schema-qualified tables", () => {
    expect(resolveScopeTables("SELECT * FROM users", ["public.users"])).toEqual(["public.users"]);
  });

  it("drops references that match no known table", () => {
    expect(resolveScopeTables("SELECT * FROM missing", ["users"])).toEqual([]);
  });
});

describe("buildSqlCompletions", () => {
  const columnsByTable = {
    users: [
      { name: "id", type: "int", nullable: false, primaryKey: true },
      { name: "email", type: "varchar(255)", nullable: true, primaryKey: false },
    ],
    orders: [{ name: "user_id", type: "int", nullable: false, primaryKey: false }],
  };

  it("suggests only that table's columns after `alias.`", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT u.",
      fullSql: "SELECT u. FROM users u",
      tables: ["users"],
      db: "app",
      columnsByTable,
    });
    expect(items.every((i) => i.kind === "column")).toBe(true);
    expect(items.map((i) => i.label).sort()).toEqual(["email", "id"]);
  });

  it("marks primary-key columns and shows the type + owner in detail", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT u.",
      fullSql: "SELECT u. FROM users u",
      tables: ["users"],
      db: "app",
      columnsByTable,
    });
    const id = items.find((i) => i.label === "id")!;
    expect(id.detail).toContain("int");
    expect(id.detail).toContain("u");
    expect(id.detail).toContain("🔑");
    const email = items.find((i) => i.label === "email")!;
    expect(email.detail).not.toContain("🔑");
  });

  it("resolves a bare `table.` prefix to that table's columns", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT users.",
      fullSql: "SELECT users. FROM users",
      tables: ["users"],
      db: "app",
      columnsByTable,
    });
    expect(items.map((i) => i.label).sort()).toEqual(["email", "id"]);
  });

  it("returns nothing for a dot on an unknown alias/table", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT x.",
      fullSql: "SELECT x. FROM users u",
      tables: ["users"],
      db: "app",
      columnsByTable,
    });
    expect(items).toEqual([]);
  });

  it("bare word: suggests in-scope columns and tables, columns ranked first", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT ",
      fullSql: "SELECT  FROM users u JOIN orders o ON u.id = o.user_id",
      tables: ["users", "orders"],
      db: "app",
      columnsByTable,
    });
    const cols = items.filter((i) => i.kind === "column");
    const tbls = items.filter((i) => i.kind === "table");
    expect(cols.map((i) => i.label)).toEqual(expect.arrayContaining(["id", "email", "user_id"]));
    expect(tbls.map((i) => i.label)).toEqual(expect.arrayContaining(["users", "orders"]));
    // every column sorts before every table
    const maxColSort = cols
      .map((i) => i.sortText)
      .sort()
      .at(-1)!;
    const minTableSort = tbls.map((i) => i.sortText).sort()[0];
    expect(maxColSort < minTableSort).toBe(true);
  });

  it("bare word with no FROM: suggests only tables (no columns)", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT * FROM ",
      fullSql: "SELECT * FROM ",
      tables: ["users", "orders"],
      db: "app",
      columnsByTable,
    });
    expect(items.every((i) => i.kind === "table")).toBe(true);
    expect(items.map((i) => i.label).sort()).toEqual(["orders", "users"]);
  });

  it("table items carry a `table · <db>` detail", () => {
    const items = buildSqlCompletions({
      textBeforeCursor: "SELECT * FROM ",
      fullSql: "SELECT * FROM ",
      tables: ["users"],
      db: "app",
      columnsByTable,
    });
    expect(items[0].detail).toContain("app");
  });
});
