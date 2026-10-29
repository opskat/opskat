import { describe, expect, it } from "vitest";
import { BUILTIN_DRIVERS, fakeAlterDriver } from "@/__tests__/dbDriverFixtures";
import { addColumn, autoIncrementEditable, updateColumn, validateDraft, type TableDraft } from "../draft";
import { addIndex, indexTypeChoices, setTableName, updateIndex } from "../indexDraft";
import { buildCreateRequest, createDraftDirty, presetDraft, qualifiedTableName } from "../createDraft";

const MYSQL = BUILTIN_DRIVERS.find((d) => d.id === "mysql")!;
const PG = BUILTIN_DRIVERS.find((d) => d.id === "postgresql")!;
const SQLITE = BUILTIN_DRIVERS.find((d) => d.id === "sqlite")!;
/** 没声明任何表设计器能力的驱动(旧扩展的样子)。 */
const LEGACY = fakeAlterDriver({ alterTableAttributes: [] });

/** 请求按 JSON 的样子(真正传给后端的)。 */
const json = (value: unknown) => JSON.parse(JSON.stringify(value));

/** 预置表加一列 name(类型取新列默认类型)与它上面的一个新索引,表名 users。 */
function presetPlusColumnAndIndex(descriptor = MYSQL): TableDraft {
  let draft = setTableName(presetDraft(descriptor), "users");
  const added = addColumn(draft, descriptor);
  draft = updateColumn(added.draft, added.key, { name: "email" });
  const index = addIndex(draft, descriptor);
  return updateIndex(index.draft, index.key, {
    columns: [{ ref: added.key, expression: false, descending: false, prefixLength: 0 }],
  });
}

describe("presetDraft", () => {
  it("声明了主键类型、自增与主键索引:预置自增的 id 与它上面的主键索引", () => {
    const draft = presetDraft(MYSQL);
    expect(draft.columns.map((c) => [c.name, c.type, c.nullable, c.autoIncrement, c.original])).toEqual([
      ["id", "BIGINT", false, true, null],
    ]);
    expect(draft.indexes.map((i) => [i.type, i.columns.map((c) => c.ref)])).toEqual([
      ["primary", [draft.columns[0].key]],
    ]);
    // 新表的名字还没填:没有问题以外的报错(表名为空)。
    expect(validateDraft(draft, MYSQL).map((i) => i.code)).toEqual(["tableNameEmpty"]);
  });

  it("自增只能在新表上设的驱动(SQLite)同样预置自增", () => {
    const draft = presetDraft(SQLITE);
    expect(draft.columns[0]).toMatchObject({ name: "id", type: "INTEGER", autoIncrement: true });
  });

  it("没声明自增:只预置主键列,不自增", () => {
    const noAuto = fakeAlterDriver({ ...MYSQL, autoIncrement: false });
    expect(presetDraft(noAuto).columns[0]).toMatchObject({ name: "id", autoIncrement: false });
    expect(presetDraft(noAuto).indexes).toHaveLength(1);
  });

  it("没声明主键类型时取新列默认类型;都没声明时类型留空(校验要求填写)", () => {
    const newColumnOnly = fakeAlterDriver({ columnTypes: { groups: [], newColumn: "text", primaryKey: "" } as never });
    expect(presetDraft(newColumnOnly).columns[0].type).toBe("text");
    expect(presetDraft(LEGACY).columns[0].type).toBe("");
    expect(validateDraft(setTableName(presetDraft(LEGACY), "t"), LEGACY).map((i) => i.code)).toEqual([
      "columnTypeEmpty",
    ]);
  });

  it("没声明索引编辑:没有主键索引(请求表达不了),列上也不标主键", () => {
    const draft = presetDraft(LEGACY);
    expect(draft.indexes).toEqual([]);
    expect(draft.columns[0].primaryKey).toBe(false);
  });
});

describe("createDraftDirty", () => {
  it("与预置的一样不算改动;改了任何东西都算,改回去又不算", () => {
    const preset = presetDraft(MYSQL);
    expect(createDraftDirty(preset, preset)).toBe(false);
    const named = setTableName(preset, "users");
    expect(createDraftDirty(named, preset)).toBe(true);
    expect(createDraftDirty(setTableName(named, ""), preset)).toBe(false);
    const added = addColumn(preset, MYSQL);
    expect(createDraftDirty(added.draft, preset)).toBe(true);
    // 加了又删掉:编号前进了,内容与预置一样。
    expect(createDraftDirty(updateColumn(added.draft, added.key, { deleted: true }), preset)).toBe(false);
    expect(createDraftDirty({ ...preset, comment: "x" }, preset)).toBe(true);
  });
});

describe("buildCreateRequest", () => {
  it("MySQL:写法、自增、注释与索引(主键是一项 primary 索引)", () => {
    let draft = presetPlusColumnAndIndex(MYSQL);
    draft = { ...draft, comment: " 用户 " };
    draft = updateColumn(draft, draft.columns[1].key, { comment: "邮箱", default: { kind: "value", text: "" } });
    expect(json(buildCreateRequest(draft, MYSQL, "users"))).toEqual({
      name: "users",
      comment: "用户",
      columns: [
        { name: "id", type: "BIGINT", nullable: false, defaultValue: "", defaultKind: "none", autoIncrement: true },
        {
          name: "email",
          type: "VARCHAR(255)",
          nullable: true,
          defaultValue: "",
          defaultKind: "value",
          comment: "邮箱",
        },
      ],
      indexes: [
        { name: "idx_users_id", type: "primary", columns: [{ name: "id" }] },
        { name: "idx_users_email", type: "normal", columns: [{ name: "email" }] },
      ],
    });
  });

  it("改表能写注释、但没声明建表注释的驱动(如已发布的 Oracle 扩展):注释不发送", () => {
    const oracleLike = fakeAlterDriver({
      capabilities: ["create_table", "alter_table"],
      alterTableAttributes: ["table_comment", "column_comment"],
    });
    let draft = setTableName(presetDraft(oracleLike), "users");
    draft = updateColumn({ ...draft, comment: "x" }, draft.columns[0].key, { comment: "y", type: "NUMBER" });
    const request = json(buildCreateRequest(draft, oracleLike, "users"));
    expect(request.comment).toBeUndefined();
    expect(request.columns).toEqual([{ name: "id", type: "NUMBER", nullable: false, defaultValue: "" }]);
  });

  it("PostgreSQL:表名带上 schema", () => {
    const draft = presetPlusColumnAndIndex(PG);
    const request = json(buildCreateRequest(draft, PG, qualifiedTableName("public", draft.name)));
    expect(request.name).toBe("public.users");
    expect(request.columns[0]).toEqual({
      name: "id",
      type: "bigint",
      nullable: false,
      defaultValue: "",
      defaultKind: "none",
      autoIncrement: true,
    });
    expect(request.indexes.map((i: { name: string }) => i.name)).toEqual(["idx_users_id", "idx_users_email"]);
  });

  it("SQLite:没有注释可写,注释不发送", () => {
    let draft = presetPlusColumnAndIndex(SQLITE);
    draft = updateColumn({ ...draft, comment: "x" }, draft.columns[1].key, { comment: "y" });
    const request = json(buildCreateRequest(draft, SQLITE, "users"));
    expect(request.comment).toBeUndefined();
    expect(request.columns).toEqual([
      { name: "id", type: "INTEGER", nullable: false, defaultValue: "", defaultKind: "none", autoIncrement: true },
      { name: "email", type: "TEXT", nullable: true, defaultValue: "", defaultKind: "none" },
    ]);
    expect(request.indexes).toEqual([
      { name: "idx_users_id", type: "primary", columns: [{ name: "id" }] },
      { name: "idx_users_email", type: "normal", columns: [{ name: "email" }] },
    ]);
  });

  it("没声明任何能力的驱动:只发名称、类型、可空与默认值原文", () => {
    let draft = setTableName(presetDraft(LEGACY), "t");
    draft = updateColumn(draft, draft.columns[0].key, { type: "NUMBER", default: { kind: "text", text: "1" } });
    expect(json(buildCreateRequest(draft, LEGACY, "t"))).toEqual({
      name: "t",
      columns: [{ name: "id", type: "NUMBER", nullable: false, defaultValue: "1" }],
    });
  });

  it("删掉的新列与它的索引都不在请求里", () => {
    let draft = presetPlusColumnAndIndex(MYSQL);
    draft = updateColumn(draft, draft.columns[1].key, { deleted: true });
    const request = json(buildCreateRequest(draft, MYSQL, "users"));
    expect(request.columns.map((c: { name: string }) => c.name)).toEqual(["id"]);
    // 索引没了列,留在草稿里(校验要求补列),请求里照草稿发。
    expect(request.indexes.map((i: { columns: unknown[] }) => i.columns.length)).toEqual([1, 0]);
  });
});

describe("新建表时的例外", () => {
  it("「主键」类型在新建表时总可选(驱动不能改已有主键也一样)", () => {
    expect(indexTypeChoices(SQLITE).find((c) => c.id === "primary")!.disabled).toBe(true);
    expect(indexTypeChoices(SQLITE, true).find((c) => c.id === "primary")!.disabled).toBe(false);
  });

  it("自增只能在新表上设的驱动:新建表的新列可勾,已有表的新列不可勾", () => {
    const column = presetDraft(SQLITE).columns[0];
    expect(autoIncrementEditable(SQLITE, column, true)).toBe(true);
    expect(autoIncrementEditable(SQLITE, column, false)).toBe(false);
    expect(autoIncrementEditable(MYSQL, column, false)).toBe(true);
    expect(autoIncrementEditable(LEGACY, column, true)).toBe(false);
  });
});
