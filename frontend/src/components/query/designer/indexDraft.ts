import type { dbdriver } from "../../../../wailsjs/go/models";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { DesignerIndex } from "./model";
import type { ColumnStatus, DraftCounts, DraftIssue, TableDraft } from "./draft";

// 草稿里的索引:改动标记、随列删除、自动命名、锁定、校验与交给驱动的请求。只在驱动声明了索引编辑
// (Descriptor.indexEditing)时能改;没声明时索引照样进草稿(读自表元数据),只用来得出随列删除的索引。

/**
 * 索引里的一列:列引用草稿里的列(按 key,列改名后跟着新名字),表达式是原文(读得出、写不了)。
 */
export interface DraftIndexColumn {
  /** 列引用时是草稿列的 key;表达式时是表达式原文。 */
  ref: string;
  expression: boolean;
  descending: boolean;
  prefixLength: number;
}

/** 草稿里的一个索引:original 是读出时的样子,新增的为 null;已有索引删除只是标记,保存前可恢复。 */
export interface DraftIndex {
  key: string;
  original: DesignerIndex | null;
  /** 用户删除了它(随列删除不在这里,由列得出)。 */
  deleted: boolean;
  name: string;
  type: string;
  columns: DraftIndexColumn[];
  /** INCLUDE 列(草稿列的 key);只有读出的索引有,带它的索引只读。 */
  include: string[];
  options: Record<string, unknown>;
  comment: string;
  /** 新索引的名字按 idx_表_列 生成并跟随;手动改过名字后为 false。 */
  autoName: boolean;
}

export type DraftIndexPatch = Partial<
  Pick<DraftIndex, "deleted" | "name" | "type" | "columns" | "options" | "comment">
>;

/** 索引上可改的一项;选项按键,如 option:method。 */
export type IndexField = "name" | "type" | "columns" | "comment" | `option:${string}`;

/** 已有列在草稿里的 key(initDraft 与 rebaseDraft 都按读出的列名给出)。 */
export function existingColumnKey(name: string): string {
  return `e:${name}`;
}

/** 读出的索引放进草稿。 */
export function existingIndex(index: DesignerIndex): DraftIndex {
  return {
    key: `ei:${index.name}`,
    original: index,
    deleted: false,
    name: index.name,
    type: index.type,
    columns: index.columns.map((c) => ({
      ref: c.expression ? c.name : existingColumnKey(c.name),
      expression: c.expression,
      descending: c.descending,
      prefixLength: c.prefixLength,
    })),
    include: index.include.map(existingColumnKey),
    options: { ...index.options },
    comment: index.comment,
    autoName: false,
  };
}

function columnByKey(draft: TableDraft, key: string) {
  return draft.columns.find((c) => c.key === key);
}

/** 索引里一列显示的名字:列引用是草稿里这一列现在的名字,表达式是原文。 */
export function indexColumnName(draft: TableDraft, column: DraftIndexColumn): string {
  if (column.expression) return column.ref;
  return columnByKey(draft, column.ref)?.name.trim() ?? column.ref;
}

/** 新索引的名字:idx_表_列(列按次序以 _ 连接,表达式不参与)。 */
export function autoIndexName(draft: TableDraft, index: DraftIndex): string {
  const parts = [
    draft.name.trim(),
    ...index.columns.filter((c) => !c.expression).map((c) => indexColumnName(draft, c)),
  ];
  return ["idx", ...parts.filter((p) => p !== "")].join("_");
}

/** 让自动命名的新索引跟上表名与列的变化;名字没变时原样返回同一份草稿。 */
export function syncAutoNames(draft: TableDraft): TableDraft {
  let changed = false;
  const indexes = draft.indexes.map((index) => {
    if (!index.autoName) return index;
    const name = autoIndexName(draft, index);
    if (name === index.name) return index;
    changed = true;
    return { ...index, name };
  });
  return changed ? { ...draft, indexes } : draft;
}

/** 改表名(新索引的自动名字随之变化)。 */
export function setTableName(draft: TableDraft, name: string): TableDraft {
  return syncAutoNames({ ...draft, name });
}

/** 新索引的类型:声明里的普通索引,没有时取第一个不是主键的类型。 */
function defaultIndexType(descriptor: DriverDescriptor | undefined): string {
  const types = descriptor?.indexEditing?.types ?? [];
  if (types.some((t) => t.id === "normal")) return "normal";
  return (types.find((t) => t.id !== "primary") ?? types[0])?.id ?? "normal";
}

/** 加一个索引(没有列,名字自动生成),返回它的 key。 */
export function addIndex(
  draft: TableDraft,
  descriptor: DriverDescriptor | undefined
): { draft: TableDraft; key: string } {
  const key = `ni:${draft.nextId}`;
  const index: DraftIndex = {
    key,
    original: null,
    deleted: false,
    name: "",
    type: defaultIndexType(descriptor),
    columns: [],
    include: [],
    options: {},
    comment: "",
    autoName: true,
  };
  return {
    draft: syncAutoNames({ ...draft, indexes: [...draft.indexes, index], nextId: draft.nextId + 1 }),
    key,
  };
}

/** 改一个索引;删除新增的索引是直接移除,删除已有索引只做标记。手动给了名字后不再自动命名。 */
export function updateIndex(draft: TableDraft, key: string, patch: DraftIndexPatch): TableDraft {
  const target = draft.indexes.find((i) => i.key === key);
  if (!target) return draft;
  if (patch.deleted && !target.original) {
    return { ...draft, indexes: draft.indexes.filter((i) => i.key !== key) };
  }
  const next = { ...target, ...patch, autoName: target.autoName && patch.name === undefined };
  return syncAutoNames({ ...draft, indexes: draft.indexes.map((i) => (i.key === key ? next : i)) });
}

/** 从各索引里拿掉对一列的引用(移除新列时)。 */
export function removeColumnRefs(indexes: DraftIndex[], columnKey: string): DraftIndex[] {
  return indexes.map((index) => {
    const columns = index.columns.filter((c) => c.expression || c.ref !== columnKey);
    const include = index.include.filter((k) => k !== columnKey);
    return columns.length === index.columns.length && include.length === index.include.length
      ? index
      : { ...index, columns, include };
  });
}

/** 关闭的开关、空的枚举与缺省都表示「默认」,比较与发送时一视同仁。 */
function optionValue(value: unknown): unknown {
  return value === false || value === "" || value === null ? undefined : value;
}

function normalizedOptions(options: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(options)) {
    const v = optionValue(value);
    if (v !== undefined) out[key] = v;
  }
  return out;
}

function sameIndexColumns(a: DraftIndexColumn[], b: DraftIndexColumn[]): boolean {
  return (
    a.length === b.length &&
    a.every(
      (c, i) =>
        c.ref === b[i].ref &&
        c.expression === b[i].expression &&
        c.descending === b[i].descending &&
        c.prefixLength === b[i].prefixLength
    )
  );
}

/**
 * 已有索引上改过的项,按编辑区的次序。名字与注释去掉首尾空白比较,列按引用比较(列改名不算改了索引),
 * 选项按「默认」归一后比较。
 */
export function changedIndexFields(index: DraftIndex): IndexField[] {
  if (!index.original) return [];
  const o = existingIndex(index.original);
  const out: IndexField[] = [];
  if (index.name.trim() !== o.name.trim()) out.push("name");
  if (index.type !== o.type) out.push("type");
  if (!sameIndexColumns(index.columns, o.columns)) out.push("columns");
  const keys = new Set([...Object.keys(index.options), ...Object.keys(o.options)]);
  for (const key of keys) {
    if (optionValue(index.options[key]) !== optionValue(o.options[key])) out.push(`option:${key}`);
  }
  if (index.comment.trim() !== o.comment.trim()) out.push("comment");
  return out;
}

/** 草稿里删除了的已有列:key 到原名。 */
function deletedColumns(draft: TableDraft): Map<string, string> {
  return new Map(draft.columns.filter((c) => c.original && c.deleted).map((c) => [c.key, c.original!.name]));
}

/** 一个索引引用的列(草稿列的 key):键列(表达式除外)与 INCLUDE 列。 */
function referencedKeys(index: Pick<DraftIndex, "columns" | "include">): string[] {
  return [...index.columns.filter((c) => !c.expression).map((c) => c.ref), ...index.include];
}

function namesIn(keys: string[], deleted: Map<string, string>): string[] {
  return [...new Set(keys.filter((k) => deleted.has(k)).map((k) => deleted.get(k)!))];
}

/**
 * 索引随哪些删除的列一起删除(列的原名):现在引用的列,以及(已有索引)读出时引用的列——驱动按表里
 * 现在的索引随列删除,改过的索引也一样。没有时为空。
 */
export function indexDroppedWith(draft: TableDraft, index: DraftIndex): string[] {
  const deleted = deletedColumns(draft);
  if (deleted.size === 0) return [];
  const keys = referencedKeys(index);
  if (index.original) keys.push(...referencedKeys(existingIndex(index.original)));
  return namesIn(keys, deleted);
}

/** 已有索引读出时引用了删除的列:驱动会随列删掉它,不必在请求里列出。 */
function droppedByDriver(draft: TableDraft, index: DraftIndex): boolean {
  return !!index.original && namesIn(referencedKeys(existingIndex(index.original)), deletedColumns(draft)).length > 0;
}

/** 索引被删除:用户删的,或随列删除。 */
export function indexDeleted(draft: TableDraft, index: DraftIndex): boolean {
  return index.deleted || indexDroppedWith(draft, index).length > 0;
}

export function indexStatus(draft: TableDraft, index: DraftIndex): ColumnStatus {
  if (indexDeleted(draft, index)) return "deleted";
  if (!index.original) return "new";
  return changedIndexFields(index).length > 0 ? "modified" : "unchanged";
}

/** 索引的改动计数:随列删除的新索引什么也不用做,不计。 */
export function indexCounts(draft: TableDraft): DraftCounts {
  const counts: DraftCounts = { added: 0, modified: 0, deleted: 0 };
  for (const index of draft.indexes) {
    const status = indexStatus(draft, index);
    if (status === "new") counts.added++;
    else if (status === "modified") counts.modified++;
    else if (status === "deleted" && index.original) counts.deleted++;
  }
  return counts;
}

/** 草稿里将删除的已有索引(原名):用户删的与随列删除的(删除警告用)。 */
export function droppedIndexes(draft: TableDraft): string[] {
  return draft.indexes.filter((i) => i.original && indexDeleted(draft, i)).map((i) => i.original!.name);
}

/** 已有索引为什么不能改:驱动无法完整表示、不可删除(也就不能重建)、或驱动不能改主键。 */
export type IndexLock = "readOnly" | "undroppable" | "primaryKey";

export function indexLock(descriptor: DriverDescriptor | undefined, index: DraftIndex): IndexLock | null {
  const o = index.original;
  if (!o) return null;
  if (o.readOnlyReason) return "readOnly";
  if (o.undroppable) return "undroppable";
  if (o.type === "primary" && !descriptor?.indexEditing?.alterPrimaryKey) return "primaryKey";
  return null;
}

/** 索引能不能在编辑区里改:驱动声明了索引编辑,且不是锁定的已有索引。 */
export function indexEditable(descriptor: DriverDescriptor | undefined, index: DraftIndex): boolean {
  return !!descriptor?.indexEditing && indexLock(descriptor, index) === null;
}

/** 索引能不能删除:只读索引可以删,不可删除的与驱动不能改的主键不行。 */
export function indexDroppable(descriptor: DriverDescriptor | undefined, index: DraftIndex): boolean {
  if (!descriptor?.indexEditing) return false;
  const lock = indexLock(descriptor, index);
  return lock === null || lock === "readOnly";
}

/** 编辑区的类型选择:声明的类型按次序;「主键」只在驱动声明主键可改时可选。 */
export function indexTypeChoices(
  descriptor: DriverDescriptor | undefined
): { id: string; label: string; disabled: boolean }[] {
  const editing = descriptor?.indexEditing;
  return (editing?.types ?? []).map((t) => ({
    id: t.id,
    label: t.label,
    disabled: t.id === "primary" && !editing?.alterPrimaryKey,
  }));
}

/** 索引可选的列:草稿里没删的列(含尚未保存的新列),按现在的名字;没起名的新列不能选。 */
export function indexColumnChoices(draft: TableDraft): { key: string; name: string; type: string; isNew: boolean }[] {
  return draft.columns
    .filter((c) => !c.deleted && c.name.trim() !== "")
    .map((c) => ({ key: c.key, name: c.name.trim(), type: c.type, isNew: !c.original }));
}

/** 显示用的定义:列按草稿里现在的名字。 */
export function indexDefinition(draft: TableDraft, index: DraftIndex): DesignerIndex {
  return {
    name: index.name,
    type: index.type,
    columns: index.columns.map((c) => ({
      name: indexColumnName(draft, c),
      expression: c.expression,
      descending: c.descending,
      prefixLength: c.prefixLength,
    })),
    include: index.include.map((k) => columnByKey(draft, k)?.name.trim() ?? k),
    options: index.options,
    comment: index.comment,
    readOnlyReason: index.original?.readOnlyReason ?? "",
    undroppable: index.original?.undroppable ?? false,
  };
}

/** 草稿里主键索引引用的列(字段表的主键标记)。 */
export function primaryKeyColumnKeys(draft: TableDraft): Set<string> {
  const keys = new Set<string>();
  for (const index of draft.indexes) {
    if (index.type !== "primary" || indexDeleted(draft, index)) continue;
    for (const c of index.columns) if (!c.expression) keys.add(c.ref);
  }
  return keys;
}

function requestColumn(name: string, column: Omit<DraftIndexColumn, "ref">): dbdriver.IndexColumn {
  const out: dbdriver.IndexColumn = { name };
  if (column.expression) out.expression = true;
  if (column.descending) out.descending = true;
  if (column.prefixLength > 0) out.prefixLength = column.prefixLength;
  return out;
}

/** 请求里一个索引的 JSON 样子(AlterTableRequest.createFrom 再转成 dbdriver.AlterIndex)。 */
export type RequestIndex = Omit<dbdriver.AlterIndex, "convertValues">;

/** 请求里的一个索引;细项只在有值时带上(没声明的能力不会出现在 JSON 里)。 */
function requestIndex(
  originalName: string,
  definition: Pick<DesignerIndex, "name" | "type" | "comment" | "options"> & {
    columns: dbdriver.IndexColumn[];
    include: string[];
  }
): RequestIndex {
  const out: RequestIndex = {
    originalName,
    name: definition.name,
    type: definition.type,
    columns: definition.columns,
  };
  if (definition.include.length > 0) out.include = definition.include;
  if (Object.keys(definition.options).length > 0) out.options = definition.options;
  if (definition.comment !== "") out.comment = definition.comment;
  return out;
}

/** 草稿列现在的名字;列引用找不到时(不会发生)原样。 */
function currentName(draft: TableDraft, key: string, fallback: string): string {
  return columnByKey(draft, key)?.name.trim() ?? fallback;
}

/**
 * 交给驱动的索引改动(见 dbdriver.AlterTableRequest):
 * - indexes:保留的已有索引(带原名)与新索引。没改的已有索引按读出的样子交回、列换成改名之后的名字,
 *   驱动比较之后不产生语句;改过的按草稿的样子(选项按「默认」归一)。
 * - indexDrops:用户删除的已有索引。随列删除的不列出:驱动按表里现在的索引补上删除(不可删除的留给数据库
 *   随列丢掉),而不可删除的、驱动不能改的主键列在 indexDrops 里反而会被拒;只有改过之后才引用被删列的
 *   索引,驱动按现状不会随列删它,这里列出。
 */
export function alterIndexes(draft: TableDraft): { indexes: RequestIndex[]; indexDrops: string[] } {
  const indexes: RequestIndex[] = [];
  const indexDrops: string[] = [];
  for (const index of draft.indexes) {
    const o = index.original;
    if (indexDeleted(draft, index)) {
      if (o && (index.deleted || !droppedByDriver(draft, index))) indexDrops.push(o.name);
      continue;
    }
    if (o && changedIndexFields(index).length === 0) {
      indexes.push(
        requestIndex(o.name, {
          ...o,
          columns: o.columns.map((c) =>
            requestColumn(c.expression ? c.name : currentName(draft, existingColumnKey(c.name), c.name), c)
          ),
          include: o.include.map((name) => currentName(draft, existingColumnKey(name), name)),
        })
      );
      continue;
    }
    indexes.push(
      requestIndex(o?.name ?? "", {
        name: index.name.trim(),
        type: index.type,
        columns: index.columns.map((c) => requestColumn(indexColumnName(draft, c), c)),
        include: index.include.map((k) => currentName(draft, k, k)),
        options: normalizedOptions(index.options),
        comment: index.comment.trim(),
      })
    );
  }
  return { indexes, indexDrops };
}

/**
 * 重新读出表之后的草稿索引(见 rebaseDraft):columnKeys 是草稿列的 key 到重新读出之后的 key(删除已执行的
 * 列不在其中)。已有索引按原名找,找不到再按目标名找(改名或重建已执行);都找不到时,删除的(删除已执行)
 * 去掉,改过的(删除已执行、重建没执行)按改完的样子成为新索引。新索引在读出的表里有同名索引时说明已建,
 * 按读出的样子成为已有索引。读出的表里草稿没有的索引按读出的样子列入。次序同 initDraft。
 */
export function rebaseIndexes(
  draft: TableDraft,
  indexes: DesignerIndex[],
  columnKeys: Map<string, string>
): { indexes: DraftIndex[]; nextId: number } {
  let nextId = draft.nextId;
  const remap = (index: DraftIndex): DraftIndex => ({
    ...index,
    columns: index.columns
      .filter((c) => c.expression || columnKeys.has(c.ref))
      .map((c) => (c.expression ? c : { ...c, ref: columnKeys.get(c.ref)! })),
    include: index.include.filter((k) => columnKeys.has(k)).map((k) => columnKeys.get(k)!),
  });
  const byName = new Map(indexes.map((i) => [i.name, i]));
  const claimed = new Map<string, DraftIndex>();
  const pending: DraftIndex[] = [];
  for (const index of draft.indexes) {
    const target = index.name.trim();
    if (!index.original) {
      const created = byName.get(target);
      if (created && !claimed.has(created.name)) claimed.set(created.name, existingIndex(created));
      else if (!indexDeleted(draft, index)) pending.push(remap(index));
      continue;
    }
    const current = byName.get(index.original.name) ?? (indexDeleted(draft, index) ? undefined : byName.get(target));
    if (current && !claimed.has(current.name)) {
      // 没改过的按读出的样子;改过的保留改动,与读出的样子比较。
      const kept =
        changedIndexFields(index).length === 0
          ? { ...existingIndex(current), deleted: index.deleted }
          : { ...remap(index), key: `ei:${current.name}`, original: current };
      claimed.set(current.name, kept);
    } else if (!indexDeleted(draft, index)) {
      pending.push({ ...remap(index), key: `ni:${nextId++}`, original: null, autoName: false });
    }
  }
  return {
    indexes: [...indexes.map((i) => claimed.get(i.name) ?? existingIndex(i)), ...pending],
    nextId,
  };
}

/** 名字与驱动的折叠规则不符:折叠为大写时含小写字母,折叠为小写时含大写字母。 */
export function violatesFolding(name: string, folding: string): boolean {
  if (folding === "upper") return name !== name.toUpperCase();
  if (folding === "lower") return name !== name.toLowerCase();
  return false;
}

/**
 * 校验索引(只看新建或改过的,读出时就有的不报)。错误:没有列、名字为空、名字在唯一范围内重名(本表的
 * 其他索引与范围内其他表的索引名,不分大小写;删除的索引不算)、两个主键、声明为独占的开关打开在两个
 * 索引上。提醒:新起或改过的名字与驱动的折叠规则不符。
 */
export function validateIndexes(
  draft: TableDraft,
  descriptor: DriverDescriptor | undefined
): { errors: DraftIssue[]; warnings: DraftIssue[] } {
  const errors: DraftIssue[] = [];
  const warnings: DraftIssue[] = [];
  const folding = descriptor?.nameFolding ?? "";
  const live = draft.indexes
    .map((index, i) => ({ index, row: i + 1 }))
    .filter(({ index }) => !indexDeleted(draft, index));
  const touched = (index: DraftIndex) => !index.original || changedIndexFields(index).length > 0;
  const taken = new Set(draft.takenIndexNames.map((n) => n.toLowerCase()));
  const issue = (
    code: DraftIssue["code"],
    index: DraftIndex,
    row: number,
    field: IndexField,
    extra: Partial<DraftIssue> = {}
  ): DraftIssue => ({
    severity: "error",
    code,
    target: { kind: "index", key: index.key, field },
    row,
    name: index.name.trim(),
    ...extra,
  });

  live.forEach(({ index, row }, i) => {
    if (!touched(index)) return;
    const name = index.name.trim();
    if (!name) {
      errors.push(issue("indexNameEmpty", index, row, "name"));
    } else {
      // 一对重名只报一次:报在改过的那个上,两个都改过时报在后一个上。
      const lower = name.toLowerCase();
      const other = live.find(
        (o, j) => j !== i && o.index.name.trim().toLowerCase() === lower && (j < i || !touched(o.index))
      );
      if (other) errors.push(issue("indexNameDuplicate", index, row, "name", { duplicateOf: other.row }));
      else if (taken.has(lower)) errors.push(issue("indexNameTaken", index, row, "name"));
      const named = !index.original || changedIndexFields(index).includes("name");
      if (named && violatesFolding(name, folding)) {
        warnings.push({ ...issue("nameFolding", index, row, "name", { folding }), severity: "warning" });
      }
    }
    if (index.columns.length === 0) errors.push(issue("indexColumnsEmpty", index, row, "columns"));
  });

  // 冲突报在改过的索引上;都是改过的,第一个不报。
  const conflicting = (members: { index: DraftIndex; row: number }[]) => {
    if (members.length < 2) return [];
    const changed = members.filter(({ index }) => touched(index));
    return changed.length === members.length ? changed.slice(1) : changed;
  };
  for (const { index, row } of conflicting(live.filter(({ index }) => index.type === "primary"))) {
    errors.push(issue("primaryKeyDuplicate", index, row, "type"));
  }
  for (const option of descriptor?.indexEditing?.options ?? []) {
    if (!option.exclusive) continue;
    const on = live.filter(({ index }) => index.options[option.key] === true);
    for (const { index, row } of conflicting(on)) {
      errors.push(issue("indexOptionExclusive", index, row, `option:${option.key}`, { option: option.label }));
    }
  }
  return { errors, warnings };
}
