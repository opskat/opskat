import { describe, expect, it } from "vitest";
import { dbdriver, query_svc } from "../../../../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeAlterDriver } from "@/__tests__/dbDriverFixtures";
import { structureFromAlterModel, structureFromMetadata, type TableStructure } from "../model";
import {
  addColumn,
  buildAlterRequest,
  draftCounts,
  initDraft,
  isDraftDirty,
  rebaseDraft,
  updateColumn,
  validateDraft,
  type TableDraft,
} from "../draft";
import {
  addIndex,
  changedIndexFields,
  droppedIndexes,
  indexColumnChoices,
  indexCounts,
  indexDefinition,
  indexDroppable,
  indexDroppedWith,
  indexEditable,
  indexStatus,
  indexTypeChoices,
  primaryKeyColumnKeys,
  setTableName,
  updateIndex,
  type DraftIndex,
} from "../indexDraft";

const MYSQL = BUILTIN_DRIVERS.find((d) => d.id === "mysql")!;
const SQLITE = BUILTIN_DRIVERS.find((d) => d.id === "sqlite")!;
const MSSQL = BUILTIN_DRIVERS.find((d) => d.id === "mssql")!;
const PG = BUILTIN_DRIVERS.find((d) => d.id === "postgresql")!;
/** 没声明索引编辑的驱动(旧扩展的样子)。 */
const LEGACY = fakeAlterDriver({ alterTableAttributes: [] });

const EMPTY_METADATA = new query_svc.TableMetadata({ columns: [], indexes: [], foreignKeys: [] });

/** 改表模型里一个索引的 JSON 样子(驱动交来的就是这样)。 */
type IndexInit = Omit<dbdriver.IndexDefinition, "convertValues" | "columns"> & {
  columns: { name: string; expression?: boolean }[];
};

function model(indexes: IndexInit[], takenIndexNames: string[] = []): dbdriver.AlterTableModel {
  return new dbdriver.AlterTableModel({
    name: "orders",
    comment: "",
    columns: [
      { name: "id", type: "bigint", nullable: false, defaultValue: "", defaultKind: "none", comment: "" },
      { name: "status", type: "varchar(16)", nullable: false, defaultValue: "", defaultKind: "none", comment: "" },
      { name: "region", type: "varchar(16)", nullable: true, defaultValue: "", defaultKind: "none", comment: "" },
      { name: "note", type: "text", nullable: true, defaultValue: "", defaultKind: "none", comment: "" },
    ],
    indexes,
    takenIndexNames,
  });
}

const MYSQL_INDEXES = [
  { name: "PRIMARY", type: "primary", columns: [{ name: "id" }] },
  { name: "idx_status", type: "normal", columns: [{ name: "status" }], options: { method: "BTREE" } },
  { name: "idx_note", type: "fulltext", columns: [{ name: "note" }] },
  { name: "idx_expr", type: "normal", columns: [{ name: "lower(note)", expression: true }], readOnlyReason: "表达式" },
] satisfies IndexInit[];

function structure(indexes: IndexInit[] = MYSQL_INDEXES, taken: string[] = []): TableStructure {
  return structureFromAlterModel(model(indexes, taken), EMPTY_METADATA);
}

const colKey = (draft: TableDraft, name: string) => draft.columns.find((c) => c.original?.name === name)!.key;
const idx = (draft: TableDraft, name: string) => draft.indexes.find((i) => i.original?.name === name)!;
const json = (value: unknown) => JSON.parse(JSON.stringify(value));

/** 加一个索引并选好列,返回草稿与它的 key。 */
function withNewIndex(draft: TableDraft, descriptor: dbdriver.Descriptor, columns: string[]) {
  const added = addIndex(draft, descriptor);
  const refs = columns.map((name) => ({
    ref: draft.columns.find((c) => c.name === name)!.key,
    expression: false,
    descending: false,
    prefixLength: 0,
  }));
  return { draft: updateIndex(added.draft, added.key, { columns: refs }), key: added.key };
}

describe("索引草稿:初始与改动标记", () => {
  it("刚读出的索引没有改动,计数为零", () => {
    const draft = initDraft(structure());
    expect(draft.indexes.map((i) => indexStatus(draft, i))).toEqual([
      "unchanged",
      "unchanged",
      "unchanged",
      "unchanged",
    ]);
    expect(indexCounts(draft)).toEqual({ added: 0, modified: 0, deleted: 0 });
    expect(isDraftDirty(draft)).toBe(false);
  });

  it("新索引:默认类型取声明里的普通索引,名字按 idx_表_列 生成并跟随列", () => {
    const { draft, key } = withNewIndex(initDraft(structure()), MYSQL, ["status", "region"]);
    const index = draft.indexes.find((i) => i.key === key)!;
    expect(index.type).toBe("normal");
    expect(index.name).toBe("idx_orders_status_region");
    expect(indexStatus(draft, index)).toBe("new");
    expect(indexCounts(draft)).toEqual({ added: 1, modified: 0, deleted: 0 });
    expect(isDraftDirty(draft)).toBe(true);
  });

  it("自动名字随列改名与表名变化;手动改过名字后不再跟随", () => {
    const created = withNewIndex(initDraft(structure()), MYSQL, ["status"]);
    const key = created.key;
    let draft = created.draft;
    draft = updateColumn(draft, colKey(draft, "status"), { name: "state" });
    expect(draft.indexes.find((i) => i.key === key)!.name).toBe("idx_orders_state");
    draft = setTableName(draft, "sales");
    expect(draft.indexes.find((i) => i.key === key)!.name).toBe("idx_sales_state");
    draft = updateIndex(draft, key, { name: "by_state" });
    draft = updateColumn(draft, colKey(draft, "region"), { name: "area" });
    const index = draft.indexes.find((i) => i.key === key)!;
    draft = updateIndex(draft, key, {
      columns: [...index.columns, { ...index.columns[0], ref: colKey(draft, "region") }],
    });
    expect(draft.indexes.find((i) => i.key === key)!.name).toBe("by_state");
  });

  it("改已有索引的列、选项或名字算修改,逐项列出;选项缺省与关闭视为相同", () => {
    let draft = initDraft(structure());
    const status = idx(draft, "idx_status");
    draft = updateIndex(draft, status.key, {
      name: "idx_status_region",
      columns: [
        ...status.columns,
        { ref: colKey(draft, "region"), expression: false, descending: false, prefixLength: 0 },
      ],
    });
    expect(changedIndexFields(idx(draft, "idx_status"))).toEqual(["name", "columns"]);
    expect(indexStatus(draft, idx(draft, "idx_status"))).toBe("modified");
    draft = updateIndex(draft, status.key, { options: { method: "HASH" } });
    expect(changedIndexFields(idx(draft, "idx_status"))).toContain("option:method");

    const mssql = initDraft(
      structureFromAlterModel(
        model([{ name: "ix", type: "normal", columns: [{ name: "status" }], options: { clustered: false } }]),
        EMPTY_METADATA
      )
    );
    const switchedOff = updateIndex(mssql, mssql.indexes[0].key, { options: {} });
    expect(changedIndexFields(switchedOff.indexes[0])).toEqual([]);
  });

  it("删除新索引直接移除;删除已有索引只做标记,可恢复", () => {
    const created = withNewIndex(initDraft(structure()), MYSQL, ["status"]);
    const key = created.key;
    let draft = created.draft;
    draft = updateIndex(draft, key, { deleted: true });
    expect(draft.indexes.some((i) => i.key === key)).toBe(false);
    draft = updateIndex(draft, idx(draft, "idx_note").key, { deleted: true });
    expect(indexStatus(draft, idx(draft, "idx_note"))).toBe("deleted");
    expect(indexCounts(draft)).toEqual({ added: 0, modified: 0, deleted: 1 });
    draft = updateIndex(draft, idx(draft, "idx_note").key, { deleted: false });
    expect(isDraftDirty(draft)).toBe(false);
  });

  it("显示用的定义按草稿里列现在的名字", () => {
    let draft = initDraft(structure());
    draft = updateColumn(draft, colKey(draft, "status"), { name: "state" });
    expect(indexDefinition(draft, idx(draft, "idx_status")).columns.map((c) => c.name)).toEqual(["state"]);
    expect(indexDefinition(draft, idx(draft, "idx_expr")).columns[0]).toMatchObject({
      name: "lower(note)",
      expression: true,
    });
  });
});

describe("随列删除", () => {
  it("删除列时引用它的索引(键列或 INCLUDE)标记删除并给出列名;恢复列一起恢复", () => {
    const withInclude = [
      ...MYSQL_INDEXES,
      { name: "ix_cover", type: "normal", columns: [{ name: "id" }], include: ["region"], readOnlyReason: "INCLUDE" },
    ] satisfies IndexInit[];
    let draft = initDraft(structure(withInclude));
    draft = updateColumn(draft, colKey(draft, "note"), { deleted: true });
    draft = updateColumn(draft, colKey(draft, "region"), { deleted: true });
    expect(indexDroppedWith(draft, idx(draft, "idx_note"))).toEqual(["note"]);
    expect(indexDroppedWith(draft, idx(draft, "ix_cover"))).toEqual(["region"]);
    // 表达式里的文本不按列名匹配。
    expect(indexDroppedWith(draft, idx(draft, "idx_expr"))).toEqual([]);
    expect(indexStatus(draft, idx(draft, "idx_note"))).toBe("deleted");
    expect(droppedIndexes(draft)).toEqual(["idx_note", "ix_cover"]);
    expect(indexCounts(draft).deleted).toBe(2);

    draft = updateColumn(draft, colKey(draft, "note"), { deleted: false });
    expect(indexStatus(draft, idx(draft, "idx_note"))).toBe("unchanged");
    expect(droppedIndexes(draft)).toEqual(["ix_cover"]);
  });

  it("另行删除的索引在恢复列之后仍是删除", () => {
    let draft = initDraft(structure());
    draft = updateIndex(draft, idx(draft, "idx_note").key, { deleted: true });
    draft = updateColumn(draft, colKey(draft, "note"), { deleted: true });
    draft = updateColumn(draft, colKey(draft, "note"), { deleted: false });
    expect(indexStatus(draft, idx(draft, "idx_note"))).toBe("deleted");
  });

  it("移除新列时把它从索引里拿掉", () => {
    let draft = initDraft(structure());
    const added = addColumn(draft, MYSQL);
    draft = updateColumn(added.draft, added.key, { name: "paid_at" });
    const created = withNewIndex(draft, MYSQL, ["status", "paid_at"]);
    draft = updateColumn(created.draft, added.key, { deleted: true });
    const index = draft.indexes.find((i) => i.key === created.key)!;
    expect(index.columns.map((c) => c.ref)).toEqual([colKey(draft, "status")]);
    expect(index.name).toBe("idx_orders_status");
  });

  it("没声明索引编辑的驱动(索引来自表元数据)同样得出随列删除的索引", () => {
    const metadata = new query_svc.TableMetadata({
      columns: [
        { name: "ID", type: "NUMBER", nullable: false, primaryKey: true, comment: "" },
        { name: "NOTE", type: "VARCHAR2(8)", nullable: true, primaryKey: false, comment: "" },
      ],
      indexes: [{ name: "IX_NOTE", columns: ["NOTE"], unique: false }],
      foreignKeys: [],
    });
    let draft = initDraft(structureFromMetadata("ORDERS", metadata));
    draft = updateColumn(draft, colKey(draft, "NOTE"), { deleted: true });
    expect(droppedIndexes(draft)).toEqual(["IX_NOTE"]);
  });
});

describe("锁定与可选项", () => {
  it("只读索引不能改、可以删;不可删除的索引两者都不行;驱动不能改主键时已有主键锁定", () => {
    const sqliteIndexes = [
      { name: "pk", type: "primary", columns: [{ name: "id" }], undroppable: true },
      { name: "sqlite_autoindex_orders_1", type: "unique", columns: [{ name: "status" }], undroppable: true },
      {
        name: "ix_lower",
        type: "normal",
        columns: [{ name: "lower(note)", expression: true }],
        readOnlyReason: "表达式",
      },
      { name: "ix_region", type: "normal", columns: [{ name: "region" }] },
    ] satisfies IndexInit[];
    const draft = initDraft(structure(sqliteIndexes));
    const at = (name: string) => idx(draft, name);
    expect([indexEditable(SQLITE, at("ix_lower")), indexDroppable(SQLITE, at("ix_lower"))]).toEqual([false, true]);
    expect([
      indexEditable(SQLITE, at("sqlite_autoindex_orders_1")),
      indexDroppable(SQLITE, at("sqlite_autoindex_orders_1")),
    ]).toEqual([false, false]);
    expect([indexEditable(SQLITE, at("ix_region")), indexDroppable(SQLITE, at("ix_region"))]).toEqual([true, true]);

    const mysql = initDraft(structure());
    expect([indexEditable(MYSQL, idx(mysql, "PRIMARY")), indexDroppable(MYSQL, idx(mysql, "PRIMARY"))]).toEqual([
      true,
      true,
    ]);
    const noAlterPk = new dbdriver.Descriptor({
      ...MYSQL,
      indexEditing: { ...MYSQL.indexEditing!, alterPrimaryKey: false },
    });
    expect([indexEditable(noAlterPk, idx(mysql, "PRIMARY")), indexDroppable(noAlterPk, idx(mysql, "PRIMARY"))]).toEqual(
      [false, false]
    );
    // 没声明索引编辑:什么都不能改。
    expect(indexEditable(LEGACY, idx(mysql, "idx_status"))).toBe(false);
    expect(indexDroppable(LEGACY, idx(mysql, "idx_status"))).toBe(false);
  });

  it("类型按声明的次序;「主键」只在驱动声明主键可改时可选", () => {
    expect(indexTypeChoices(MYSQL).map((c) => [c.id, c.disabled])).toEqual([
      ["normal", false],
      ["unique", false],
      ["fulltext", false],
      ["primary", false],
    ]);
    expect(indexTypeChoices(SQLITE).find((c) => c.id === "primary")!.disabled).toBe(true);
  });

  it("可选的列是草稿里没删的列,按现在的名字,含尚未保存的新列", () => {
    let draft = initDraft(structure());
    draft = updateColumn(draft, colKey(draft, "note"), { deleted: true });
    draft = updateColumn(draft, colKey(draft, "status"), { name: "state" });
    const added = addColumn(draft, MYSQL);
    draft = updateColumn(added.draft, added.key, { name: "paid_at", type: "datetime" });
    expect(indexColumnChoices(draft).map((c) => [c.name, c.isNew])).toEqual([
      ["id", false],
      ["state", false],
      ["region", false],
      ["paid_at", true],
    ]);
  });

  it("字段表的主键标记跟随草稿里的主键索引", () => {
    let draft = initDraft(structure());
    const pk = idx(draft, "PRIMARY");
    draft = updateIndex(draft, pk.key, {
      columns: [...pk.columns, { ref: colKey(draft, "region"), expression: false, descending: false, prefixLength: 0 }],
    });
    expect([...primaryKeyColumnKeys(draft)]).toEqual([colKey(draft, "id"), colKey(draft, "region")]);
  });
});

describe("索引校验", () => {
  const codes = (draft: TableDraft, descriptor = MYSQL) =>
    validateDraft(draft, descriptor)
      .filter((i) => i.target.kind === "index")
      .map((i) => [i.code, i.severity, i.target.kind === "index" ? i.target.field : null, i.name]);

  it("刚读出的索引没有问题(即便驱动不分大小写比较会撞名)", () => {
    const pg = [
      { name: "Idx_a", type: "normal", columns: [{ name: "status" }] },
      { name: "idx_a", type: "normal", columns: [{ name: "region" }] },
    ] satisfies IndexInit[];
    expect(validateDraft(initDraft(structure(pg)), PG)).toEqual([]);
  });

  it("没有列、名字为空是错误", () => {
    const created = withNewIndex(initDraft(structure()), MYSQL, []);
    const key = created.key;
    let draft = created.draft;
    draft = updateIndex(draft, key, { name: " " });
    expect(codes(draft)).toEqual([
      ["indexNameEmpty", "error", "name", ""],
      ["indexColumnsEmpty", "error", "columns", ""],
    ]);
  });

  it("名字在范围内重名(不分大小写):本表的其他索引与范围内其他表的索引名;删除的索引不算", () => {
    const created = withNewIndex(initDraft(structure([...MYSQL_INDEXES], ["IDX_TAKEN"])), MYSQL, ["region"]);
    const key = created.key;
    let draft = created.draft;
    draft = updateIndex(draft, key, { name: "IDX_STATUS" });
    expect(codes(draft)).toEqual([["indexNameDuplicate", "error", "name", "IDX_STATUS"]]);
    expect(validateDraft(draft, MYSQL).find((i) => i.code === "indexNameDuplicate")!.duplicateOf).toBe(2);
    draft = updateIndex(draft, key, { name: "idx_taken" });
    expect(codes(draft)).toEqual([["indexNameTaken", "error", "name", "idx_taken"]]);
    draft = updateIndex(draft, key, { name: "idx_note" });
    draft = updateIndex(draft, idx(draft, "idx_note").key, { deleted: true });
    expect(codes(draft, PG)).toEqual([]);
  });

  it("同一张表两个主键是错误,标在新改的那个上", () => {
    const created = withNewIndex(initDraft(structure()), MYSQL, ["status"]);
    const key = created.key;
    let draft = created.draft;
    draft = updateIndex(draft, key, { type: "primary" });
    expect(codes(draft)).toEqual([["primaryKeyDuplicate", "error", "type", "idx_orders_status"]]);
  });

  it("声明为独占的开关在两个索引上打开是错误", () => {
    const mssqlIndexes = [
      { name: "ix_a", type: "normal", columns: [{ name: "status" }], options: { clustered: true } },
    ] satisfies IndexInit[];
    const created = withNewIndex(initDraft(structure(mssqlIndexes)), MSSQL, ["region"]);
    const key = created.key;
    let draft = created.draft;
    draft = updateIndex(draft, key, { options: { clustered: true } });
    const issues = validateDraft(draft, MSSQL).filter((i) => i.code === "indexOptionExclusive");
    expect(issues).toHaveLength(1);
    expect(issues[0]).toMatchObject({
      severity: "error",
      target: { kind: "index", key, field: "option:clustered" },
      option: "query.designer.indexOption.clustered",
    });
  });

  it("名称折叠:新起或改过的索引名与规则不符时提醒", () => {
    const { draft } = withNewIndex(initDraft(structure()), PG, ["status"]);
    const renamed = updateIndex(draft, draft.indexes[draft.indexes.length - 1].key, { name: "Idx_Status2" });
    expect(codes(renamed, PG)).toEqual([["nameFolding", "warning", "name", "Idx_Status2"]]);
  });
});

describe("改表请求里的索引", () => {
  const indexesOf = (draft: TableDraft, descriptor = MYSQL) => json(buildAlterRequest(draft, descriptor));

  it("没改动:每个已有索引按读出的样子交回(带原名),没有删除", () => {
    const req = indexesOf(initDraft(structure()));
    expect(req.indexes).toEqual([
      { originalName: "PRIMARY", name: "PRIMARY", type: "primary", columns: [{ name: "id" }] },
      {
        originalName: "idx_status",
        name: "idx_status",
        type: "normal",
        columns: [{ name: "status" }],
        options: { method: "BTREE" },
      },
      { originalName: "idx_note", name: "idx_note", type: "fulltext", columns: [{ name: "note" }] },
      {
        originalName: "idx_expr",
        name: "idx_expr",
        type: "normal",
        columns: [{ name: "lower(note)", expression: true }],
      },
    ]);
    expect(req.indexDrops).toEqual([]);
  });

  it("没声明索引编辑的驱动收不到索引字段", () => {
    const req = indexesOf(initDraft(structure()), LEGACY);
    expect(req).not.toHaveProperty("indexes");
    expect(req).not.toHaveProperty("indexDrops");
  });

  it("列改名后,没改的索引交回列的新名字", () => {
    let draft = initDraft(structure());
    draft = updateColumn(draft, colKey(draft, "status"), { name: "state" });
    expect(indexesOf(draft).indexes[1]).toMatchObject({ originalName: "idx_status", columns: [{ name: "state" }] });
  });

  it("在新列上加索引:不带原名,列是新列的名字,只发声明了的细项", () => {
    let draft = initDraft(structure());
    const added = addColumn(draft, MYSQL);
    draft = updateColumn(added.draft, added.key, { name: "paid_at", type: "datetime" });
    let created = withNewIndex(draft, MYSQL, ["paid_at"]);
    const index = created.draft.indexes.find((i) => i.key === created.key)!;
    created = {
      ...created,
      draft: updateIndex(created.draft, created.key, {
        columns: [{ ...index.columns[0], descending: true, prefixLength: 4 }],
        comment: " 按付款时间 ",
      }),
    };
    expect(indexesOf(created.draft).indexes[4]).toEqual({
      originalName: "",
      name: "idx_orders_paid_at",
      type: "normal",
      columns: [{ name: "paid_at", descending: true, prefixLength: 4 }],
      comment: "按付款时间",
    });
  });

  it("改已有索引:带原名与改完的样子;选项关闭的不发", () => {
    let draft = initDraft(structure());
    const status = idx(draft, "idx_status");
    draft = updateIndex(draft, status.key, {
      type: "unique",
      columns: [
        ...status.columns,
        { ref: colKey(draft, "region"), expression: false, descending: false, prefixLength: 0 },
      ],
      options: {},
    });
    expect(indexesOf(draft).indexes[1]).toEqual({
      originalName: "idx_status",
      name: "idx_status",
      type: "unique",
      columns: [{ name: "status" }, { name: "region" }],
    });
  });

  it("删除列连同它的索引:索引不再交回,留给驱动随列删除;另行删除的索引列在 indexDrops", () => {
    let draft = initDraft(structure());
    draft = updateColumn(draft, colKey(draft, "note"), { deleted: true });
    draft = updateIndex(draft, idx(draft, "idx_expr").key, { deleted: true });
    const req = indexesOf(draft);
    expect(req.drops).toEqual(["note"]);
    expect(req.indexes.map((i: { originalName: string }) => i.originalName)).toEqual(["PRIMARY", "idx_status"]);
    expect(req.indexDrops).toEqual(["idx_expr"]);
  });

  it("改过的索引加进了随后删除的列:驱动按现状不会随列删它,请求里明确删除", () => {
    let draft = initDraft(structure());
    const status = idx(draft, "idx_status");
    draft = updateIndex(draft, status.key, {
      columns: [
        ...status.columns,
        { ref: colKey(draft, "region"), expression: false, descending: false, prefixLength: 0 },
      ],
    });
    draft = updateColumn(draft, colKey(draft, "region"), { deleted: true });
    expect(indexStatus(draft, idx(draft, "idx_status"))).toBe("deleted");
    expect(indexesOf(draft).indexDrops).toEqual(["idx_status"]);
  });

  it("改主键:主键索引换列", () => {
    let draft = initDraft(structure());
    const pk = idx(draft, "PRIMARY");
    draft = updateIndex(draft, pk.key, {
      columns: [...pk.columns, { ref: colKey(draft, "status"), expression: false, descending: false, prefixLength: 0 }],
    });
    expect(indexesOf(draft).indexes[0]).toEqual({
      originalName: "PRIMARY",
      name: "PRIMARY",
      type: "primary",
      columns: [{ name: "id" }, { name: "status" }],
    });
  });
});

describe("rebaseDraft 与索引", () => {
  function reread(base: TableStructure, patch: Partial<TableStructure>): TableStructure {
    return { ...base, ...patch };
  }

  it("已执行的新建索引成为已有索引,没执行的留在草稿里", () => {
    const base = structure();
    let draft = initDraft(base);
    const a = withNewIndex(draft, MYSQL, ["status"]);
    const b = withNewIndex(a.draft, MYSQL, ["region"]);
    draft = b.draft;
    const after = reread(base, {
      indexes: [
        ...base.indexes,
        { ...base.indexes[1], name: "idx_orders_status", options: {}, columns: [base.indexes[1].columns[0]] },
      ],
    });
    const rebased = rebaseDraft(draft, after);
    expect(rebased.indexes.map((i) => [i.original?.name ?? null, i.name, indexStatus(rebased, i)])).toEqual([
      ["PRIMARY", "PRIMARY", "unchanged"],
      ["idx_status", "idx_status", "unchanged"],
      ["idx_note", "idx_note", "unchanged"],
      ["idx_expr", "idx_expr", "unchanged"],
      ["idx_orders_status", "idx_orders_status", "unchanged"],
      [null, "idx_orders_region", "new"],
    ]);
  });

  it("改动的索引删掉了、没重建:按改完的样子作为新索引留下", () => {
    const base = structure();
    let draft = initDraft(base);
    draft = updateIndex(draft, idx(draft, "idx_status").key, { type: "unique" });
    const after = reread(base, { indexes: base.indexes.filter((i) => i.name !== "idx_status") });
    const rebased = rebaseDraft(draft, after);
    const pending = rebased.indexes.find((i) => i.name === "idx_status")!;
    expect(pending.original).toBeNull();
    expect(pending.type).toBe("unique");
    expect(json(buildAlterRequest(rebased, MYSQL)).indexes.at(-1)).toMatchObject({
      originalName: "",
      name: "idx_status",
      type: "unique",
    });
  });

  it("列改名已执行:索引里对这一列的引用跟着换到新的列", () => {
    const base = structure();
    let draft = initDraft(base);
    draft = updateColumn(draft, colKey(draft, "status"), { name: "state" });
    const created = withNewIndex(draft, MYSQL, ["state"]);
    const after = reread(base, {
      columns: base.columns.map((c) => (c.name === "status" ? { ...c, name: "state" } : c)),
      indexes: base.indexes.map((i) =>
        i.name === "idx_status" ? { ...i, columns: [{ ...i.columns[0], name: "state" }] } : i
      ),
    });
    const rebased = rebaseDraft(created.draft, after);
    expect(draftCounts(rebased)).toEqual({ added: 0, modified: 0, deleted: 0 });
    expect(indexCounts(rebased)).toEqual({ added: 1, modified: 0, deleted: 0 });
    const pending = rebased.indexes.find((i: DraftIndex) => !i.original)!;
    expect(indexDefinition(rebased, pending).columns.map((c) => c.name)).toEqual(["state"]);
  });
});
