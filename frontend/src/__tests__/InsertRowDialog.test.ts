import { describe, expect, it } from "vitest";
import { buildInsertSql } from "../components/query/InsertRowDialog";

describe("buildInsertSql", () => {
  it("builds mysql INSERT with db-qualified name and escaped string values", () => {
    const sql = buildInsertSql({
      driver: "mysql",
      database: "appdb",
      table: "users",
      columnsValues: [
        { name: "name", value: "O'Brien" },
        { name: "age", value: "30" },
      ],
    });

    expect(sql).toBe("INSERT INTO `appdb`.`users` (`name`, `age`) VALUES ('O''Brien', '30')");
  });

  it("builds postgresql INSERT with unqualified name", () => {
    const sql = buildInsertSql({
      driver: "postgresql",
      database: "appdb",
      table: "users",
      columnsValues: [{ name: "email", value: "a@b.com" }],
    });

    expect(sql).toBe('INSERT INTO "users" ("email") VALUES (\'a@b.com\')');
  });

  it("emits DEFAULT VALUES for postgresql when no columns selected", () => {
    const sql = buildInsertSql({
      driver: "postgresql",
      database: "appdb",
      table: "logs",
      columnsValues: [],
    });
    expect(sql).toBe('INSERT INTO "logs" DEFAULT VALUES');
  });

  it("emits empty column list for mysql when no columns selected", () => {
    const sql = buildInsertSql({
      driver: "mysql",
      database: "appdb",
      table: "logs",
      columnsValues: [],
    });
    expect(sql).toBe("INSERT INTO `appdb`.`logs` () VALUES ()");
  });

  it("preserves empty string values (does not drop them)", () => {
    const sql = buildInsertSql({
      driver: "mysql",
      database: "appdb",
      table: "notes",
      columnsValues: [{ name: "body", value: "" }],
    });
    expect(sql).toBe("INSERT INTO `appdb`.`notes` (`body`) VALUES ('')");
  });

  it("escapes embedded delimiters in identifiers", () => {
    const mysql = buildInsertSql({
      driver: "mysql",
      database: "app",
      table: "we`ird",
      columnsValues: [{ name: "col`1", value: "v" }],
    });
    expect(mysql).toBe("INSERT INTO `app`.`we``ird` (`col``1`) VALUES ('v')");

    const pg = buildInsertSql({
      driver: "postgresql",
      database: "app",
      table: 'we"ird',
      columnsValues: [{ name: 'col"1', value: "v" }],
    });
    expect(pg).toBe('INSERT INTO "we""ird" ("col""1") VALUES (\'v\')');
  });
});
