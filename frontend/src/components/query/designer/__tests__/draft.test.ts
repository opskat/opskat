import { describe, expect, it } from "vitest";
import { dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeAlterDriver } from "@/__tests__/dbDriverFixtures";
import { structureFromAlterModel, type TableStructure } from "../model";
import {
  addColumn,
  buildAlterRequest,
  capabilityLimits,
  changedFields,
  columnStatus,
  draftCounts,
  indexesDroppedWith,
  initDraft,
  isDraftDirty,
  lockedFields,
  revertField,
  updateColumn,
  validateDraft,
  type TableDraft,
} from "../draft";

const MYSQL = BUILTIN_DRIVERS.find((d) => d.id === "mysql")!;
/** 没声明任何列细项的驱动(旧扩展的样子):只能改表名、列名、增删列。 */
const LEGACY = fakeAlterDriver({ alterTableAttributes: [] });

const EMPTY_METADATA = new query_svc.TableMetadata({ columns: [], indexes: [], foreignKeys: [] });

function mysqlModel(): dbdriver.AlterTableModel {
  return new dbdriver.AlterTableModel({
    name: "orders",
    comment: "销售订单",
    columns: [
      {
        name: "id",
        type: "bigint unsigned",
        nullable: false,
        defaultValue: "",
        defaultKind: "none",
        comment: "订单 ID",
        autoIncrement: true,
      },
      {
        name: "status",
        type: "varchar(16)",
        nullable: false,
        defaultValue: "pending",
        defaultKind: "value",
        comment: "",
      },
      {
        name: "created_at",
        type: "datetime",
        nullable: false,
        defaultValue: "CURRENT_TIMESTAMP",
        defaultKind: "expression",
        comment: "",
      },
      { name: "note", type: "varchar(255)", nullable: true, defaultValue: "", defaultKind: "null", comment: "备注" },
    ],
    indexes: [
      { name: "PRIMARY", type: "primary", columns: [{ name: "id" }] },
      { name: "idx_note", type: "normal", columns: [{ name: "note" }] },
      { name: "idx_status_note", type: "normal", columns: [{ name: "status" }, { name: "note" }] },
      { name: "idx_expr", type: "normal", columns: [{ name: "lower(note)", expression: true }] },
    ],
  });
}

function mysqlStructure(): TableStructure {
  return structureFromAlterModel(mysqlModel(), EMPTY_METADATA);
}

function legacyStructure(): TableStructure {
  return structureFromAlterModel(
    new dbdriver.AlterTableModel({
      name: "ORDERS",
      comment: "",
      columns: [
        { name: "ID", type: "NUMBER(19)", nullable: false, defaultValue: "", comment: "" },
        { name: "STATUS", type: "VARCHAR2(16)", nullable: true, defaultValue: "'pending'", comment: "" },
      ],
    }),
    EMPTY_METADATA
  );
}

function keyOf(draft: TableDraft, name: string): string {
  return draft.columns.find((c) => c.original?.name === name)!.key;
}

describe("buildAlterRequest", () => {
  it("原样交回读出的表:每列带原名、写法与值都与模型一致,没有删除,不带索引与已有列的自增", () => {
    const model = mysqlModel();
    const req = buildAlterRequest(initDraft(mysqlStructure()), MYSQL);
    expect(req.name).toBe("orders");
    expect(req.comment).toBe("销售订单");
    expect(req.drops).toEqual([]);
    expect(JSON.parse(JSON.stringify(req))).not.toHaveProperty("indexes");
    expect(JSON.parse(JSON.stringify(req))).not.toHaveProperty("indexDrops");
    expect(req.columns).toEqual(
      model.columns.map((c) => ({
        originalName: c.name,
        name: c.name,
        type: c.type,
        nullable: c.nullable,
        defaultValue: c.defaultValue,
        defaultKind: c.defaultKind,
        comment: c.comment,
      }))
    );
  });

  it("驱动没声明默认值写法时不发 defaultKind,默认值是原文;没声明自增时新列不带自增", () => {
    let draft = initDraft(legacyStructure());
    const added = addColumn(draft, LEGACY);
    draft = updateColumn(added.draft, added.key, { name: "NOTE", type: "VARCHAR2(64)", autoIncrement: true });
    const req = buildAlterRequest(draft, LEGACY);
    // 发给驱动的是 JSON:没声明的字段不能出现在里面。
    for (const column of JSON.parse(JSON.stringify(req)).columns) {
      expect(column).not.toHaveProperty("defaultKind");
      expect(column).not.toHaveProperty("autoIncrement");
    }
    expect(req.columns[1]).toMatchObject({ originalName: "STATUS", defaultValue: "'pending'" });
    expect(req.columns[2]).toMatchObject({ originalName: "", name: "NOTE", defaultValue: "" });
  });

  it("新列按声明带写法与自增;删除的已有列列在 drops,不再出现在 columns", () => {
    let draft = initDraft(mysqlStructure());
    const added = addColumn(draft, MYSQL);
    draft = updateColumn(added.draft, added.key, {
      name: "seq",
      autoIncrement: true,
      default: { kind: "expression", text: "0" },
    });
    draft = updateColumn(draft, keyOf(draft, "note"), { deleted: true });
    draft = updateColumn(draft, keyOf(draft, "status"), { name: "state" });
    const req = buildAlterRequest(draft, MYSQL);
    expect(req.drops).toEqual(["note"]);
    expect(req.columns.map((c) => c.originalName)).toEqual(["id", "status", "created_at", ""]);
    expect(req.columns[1]).toMatchObject({ originalName: "status", name: "state" });
    expect(req.columns[3]).toEqual({
      originalName: "",
      name: "seq",
      type: "VARCHAR(255)",
      nullable: true,
      defaultValue: "0",
      defaultKind: "expression",
      comment: "",
      autoIncrement: true,
    });
  });

  it("表名去掉首尾空白", () => {
    const draft = { ...initDraft(mysqlStructure()), name: "  orders_v2 " };
    expect(buildAlterRequest(draft, MYSQL).name).toBe("orders_v2");
  });
});

describe("改动标记与计数", () => {
  it("刚读出的草稿没有改动", () => {
    const structure = mysqlStructure();
    const draft = initDraft(structure);
    expect(isDraftDirty(draft)).toBe(false);
    expect(draftCounts(draft)).toEqual({ added: 0, modified: 0, deleted: 0 });
    expect(draft.columns.every((c) => columnStatus(c) === "unchanged")).toBe(true);
  });

  it("类型只改大小写、注释只差首尾空白不算改动", () => {
    let draft = initDraft(mysqlStructure());
    draft = updateColumn(draft, keyOf(draft, "status"), { type: "VARCHAR(16)", comment: "  " });
    const status = draft.columns.find((c) => c.original?.name === "status")!;
    expect(changedFields(status)).toEqual([]);
  });

  it("逐格标出改过的属性,计数分新增 / 修改 / 删除", () => {
    const structure = mysqlStructure();
    let draft = initDraft(structure);
    draft = updateColumn(draft, keyOf(draft, "status"), { type: "varchar(32)", default: { kind: "null" } });
    draft = updateColumn(draft, keyOf(draft, "note"), { deleted: true });
    draft = addColumn(draft, MYSQL).draft;
    const status = draft.columns.find((c) => c.original?.name === "status")!;
    expect(changedFields(status)).toEqual(["type", "default"]);
    expect(columnStatus(status)).toBe("modified");
    expect(draftCounts(draft)).toEqual({ added: 1, modified: 1, deleted: 1 });
    expect(isDraftDirty(draft)).toBe(true);
  });

  it("改表名或表注释也算有改动", () => {
    const structure = mysqlStructure();
    expect(isDraftDirty({ ...initDraft(structure), name: "orders2" })).toBe(true);
    expect(isDraftDirty({ ...initDraft(structure), comment: "x" })).toBe(true);
  });

  it("逐项还原回原值", () => {
    let draft = initDraft(mysqlStructure());
    const key = keyOf(draft, "status");
    draft = updateColumn(draft, key, { type: "text", default: { kind: "expression", text: "uuid()" } });
    const reverted = revertField(
      draft.columns.find((c) => c.key === key)!,
      "default"
    );
    expect(changedFields(reverted)).toEqual(["type"]);
    expect(reverted.default).toEqual({ kind: "value", text: "pending" });
  });

  it("新列预填驱动声明的类型,没声明时留空", () => {
    const mysqlDraft = addColumn(initDraft(mysqlStructure()), MYSQL);
    expect(mysqlDraft.draft.columns.at(-1)).toMatchObject({ type: "VARCHAR(255)", original: null });
    const legacyDraft = addColumn(initDraft(legacyStructure()), LEGACY);
    expect(legacyDraft.draft.columns.at(-1)!.type).toBe("");
  });
});

describe("锁定与能力说明", () => {
  it("已有列上没声明可改的属性被锁定;新列只在驱动不能写列注释时锁注释", () => {
    const mssqlLike = fakeAlterDriver({ alterTableAttributes: ["column_type", "column_nullable"] });
    const draft = addColumn(initDraft(legacyStructure()), mssqlLike).draft;
    expect(lockedFields(mssqlLike, draft.columns[0])).toEqual(["default", "comment"]);
    expect(lockedFields(mssqlLike, draft.columns[2])).toEqual(["comment"]);
    expect(lockedFields(MYSQL, initDraft(mysqlStructure()).columns[0])).toEqual([]);
  });

  it("声明了全部属性时没有说明;否则列出能改与不能改的", () => {
    expect(capabilityLimits(MYSQL)).toBeNull();
    expect(capabilityLimits(fakeAlterDriver({ alterTableAttributes: ["column_type", "column_nullable"] }))).toEqual({
      can: ["column_type", "column_nullable"],
      cannot: ["column_default", "column_comment", "table_comment"],
    });
    expect(capabilityLimits(LEGACY)).toEqual({
      can: [],
      cannot: ["column_type", "column_nullable", "column_default", "column_comment", "table_comment"],
    });
  });
});

describe("validateDraft", () => {
  it("刚读出的表没有问题", () => {
    expect(validateDraft(initDraft(mysqlStructure()), MYSQL)).toEqual([]);
  });

  it("表名为空、列名为空、重名(不分大小写)、类型为空都是错误,指向对应的格子", () => {
    let draft = { ...initDraft(mysqlStructure()), name: " " };
    const a = addColumn(draft, MYSQL);
    draft = updateColumn(a.draft, a.key, { name: "Status" });
    const b = addColumn(draft, MYSQL);
    draft = updateColumn(b.draft, b.key, { name: "", type: " " });
    const issues = validateDraft(draft, MYSQL);
    expect(issues).toEqual([
      { severity: "error", code: "tableNameEmpty", target: { kind: "tableName" } },
      {
        severity: "error",
        code: "columnNameDuplicate",
        target: { kind: "column", key: a.key, field: "name" },
        row: 5,
        name: "Status",
        duplicateOf: 2,
      },
      {
        severity: "error",
        code: "columnNameEmpty",
        target: { kind: "column", key: b.key, field: "name" },
        row: 6,
        name: "",
      },
      {
        severity: "error",
        code: "columnTypeEmpty",
        target: { kind: "column", key: b.key, field: "type" },
        row: 6,
        name: "",
      },
    ]);
  });

  it("删除的列不参与重名;全部删除时报没有列", () => {
    let draft = initDraft(mysqlStructure());
    for (const c of draft.columns) draft = updateColumn(draft, c.key, { deleted: true });
    expect(validateDraft(draft, MYSQL)).toEqual([
      { severity: "error", code: "noColumns", target: { kind: "columns" } },
    ]);
  });

  it("名称折叠:新起或改过的名字与规则不符时提醒(只提醒),已有的名字不提醒", () => {
    const oracleLike = fakeAlterDriver({ alterTableAttributes: [], nameFolding: "upper" });
    let draft = { ...initDraft(legacyStructure()), name: "orders" };
    const a = addColumn(draft, oracleLike);
    draft = updateColumn(a.draft, a.key, { name: "updated_at", type: "DATE" });
    const issues = validateDraft(draft, oracleLike);
    expect(issues).toEqual([
      { severity: "warning", code: "nameFolding", target: { kind: "tableName" }, name: "orders", folding: "upper" },
      {
        severity: "warning",
        code: "nameFolding",
        target: { kind: "column", key: a.key, field: "name" },
        row: 3,
        name: "updated_at",
        folding: "upper",
      },
    ]);
    // 没声明折叠的驱动不提醒。
    expect(validateDraft(draft, LEGACY)).toEqual([]);
  });

  it("折叠为小写时提醒含大写字母的名字", () => {
    const pgLike = fakeAlterDriver({ alterTableAttributes: [], nameFolding: "lower" });
    let draft = initDraft(mysqlStructure());
    draft = updateColumn(draft, keyOf(draft, "status"), { name: "orderStatus" });
    expect(validateDraft(draft, pgLike)).toMatchObject([
      { severity: "warning", code: "nameFolding", folding: "lower" },
    ]);
  });
});

describe("indexesDroppedWith", () => {
  it("列出引用被删列的已有索引(按草稿得出),表达式索引不按列名匹配", () => {
    const structure = mysqlStructure();
    let draft = initDraft(structure);
    draft = updateColumn(draft, keyOf(draft, "note"), { deleted: true });
    expect(indexesDroppedWith(draft, structure.indexes)).toEqual(["idx_note", "idx_status_note"]);
    expect(indexesDroppedWith(initDraft(structure), structure.indexes)).toEqual([]);
  });

  it("只在 INCLUDE 里引用被删列的索引也随列删除", () => {
    const structure = mysqlStructure();
    const indexes = [
      ...structure.indexes,
      {
        ...structure.indexes[0],
        name: "idx_cover",
        columns: [{ name: "id", expression: false, descending: false, prefixLength: 0 }],
        include: ["note"],
      },
    ];
    const draft = updateColumn(initDraft(structure), keyOf(initDraft(structure), "note"), { deleted: true });
    expect(indexesDroppedWith(draft, indexes)).toContain("idx_cover");
  });
});
