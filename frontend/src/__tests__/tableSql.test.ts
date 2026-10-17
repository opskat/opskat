import { describe, expect, it } from "vitest";
import { quoteIdent } from "@/lib/tableSql";

// 表格编辑的 UPDATE / DELETE 改由驱动生成,其文本断言已迁到 internal/dbdriver/*/gridedit_test.go。
describe("table SQL helpers", () => {
  it("escapes embedded quotes in identifiers", () => {
    expect(quoteIdent("name`with`backtick", "mysql")).toBe("`name``with``backtick`");
    expect(quoteIdent('name"with"quote', "postgresql")).toBe('"name""with""quote"');
    expect(quoteIdent('name"with"quote', "sqlite")).toBe('"name""with""quote"');
    expect(quoteIdent("plain", "mysql")).toBe("`plain`");
    expect(quoteIdent("plain", "postgresql")).toBe('"plain"');
  });

  it("quotes MSSQL identifiers with brackets", () => {
    expect(quoteIdent("plain", "mssql")).toBe("[plain]");
    expect(quoteIdent("a]b", "mssql")).toBe("[a]]b]");
  });
});
