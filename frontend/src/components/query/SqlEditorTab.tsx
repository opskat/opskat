import { memo, useState, useCallback, useRef, useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Play, Loader2, History, FileCode, PanelRight, ChevronDown } from "lucide-react";
import { toast } from "sonner";
import type * as MonacoNS from "monaco-editor";
import {
  Button,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  Popover,
  PopoverTrigger,
  PopoverContent,
  ConfirmDialog,
  useResizeHandle,
} from "@opskat/ui";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { ExecuteSQLPaged } from "../../../wailsjs/go/query/Query";
import { TableDataStatusBar } from "./TableEditorToolbar";
import { QueryResultTable } from "./QueryResultTable";
import { QueryErrorPanel } from "./QueryErrorPanel";
import { QueryResultJsonView } from "./QueryResultJsonView";
import { QueryRowDetailPanel } from "./QueryRowDetailPanel";
import { QueryViewModeToggle, type QueryViewMode } from "./QueryViewModeToggle";
import { CodeEditor } from "@/components/CodeEditor";
import { SnippetPopover } from "@/components/snippet/SnippetPopover";
import type { DynamicCompletionGetter } from "@/lib/monaco-completions";
import { buildSqlCompletions, resolveScopeTables, type SqlColumn } from "@/lib/sql-context";
import {
  isDangerousSql,
  isDangerousStatement,
  statementAt,
  statementAtOffset,
  type SqlStatement,
} from "@/lib/sqlStatements";
import { driverHasCapability, findDatabaseDriver, loadDatabaseDrivers, useDatabaseDrivers } from "@/lib/dbDrivers";
import { useDriverStatements } from "./useDriverStatements";
import { formatModKey } from "@/stores/shortcutStore";

interface SqlEditorTabProps {
  tabId: string;
  innerTabId: string;
}

interface SQLPagedResult {
  columns?: string[];
  rows?: Record<string, unknown>[];
  count?: number;
  total_count?: number;
  affected_rows?: number;
}

// 页大小可输入范围:SQL 结果整页进内存,上限沿用原下拉的最大档 500。
const SQL_PAGE_SIZE_RANGE = { min: 50, max: 500 };
const DEFAULT_PAGE_SIZE = 100;
const noop = () => {};
// 当前语句高亮走 Monaco 装饰。Monaco 会把类名里的 "/" 等字符替换成空格(bg-primary/5 会退化成不透明的 bg-primary),
// 所以这里只能放纯字母/连字符的领域类,样式定义在 globals.css。
const CURRENT_STATEMENT_DECORATION = {
  isWholeLine: true,
  className: "sql-current-statement",
  linesDecorationsClassName: "sql-current-statement-bar",
};
type RunMode = "current" | "all";
// 驱动描述里「自己切分并分类 SQL」的功能名(Go 侧 dbdriver.CapClassifyStatements)。
const CLASSIFY_STATEMENTS = "classify_statements";

/** 要执行的来源：current 模式且无选区时是整段文本与光标位置，否则是要执行的文本本身。 */
interface ExecutionSource {
  text: string;
  cursor?: number;
}

/** 驱动 driverId 是否声明了语句切分分类；以执行这一刻的驱动描述为准。 */
async function driverSplitsStatements(driverId: string | undefined): Promise<boolean> {
  if (!driverId) return false;
  return driverHasCapability(findDatabaseDriver(await loadDatabaseDrivers(), driverId), CLASSIFY_STATEMENTS);
}

// props 为稳定字符串 —— memo 可阻止父组件的 innerTabs 结构变化传导进来
export const SqlEditorTab = memo(function SqlEditorTab({ tabId, innerTabId }: SqlEditorTabProps) {
  const { t } = useTranslation();
  // 细粒度订阅：actions 是稳定引用，dbState 整体取用（innerTab.sql 不再每键入都写回，所以 dbState 不会高频变化）
  const dbState = useQueryStore((s) => s.dbStates[tabId]);
  const updateInnerTab = useQueryStore((s) => s.updateInnerTab);
  const addSqlHistory = useQueryStore((s) => s.addSqlHistory);
  const loadTableMeta = useQueryStore((s) => s.loadTableMeta);
  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));
  const queryMeta = tab?.meta as QueryTabMeta | undefined;
  const editorRef = useRef<MonacoNS.editor.IStandaloneCodeEditor | null>(null);

  const assetId = queryMeta?.assetId ?? 0;
  const driverId = queryMeta?.driver;
  // 声明了语句切分分类的驱动:当前语句、可执行状态与危险确认按驱动的切分;其余照旧按分号切分。
  const drivers = useDatabaseDrivers();
  const driverSplits =
    drivers.status === "ready" &&
    !!driverId &&
    driverHasCapability(findDatabaseDriver(drivers.drivers, driverId), CLASSIFY_STATEMENTS);
  const driverStatements = useDriverStatements(assetId, driverSplits);
  const databases = useMemo(() => dbState?.databases || [], [dbState?.databases]);

  // 只在 mount 时读一次持久化内容 —— 之后编辑器内容由 Monaco 自管（非受控）
  const innerTabAtMount = useRef(dbState?.innerTabs.find((t) => t.id === innerTabId));
  const persistedSql = innerTabAtMount.current?.type === "sql" ? innerTabAtMount.current.sql : undefined;
  const persistedDb = innerTabAtMount.current?.type === "sql" ? innerTabAtMount.current.selectedDb : undefined;
  const persistedHeight = innerTabAtMount.current?.type === "sql" ? innerTabAtMount.current.editorHeight : undefined;

  // history 需要跟当前 innerTab 保持同步（执行 SQL 后会追加）
  const innerTab = dbState?.innerTabs.find((t) => t.id === innerTabId);
  const sqlHistory = innerTab?.type === "sql" ? innerTab.history || [] : [];

  // Editor/result split — drag the bar between them to adjust editor height.
  const editorAreaRef = useRef<HTMLDivElement>(null);
  const { size: editorHeight, handleMouseDown: handleSplitterDown } = useResizeHandle({
    axis: "y",
    defaultSize: persistedHeight && persistedHeight > 0 ? persistedHeight : 160,
    minSize: 80,
    maxSize: 800,
    onResizeEnd: (h) => updateInnerTab(tabId, innerTabId, { editorHeight: h }),
    targetRef: editorAreaRef,
  });

  // 当前 SQL 不作为 React state：由 editor model 持有，通过 ref/getValue 读取
  const sqlRef = useRef(persistedSql || "");
  // 仅 "空 ↔ 非空" 跨边界时 setState，用于驱动"执行"按钮禁用态
  const [isEmpty, setIsEmpty] = useState((persistedSql || "").length === 0);
  // 执行按钮主操作(⌘↵)是否有东西可执行:有选区,或光标处有语句。光标起点在 0。
  const [runnable, setRunnable] = useState(() => statementAt(persistedSql || "", 0) !== null);
  const [hasSelection, setHasSelection] = useState(false);
  const [selectedDb, setSelectedDb] = useState(persistedDb || queryMeta?.defaultDatabase || "");
  const [columns, setColumns] = useState<string[]>([]);
  const [rows, setRows] = useState<Record<string, unknown>[]>([]);
  const [totalRows, setTotalRows] = useState<number | null>(null);
  const [affectedRows, setAffectedRows] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [elapsedMs, setElapsedMs] = useState<number | null>(null);
  const latestRequest = useRef(0);
  const [error, setError] = useState<string | null>(null);
  // 待危险确认的文本:确认时执行的就是弹窗前判断过的这段,不再重读编辑器。
  const [pendingDangerSql, setPendingDangerSql] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);

  // Pagination state
  const [page, setPage] = useState(0);
  // 内层 tab 常驻挂载,本地 state 即"按 tab 记住"。
  const [viewMode, setViewMode] = useState<QueryViewMode>("table");
  const [rowDetailOpen, setRowDetailOpen] = useState(false);
  const [detailRowIdx, setDetailRowIdx] = useState<number | null>(null);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [pageInput, setPageInput] = useState("1");
  // Store the last executed SQL for pagination
  const [lastExecSql, setLastExecSql] = useState("");

  const totalPages = totalRows != null ? Math.max(1, Math.ceil(totalRows / pageSize)) : null;

  // Set default database when databases load
  useEffect(() => {
    if (!selectedDb && databases.length > 0) {
      setSelectedDb(queryMeta?.defaultDatabase || databases[0]);
    }
  }, [databases, selectedDb, queryMeta?.defaultDatabase]);

  // Persist selectedDb to store (SQL 通过 commit 时机单独同步)
  useEffect(() => {
    updateInnerTab(tabId, innerTabId, { selectedDb });
  }, [selectedDb, tabId, innerTabId, updateInnerTab]);

  // 把最新的 sql 同步进 store。用 ref 桥接，handleEditorMount 里注册的回调永远读到最新版本。
  const commitSqlRef = useRef<() => void>(() => {
    updateInnerTab(tabId, innerTabId, { sql: sqlRef.current });
  });
  useEffect(() => {
    commitSqlRef.current = () => {
      updateInnerTab(tabId, innerTabId, { sql: sqlRef.current });
    };
  }, [tabId, innerTabId, updateInnerTab]);

  // 卸载时立即 flush（关闭 inner tab / 关闭整个 query tab 时触发）
  useEffect(() => {
    return () => {
      commitSqlRef.current();
    };
  }, []);

  // Sync page input
  useEffect(() => {
    setPageInput(String(page + 1));
  }, [page]);

  // 必须是稳定引用:网格在这两个回调的引用变化时会重置选中态,内联箭头会让每次
  // 父组件 re-render 都把刚选中的单元格清掉,行详情面板永远停在空态。
  const handleSelectedCellChange = useCallback((cell: { rowIdx: number } | null) => {
    setDetailRowIdx(cell?.rowIdx ?? null);
  }, []);

  // 网格选中单元格时会先发 onSelectedCellChange 再发 onSelectedRowsChange([]),
  // 无条件接管会把刚设好的当前行抹掉 —— 只有真的选了整行才接管。
  const handleSelectedRowsChange = useCallback((rowIdxs: number[]) => {
    if (rowIdxs.length > 0) setDetailRowIdx(rowIdxs.length === 1 ? rowIdxs[0] : null);
  }, []);

  // ⌘↵:有选区执行选区,否则执行光标所在语句;⇧⌘↵:执行编辑器全部内容。按下那一刻读编辑器。
  const getExecutionSource = useCallback((mode: RunMode): ExecutionSource | null => {
    const editor = editorRef.current;
    const model = editor?.getModel();
    if (!editor || !model) return null;
    if (mode === "all") return { text: model.getValue().trim() };
    const sel = editor.getSelection();
    if (sel && !sel.isEmpty()) return { text: model.getValueInRange(sel).trim() };
    return { text: model.getValue(), cursor: model.getOffsetAt(editor.getPosition()!) };
  }, []);

  const fetchPage = useCallback(
    async (execSql: string, pageNum: number, size = pageSize) => {
      if (!execSql || !assetId) return;

      const requestId = ++latestRequest.current;
      setLoading(true);
      setError(null);

      const startedAt = performance.now();
      try {
        const result = await ExecuteSQLPaged(assetId, execSql, selectedDb, pageNum, size);
        if (latestRequest.current !== requestId) return;
        const parsed: SQLPagedResult = JSON.parse(result);
        setElapsedMs(Math.round(performance.now() - startedAt));

        if (parsed.affected_rows !== undefined) {
          setAffectedRows(parsed.affected_rows);
          setColumns([]);
          setRows([]);
          setTotalRows(null);
        } else {
          setAffectedRows(null);
          setColumns(parsed.columns || []);
          setRows(parsed.rows || []);
          setTotalRows(parsed.total_count ?? null);
        }
      } catch (e) {
        if (latestRequest.current !== requestId) return;
        setElapsedMs(null);
        setError(String(e));
        // 失败不再是 DML 结果:底部状态条要显示「加载失败」。列保留:重试拿到同样的列时列宽、冻结等不重置。
        setAffectedRows(null);
        setRows([]);
        setTotalRows(null);
      } finally {
        if (latestRequest.current === requestId) setLoading(false);
      }
    },
    [assetId, selectedDb, pageSize]
  );

  const doExecute = useCallback(
    async (execSql: string) => {
      addSqlHistory(tabId, innerTabId, execSql);

      setLastExecSql(execSql);
      setPage(0);
      await fetchPage(execSql, 0);
    },
    [fetchPage, addSqlHistory, tabId, innerTabId]
  );

  // 翻页 / 改页大小都在事件里直接取数:page 与取数同步更新,回到第 1 页也会重新取。
  const goToPage = useCallback(
    (target: number) => {
      setPage(target);
      void fetchPage(lastExecSql, target);
    },
    [fetchPage, lastExecSql]
  );

  const handlePageSizeChange = useCallback(
    (size: number) => {
      setPageSize(size);
      setPage(0);
      void fetchPage(lastExecSql, 0, size);
    },
    [fetchPage, lastExecSql]
  );

  const handleStopLoading = useCallback(() => {
    latestRequest.current += 1;
    setLoading(false);
    toast.info(t("query.stopLoadingToast"));
  }, [t]);

  // 按执行这一刻的文本定出要执行的 SQL 与是否危险。驱动切分时语句与类型取自驱动(执行时重新取),
  // 驱动出错或不可用时抛出它给的原因,不退回按分号切分。
  const resolveExecution = useCallback(
    async (source: ExecutionSource): Promise<{ sql: string; dangerous: boolean }> => {
      if (!(await driverSplitsStatements(driverId))) {
        const sql = source.cursor === undefined ? source.text : (statementAt(source.text, source.cursor)?.text ?? "");
        return { sql, dangerous: isDangerousSql(sql) };
      }
      const statements = await driverStatements.split(source.text);
      if (source.cursor === undefined) {
        return { sql: source.text, dangerous: statements.some(isDangerousStatement) };
      }
      const current = statementAtOffset(statements, source.text.length, source.cursor);
      return { sql: current?.text ?? "", dangerous: !!current && isDangerousStatement(current) };
    },
    [driverId, driverStatements]
  );

  const execute = useCallback(
    async (mode: RunMode) => {
      const source = getExecutionSource(mode);
      if (!source?.text.trim() || !assetId) return;
      let resolved: { sql: string; dangerous: boolean };
      try {
        resolved = await resolveExecution(source);
      } catch (e) {
        toast.error(t("query.statementSplitFailed"), { description: String(e) });
        return;
      }
      if (!resolved.sql) return;
      if (resolved.dangerous) {
        setPendingDangerSql(resolved.sql);
      } else {
        void doExecute(resolved.sql);
      }
    },
    [getExecutionSource, assetId, resolveExecution, doExecute, t]
  );

  // Monaco 命令是在 onMount 时一次性注册的，闭包里拿到的 execute 会过期；
  // 用 ref 桥接到最新版。
  const executeRef = useRef(execute);
  useEffect(() => {
    executeRef.current = execute;
  }, [execute]);

  // 字段补全的按需预取：用 ref 桥接，供 onMount 时注册的内容变化回调调用最新实现。
  const prefetchColumnsRef = useRef<(sql: string) => void>(() => {});
  const inflightMetaRef = useRef<Set<string>>(new Set());

  const handleEditorMount = useCallback(
    (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: typeof MonacoNS) => {
      editorRef.current = editor;
      editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => {
        void executeRef.current("current");
      });
      editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.Enter, () => {
        void executeRef.current("all");
      });

      // 光标 / 选区 / 焦点变化:刷新"可执行"状态与当前语句高亮。
      const decorations = editor.createDecorationsCollection();
      let focused = editor.hasTextFocus();
      // 光标处的语句。驱动切分时取与当前文本对应的驱动结果:还没取到(输入后尚未停顿)返回 undefined,
      // 保持现有高亮与可执行状态等它;驱动出错时没有当前语句,但仍可执行,执行时提示原因。
      const currentStatement = (text: string, offset: number): { statement: SqlStatement | null } | undefined => {
        if (!driverStatements.enabled()) return { statement: statementAt(text, offset) };
        const split = driverStatements.resultFor(text);
        if (!split) return undefined;
        if (split.error !== undefined) return { statement: null };
        return { statement: statementAtOffset(split.statements, text.length, offset) };
      };
      const syncCursorState = () => {
        const model = editor.getModel();
        const position = editor.getPosition();
        if (!model || !position) return;
        const sel = editor.getSelection();
        const selected = !!sel && !sel.isEmpty();
        setHasSelection(selected);
        if (selected) {
          setRunnable(model.getValueInRange(sel).trim() !== "");
          decorations.clear();
          return;
        }
        const text = model.getValue();
        const found = currentStatement(text, model.getOffsetAt(position));
        if (!found) return;
        const current = found.statement;
        const splitFailed = driverStatements.enabled() && driverStatements.resultFor(text)?.error !== undefined;
        setRunnable(current !== null || (splitFailed && text.trim() !== ""));
        if (focused && current) {
          decorations.set([
            {
              range: {
                startLineNumber: model.getPositionAt(current.start).lineNumber,
                startColumn: 1,
                endLineNumber: model.getPositionAt(current.end).lineNumber,
                endColumn: 1,
              },
              options: CURRENT_STATEMENT_DECORATION,
            },
          ]);
        } else {
          decorations.clear();
        }
      };
      editor.onDidChangeCursorSelection(syncCursorState);
      driverStatements.onChange(syncCursorState);
      editor.onDidFocusEditorWidget(() => {
        focused = true;
        syncCursorState();
      });
      syncCursorState();

      // 订阅内容变化：更新 ref + 维护 isEmpty + debounce 300ms 回写 store
      const model = editor.getModel();
      if (model) {
        let prevEmpty = model.getValue().length === 0;
        let timer: ReturnType<typeof setTimeout> | null = null;
        model.onDidChangeContent(() => {
          const val = model.getValue();
          sqlRef.current = val;
          const nowEmpty = val.length === 0;
          if (nowEmpty !== prevEmpty) {
            prevEmpty = nowEmpty;
            setIsEmpty(nowEmpty);
          }
          if (timer) clearTimeout(timer);
          timer = setTimeout(() => {
            timer = null;
            commitSqlRef.current();
            prefetchColumnsRef.current(sqlRef.current);
            driverStatements.refresh(sqlRef.current);
          }, 300);
        });
      }

      // 失焦立即提交，避免等 300ms debounce
      editor.onDidBlurEditorWidget(() => {
        focused = false;
        syncCursorState();
        commitSqlRef.current();
      });
    },
    [driverStatements]
  );

  // 驱动切分启用(驱动描述加载完成)或换了资产时,先按编辑器现有文本取一次切分。
  useEffect(() => {
    if (driverSplits) driverStatements.refresh(sqlRef.current);
  }, [driverSplits, assetId, driverStatements]);

  // Insert snippet text at the current Monaco selection / cursor, without auto-execute.
  const handleSnippetInsert = useCallback((content: string) => {
    const editor = editorRef.current;
    if (!editor) return;
    const selection = editor.getSelection();
    if (!selection) return;
    editor.executeEdits("snippet-insert", [
      {
        range: selection,
        text: content,
        forceMoveMarkers: true,
      },
    ]);
    editor.focus();
  }, []);

  // 当前选中库下的表 + 已加载表结构，一并注入 monaco 补全（表名 & 字段）。
  const tables = useMemo(() => dbState?.tables?.[selectedDb] ?? [], [dbState?.tables, selectedDb]);
  const loadedMeta = dbState?.tableMeta;

  // 规范表名 -> 列，供 buildSqlCompletions 组装字段项（key 与 tables 中的条目一致）。
  const columnsByTable = useMemo(() => {
    const map: Record<string, SqlColumn[]> = {};
    if (loadedMeta) {
      for (const tname of tables) {
        const cols = loadedMeta[`${selectedDb}.${tname}`]?.columns;
        if (cols) map[tname] = cols;
      }
    }
    return map;
  }, [loadedMeta, tables, selectedDb]);

  const sqlCompletions = useCallback<DynamicCompletionGetter>(
    ({ monaco, range, model, position }) => {
      const line = model.getLineContent(position.lineNumber);
      const textBeforeCursor = line.slice(0, position.column - 1);
      return buildSqlCompletions({
        textBeforeCursor,
        fullSql: model.getValue(),
        tables,
        db: selectedDb,
        columnsByTable,
      }).map((it) => ({
        label: it.label,
        kind:
          it.kind === "column" ? monaco.languages.CompletionItemKind.Field : monaco.languages.CompletionItemKind.Class,
        insertText: it.insertText,
        detail: it.detail,
        range,
        sortText: it.sortText,
      }));
    },
    [tables, selectedDb, columnsByTable]
  );

  // 预取当前 SQL 中 FROM/JOIN 涉及的表结构（去重 + 跳过在途/已缓存）。字段补全同步读取缓存。
  const prefetchColumns = useCallback(
    (sql: string) => {
      if (!selectedDb) return;
      for (const tname of resolveScopeTables(sql, tables)) {
        const key = `${selectedDb}.${tname}`;
        if (loadedMeta?.[key] || inflightMetaRef.current.has(key)) continue;
        inflightMetaRef.current.add(key);
        void Promise.resolve(loadTableMeta(tabId, selectedDb, tname)).finally(() =>
          inflightMetaRef.current.delete(key)
        );
      }
    },
    [selectedDb, tables, loadedMeta, loadTableMeta, tabId]
  );
  useEffect(() => {
    prefetchColumnsRef.current = prefetchColumns;
  }, [prefetchColumns]);
  // 初次加载 / 切库 / 表列表就绪后，预取一次已有 SQL 涉及的表结构。
  useEffect(() => {
    prefetchColumnsRef.current(sqlRef.current);
  }, [selectedDb, tables]);

  const handlePageInputConfirm = useCallback(() => {
    const num = parseInt(pageInput, 10);
    if (isNaN(num) || num < 1) {
      setPageInput(String(page + 1));
      return;
    }
    const target = totalPages ? Math.min(num, totalPages) - 1 : num - 1;
    if (target !== page) goToPage(target);
    else setPageInput(String(page + 1));
  }, [pageInput, page, totalPages, goToPage]);

  const hasNext = totalPages != null ? page < totalPages - 1 : rows.length === pageSize;
  const hasPrev = page > 0;
  const findScope = useMemo(() => ({ rowCount: rows.length, page: page + 1 }), [rows.length, page]);
  const errorPanel = error ? (
    <QueryErrorPanel title={t("query.executeFailed")} error={error} onRetry={() => void fetchPage(lastExecSql, page)} />
  ) : null;

  return (
    <div className="flex flex-col h-full">
      {/* SQL editor area */}
      <div ref={editorAreaRef} className="flex flex-col shrink-0" style={{ height: editorHeight }}>
        {/* Toolbar */}
        <div className="flex items-center gap-2 px-3 py-1.5 border-b border-border bg-muted/30">
          <div className="flex items-center">
            <Button
              variant="default"
              size="sm"
              data-testid="sql-execute-button"
              className="h-7 text-xs gap-1 rounded-r-none"
              onClick={() => void execute("current")}
              disabled={loading || !runnable}
            >
              {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              {loading ? t("query.executing") : t("query.execute")}
              {!loading && <span className="text-[10px] opacity-70">{formatModKey("Enter")}</span>}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant="default"
                  size="sm"
                  data-testid="sql-execute-menu-button"
                  className="h-7 w-6 px-0 rounded-l-none border-l border-primary-foreground/30"
                  aria-label={t("query.executeMenu")}
                  disabled={loading}
                >
                  <ChevronDown className="h-3.5 w-3.5" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="min-w-[14rem]">
                <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
                  {hasSelection ? t("query.executeHintSelection") : t("query.executeHintCursor")}
                </DropdownMenuLabel>
                <DropdownMenuItem
                  className="text-xs justify-between gap-4"
                  disabled={!runnable}
                  onSelect={() => void execute("current")}
                >
                  {hasSelection ? t("query.executeSelection") : t("query.executeCurrent")}
                  <span className="text-muted-foreground">{formatModKey("Enter")}</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  className="text-xs justify-between gap-4"
                  disabled={isEmpty}
                  onSelect={() => void execute("all")}
                >
                  {t("query.executeAll")}
                  <span className="text-muted-foreground">{formatModKey("Enter", { shift: true })}</span>
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          <Select value={selectedDb} onValueChange={setSelectedDb}>
            <SelectTrigger size="sm" className="h-7 w-[160px] text-xs">
              <SelectValue placeholder={t("query.databases")} />
            </SelectTrigger>
            <SelectContent>
              {databases.map((db) => (
                <SelectItem key={db} value={db} className="text-xs">
                  {db}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <SnippetPopover
            category="sql"
            onInsert={handleSnippetInsert}
            trigger={
              <Button
                variant="ghost"
                size="sm"
                className="h-7 text-xs gap-1"
                title={t("snippet.popover.triggerButton")}
                aria-label={t("snippet.popover.triggerButton")}
              >
                <FileCode className="h-3.5 w-3.5" />
                {t("snippet.popover.insert")}
              </Button>
            }
          />
          {sqlHistory.length > 0 && (
            <Popover open={showHistory} onOpenChange={setShowHistory}>
              <PopoverTrigger asChild>
                <Button variant="ghost" size="sm" className="h-7 text-xs gap-1">
                  <History className="h-3.5 w-3.5" />
                  {t("query.history")}
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-[400px] max-h-[300px] overflow-auto p-1">
                {sqlHistory.map((item, idx) => (
                  <button
                    key={idx}
                    className="w-full text-left px-2 py-1.5 text-xs font-mono rounded hover:bg-accent truncate block"
                    onClick={() => {
                      editorRef.current?.setValue(item);
                      setShowHistory(false);
                    }}
                    title={item}
                  >
                    {item.length > 80 ? item.substring(0, 80) + "..." : item}
                  </button>
                ))}
              </PopoverContent>
            </Popover>
          )}
        </div>
        {/* Monaco editor; automaticLayout lets it reflow when the splitter changes height. */}
        <div className="flex-1 min-h-0 w-full overflow-hidden bg-background">
          <CodeEditor
            testId="sql-editor"
            defaultValue={persistedSql || ""}
            language="sql"
            placeholder={t("query.sqlPlaceholder")}
            onMount={handleEditorMount}
            dynamicCompletions={sqlCompletions}
          />
        </div>
      </div>

      {/* Vertical splitter between editor and results */}
      <div
        role="separator"
        aria-orientation="horizontal"
        className="h-[4px] shrink-0 cursor-row-resize border-y border-border bg-muted/30 hover:bg-ring/40 active:bg-ring/60 transition-colors"
        onMouseDown={handleSplitterDown}
      />

      {/* Result area */}
      <div className="flex-1 min-h-0 flex flex-col">
        {affectedRows !== null && !error && (
          <div className="px-3 py-2 text-xs text-muted-foreground">
            {t("query.affectedRows")}: {affectedRows}
          </div>
        )}
        {columns.length > 0 && (
          <div className="flex items-center gap-2 border-b border-border bg-muted/20 px-3 py-1.5 shrink-0">
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
          </div>
        )}
        <div className="flex min-h-0 flex-1">
          <div className="flex min-w-0 flex-1 flex-col">
            {viewMode === "json" && error ? (
              errorPanel
            ) : viewMode === "json" ? (
              <QueryResultJsonView rows={rows} columns={columns} />
            ) : (
              <QueryResultTable
                columns={columns}
                rows={rows}
                loading={loading}
                keepShapeWhileLoading
                error={error ?? undefined}
                errorPanel={errorPanel}
                showRowNumber
                rowNumberOffset={page * pageSize}
                onSelectedCellChange={handleSelectedCellChange}
                onSelectedRowsChange={handleSelectedRowsChange}
                findEnabled
                findScope={findScope}
              />
            )}
          </div>
          {rowDetailOpen && (
            <QueryRowDetailPanel
              rows={rows}
              columns={columns}
              rowIdx={detailRowIdx}
              rowNumberOffset={page * pageSize}
              onClose={() => setRowDetailOpen(false)}
            />
          )}
        </div>
        {lastExecSql && affectedRows === null && (
          <TableDataStatusBar
            gridEdit={false}
            pendingEditCount={0}
            sqlSummary=""
            totalRows={totalRows}
            page={page}
            totalPages={totalPages}
            pageSize={pageSize}
            pageInput={pageInput}
            hasPrev={hasPrev}
            hasNext={hasNext}
            selectedRowCount={0}
            submitting={false}
            loading={loading}
            loadingPage={loading ? page : null}
            failed={!!error}
            elapsedMs={elapsedMs}
            rowCount={rows.length}
            pageSizeRange={SQL_PAGE_SIZE_RANGE}
            refreshTitle={t("query.refreshTable")}
            onRefresh={() => void fetchPage(lastExecSql, page)}
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
            onAddRow={noop}
            onDeleteRows={noop}
            onApplyChanges={noop}
            onDiscardChanges={noop}
          />
        )}
      </div>

      {/* Dangerous SQL confirmation */}
      <ConfirmDialog
        open={pendingDangerSql !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDangerSql(null);
        }}
        title={t("query.dangerousSqlTitle")}
        description={t("query.dangerousSqlDesc")}
        cancelText={t("action.cancel")}
        confirmText={t("query.execute")}
        onConfirm={() => pendingDangerSql && void doExecute(pendingDangerSql)}
      />
    </div>
  );
});
