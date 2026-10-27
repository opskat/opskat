import { useCallback, useMemo, useState } from "react";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import type { TableStructure } from "./model";
import {
  addColumn as addDraftColumn,
  buildAlterRequest,
  draftCounts,
  initDraft,
  isDraftDirty,
  rebaseDraft,
  revertField as revertDraftField,
  updateColumn as updateDraftColumn,
  validateDraft,
  type ColumnField,
  type DraftColumnPatch,
  type TableDraft,
} from "./draft";

/**
 * 设计器的草稿状态:从读出的表开始,记下表名、表注释、各列的改动与删除标记。读出的表换了(重新读取)
 * 时草稿重新开始。派生出改动计数、是否有改动、校验结果与交给驱动的改表请求。
 * rebaseFrom 是保存停在中途时的草稿:开始时把它搬到读出的表上(已生效的不再标记,没执行的留下)。
 */
export function useTableDraft(
  structure: TableStructure,
  descriptor: DriverDescriptor | undefined,
  rebaseFrom?: TableDraft | null
) {
  const [state, setState] = useState(() => ({
    structure,
    draft: rebaseFrom ? rebaseDraft(rebaseFrom, structure) : initDraft(structure),
  }));
  let draft = state.draft;
  if (state.structure !== structure) {
    // 渲染期对比上次的表:换了就丢弃草稿(替代 effect 里的同步 setState)。
    draft = initDraft(structure);
    setState({ structure, draft });
  }

  const edit = useCallback((fn: (d: TableDraft) => TableDraft) => {
    setState((s) => ({ ...s, draft: fn(s.draft) }));
  }, []);

  const setTableName = useCallback((name: string) => edit((d) => ({ ...d, name })), [edit]);
  const setTableComment = useCallback((comment: string) => edit((d) => ({ ...d, comment })), [edit]);
  const updateColumn = useCallback(
    (key: string, patch: DraftColumnPatch) => edit((d) => updateDraftColumn(d, key, patch)),
    [edit]
  );
  const deleteColumn = useCallback((key: string) => updateColumn(key, { deleted: true }), [updateColumn]);
  const restoreColumn = useCallback((key: string) => updateColumn(key, { deleted: false }), [updateColumn]);
  const revertField = useCallback(
    (key: string, field: ColumnField) =>
      edit((d) => ({ ...d, columns: d.columns.map((c) => (c.key === key ? revertDraftField(c, field) : c)) })),
    [edit]
  );
  const revertAll = useCallback(() => setState((s) => ({ ...s, draft: initDraft(s.structure) })), []);

  /** 加一列,返回它的 key(新列的 key 由草稿的编号决定,这里按当前草稿算出)。 */
  const addColumn = useCallback((): string => {
    const key = addDraftColumn(draft, descriptor).key;
    edit((d) => addDraftColumn(d, descriptor).draft);
    return key;
  }, [draft, descriptor, edit]);

  const counts = useMemo(() => draftCounts(draft), [draft]);
  const isDirty = useMemo(() => isDraftDirty(draft), [draft]);
  const issues = useMemo(() => validateDraft(draft, descriptor), [draft, descriptor]);
  const request = useMemo(() => buildAlterRequest(draft, descriptor), [draft, descriptor]);

  return {
    draft,
    counts,
    isDirty,
    issues,
    request,
    setTableName,
    setTableComment,
    addColumn,
    updateColumn,
    deleteColumn,
    restoreColumn,
    revertField,
    revertAll,
  };
}

export type TableDraftState = ReturnType<typeof useTableDraft>;
