import { describe, expect, it } from "vitest";
import { validateInsertRow, type TableColumnRule } from "@/lib/tableEdit";

const rules: TableColumnRule[] = [
  { name: "id", nullable: false, hasDefault: false, autoIncrement: true },
  { name: "name", nullable: false, hasDefault: false },
  { name: "email", nullable: true, hasDefault: false },
  { name: "note", nullable: false, hasDefault: true },
];

// 新增行的 INSERT 改由驱动生成,其文本断言已迁到 internal/dbdriver/*/gridedit_test.go。
describe("table edit helpers", () => {
  it("requires non-null columns that do not have defaults before inserting", () => {
    expect(validateInsertRow(rules, { email: "a@example.com" })).toEqual(["name"]);
    expect(validateInsertRow(rules, { name: "Alice" })).toEqual([]);
  });
});
