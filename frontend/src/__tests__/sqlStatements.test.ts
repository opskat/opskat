import { describe, expect, it } from "vitest";
import { isDangerousSql, splitStatements, statementAt } from "@/lib/sqlStatements";

const texts = (sql: string) => splitStatements(sql).map((s) => s.text);

describe("splitStatements", () => {
  it("splits on semicolons and reports offsets into the full text", () => {
    const sql = "SELECT 1;  SELECT 2";
    const out = splitStatements(sql);
    expect(out.map((s) => s.text)).toEqual(["SELECT 1", "SELECT 2"]);
    for (const s of out) expect(sql.slice(s.start, s.end)).toBe(s.text);
    expect(out[1].start).toBe(11);
  });

  it("ignores semicolons inside quotes, backticks and bracket identifiers", () => {
    expect(texts("SELECT 'a;b'; SELECT \"c;d\"; SELECT `e;f`; SELECT [g;h]")).toEqual([
      "SELECT 'a;b'",
      'SELECT "c;d"',
      "SELECT `e;f`",
      "SELECT [g;h]",
    ]);
  });

  it("does not end a string early on doubled quotes or backslash escapes", () => {
    expect(texts("SELECT 'it''s; ok'; SELECT 2")).toEqual(["SELECT 'it''s; ok'", "SELECT 2"]);
    expect(texts("SELECT 'a\\'; b'; SELECT 2")).toEqual(["SELECT 'a\\'; b'", "SELECT 2"]);
    expect(texts('SELECT "a""; b"; SELECT 2')).toEqual(['SELECT "a""; b"', "SELECT 2"]);
    expect(texts("SELECT `a``;b`; SELECT 2")).toEqual(["SELECT `a``;b`", "SELECT 2"]);
  });

  it("ignores semicolons inside line and block comments", () => {
    expect(texts("SELECT 1 -- a; b\n+ 1; SELECT 2 /* c; d */ + 2")).toEqual([
      "SELECT 1 -- a; b\n+ 1",
      "SELECT 2 /* c; d */ + 2",
    ]);
  });

  it("ignores semicolons inside PostgreSQL dollar-quoted bodies", () => {
    expect(texts("DO $$ BEGIN PERFORM 1; END $$; SELECT 2")).toEqual(["DO $$ BEGIN PERFORM 1; END $$", "SELECT 2"]);
    expect(texts("SELECT $fn$ a; $$ b; $fn$; SELECT 2")).toEqual(["SELECT $fn$ a; $$ b; $fn$", "SELECT 2"]);
  });

  it("does not treat $1 parameters as dollar quotes", () => {
    expect(texts("SELECT $1; SELECT $2")).toEqual(["SELECT $1", "SELECT $2"]);
  });

  it("drops fragments of only whitespace and comments", () => {
    expect(texts(";; ;\n-- nothing\n; /* x */ ;")).toEqual([]);
    expect(texts("SELECT 1; -- trailing note")).toEqual(["SELECT 1"]);
  });

  it("does not recognise GO as a separator", () => {
    expect(texts("SELECT 1\nGO\nSELECT 2")).toEqual(["SELECT 1\nGO\nSELECT 2"]);
  });

  it("treats an unterminated string as running to the end", () => {
    expect(texts("SELECT 'abc; SELECT 2")).toEqual(["SELECT 'abc; SELECT 2"]);
  });
});

describe("statementAt", () => {
  const sql = "SELECT 1;\nSELECT 2;\n-- end\n";

  it("returns the statement containing the offset", () => {
    expect(statementAt(sql, 3)?.text).toBe("SELECT 1");
    expect(statementAt(sql, sql.indexOf("SELECT 2") + 4)?.text).toBe("SELECT 2");
  });

  it("treats both edges of a statement as inside it", () => {
    expect(statementAt(sql, 0)?.text).toBe("SELECT 1");
    expect(statementAt(sql, 8)?.text).toBe("SELECT 1");
    expect(statementAt(sql, 9)?.text).toBe("SELECT 1"); // right after the semicolon
  });

  it("assigns whitespace between statements to the previous one", () => {
    expect(statementAt("SELECT 1;\n\n\nSELECT 2", 11)?.text).toBe("SELECT 1");
  });

  it("assigns the whitespace or comment area after the last semicolon to the previous statement", () => {
    expect(statementAt(sql, sql.length)?.text).toBe("SELECT 2");
    expect(statementAt(sql, sql.indexOf("-- end") + 2)?.text).toBe("SELECT 2");
  });

  it("assigns leading whitespace and comments to the first statement", () => {
    expect(statementAt("\n  SELECT 1", 0)?.text).toBe("SELECT 1");
    expect(statementAt("-- hi\nSELECT 1", 2)?.text).toBe("SELECT 1");
  });

  it("returns a statement that has no terminating semicolon", () => {
    expect(statementAt("SELECT 1; SELECT 2", 15)?.text).toBe("SELECT 2");
  });

  it("returns null for empty or comment-only text", () => {
    expect(statementAt("", 0)).toBeNull();
    expect(statementAt("  \n ", 2)).toBeNull();
    expect(statementAt("-- only a comment", 5)).toBeNull();
    expect(statementAt(";;", 1)).toBeNull();
  });

  it("returns null for an offset outside the text", () => {
    expect(statementAt("SELECT 1", -1)).toBeNull();
    expect(statementAt("SELECT 1", 99)).toBeNull();
  });
});

describe("isDangerousSql", () => {
  it.each(["DELETE FROM t", "drop table x", "TRUNCATE t", "ALTER TABLE t ADD c int", "-- c\n/* d */ DELETE FROM t"])(
    "flags %s",
    (sql) => expect(isDangerousSql(sql)).toBe(true)
  );

  it.each([
    "SELECT * FROM t",
    "UPDATE t SET a = 1 WHERE id = 2",
    "INSERT INTO t VALUES (1)",
    "SELECT 'DROP TABLE x'",
    "SELECT 1; SELECT 2",
    "",
  ])("does not flag %s", (sql) => expect(isDangerousSql(sql)).toBe(false));

  it("flags UPDATE without WHERE, ignoring WHERE inside strings, comments and quoted names", () => {
    expect(isDangerousSql("UPDATE t SET a = 1")).toBe(true);
    expect(isDangerousSql("UPDATE t SET a = 'where'")).toBe(true);
    expect(isDangerousSql("UPDATE t SET a = 1 -- where id = 1")).toBe(true);
    expect(isDangerousSql("UPDATE t SET `where` = 1")).toBe(true);
    expect(isDangerousSql("UPDATE t SET a = 1\nWHERE\nid = 2")).toBe(false);
    expect(isDangerousSql("UPDATE t SET a = 1 where id = 2")).toBe(false);
  });

  it("checks every statement, not just the first", () => {
    expect(isDangerousSql("SELECT 1; DROP TABLE x")).toBe(true);
    expect(isDangerousSql("UPDATE a SET x = 1 WHERE id = 1; UPDATE b SET y = 2")).toBe(true);
    expect(isDangerousSql("SELECT 1; -- DROP TABLE x\nSELECT 2")).toBe(false);
  });
});
