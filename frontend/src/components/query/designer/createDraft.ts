import { dbdriver } from "../../../../wailsjs/go/models";
import { driverCanAlter, type DriverDescriptor } from "@/lib/dbDrivers";
import type { TableStructure } from "./model";
import { defaultText, type DraftColumn, type TableDraft } from "./draft";
import { createIndexes, syncAutoNames, type DraftIndex } from "./indexDraft";

// 新建表:设计器从一张空表开始,预置一列 id 与它上面的主键;保存时把草稿写成建表请求交给驱动。

/** 新建表的「读出的表」:什么都没有,索引能不能改看驱动有没有声明索引编辑。 */
export function createStructure(descriptor: DriverDescriptor | undefined): TableStructure {
  return {
    name: "",
    comment: "",
    columns: [],
    indexes: [],
    foreignKeys: [],
    takenIndexNames: [],
    indexesEditable: !!descriptor?.indexEditing,
  };
}

/**
 * 新建表的起点:一列 id(类型取驱动声明的主键默认类型,其次新列默认类型,都没有时留空由校验要求填写;
 * 不可空;驱动声明了自增时自增),驱动的索引编辑声明了主键类型时再加一条 id 上的主键索引(名字自动生成)。
 * 驱动没声明索引编辑时请求里表达不了主键,列上也不标。
 */
export function presetDraft(descriptor: DriverDescriptor | undefined): TableDraft {
  const types = descriptor?.columnTypes;
  const id: DraftColumn = {
    key: "n:1",
    original: null,
    deleted: false,
    name: "id",
    type: types?.primaryKey || types?.newColumn || "",
    nullable: false,
    default: { kind: "none" },
    comment: "",
    primaryKey: false,
    autoIncrement: !!descriptor?.autoIncrement,
  };
  const indexes: DraftIndex[] = descriptor?.indexEditing?.types.some((t) => t.id === "primary")
    ? [
        {
          key: "ni:2",
          original: null,
          deleted: false,
          name: "",
          type: "primary",
          columns: [{ ref: id.key, expression: false, descending: false, prefixLength: 0 }],
          include: [],
          options: {},
          comment: "",
          autoName: true,
        },
      ]
    : [];
  return syncAutoNames({
    original: { name: "", comment: "" },
    name: "",
    comment: "",
    columns: [id],
    indexes,
    takenIndexNames: [],
    nextId: 3,
  });
}

/** 草稿里决定建出什么样的表的部分(编号除外:加了又删掉的列不算改动)。 */
function content(draft: TableDraft): string {
  return JSON.stringify([draft.name, draft.comment, draft.columns, draft.indexes]);
}

/** 新建表的草稿与起点相比有没有改动(有改动的标签关闭前要拦下来问)。 */
export function createDraftDirty(draft: TableDraft, preset: TableDraft): boolean {
  return content(draft) !== content(preset);
}

/** 有 schema 的驱动建到某个 schema 里时的表名(schema.表);没选 schema 时是表名本身(驱动的默认 schema)。 */
export function qualifiedTableName(schema: string, name: string): string {
  const table = name.trim();
  return schema ? `${schema}.${table}` : table;
}

/**
 * 草稿交给驱动的建表请求,name 是要建的表名(有 schema 时带上,见 qualifiedTableName)。只发驱动声明了的字段:
 * 注释只在驱动能写(可改属性里有 table_comment / column_comment)且非空时发,默认值写法只在声明了
 * explicitDefaults 时发,自增只在声明了 autoIncrement 时发,索引(含主键)只在声明了 indexEditing 时发。
 */
export function buildCreateRequest(
  draft: TableDraft,
  descriptor: DriverDescriptor | undefined,
  name: string
): dbdriver.CreateTableRequest {
  const tableComments = driverCanAlter(descriptor, "table_comment");
  const columnComments = driverCanAlter(descriptor, "column_comment");
  const columns = draft.columns
    .filter((c) => !c.deleted)
    .map((c) => {
      const column: dbdriver.NewColumn = {
        name: c.name.trim(),
        type: c.type.trim(),
        nullable: c.nullable,
        defaultValue: defaultText(c.default),
      };
      if (columnComments && c.comment.trim() !== "") column.comment = c.comment.trim();
      // text 是没给写法的原文:不带写法,按旧规则由驱动猜。
      if (descriptor?.explicitDefaults && c.default.kind !== "text") column.defaultKind = c.default.kind;
      if (descriptor?.autoIncrement && c.autoIncrement) column.autoIncrement = true;
      return column;
    });
  const comment = draft.comment.trim();
  return dbdriver.CreateTableRequest.createFrom({
    name,
    columns,
    ...(tableComments && comment !== "" ? { comment } : {}),
    ...(descriptor?.indexEditing ? { indexes: createIndexes(draft) } : {}),
  });
}
