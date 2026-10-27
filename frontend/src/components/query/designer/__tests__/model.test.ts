import { describe, expect, it } from "vitest";
import { dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import type { TFunction } from "i18next";
import {
  declaredLabel,
  designerAccess,
  indexTypeLabel,
  structureFromAlterModel,
  structureFromMetadata,
} from "../model";
import { fakeDriverWith } from "@/__tests__/dbDriverFixtures";

const metadata = (patch: Record<string, unknown> = {}) =>
  new query_svc.TableMetadata({
    columns: [
      { name: "id", type: "bigint", nullable: false, primaryKey: true, comment: "订单 ID", default: null },
      { name: "status", type: "varchar(16)", nullable: false, primaryKey: false, comment: "", default: "'pending'" },
      { name: "note", type: "text", nullable: true, primaryKey: false, comment: "" },
    ],
    indexes: [
      { name: "PRIMARY", columns: ["id"], unique: true },
      { name: "uk_status", columns: ["status"], unique: true },
      { name: "idx_note", columns: ["note"], unique: false, include: ["status"] },
    ],
    foreignKeys: [
      {
        name: "fk_a",
        column: "a1",
        referencedTable: "t",
        referencedColumn: "x1",
        onUpdate: "CASCADE",
        onDelete: "RESTRICT",
      },
      {
        name: "fk_a",
        column: "a2",
        referencedTable: "t",
        referencedColumn: "x2",
        onUpdate: "CASCADE",
        onDelete: "RESTRICT",
      },
      { name: "fk_b", column: "b", referencedTable: "u", referencedColumn: "id" },
    ],
    ...patch,
  });

describe("structureFromMetadata", () => {
  it("列、默认值原文、主键、注释照表元数据给出", () => {
    const s = structureFromMetadata("orders", metadata());
    expect(s.name).toBe("orders");
    expect(s.comment).toBe("");
    expect(s.columns).toEqual([
      {
        name: "id",
        type: "bigint",
        nullable: false,
        primaryKey: true,
        autoIncrement: false,
        default: { kind: "none" },
        comment: "订单 ID",
      },
      {
        name: "status",
        type: "varchar(16)",
        nullable: false,
        primaryKey: false,
        autoIncrement: false,
        default: { kind: "text", text: "'pending'" },
        comment: "",
      },
      {
        name: "note",
        type: "text",
        nullable: true,
        primaryKey: false,
        autoIncrement: false,
        default: { kind: "none" },
        comment: "",
      },
    ]);
  });

  it("与主键列相同的唯一索引是主键,其余按唯一 / 普通;索引只能查看", () => {
    const s = structureFromMetadata("orders", metadata());
    expect(s.indexesEditable).toBe(false);
    expect(s.indexes.map((i) => [i.name, i.type, i.columns.map((c) => c.name), i.include])).toEqual([
      ["PRIMARY", "primary", ["id"], []],
      ["uk_status", "unique", ["status"], []],
      ["idx_note", "normal", ["note"], ["status"]],
    ]);
  });

  it("外键按名字把逐列的映射合成一条", () => {
    const s = structureFromMetadata("orders", metadata());
    expect(s.foreignKeys).toEqual([
      {
        name: "fk_a",
        columns: ["a1", "a2"],
        referencedTable: "t",
        referencedColumns: ["x1", "x2"],
        onUpdate: "CASCADE",
        onDelete: "RESTRICT",
      },
      { name: "fk_b", columns: ["b"], referencedTable: "u", referencedColumns: ["id"], onUpdate: "", onDelete: "" },
    ]);
  });
});

describe("structureFromAlterModel", () => {
  // 与绑定一样按 JSON 构造:嵌套对象由 models.ts 的构造函数转换。
  const model = (patch: Record<string, unknown> = {}) =>
    new dbdriver.AlterTableModel({
      name: "orders",
      comment: "销售订单",
      columns: [
        {
          name: "id",
          type: "bigint",
          nullable: false,
          defaultValue: "",
          comment: "",
          defaultKind: "none",
          autoIncrement: true,
        },
        {
          name: "status",
          type: "varchar(16)",
          nullable: false,
          defaultValue: "pending",
          comment: "",
          defaultKind: "value",
        },
        {
          name: "created_at",
          type: "datetime",
          nullable: true,
          defaultValue: "CURRENT_TIMESTAMP",
          comment: "",
          defaultKind: "expression",
        },
        { name: "note", type: "text", nullable: true, defaultValue: "", comment: "", defaultKind: "null" },
      ],
      ...patch,
    });

  it("默认值按驱动给出的写法,自增与表注释取自改表模型,外键取自表元数据", () => {
    const s = structureFromAlterModel(model(), metadata());
    expect(s.name).toBe("orders");
    expect(s.comment).toBe("销售订单");
    expect(s.columns.map((c) => [c.name, c.default, c.autoIncrement, c.primaryKey])).toEqual([
      ["id", { kind: "none" }, true, true],
      ["status", { kind: "value", text: "pending" }, false, false],
      ["created_at", { kind: "expression", text: "CURRENT_TIMESTAMP" }, false, false],
      ["note", { kind: "null" }, false, false],
    ]);
    expect(s.foreignKeys.map((f) => f.name)).toEqual(["fk_a", "fk_b"]);
  });

  it("驱动没声明默认值写法时,有默认值的是原文、没有的是无", () => {
    const s = structureFromAlterModel(
      model({
        columns: [
          { name: "a", type: "int", nullable: false, defaultValue: "0", comment: "" },
          { name: "b", type: "int", nullable: true, defaultValue: "", comment: "" },
        ],
      }),
      metadata({ columns: [] })
    );
    expect(s.columns.map((c) => c.default)).toEqual([{ kind: "text", text: "0" }, { kind: "none" }]);
  });

  it("带索引时索引与主键取自改表模型,保留只读原因、不可删除与选项,并可在设计器里改", () => {
    const s = structureFromAlterModel(
      model({
        indexes: [
          { name: "pk", type: "primary", columns: [{ name: "status" }] },
          {
            name: "idx_expr",
            type: "normal",
            columns: [
              { name: "lower(note)", expression: true },
              { name: "id", descending: true, prefixLength: 8 },
            ],
            options: { method: "BTREE" },
            comment: "c",
            readOnlyReason: "表达式索引",
            undroppable: true,
          },
        ],
        takenIndexNames: ["idx_other"],
      }),
      metadata()
    );
    expect(s.indexesEditable).toBe(true);
    expect(s.takenIndexNames).toEqual(["idx_other"]);
    expect(s.columns.filter((c) => c.primaryKey).map((c) => c.name)).toEqual(["status"]);
    expect(s.indexes[1]).toEqual({
      name: "idx_expr",
      type: "normal",
      columns: [
        { name: "lower(note)", expression: true, descending: false, prefixLength: 0 },
        { name: "id", expression: false, descending: true, prefixLength: 8 },
      ],
      include: [],
      options: { method: "BTREE" },
      comment: "c",
      readOnlyReason: "表达式索引",
      undroppable: true,
    });
  });

  it("不带索引(驱动没声明索引编辑)时索引与主键取自表元数据,只能查看", () => {
    const s = structureFromAlterModel(model(), metadata());
    expect(s.indexesEditable).toBe(false);
    expect(s.indexes.map((i) => [i.name, i.type])).toEqual([
      ["PRIMARY", "primary"],
      ["uk_status", "unique"],
      ["idx_note", "normal"],
    ]);
    expect(s.columns.filter((c) => c.primaryKey).map((c) => c.name)).toEqual(["id"]);
  });
});

describe("designerAccess", () => {
  it("声明了改表且连接不是只读:可以改", () => {
    expect(designerAccess(fakeDriverWith(["alter_table"]), false)).toEqual({ canAlter: true, readOnlyReason: null });
  });

  it("连接只读:只读,原因是只读连接(优先于驱动)", () => {
    expect(designerAccess(fakeDriverWith(["alter_table"]), true)).toEqual({
      canAlter: false,
      readOnlyReason: "readOnlyConnection",
    });
    expect(designerAccess(fakeDriverWith([]), true).readOnlyReason).toBe("readOnlyConnection");
  });

  it("驱动没声明改表:只读,原因是驱动不能改表", () => {
    expect(designerAccess(fakeDriverWith([]), false)).toEqual({ canAlter: false, readOnlyReason: "driverCannotAlter" });
  });
});

describe("declaredLabel / indexTypeLabel", () => {
  // 内置驱动的显示名是宿主 i18n 键(前端翻译);扩展驱动的已由宿主按扩展的 locales 翻译成文本。
  const known: Record<string, string> = { "query.designer.indexType.unique": "Unique" };
  const t = ((key: string, options?: { defaultValue?: string }) =>
    known[key] ?? options?.defaultValue ?? key) as unknown as TFunction;

  it("内置驱动声明的键经宿主翻译,扩展给出的文本原样显示", () => {
    expect(declaredLabel(t, "query.designer.indexType.unique")).toBe("Unique");
    expect(declaredLabel(t, "Bitmap: low cardinality")).toBe("Bitmap: low cardinality");
  });

  it("索引类型的显示名取声明并翻译", () => {
    const descriptor = new dbdriver.Descriptor({
      ...fakeDriverWith(["alter_table"]),
      indexEditing: { types: [{ id: "unique", label: "query.designer.indexType.unique" }], nameScope: "table" },
    });
    expect(indexTypeLabel(descriptor, "unique", t)).toBe("Unique");
  });
});
