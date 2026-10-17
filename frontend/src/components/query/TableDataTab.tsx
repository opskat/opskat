import { memo, useEffect, useState, useCallback, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { FileCode2, Copy, TriangleAlert, Download, PanelRight } from "lucide-react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
} from "@opskat/ui";
import { CodeEditor } from "@/components/CodeEditor";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { useQueryStore } from "@/stores/queryStore";
import { isMac, formatModKey } from "@/stores/shortcutStore";
import {
  CountTableRows,
  ExecuteSQL,
  GetGridEditStatements,
  GetRowStatements,
  GetTableDDL,
  GetTableMetadata,
  OpenTable,
  QueryTableRows,
} from "../../../wailsjs/go/query/Query";
import { dbdriver, query_svc } from "../../../wailsjs/go/models";
import {
  QueryResultTable,
  CellEdit,
  SortDir,
  type CopyAsFormat,
  type FocusCellRequest,
  type RowDensity,
} from "./QueryResultTable";
import { SqlPreviewDialog } from "./SqlPreviewDialog";
import { UnsavedEditsDialog, type UnsavedEditSummaryItem } from "./UnsavedEditsDialog";
import { driverHasCapability, findDatabaseDriver, useDatabaseDrivers } from "@/lib/dbDrivers";
import { ImportTableDataDialog } from "./ImportTableDataDialog";
import type { ImportColumnCategory } from "@/lib/tableImport";
import { ExportTableDataDialog } from "./ExportTableDataDialog";
import { TableFilterBuilder } from "./TableFilterBuilder";
import { TableDataStatusBar, TableEditorToolbar, type TableExportFormat } from "./TableEditorToolbar";
import { QueryErrorPanel } from "./QueryErrorPanel";
import { QueryResultJsonView } from "./QueryResultJsonView";
import { QueryRowDetailPanel } from "./QueryRowDetailPanel";
import { QueryViewModeToggle, type QueryViewMode } from "./QueryViewModeToggle";
import { toast } from "sonner";
import { notifyCopied, notifySuccess } from "@/lib/notify";
import { toTsv, toTsvData, toTsvFields } from "@/lib/tableExport";
import {
  collectGridEdits,
  dropRowEdits,
  rebaseNewRowEdits,
  type PendingGridEdits,
  type TableColumnRule,
} from "@/lib/tableEdit";
import type { CellValueFilterOperator } from "@/lib/tableSql";
import {
  createFilterCondition,
  removeFilterItemsByColumn,
  toFilterRequest,
  toSortRequest,
  type TableFilterItem,
  type TableSortItem,
} from "@/lib/tableFilter";
import { filterOperatorNeedsRange } from "@/lib/tableFilterOperators";
import { cellValueToDisplayText } from "@/lib/cellValue";

interface TableDataTabProps {
  tabId: string;
  innerTabId: string;
  database: string;
  table: string;
}

const DEFAULT_PAGE_SIZE = 1000;

interface SQLResult {
  columns?: string[];
  rows?: Record<string, unknown>[];
  count?: number;
  affected_rows?: number;
}

// 与后端 query_svc.OpenTableResult 对齐
interface OpenTableResult {
  columns: string[];
  columnTypes: Record<string, string>;
  columnCategories: Record<string, ImportColumnCategory>;
  columnRules: TableColumnRule[];
  primaryKeys: string[];
  totalCount: number;
  firstPage: Record<string, unknown>[];
  pageSize: number;
}

const REFRESH_SHORTCUT_LABEL = formatModKey("KeyR");

// 字符串 props 稳定 —— memo 避免 DatabasePanel 的 innerTabs 结构变化传导
export const TableDataTab = memo(function TableDataTab(props: TableDataTabProps) {
  const { t } = useTranslation();
  const { markTableTabLoaded } = useQueryStore();
  const innerTab = useQueryStore((s) => s.dbStates[props.tabId]?.innerTabs.find((it) => it.id === props.innerTabId));
  const pendingLoad = innerTab?.type === "table" && innerTab.pendingLoad === true;
  const isOuterActive = useTabStore((s) => s.activeTabId === props.tabId);
  const isInnerActive = useQueryStore((s) => s.dbStates[props.tabId]?.activeInnerTabId === props.innerTabId);

  useEffect(() => {
    if (!pendingLoad || !isOuterActive || !isInnerActive) return;
    const handler = (e: KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey;
      if (mod && e.code === "KeyR" && !e.shiftKey && !e.altKey) {
        e.preventDefault();
        markTableTabLoaded(props.tabId, props.innerTabId);
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [pendingLoad, isOuterActive, isInnerActive, markTableTabLoaded, props.tabId, props.innerTabId]);

  if (pendingLoad) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-3 text-muted-foreground">
        <p className="text-xs">
          {t("query.tableRestoredHint", { table: props.table, shortcut: REFRESH_SHORTCUT_LABEL })}
        </p>
        <Button
          variant="outline"
          size="sm"
          className="h-8 text-xs gap-1"
          onClick={() => markTableTabLoaded(props.tabId, props.innerTabId)}
        >
          <Download className="h-3.5 w-3.5" />
          {t("query.loadData")}
        </Button>
      </div>
    );
  }

  return <TableDataTabContent {...props} />;
});

/** 有未提交修改时被拦下的操作。 */
interface GuardedAction {
  run: () => void;
  description: string;
}

/**
 * 一次提交要执行的语句：由驱动生成，预览里展示、确认后逐条执行的正是这些（新增在前、改动在后）。
 * rowIdxs[i] 是第 i 条语句来自的表格行，行号基于当时的已保存行数 rowsLength。
 */
interface GridEditPlan {
  statements: string[];
  rowIdxs: number[];
  rowsLength: number;
}

function TableDataTabContent({ tabId, innerTabId, database, table }: TableDataTabProps) {
  const { t } = useTranslation();
  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));
  const queryMeta = tab?.meta as QueryTabMeta | undefined;
  const isOuterActive = useTabStore((s) => s.activeTabId === tabId);
  const isInnerActive = useQueryStore((s) => s.dbStates[tabId]?.activeInnerTabId === innerTabId);

  const [columns, setColumns] = useState<string[]>([]);
  const [columnTypes, setColumnTypes] = useState<Record<string, string>>({});
  const [columnCategories, setColumnCategories] = useState<Record<string, ImportColumnCategory>>({});
  const [columnRules, setColumnRules] = useState<TableColumnRule[]>([]);
  const [rows, setRows] = useState<Record<string, unknown>[]>([]);
  const [newRows, setNewRows] = useState<Record<string, unknown>[]>([]);
  const [totalRows, setTotalRows] = useState<number | null>(null);
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [pageInput, setPageInput] = useState("1");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // 首次打开(OpenTable)失败后重试要重跑打开流程:主键 / 列类型只有它会填。
  const [openAttempt, setOpenAttempt] = useState(0);
  // 最近一次成功取数(打开表 / 翻页 / 刷新)的耗时,显示在状态栏。
  const [elapsedMs, setElapsedMs] = useState<number | null>(null);
  const [edits, setEdits] = useState<Map<string, unknown>>(new Map());
  const [submitting, setSubmitting] = useState(false);
  // `preview` = read-only view (opened by the "Preview SQL" button).
  // `confirm` = confirmation before submit (opened by the "Submit" button).
  const [sqlDialog, setSqlDialog] = useState<{ mode: "preview" | "confirm"; plan: GridEditPlan } | null>(null);
  // 状态栏的语句摘要:驱动为哪一份修改生成的第一条语句(修改变了就不再显示旧的)。
  const [sqlSummary, setSqlSummary] = useState<{ source: PendingGridEdits; statement: string } | null>(null);
  const [showDDLDialog, setShowDDLDialog] = useState(false);
  const [ddlLoading, setDdlLoading] = useState(false);
  const [ddlSQL, setDdlSQL] = useState("");
  const [filters, setFilters] = useState<TableFilterItem[]>([]);
  const [sorts, setSorts] = useState<TableSortItem[]>([]);
  const [showFilterSort, setShowFilterSort] = useState(false);
  // filters / sorts 是筛选器里正在编辑的内容;applied* 是已经生效、随请求发给驱动的那一份。
  const [appliedFilters, setAppliedFilters] = useState<TableFilterItem[]>([]);
  const [appliedSorts, setAppliedSorts] = useState<TableSortItem[]>([]);
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<SortDir>(null);
  const [applyVersion, setApplyVersion] = useState(0);
  const [primaryKeys, setPrimaryKeys] = useState<string[]>([]);
  // 列注释 / 默认值 / 索引,只供「显示注释」与表头信息卡。undefined = 未加载或加载失败(两者都不影响表格)。
  const [tableMeta, setTableMeta] = useState<query_svc.TableMetadata | undefined>(undefined);
  const [pkLoaded, setPkLoaded] = useState(false);
  const [deletePreview, setDeletePreview] = useState<{
    statements: string[];
    usesPrimaryKey: boolean;
  } | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [selectedRowIdx, setSelectedRowIdx] = useState<number | null>(null);
  const [selectedRowIdxs, setSelectedRowIdxs] = useState<number[]>([]);
  const [exportFormat, setExportFormat] = useState<TableExportFormat>("csv");
  const [showExportDialog, setShowExportDialog] = useState(false);
  const [showImportDialog, setShowImportDialog] = useState(false);
  const [importing, setImporting] = useState(false);
  const [visibleColumns, setVisibleColumns] = useState<string[]>([]);
  const [rowDensity, setRowDensity] = useState<RowDensity>("default");
  // 内层 tab 常驻挂载(DatabasePanel 用 display 切换),所以本地 state 就是"按 tab 记住"。
  const [viewMode, setViewMode] = useState<QueryViewMode>("table");
  const [rowDetailOpen, setRowDetailOpen] = useState(false);
  const [focusCellRequest, setFocusCellRequest] = useState<FocusCellRequest | null>(null);
  // 有未提交修改时被拦下的操作:对话框打开期间挂在这里,按用户选择丢弃或执行。
  const [guardedAction, setGuardedAction] = useState<GuardedAction | null>(null);
  // 选「提交修改…」后,被拦下的操作等提交成功才执行;预览取消或提交失败时清掉。
  const continueAfterSubmit = useRef<(() => void) | null>(null);
  const requestSeq = useRef(0);
  const latestDataRequest = useRef(0);
  const latestCountRequest = useRef(0);
  const latestImportRequest = useRef(0);
  const cancelledRequests = useRef(new Set<number>());
  // openTable 完成后置 true,作为 fetchCount/fetchData 在初次挂载时的跳过门闩
  // —— 防止 OpenTable 已经一次拿全首屏数据后,后续 effect 仍重复请求 count + page 0。
  const openedRef = useRef(false);

  const driver = queryMeta?.driver;
  const assetId = queryMeta?.assetId ?? 0;
  // 入口按资产驱动声明的功能显隐;描述未就绪或驱动未注册时,这些入口都不出现。
  const driversState = useDatabaseDrivers();
  const driverDescriptor =
    driversState.status === "ready" && driver ? findDatabaseDriver(driversState.drivers, driver) : undefined;
  const can = (capability: string) => driverHasCapability(driverDescriptor, capability);
  const canGridEdit = can("grid_edit");

  const totalPages = totalRows != null ? Math.max(1, Math.ceil(totalRows / pageSize)) : null;
  const nextRequestId = useCallback(() => {
    requestSeq.current += 1;
    return requestSeq.current;
  }, []);
  const isCancelled = useCallback((requestId: number) => cancelledRequests.current.has(requestId), []);

  // 切换表(或资产/库)时同步清空数据并进入加载态:渲染期对比上次值(含首次渲染,
  // 对齐原 effect 挂载即跑),替代 effect 里的同步 setState;requestId/openedRef 的
  // ref 记账与异步拉取仍留在下面的 effect。
  const [prevTableKey, setPrevTableKey] = useState<{ assetId?: number; database?: string; table?: string } | null>(
    null
  );
  if (
    prevTableKey === null ||
    assetId !== prevTableKey.assetId ||
    database !== prevTableKey.database ||
    table !== prevTableKey.table
  ) {
    setPrevTableKey({ assetId, database, table });
    if (assetId) {
      setPrimaryKeys([]);
      setColumnTypes({});
      setColumnRules([]);
      setColumns([]);
      setRows([]);
      setTotalRows(null);
      setPkLoaded(false);
      setLoading(true);
      setError(null);
      setElapsedMs(null);
    }
  }

  // 打开表时一次性拉取主键 / 列类型 / 总行数 / 首页数据。
  // 后续 fetchCount/fetchData 仍按需触发(filter apply / 翻页 / 排序),
  // openedRef 作为初次挂载时的门闩,避免和 OpenTable 重复请求。
  // 用 requestId 接入现有取消系统,让"停止加载"按钮也能丢弃首次加载的结果。
  useEffect(() => {
    if (!assetId) return;
    const requestId = nextRequestId();
    latestDataRequest.current = requestId;
    latestCountRequest.current = requestId;
    openedRef.current = false;
    (async () => {
      const startedAt = performance.now();
      try {
        const raw = await OpenTable(assetId, database, table, pageSize);
        if (isCancelled(requestId) || latestDataRequest.current !== requestId) return;
        const parsed = JSON.parse(raw) as OpenTableResult;
        setElapsedMs(Math.round(performance.now() - startedAt));
        setPrimaryKeys(parsed.primaryKeys ?? []);
        setColumnTypes(parsed.columnTypes ?? {});
        setColumnCategories(parsed.columnCategories ?? {});
        setColumnRules(parsed.columnRules ?? []);
        setColumns(parsed.columns ?? []);
        setRows(parsed.firstPage ?? []);
        setTotalRows(typeof parsed.totalCount === "number" ? parsed.totalCount : null);
        openedRef.current = true;
      } catch (e) {
        if (isCancelled(requestId) || latestDataRequest.current !== requestId) return;
        setError(String(e));
      } finally {
        if (!isCancelled(requestId) && latestDataRequest.current === requestId) {
          setPkLoaded(true);
          setLoading(false);
        }
        cancelledRequests.current.delete(requestId);
      }
    })();
    // pageSize 故意不在 deps 里:OpenTable 只在切换表时跑一次,
    // 用户改 pageSize 由下方 fetchData 的 useEffect 处理。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetId, database, table, openAttempt, nextRequestId, isCancelled]);

  // 直接调绑定而不走 queryStore.loadTableMeta:后者失败会写整个面板的 dbState.error,
  // 而元数据只是增强信息,拿不到时仅少了注释与信息卡。
  useEffect(() => {
    if (!assetId) return;
    let stale = false;
    GetTableMetadata(assetId, database, table).then(
      (meta) => {
        if (!stale) setTableMeta(meta);
      },
      () => {
        // 失败:表格不显示注释开关与信息卡,也不写面板错误。
        if (!stale) setTableMeta(undefined);
      }
    );
    return () => {
      stale = true;
    };
  }, [assetId, database, table, openAttempt]);
  const columnMeta = useMemo(() => {
    if (!tableMeta) return undefined;
    const indexesByColumn = new Map<string, string[]>();
    for (const index of tableMeta.indexes ?? []) {
      for (const col of index.columns ?? [])
        indexesByColumn.set(col, [...(indexesByColumn.get(col) ?? []), index.name]);
    }
    return Object.fromEntries(
      (tableMeta.columns ?? []).map((col) => [
        col.name,
        {
          comment: col.comment,
          // 后端以 JSON null 表示「没有默认值」(生成的类型只写了 string | undefined),在这里统一成 undefined。
          default: col.default ?? undefined,
          type: col.type,
          nullable: col.nullable,
          primaryKey: col.primaryKey,
          indexes: indexesByColumn.get(col.name) ?? [],
        },
      ])
    );
  }, [tableMeta]);

  // 翻页、计数发给驱动的是结构化的筛选条件与排序项,不是 SQL:怎么变成查询由驱动决定。
  const notNullColumns = useMemo(
    () => new Set(columnRules.filter((rule) => !rule.nullable).map((rule) => rule.name)),
    [columnRules]
  );
  const filterRequest = useMemo(
    () => toFilterRequest(appliedFilters).map((item) => dbdriver.FilterItem.createFrom(item)),
    [appliedFilters]
  );
  // Header-click sort takes precedence over the builder's sort criteria.
  const sortRequest = useMemo(
    () => (sortColumn && sortDir ? [{ column: sortColumn, dir: sortDir }] : toSortRequest(appliedSorts)),
    [appliedSorts, sortColumn, sortDir]
  );

  // 修改按 "行号:列名" 记,新增行排在已保存的行之后:重新取到的页长度变了(提交后只清掉成功的修改,
  // 失败的新增行还在),新增行的修改跟着移到新的行号上。rowsLengthRef 是修改当前所基于的已保存行数。
  const rowsLengthRef = useRef(0);
  useEffect(() => {
    rowsLengthRef.current = rows.length;
  }, [rows.length]);
  const replaceRows = useCallback((next: Record<string, unknown>[]) => {
    const previous = rowsLengthRef.current;
    setRows(next);
    setEdits((prev) => rebaseNewRowEdits(prev, previous, next.length));
  }, []);

  // Fetch total count
  const fetchCount = useCallback(async () => {
    if (!assetId) return;
    const requestId = nextRequestId();
    latestCountRequest.current = requestId;
    try {
      const total = await CountTableRows(assetId, database, table, filterRequest);
      if (isCancelled(requestId) || latestCountRequest.current !== requestId) return;
      setTotalRows(total);
    } catch {
      if (isCancelled(requestId) || latestCountRequest.current !== requestId) return;
      setTotalRows(null);
    } finally {
      cancelledRequests.current.delete(requestId);
    }
  }, [assetId, database, table, filterRequest, nextRequestId, isCancelled]);

  const fetchData = useCallback(
    async (pageNum: number) => {
      if (!assetId) return;
      const requestId = nextRequestId();
      latestDataRequest.current = requestId;
      setLoading(true);
      setError(null);

      const startedAt = performance.now();
      try {
        const result = await QueryTableRows(
          assetId,
          database,
          table,
          query_svc.TableRowsRequest.createFrom({
            filters: filterRequest,
            sorts: sortRequest,
            page: pageNum,
            pageSize,
          })
        );
        if (isCancelled(requestId) || latestDataRequest.current !== requestId) return;
        setElapsedMs(Math.round(performance.now() - startedAt));
        setColumns(result.columns);
        replaceRows(result.rows);
        setSelectedRowIdx(null);
      } catch (e) {
        if (isCancelled(requestId) || latestDataRequest.current !== requestId) return;
        setElapsedMs(null);
        setError(String(e));
        // 列保留:筛选器与列状态在重试 / 改筛选后仍沿用,错误卡片取代的只是表体。
        replaceRows([]);
        setSelectedRowIdx(null);
      } finally {
        if (!isCancelled(requestId) && latestDataRequest.current === requestId) setLoading(false);
        cancelledRequests.current.delete(requestId);
      }
    },
    [assetId, database, table, pageSize, filterRequest, sortRequest, nextRequestId, isCancelled, replaceRows]
  );

  useEffect(() => {
    // OpenTable 已经在初次挂载时填好 totalRows;此处只在 filter apply / 表名变更后
    // openedRef 仍为 true 时跑(切表会先把 openedRef 重置为 false)。
    if (!openedRef.current) return;
    fetchCount();
  }, [fetchCount, applyVersion]);

  useEffect(() => {
    if (!openedRef.current) return;
    fetchData(page);
  }, [fetchData, page, applyVersion]);

  // Sync page input
  // 翻页时同步页码输入框:渲染期对比上次值(含首次渲染,对齐原 effect 挂载即跑)。
  const [prevPage, setPrevPage] = useState<number | null>(null);
  if (page !== prevPage) {
    setPrevPage(page);
    setPageInput(String(page + 1));
  }

  // 隐藏列只在有序列名变化时重置(刷新 / 翻页 / 排序 / 筛选每次给新数组,名字不变就保留):
  // 渲染期对比上次值(含首次渲染,对齐原 effect 挂载即跑)。
  const columnsKey = JSON.stringify(columns);
  const [prevColumnsKey, setPrevColumnsKey] = useState<string | null>(null);
  if (columnsKey !== prevColumnsKey) {
    setPrevColumnsKey(columnsKey);
    setVisibleColumns(columns);
  }

  const handleCellEdit = useCallback((edit: CellEdit) => {
    setEdits((prev) => {
      const next = new Map(prev);
      const key = `${edit.rowIdx}:${edit.col}`;
      next.set(key, edit.value);
      return next;
    });
  }, []);

  const handleDiscard = useCallback(() => {
    setEdits(new Map());
    setNewRows([]);
  }, []);

  const hasEdits = edits.size > 0 || newRows.length > 0;
  // 会丢掉当前页修改的操作(翻页、改页大小、排序、筛选、刷新)都经过这里:没有修改直接执行,
  // 有修改先弹框让用户选择留下、放弃后继续,或提交成功后再继续。
  const guardEdits = useCallback(
    (action: () => void, description: string) => {
      if (!hasEdits) {
        action();
        return;
      }
      setGuardedAction({ run: action, description });
    },
    [hasEdits]
  );

  // 有无修改上报给 queryStore:关闭本标签、关闭外层数据库标签、⌘W 据此决定是否先拦下来问。
  const setInnerTabDirty = useQueryStore((s) => s.setInnerTabDirty);
  useEffect(() => {
    setInnerTabDirty(tabId, innerTabId, hasEdits);
    return () => setInnerTabDirty(tabId, innerTabId, false);
  }, [hasEdits, innerTabId, setInnerTabDirty, tabId]);

  // 关闭请求只涉及本标签这一个脏标签时,由这里的三选项对话框决定(多个时 DatabasePanel 弹批量框)。
  const closeRequest = useQueryStore((s) => s.dbStates[tabId]?.closeRequest);
  const forceCloseInnerTabs = useQueryStore((s) => s.forceCloseInnerTabs);
  const cancelCloseRequest = useQueryStore((s) => s.cancelCloseRequest);
  const closeGuard = useMemo((): GuardedAction | null => {
    if (closeRequest?.dirtyIds.length !== 1 || closeRequest.dirtyIds[0] !== innerTabId) return null;
    const { ids, closeOuter } = closeRequest;
    return {
      run: () => forceCloseInnerTabs(tabId, ids, closeOuter),
      description: t("query.unsavedEditsDescClose"),
    };
  }, [closeRequest, forceCloseInnerTabs, innerTabId, t, tabId]);
  const pendingGuard = closeGuard ?? guardedAction;

  const handleAddInlineRow = useCallback(() => {
    const firstVisibleColumn = visibleColumns.find((column) => columns.includes(column)) ?? columns[0];
    if (!firstVisibleColumn) return;
    const rowIdx = rows.length + newRows.length;
    setNewRows((prev) => [...prev, {}]);
    setSelectedRowIdx(rowIdx);
    setFocusCellRequest({ rowIdx, col: firstVisibleColumn, nonce: Date.now() });
  }, [columns, newRows.length, rows.length, visibleColumns]);

  // A pasted block addresses rows in the grid's own coordinate space, where unsaved rows
  // are appended after the loaded page. Materialise as many as the block reaches, then
  // stage the values as ordinary pending edits.
  const handlePasteBlock = useCallback((block: { edits: CellEdit[]; newRowCount: number }) => {
    if (block.newRowCount > 0) {
      setNewRows((prev) => [...prev, ...Array.from({ length: block.newRowCount }, () => ({}))]);
    }
    setEdits((prev) => {
      const next = new Map(prev);
      for (const edit of block.edits) next.set(`${edit.rowIdx}:${edit.col}`, edit.value);
      return next;
    });
  }, []);

  const removeNewRow = useCallback(
    (rowIdx: number) => {
      const newRowIdx = rowIdx - rows.length;
      if (newRowIdx < 0 || newRowIdx >= newRows.length) return;
      setNewRows((prev) => prev.filter((_, idx) => idx !== newRowIdx));
      setEdits((prev) => dropRowEdits(prev, rows.length, new Set([rowIdx])));
      setSelectedRowIdx(null);
    },
    [newRows.length, rows.length]
  );

  // 表格里的修改整理成发给驱动的意图;语句由驱动生成(GetGridEditStatements)。
  const pendingEdits = useMemo(
    () => collectGridEdits({ rows, newRowCount: newRows.length, columns, primaryKeys, columnRules, edits }),
    [rows, newRows.length, columns, primaryKeys, columnRules, edits]
  );
  // 生成要等后端:等回来时修改若已变了,这次的结果就不再对应表格里的修改。
  const latestPendingEdits = useRef(pendingEdits);
  useEffect(() => {
    latestPendingEdits.current = pendingEdits;
  }, [pendingEdits]);
  const hasPendingStatements = pendingEdits.intent.inserts.length + pendingEdits.intent.updates.length > 0;

  const generateGridEdits = useCallback(
    (intent: Partial<PendingGridEdits["intent"]>) =>
      GetGridEditStatements(
        assetId,
        database,
        table,
        dbdriver.GridEditRequest.createFrom({ columns, primaryKeys, inserts: [], updates: [], deletes: [], ...intent })
      ),
    [assetId, columns, database, primaryKeys, table]
  );

  // 状态栏摘要:驱动为当前修改生成的第一条语句(新增在前)。生成失败时不显示摘要,失败原因在
  // 预览 / 提交时显示。
  useEffect(() => {
    if (!canGridEdit || !assetId || !hasPendingStatements || pendingEdits.missingFields.length > 0) return;
    let stale = false;
    generateGridEdits(pendingEdits.intent).then(
      (generated) => {
        if (!stale) setSqlSummary({ source: pendingEdits, statement: [...generated.inserts, ...generated.updates][0] });
      },
      () => {
        if (!stale) setSqlSummary({ source: pendingEdits, statement: "" });
      }
    );
    return () => {
      stale = true;
    };
  }, [assetId, canGridEdit, generateGridEdits, hasPendingStatements, pendingEdits]);
  const pendingSqlSummary = sqlSummary?.source === pendingEdits ? sqlSummary.statement : "";

  const showInsertValidationError = useCallback(
    (fields: string[]) => {
      toast.error(t("query.insertRequiredFields", { fields: fields.join(", ") }));
    },
    [t]
  );

  // 让驱动为当前修改生成语句并打开预览 / 确认;返回是否打开了。生成失败时显示错误,不打开预览。
  const openSqlDialog = useCallback(
    async (mode: "preview" | "confirm"): Promise<boolean> => {
      const source = pendingEdits;
      if (source.missingFields.length > 0) {
        showInsertValidationError(source.missingFields);
        return false;
      }
      if (!hasPendingStatements) return false;
      let generated: dbdriver.GridEditStatements;
      try {
        generated = await generateGridEdits(source.intent);
      } catch (e) {
        toast.error(String(e));
        return false;
      }
      if (latestPendingEdits.current !== source) return false;
      setSqlDialog({
        mode,
        plan: {
          statements: [...generated.inserts, ...generated.updates],
          rowIdxs: [...source.insertRowIdxs, ...source.updateRowIdxs],
          rowsLength: rows.length,
        },
      });
      return true;
    },
    [generateGridEdits, hasPendingStatements, pendingEdits, rows.length, showInsertValidationError]
  );

  // 逐条执行预览里的语句(不包事务,单条失败不影响其余)。返回是否全部成功(每条都执行成功且命中了
  // 行),拦截后的「提交后继续」靠它决定是否继续。成功的修改与新增行清掉并刷新,其余留在表格里。
  const handleSubmit = useCallback(
    async (plan: GridEditPlan): Promise<boolean> => {
      setSubmitting(true);
      let affectedTotal = 0;
      let zeroAffected = 0;
      let errorMsg = "";
      const succeeded = new Set<number>();

      for (const [index, sql] of plan.statements.entries()) {
        try {
          const result = await ExecuteSQL(assetId, sql, database);
          const parsed: SQLResult = JSON.parse(result);
          const affected = Number(parsed.affected_rows ?? 0);
          if (affected > 0) {
            affectedTotal += affected;
            succeeded.add(plan.rowIdxs[index]);
          } else zeroAffected++;
        } catch (e) {
          errorMsg += String(e) + "\n";
        }
      }

      setSubmitting(false);
      setSqlDialog(null);

      if (affectedTotal > 0) {
        notifySuccess(t("query.updateSuccessAffected", { affected: affectedTotal }));
        setNewRows((prev) => prev.filter((_, newRowIdx) => !succeeded.has(plan.rowsLength + newRowIdx)));
        setEdits((prev) => dropRowEdits(prev, plan.rowsLength, succeeded));
        fetchData(page);
        fetchCount();
      }
      if (zeroAffected > 0) {
        toast.warning(t("query.updateMismatch", { count: zeroAffected }));
      }
      if (errorMsg) {
        toast.error(errorMsg.trim());
      }
      return !errorMsg && zeroAffected === 0;
    },
    [assetId, database, page, fetchData, fetchCount, t]
  );

  const goToPage = useCallback(
    (target: number) => {
      if (target === page) return;
      guardEdits(() => setPage(target), t("query.unsavedEditsDescPage", { page: target + 1 }));
    },
    [guardEdits, page, t]
  );

  const handlePageInputConfirm = useCallback(() => {
    const num = parseInt(pageInput, 10);
    // 输入框先回到当前页:真的翻过去时由翻页同步成新页码,留在本页时不残留输入。
    setPageInput(String(page + 1));
    if (isNaN(num) || num < 1) return;
    goToPage(totalPages ? Math.min(num, totalPages) - 1 : num - 1);
  }, [goToPage, pageInput, page, totalPages]);

  const handlePageSizeChange = useCallback(
    (nextPageSize: number) => {
      guardEdits(
        () => {
          setPageSize(nextPageSize);
          setPage(0);
        },
        t("query.unsavedEditsDescPageSize", { size: nextPageSize })
      );
    },
    [guardEdits, t]
  );

  const handleRefresh = useCallback(() => {
    guardEdits(() => {
      fetchData(page);
      fetchCount();
    }, t("query.unsavedEditsDescRefresh"));
  }, [fetchData, fetchCount, guardEdits, page, t]);

  const handleRetry = useCallback(() => {
    if (!openedRef.current) {
      setError(null);
      setLoading(true);
      setOpenAttempt((n) => n + 1);
      return;
    }
    handleRefresh();
  }, [handleRefresh]);

  const handleStopLoading = useCallback(() => {
    if (!loading && !importing) return;
    cancelledRequests.current.add(latestDataRequest.current);
    cancelledRequests.current.add(latestCountRequest.current);
    cancelledRequests.current.add(latestImportRequest.current);
    setLoading(false);
    setImporting(false);
    toast.info(t("query.stopLoadingToast"));
  }, [importing, loading, t]);

  useEffect(() => {
    if (!isOuterActive || !isInnerActive) return;
    const handler = (e: KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey;
      if (mod && e.code === "KeyR" && !e.shiftKey && !e.altKey) {
        e.preventDefault();
        handleRefresh();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [isOuterActive, isInnerActive, handleRefresh]);

  const handleApplyQuery = useCallback(
    (nextFilters?: TableFilterItem[]) => {
      const applied = nextFilters ?? filters;
      guardEdits(() => {
        setAppliedFilters(applied);
        setAppliedSorts(sorts);
        // Builder sort criteria override the header-click sort state.
        if (sorts.length > 0) {
          setSortColumn(null);
          setSortDir(null);
        }
        setPage(0);
        setApplyVersion((v) => v + 1);
      }, t("query.unsavedEditsDescFilter"));
    },
    [filters, guardEdits, sorts, t]
  );

  const handleSortChange = useCallback(
    (col: string | null, dir: SortDir) => {
      guardEdits(() => {
        setSortColumn(col);
        setSortDir(dir);
        // Header click takes precedence — clear the builder's sort criteria so the user
        // can see which sort is actually applied.
        setSorts([]);
        setAppliedSorts([]);
        setPage(0);
        setApplyVersion((v) => v + 1);
      }, t("query.unsavedEditsDescSort"));
    },
    [guardEdits, t]
  );

  const handleFilterByCellValue = useCallback(
    ({ col, value, operator = "=" }: { col: string; value: unknown; operator?: CellValueFilterOperator }) => {
      const condition = (cellValue: unknown) =>
        createFilterCondition(`cell-${col}`, col, {
          value: filterOperatorNeedsRange(operator) ? [cellValue, cellValue] : cellValue,
          operator,
        });
      guardEdits(() => {
        setFilters([condition(value)]);
        setShowFilterSort(true);
        // 没有值的单元格(尚未提交的新增行)按 NULL 筛选,而不是当作还没填值的条件被略过;
        // 区间运算符两端都缺,条件照旧不产生限制。
        const unfilledRange = value === undefined && filterOperatorNeedsRange(operator);
        setAppliedFilters([
          unfilledRange
            ? createFilterCondition(`cell-${col}`, col, { operator })
            : condition(value === undefined ? null : value),
        ]);
        setPage(0);
        setApplyVersion((v) => v + 1);
      }, t("query.unsavedEditsDescFilter"));
    },
    [guardEdits, t]
  );

  const handleRemoveColumnFilter = useCallback(
    (column: string) => {
      guardEdits(() => {
        const next = removeFilterItemsByColumn(filters, column);
        setFilters(next);
        setAppliedFilters(next);
        setPage(0);
        setApplyVersion((v) => v + 1);
      }, t("query.unsavedEditsDescFilter"));
    },
    [filters, guardEdits, t]
  );

  const handleRemoveAllFilters = useCallback(() => {
    guardEdits(() => {
      setFilters([]);
      setAppliedFilters([]);
      setPage(0);
      setApplyVersion((v) => v + 1);
    }, t("query.unsavedEditsDescFilter"));
  }, [guardEdits, t]);

  const handleSortByColumn = useCallback(
    (col: string, dir: Exclude<SortDir, null>) => {
      handleSortChange(col, dir);
    },
    [handleSortChange]
  );

  const handleClearFilterSort = useCallback(() => {
    guardEdits(() => {
      setFilters([]);
      setAppliedFilters([]);
      setSorts([]);
      setAppliedSorts([]);
      setSortColumn(null);
      setSortDir(null);
      setPage(0);
      setApplyVersion((v) => v + 1);
    }, t("query.unsavedEditsDescFilter"));
  }, [guardEdits, t]);

  // 拦截对话框里的修改摘要:已保存行每个改动的单元格一条,新增行每行一条;行号与网格显示一致。
  const unsavedSummary = useMemo((): UnsavedEditSummaryItem[] => {
    const rowNumberOffset = page * pageSize;
    const updates: { rowIdx: number; colIdx: number; item: UnsavedEditSummaryItem }[] = [];
    for (const [key, value] of edits) {
      const sep = key.indexOf(":");
      const rowIdx = Number(key.substring(0, sep));
      const column = key.substring(sep + 1);
      if (rowIdx >= rows.length) continue;
      updates.push({
        rowIdx,
        colIdx: columns.indexOf(column),
        item: {
          kind: "update",
          row: rowNumberOffset + rowIdx + 1,
          column,
          oldValue: rows[rowIdx][column],
          newValue: value,
        },
      });
    }
    updates.sort((a, b) => a.rowIdx - b.rowIdx || a.colIdx - b.colIdx);
    const inserts = newRows.map((_, newRowIdx): UnsavedEditSummaryItem => {
      const rowIdx = rows.length + newRowIdx;
      return {
        kind: "insert",
        row: rowNumberOffset + rowIdx + 1,
        values: columns
          .filter((column) => edits.has(`${rowIdx}:${column}`))
          .map((column) => ({ column, value: edits.get(`${rowIdx}:${column}`) })),
      };
    });
    return [...updates.map((u) => u.item), ...inserts];
  }, [columns, edits, newRows, page, pageSize, rows]);

  // 只收起当前显示的那一个:关闭请求优先显示,收起后被它盖住的翻页等拦截照旧。
  const handleGuardStay = useCallback(() => {
    if (closeGuard) cancelCloseRequest(tabId);
    else setGuardedAction(null);
  }, [cancelCloseRequest, closeGuard, tabId]);

  const handleGuardDiscard = useCallback(() => {
    if (!pendingGuard) return;
    handleGuardStay();
    handleDiscard();
    pendingGuard.run();
  }, [handleDiscard, handleGuardStay, pendingGuard]);

  const handleGuardSubmit = useCallback(async () => {
    if (!pendingGuard) return;
    handleGuardStay();
    if (await openSqlDialog("confirm")) continueAfterSubmit.current = pendingGuard.run;
  }, [handleGuardStay, openSqlDialog, pendingGuard]);

  const handleConfirmSubmit = useCallback(async () => {
    if (!sqlDialog) return;
    const next = continueAfterSubmit.current;
    continueAfterSubmit.current = null;
    if ((await handleSubmit(sqlDialog.plan)) && next) next();
  }, [handleSubmit, sqlDialog]);

  const handleDeleteRows = useCallback(
    async (rowIdxs: number[]) => {
      // rows 之后的下标是尚未提交的新增行:它们只存在于客户端,没有可删的记录。
      // 从大到小移除,避免前一次移除把后面的下标往前挪。
      const unsaved = rowIdxs.filter((idx) => idx >= rows.length).sort((a, b) => b - a);
      for (const idx of unsaved) removeNewRow(idx);

      const persisted = rowIdxs.filter((idx) => idx < rows.length);
      if (persisted.length === 0) return;
      // DELETE 由驱动生成;生成失败时显示错误,不打开预览。
      try {
        const generated = await generateGridEdits({ deletes: persisted.map((idx) => rows[idx]) });
        setDeletePreview({ statements: generated.deletes, usesPrimaryKey: primaryKeys.length > 0 });
      } catch (e) {
        toast.error(String(e));
      }
    },
    [generateGridEdits, primaryKeys.length, removeNewRow, rows]
  );

  const handleConfirmDelete = useCallback(async () => {
    if (!assetId || !deletePreview) return;
    setDeleting(true);
    let succeeded = 0;
    let affectedTotal = 0;
    let zeroAffected = 0;
    let errorMsg = "";

    // 与 handleSubmit 一致:逐条执行并汇总,单条失败不影响其余语句。
    for (const sql of deletePreview.statements) {
      try {
        const result = await ExecuteSQL(assetId, sql, database);
        const parsed: SQLResult = JSON.parse(result);
        const affected = Number(parsed.affected_rows ?? 0);
        succeeded++;
        if (affected > 0) affectedTotal += affected;
        else zeroAffected++;
      } catch (e) {
        errorMsg += String(e) + "\n";
      }
    }

    setDeleting(false);
    setDeletePreview(null);

    if (affectedTotal > 0) notifySuccess(t("query.deleteRecordSuccess", { affected: affectedTotal }));
    // 只要有语句执行成功就刷新:命中 0 行也说明服务端数据与网格已经不一致。
    if (succeeded > 0) {
      setEdits(new Map());
      setNewRows([]);
      await fetchData(page);
      await fetchCount();
    }
    if (zeroAffected > 0) toast.warning(t("query.deleteMismatch", { count: zeroAffected }));
    if (errorMsg) toast.error(errorMsg.trim());
  }, [assetId, database, deletePreview, fetchCount, fetchData, page, t]);

  // 状态栏的删除按钮:优先整段行选中,退回到当前单元格所在行。
  const deletableRowIdxs = useMemo(
    () => (selectedRowIdxs.length > 0 ? selectedRowIdxs : selectedRowIdx != null ? [selectedRowIdx] : []),
    [selectedRowIdx, selectedRowIdxs]
  );

  const handleDeleteSelectedRows = useCallback(() => {
    if (deletableRowIdxs.length === 0) return;
    handleDeleteRows(deletableRowIdxs);
  }, [deletableRowIdxs, handleDeleteRows]);

  const handleSelectedCellChange = useCallback((cell: { rowIdx: number } | null) => {
    setSelectedRowIdx(cell?.rowIdx ?? null);
  }, []);

  const handleSelectedRowsChange = useCallback((rowIdxs: number[]) => {
    setSelectedRowIdxs(rowIdxs);
    // 网格选中单元格时会先发 onSelectedCellChange 再发 onSelectedRowsChange([]),
    // 无条件清空会把刚设好的当前行抹掉 —— 只有真的选了整行才接管。
    if (rowIdxs.length > 0) setSelectedRowIdx(null);
  }, []);

  const handleExport = useCallback(() => {
    if (rows.length === 0 || columns.length === 0) return;
    setShowExportDialog(true);
  }, [columns.length, rows.length]);

  const handleCopyAs = useCallback(
    async (
      format: CopyAsFormat,
      ctx: { rowIdx: number; selectedColumns?: string[]; selectedRowIndices?: number[] }
    ) => {
      const activeColumns = ctx.selectedColumns?.length ? ctx.selectedColumns : columns;
      const activeRows = ctx.selectedRowIndices?.length
        ? ctx.selectedRowIndices.map((i) => rows[i]).filter(Boolean)
        : [rows[ctx.rowIdx]];
      if (activeRows.length === 0) return;
      // INSERT / UPDATE 由驱动按自己的方言生成（每行一条 INSERT；UPDATE 只取第一行），TSV 在这里拼。
      const rowStatements = async (kind: "insert" | "update", statementRows: Record<string, unknown>[]) => {
        const statements = await GetRowStatements(
          assetId,
          database,
          table,
          dbdriver.RowStatementsRequest.createFrom({ kind, columns: activeColumns, rows: statementRows, primaryKeys })
        );
        return statements.join("\n");
      };
      const contentByFormat: Record<CopyAsFormat, () => string | Promise<string>> = {
        insert: () => rowStatements("insert", activeRows),
        update: () => rowStatements("update", activeRows.slice(0, 1)),
        "tsv-data": () => toTsvData(activeColumns, activeRows),
        "tsv-fields": () => toTsvFields(activeColumns),
        "tsv-fields-data": () => toTsv(activeColumns, activeRows),
      };
      try {
        await navigator.clipboard.writeText(await contentByFormat[format]());
        notifyCopied(t("query.copied"));
      } catch (e) {
        toast.error(String(e));
      }
    },
    [assetId, columns, database, primaryKeys, rows, table, t]
  );

  const handleImportSuccess = useCallback(async () => {
    setImporting(false);
    setPage(0);
    setEdits(new Map());
    setNewRows([]);
    setApplyVersion((v) => v + 1);
    await fetchData(0);
    await fetchCount();
  }, [fetchData, fetchCount]);

  const handleImportSubmitStart = useCallback(() => {
    const requestId = nextRequestId();
    latestImportRequest.current = requestId;
    return requestId;
  }, [nextRequestId]);

  const isImportSubmitCancelled = useCallback(
    (requestId: number) => isCancelled(requestId) || latestImportRequest.current !== requestId,
    [isCancelled]
  );

  const handleVisibleColumnToggle = useCallback(
    (column: string) => {
      setVisibleColumns((prev) => {
        const current = prev.length > 0 ? prev : columns;
        if (current.includes(column)) {
          if (current.length === 1) return current;
          return current.filter((col) => col !== column);
        }
        return columns.filter((col) => col === column || current.includes(col));
      });
    },
    [columns]
  );

  const handleHideColumn = useCallback(
    (column: string) => {
      setVisibleColumns((prev) => {
        const current = prev.length > 0 ? prev : columns;
        if (!current.includes(column) || current.length === 1) return current;
        return current.filter((col) => col !== column);
      });
    },
    [columns]
  );

  const handleAddColumnFilter = useCallback((column: string) => {
    setFilters((prev) => [...prev, createFilterCondition(`column-${column}-${Date.now()}`, column)]);
    setShowFilterSort(true);
  }, []);

  const handleViewDDL = useCallback(async () => {
    if (!assetId) return;
    setShowDDLDialog(true);
    setDdlLoading(true);

    try {
      const ddl = await GetTableDDL(assetId, database, table);
      setDdlSQL(ddl || t("query.ddlEmpty"));
    } catch (e) {
      setDdlSQL(String(e));
    } finally {
      setDdlLoading(false);
    }
  }, [assetId, table, database, t]);

  const handleCopyDDL = useCallback(async () => {
    const text = ddlLoading ? "" : ddlSQL;
    if (!text) return;
    await navigator.clipboard.writeText(text);
    notifyCopied(t("query.copied"));
  }, [ddlLoading, ddlSQL, t]);

  // 提到顶层走 useCallback,使 QueryResultTable 的 memo 浅比较生效;
  // 只依赖 rows.length 与 t —— 新增行用它来判断默认值占位。
  const rowsLength = rows.length;
  const renderCell = useCallback(
    (value: unknown, ctx: { rowIdx: number; col: string }) => {
      if (ctx.rowIdx >= rowsLength && value == null) {
        return <span className="text-muted-foreground/70 italic">{t("query.newRowDefaultPlaceholder")}</span>;
      }
      if (value == null) return <span className="text-muted-foreground italic">{t("query.nullValue")}</span>;
      return <span className="truncate block">{cellValueToDisplayText(value)}</span>;
    },
    [rowsLength, t]
  );

  const hasNext = totalPages != null ? page < totalPages - 1 : rows.length === pageSize;
  const hasPrev = page > 0;
  const tableRows = useMemo(() => [...rows, ...newRows], [newRows, rows]);
  const pendingEditCount = edits.size + newRows.length;
  const deletableRowCount = deletableRowIdxs.filter((idx) => tableRows[idx] != null).length;
  // 详情面板跟随"当前行":选中单元格所在行,或唯一被整行选中的那行。
  const detailRowIdx = selectedRowIdx ?? (selectedRowIdxs.length === 1 ? selectedRowIdxs[0] : null);
  // memo 用 Object.is 比较 props,这里走三元每次会在 visibleColumns 与 columns 间切引用,
  // 不 useMemo 包的话,父组件任意 re-render 都会让 QueryResultTable 收到"新"数组。
  const effectiveVisibleColumns = useMemo(
    () => (visibleColumns.length > 0 ? visibleColumns : columns),
    [visibleColumns, columns]
  );
  const findScope = useMemo(() => ({ rowCount: rows.length, page: page + 1 }), [rows.length, page]);
  const hasActiveFilterSort =
    filters.length > 0 ||
    sorts.length > 0 ||
    filterRequest.length > 0 ||
    sortRequest.length > 0 ||
    !!sortColumn ||
    !!sortDir;

  const errorPanel = error ? (
    <QueryErrorPanel
      title={t("query.loadTableFailed", { table: `${database}.${table}` })}
      error={error}
      onRetry={handleRetry}
      onEditFilter={filterRequest.length > 0 ? () => setShowFilterSort(true) : undefined}
    />
  ) : null;

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 border-b border-border bg-muted/20 px-3 py-1.5 shrink-0">
        {can("view_ddl") && (
          <Button variant="outline" size="sm" className="h-7 text-xs gap-1 shrink-0" onClick={handleViewDDL}>
            <FileCode2 className="h-3.5 w-3.5" />
            {t("query.viewDDL")}
          </Button>
        )}
        <QueryViewModeToggle value={viewMode} onChange={setViewMode} />
        <Button
          variant={rowDetailOpen ? "secondary" : "ghost"}
          size="icon-xs"
          aria-pressed={rowDetailOpen}
          title={t("query.rowDetail")}
          onClick={() => setRowDetailOpen((open) => !open)}
        >
          <PanelRight className="h-3.5 w-3.5" />
        </Button>
        <TableEditorToolbar
          hasEdits={hasEdits}
          submitting={submitting || deleting}
          canExport={rows.length > 0}
          canImport={columns.length > 0}
          gridEdit={canGridEdit}
          importAvailable={can("import")}
          sqlExportAvailable={can("export_sql")}
          columns={columns}
          visibleColumns={effectiveVisibleColumns}
          rowDensity={rowDensity}
          exportFormat={exportFormat}
          onExportFormatChange={setExportFormat}
          onVisibleColumnToggle={handleVisibleColumnToggle}
          onRowDensityChange={setRowDensity}
          filterSortOpen={showFilterSort}
          filterSortActive={hasActiveFilterSort}
          onToggleFilterSort={() => setShowFilterSort((open) => !open)}
          onSubmit={() => openSqlDialog("confirm")}
          onDiscard={handleDiscard}
          onImport={() => setShowImportDialog(true)}
          onExport={handleExport}
          onPreviewSql={() => openSqlDialog("preview")}
        />
        {canGridEdit &&
          !driverDescriptor?.locatesRowWithoutPrimaryKey &&
          pkLoaded &&
          primaryKeys.length === 0 &&
          columns.length > 0 && (
            <span
              className="flex items-center gap-1 text-[11px] text-warning shrink-0"
              title={t("query.noPrimaryKeyTooltip")}
            >
              <TriangleAlert className="h-3.5 w-3.5" />
              {t("query.noPrimaryKey")}
            </span>
          )}
      </div>

      {showFilterSort && (
        <TableFilterBuilder
          columns={columns}
          rows={rows}
          filters={filters}
          sorts={sorts}
          driver={driver}
          onChange={setFilters}
          onSortsChange={setSorts}
          onApply={handleApplyQuery}
        />
      )}

      {/* Table content */}
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          {viewMode === "json" && error ? (
            errorPanel
          ) : viewMode === "json" ? (
            <QueryResultJsonView rows={tableRows} columns={effectiveVisibleColumns} edits={edits} />
          ) : (
            <QueryResultTable
              columns={columns}
              rows={tableRows}
              loading={loading || importing}
              keepShapeWhileLoading
              error={error ?? undefined}
              errorPanel={errorPanel}
              editable={canGridEdit}
              edits={edits}
              onCellEdit={handleCellEdit}
              onSetCellValue={handleCellEdit}
              onPasteCell={handleCellEdit}
              onPasteBlock={handlePasteBlock}
              onGenerateUuid={handleCellEdit}
              onCopyAs={handleCopyAs}
              copyAsSql={can("copy_sql")}
              onFilterByCellValue={handleFilterByCellValue}
              onSortByColumn={handleSortByColumn}
              onClearFilterSort={handleClearFilterSort}
              onAddColumnFilter={handleAddColumnFilter}
              onRemoveColumnFilter={handleRemoveColumnFilter}
              onRemoveAllFilters={handleRemoveAllFilters}
              onDeleteRows={handleDeleteRows}
              onHideColumn={handleHideColumn}
              onVisibleColumnToggle={handleVisibleColumnToggle}
              onSelectedCellChange={handleSelectedCellChange}
              onSelectedRowsChange={handleSelectedRowsChange}
              onRefresh={handleRefresh}
              showRowNumber
              rowNumberOffset={page * pageSize}
              sortColumn={sortColumn}
              sortDir={sortDir}
              onSortChange={handleSortChange}
              enableColumnFilter
              visibleColumns={effectiveVisibleColumns}
              columnTypes={columnTypes}
              rowDensity={rowDensity}
              focusCellRequest={focusCellRequest}
              renderCell={renderCell}
              findEnabled
              findScope={findScope}
              notNullColumns={notNullColumns}
              columnMeta={columnMeta}
            />
          )}
        </div>
        {rowDetailOpen && (
          <QueryRowDetailPanel
            rows={tableRows}
            columns={effectiveVisibleColumns}
            rowIdx={detailRowIdx}
            rowNumberOffset={page * pageSize}
            edits={edits}
            onClose={() => setRowDetailOpen(false)}
          />
        )}
      </div>

      {/* Footer bar */}
      <TableDataStatusBar
        gridEdit={canGridEdit}
        pendingEditCount={pendingEditCount}
        sqlSummary={pendingSqlSummary}
        totalRows={totalRows}
        page={page}
        totalPages={totalPages}
        pageSize={pageSize}
        pageInput={pageInput}
        hasPrev={hasPrev}
        hasNext={hasNext}
        selectedRowCount={deletableRowCount}
        submitting={submitting || deleting}
        loading={loading || importing}
        loadingPage={loading ? page : null}
        failed={!!error}
        elapsedMs={elapsedMs}
        refreshTitle={`${t("query.refreshTable")} (${REFRESH_SHORTCUT_LABEL})`}
        onRefresh={handleRefresh}
        onStopLoading={handleStopLoading}
        onPageInputChange={setPageInput}
        onPageInputConfirm={handlePageInputConfirm}
        onPageSizeChange={handlePageSizeChange}
        onFirstPage={() => goToPage(0)}
        onPreviousPage={() => goToPage(page - 1)}
        onNextPage={() => goToPage(page + 1)}
        onLastPage={() => {
          if (totalPages != null) goToPage(totalPages - 1);
        }}
        onAddRow={handleAddInlineRow}
        onDeleteRows={handleDeleteSelectedRows}
        onApplyChanges={() => openSqlDialog("confirm")}
        onDiscardChanges={handleDiscard}
      />

      {/* DDL dialog */}
      <AlertDialog open={showDDLDialog} onOpenChange={setShowDDLDialog}>
        <AlertDialogContent className="max-w-3xl" onOverlayClick={() => setShowDDLDialog(false)}>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("query.ddlDialogTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("query.ddlDialogDesc", { table })}</AlertDialogDescription>
          </AlertDialogHeader>
          <div className="h-[420px] rounded-md border border-border overflow-hidden bg-muted/30">
            {ddlLoading ? (
              <div className="h-full w-full p-3 text-xs font-mono text-muted-foreground">{t("query.loadingDDL")}</div>
            ) : (
              <CodeEditor
                value={ddlSQL}
                language="sql"
                readOnly
                options={{ lineNumbers: "off", folding: false, glyphMargin: false, lineDecorationsWidth: 0 }}
              />
            )}
          </div>
          <AlertDialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs gap-1"
              onClick={handleCopyDDL}
              disabled={ddlLoading || !ddlSQL}
            >
              <Copy className="h-3.5 w-3.5" />
              {t("action.copy")}
            </Button>
            <AlertDialogCancel size="sm" className="h-7 text-xs px-3">
              {t("action.close")}
            </AlertDialogCancel>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <ImportTableDataDialog
        open={showImportDialog}
        onOpenChange={setShowImportDialog}
        assetId={assetId}
        database={database}
        table={table}
        columns={columns}
        columnTypes={columnTypes}
        columnCategories={columnCategories}
        primaryKeys={primaryKeys}
        driver={driver}
        onSubmittingChange={setImporting}
        onSubmitStart={handleImportSubmitStart}
        isSubmitCancelled={isImportSubmitCancelled}
        onSuccess={handleImportSuccess}
      />

      <ExportTableDataDialog
        open={showExportDialog}
        onOpenChange={setShowExportDialog}
        assetId={assetId}
        database={database}
        table={table}
        columns={columns}
        rows={rows}
        totalRows={totalRows}
        page={page}
        pageSize={pageSize}
        filters={filterRequest}
        sorts={sortRequest}
        initialFormat={exportFormat}
        sqlFormatAvailable={can("export_sql")}
        onFormatChange={setExportFormat}
      />

      {/* SQL preview / submit confirmation */}
      <SqlPreviewDialog
        open={sqlDialog !== null}
        onOpenChange={(open) => {
          if (open || submitting) return;
          setSqlDialog(null);
          continueAfterSubmit.current = null;
        }}
        statements={sqlDialog?.plan.statements ?? []}
        onConfirm={sqlDialog?.mode === "confirm" ? handleConfirmSubmit : undefined}
        submitting={submitting}
      />

      <UnsavedEditsDialog
        open={pendingGuard !== null}
        summary={unsavedSummary}
        description={pendingGuard?.description}
        onStay={handleGuardStay}
        onDiscard={handleGuardDiscard}
        onSubmit={handleGuardSubmit}
      />

      <SqlPreviewDialog
        open={deletePreview !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setDeletePreview(null);
        }}
        statements={deletePreview?.statements ?? []}
        onConfirm={handleConfirmDelete}
        submitting={deleting}
        warning={
          deletePreview && !deletePreview.usesPrimaryKey ? (
            <div className="rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-warning">
              {t("query.deleteRecordNoPrimaryKeyWarning")}
            </div>
          ) : undefined
        }
      />
    </div>
  );
}
