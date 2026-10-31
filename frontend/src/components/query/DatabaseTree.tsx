import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import {
  ChevronRight,
  ChevronDown,
  Database,
  Folder,
  Table2,
  SquarePen,
  RefreshCw,
  Loader2,
  AlertCircle,
  Search,
  Plus,
  Wrench,
  Trash2,
  Eraser,
  Columns3,
} from "lucide-react";
import {
  Button,
  Input,
  ScrollArea,
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ConfirmDialog,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@opskat/ui";
import {
  ExecuteSQL,
  GetDropTableStatements,
  GetStarterQuery,
  GetTruncateTableStatements,
} from "../../../wailsjs/go/query/Query";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { CreateDatabaseDialog } from "./CreateDatabaseDialog";
import { ObjectBrowserSection } from "./ObjectBrowserSection";
import { useCanCreateTable, useDesignerAccess } from "./designer/access";
import { defaultCreateTarget, targetLabel, targetOfTable, type CreateTableTarget } from "./designer/createTarget";
import {
  OBJECT_BROWSER_CAPABILITIES,
  driverHasCapability,
  findDatabaseDriver,
  useDatabaseDrivers,
} from "@/lib/dbDrivers";

interface DatabaseTreeProps {
  tabId: string;
}

interface TableNode {
  name: string;
  qualifiedName: string;
}

interface SchemaGroup {
  schema: string;
  schemaMatch: boolean;
  tables: TableNode[];
}

interface TableRef {
  database: string;
  table: string;
}

function tableKey(database: string, table: string): string {
  return `${database}.${table}`;
}

interface VisibleDb {
  db: string;
  dbMatch: boolean;
  tables?: string[];
  schemas?: SchemaGroup[];
}

function splitSchemaTable(table: string): { schema: string; name: string; qualifiedName: string } | null {
  const dot = table.indexOf(".");
  if (dot <= 0) return null;
  return { schema: table.slice(0, dot), name: table.slice(dot + 1), qualifiedName: table };
}

function buildSchemaGroups(tables: string[], filterLower: string, dbMatch: boolean) {
  const groups = new Map<string, { schemaMatch: boolean; tables: TableNode[] }>();
  for (const table of tables) {
    const parsed = splitSchemaTable(table);
    if (!parsed) continue;
    const qualifiedLower = parsed.qualifiedName.toLowerCase();
    const nameLower = parsed.name.toLowerCase();
    const schemaLower = parsed.schema.toLowerCase();
    const schemaMatch = !!filterLower && schemaLower.includes(filterLower);
    const tableMatch =
      !filterLower || dbMatch || schemaMatch || nameLower.includes(filterLower) || qualifiedLower.includes(filterLower);
    if (!tableMatch) continue;

    const group = groups.get(parsed.schema) ?? { schemaMatch: false, tables: [] };
    group.schemaMatch ||= schemaMatch;
    group.tables.push({ name: parsed.name, qualifiedName: parsed.qualifiedName });
    groups.set(parsed.schema, group);
  }
  return Array.from(groups.entries()).map(([schema, group]) => ({
    schema,
    schemaMatch: group.schemaMatch,
    tables: group.tables,
  }));
}

function buildUngroupedTables(tables: string[], filterLower: string, dbMatch: boolean): string[] {
  return tables.filter((table) => {
    if (splitSchemaTable(table)) return false;
    return !filterLower || dbMatch || table.toLowerCase().includes(filterLower);
  });
}

/** 资产的驱动没有注册时的提示(侧栏与右侧内容区共用)。 */
export function DriverUnavailableNotice({ driver }: { driver: string | undefined }) {
  const { t } = useTranslation();
  return (
    <div
      role="alert"
      className="flex items-start gap-2 border-b border-destructive/20 bg-destructive/10 px-2 py-2 text-xs text-destructive"
    >
      <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
      <span className="break-all">{t("query.driverUnavailable", { driver: driver ?? "" })}</span>
    </div>
  );
}

export function DatabaseTree({ tabId }: DatabaseTreeProps) {
  const { t } = useTranslation();
  const {
    dbStates,
    loadDatabases,
    toggleDbExpand,
    toggleSchemaExpand,
    openTableTab,
    openDesignerTab,
    openSqlTab,
    openCreateTableTab,
    refreshTables,
  } = useQueryStore();
  const treeRef = useRef<HTMLDivElement>(null);
  const [showCreateDatabase, setShowCreateDatabase] = useState(false);
  const [confirmAction, setConfirmAction] = useState<{
    type: "drop" | "truncate";
    targets: TableRef[];
  } | null>(null);
  const [executingAction, setExecutingAction] = useState(false);

  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));
  const tabMeta = tab?.meta as QueryTabMeta | undefined;
  const driver = tabMeta?.driver;
  const driversState = useDatabaseDrivers();
  const driverDescriptor = useMemo(
    () => (driversState.status === "ready" ? findDatabaseDriver(driversState.drivers, driver ?? "") : undefined),
    [driversState, driver]
  );
  const driverUnavailable = driversState.status === "ready" && !driverDescriptor;
  const driverReady = !!driverDescriptor;
  const can = (capability: string) => driverHasCapability(driverDescriptor, capability);
  // 能改表时「设计表」打开设计器,不能改(驱动没声明或只读连接)时「查看结构」打开只读设计器。
  const access = useDesignerAccess(driverDescriptor, tabMeta?.assetId);
  // 驱动没声明建表或资产是只读连接时,新建表的入口一个都不出现。
  const canCreateTable = useCanCreateTable(driverDescriptor, tabMeta?.assetId);
  const defaultDatabase = tabMeta?.defaultDatabase ?? "";
  const openNewTable = (target: CreateTableTarget) => openCreateTableTab(tabId, target.database, target.schema);

  const dbState = dbStates[tabId];
  const [filter, setFilter] = useState("");
  const [showFilter, setShowFilter] = useState(false);
  // 选中集用 "db.table" 作键;顺序即用户可见的树顺序,批量操作照此执行。
  const [selectedKeys, setSelectedKeys] = useState<string[]>([]);
  const selectionAnchorRef = useRef<string | null>(null);
  const [openTables, setOpenTables] = useState<Record<string, boolean>>({});

  // Auto-load only when there's nothing cached. Restored tabs come in with
  // databases/tables already populated from localStorage, so we skip the
  // refetch and rely on the user's refresh button.
  // Nothing is sent before the driver is known to be registered.
  useEffect(() => {
    if (driverReady && dbState && dbState.databases.length === 0 && !dbState.loadingDbs) {
      loadDatabases(tabId);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabId, driverReady]);

  const filterLower = filter.trim().toLowerCase();

  const visibleDbs = useMemo(() => {
    if (!dbState) return [];
    const schemaAware = driverHasCapability(driverDescriptor, "schemas");
    if (!filterLower) {
      return dbState.databases.map((db) => {
        const loaded = dbState.tables[db];
        return schemaAware && loaded
          ? {
              db,
              dbMatch: false,
              tables: buildUngroupedTables(loaded, "", false),
              schemas: buildSchemaGroups(loaded, "", false),
            }
          : { db, dbMatch: false, tables: loaded };
      });
    }
    const out: VisibleDb[] = [];
    for (const db of dbState.databases) {
      const dbMatch = db.toLowerCase().includes(filterLower);
      const loaded = dbState.tables[db];
      const schemaGroups = schemaAware && loaded ? buildSchemaGroups(loaded, filterLower, dbMatch) : undefined;
      const matchedTables = schemaAware
        ? loaded && buildUngroupedTables(loaded, filterLower, dbMatch)
        : loaded?.filter((t) => dbMatch || t.toLowerCase().includes(filterLower));
      if (dbMatch) {
        out.push({ db, dbMatch: true, tables: matchedTables, schemas: schemaGroups });
      } else if (schemaGroups && schemaGroups.length > 0) {
        out.push({ db, dbMatch: false, tables: matchedTables, schemas: schemaGroups });
      } else if (matchedTables && matchedTables.length > 0) {
        out.push({ db, dbMatch: false, tables: matchedTables });
      }
    }
    return out;
  }, [dbState, driverDescriptor, filterLower]);

  // 树上此刻真正可见的表节点,顺序与渲染顺序一致 —— Shift 取区间和筛选后剪枝都以它为准。
  const visibleTableRefs = useMemo(() => {
    const refs: TableRef[] = [];
    for (const { db, tables, schemas } of visibleDbs) {
      const expanded = filterLower ? true : (dbState?.expandedDbs ?? []).includes(db);
      if (!expanded) continue;
      const tablesOpen = filterLower ? true : (openTables[db] ?? true);
      if (!tablesOpen) continue;
      for (const table of tables ?? []) refs.push({ database: db, table });
      const expandedSchemas = dbState?.expandedSchemas[db] ?? [];
      for (const group of schemas ?? []) {
        if (!filterLower && !expandedSchemas.includes(group.schema)) continue;
        for (const node of group.tables) refs.push({ database: db, table: node.qualifiedName });
      }
    }
    return refs;
  }, [dbState?.expandedDbs, dbState?.expandedSchemas, filterLower, openTables, visibleDbs]);

  const visibleKeys = useMemo(
    () => visibleTableRefs.map((ref) => tableKey(ref.database, ref.table)),
    [visibleTableRefs]
  );

  // 离开树的节点要真的从选中集里删掉。只在渲染时过滤是不够的 —— 重新展开或清掉筛选
  // 会把它们原样复活,之后的批量清空 / 删除就会悄悄多带上几张表。

  /** 改筛选:按改之前树上可见的节点剪枝,用户看得见的留下,被筛掉的不再回来。 */
  const changeFilter = useCallback(
    (value: string) => {
      setSelectedKeys((prev) => prev.filter((key) => visibleKeys.includes(key)));
      setFilter(value);
    },
    [visibleKeys]
  );

  /** 折叠库 / 折叠表目录 / 折叠 schema / 刷新表:该前缀下的节点整批离开树。 */
  const dropSelectionUnder = useCallback((prefix: string) => {
    setSelectedKeys((prev) => prev.filter((key) => !key.startsWith(prefix)));
  }, []);

  // 选中集按树的显示顺序排列 —— 批量操作照此顺序执行。
  const selectedVisibleKeys = useMemo(
    () => visibleKeys.filter((key) => selectedKeys.includes(key)),
    [selectedKeys, visibleKeys]
  );

  const selectedRefs = useMemo(
    () => visibleTableRefs.filter((ref) => selectedKeys.includes(tableKey(ref.database, ref.table))),
    [selectedKeys, visibleTableRefs]
  );

  const selectTable = useCallback(
    (database: string, table: string, event?: Pick<React.MouseEvent, "ctrlKey" | "metaKey" | "shiftKey">) => {
      const key = tableKey(database, table);
      const anchorKey = selectionAnchorRef.current;
      const isRange = !!event?.shiftKey && anchorKey != null;
      const isToggle = !!event?.ctrlKey || !!event?.metaKey;

      if (isRange) {
        const from = visibleKeys.indexOf(anchorKey);
        const to = visibleKeys.indexOf(key);
        if (from === -1 || to === -1) {
          setSelectedKeys([key]);
          selectionAnchorRef.current = key;
          return;
        }
        setSelectedKeys(visibleKeys.slice(Math.min(from, to), Math.max(from, to) + 1));
        return;
      }

      if (isToggle) {
        setSelectedKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
      } else {
        setSelectedKeys([key]);
      }
      selectionAnchorRef.current = key;
    },
    [visibleKeys]
  );

  // 「在左侧树中定位」:表列表到位后(未加载的库由 revealTable 触发加载)在渲染期同步本地状态 ——
  // 清筛选、展开表分组、单选该表;schema 展开(store 动作)与滚动在 effect 里做。
  const revealRequest = dbState?.revealRequest;
  const [handledNonce, setHandledNonce] = useState(revealRequest?.nonce ?? 0);
  const [reveal, setReveal] = useState<{ nonce: number; db: string; table: string } | null>(null);
  if (revealRequest && revealRequest.nonce !== handledNonce && dbState?.tables[revealRequest.db]) {
    setHandledNonce(revealRequest.nonce);
    setReveal(revealRequest);
    setFilter("");
    setOpenTables((prev) => ({ ...prev, [revealRequest.db]: true }));
    setSelectedKeys([tableKey(revealRequest.db, revealRequest.table)]);
  }
  const revealSchema = reveal
    ? visibleDbs
        .find((entry) => entry.db === reveal.db)
        ?.schemas?.find((g) => g.tables.some((node) => node.qualifiedName === reveal.table))?.schema
    : undefined;
  const revealSchemaOpen = !!reveal && (dbState?.expandedSchemas[reveal.db] ?? []).includes(revealSchema ?? "");
  useEffect(() => {
    if (!reveal) return;
    selectionAnchorRef.current = tableKey(reveal.db, reveal.table);
    if (revealSchema && !revealSchemaOpen) toggleSchemaExpand(tabId, reveal.db, revealSchema);
  }, [reveal, revealSchema, revealSchemaOpen, toggleSchemaExpand, tabId]);

  const scrolledNonceRef = useRef(0);
  useEffect(() => {
    if (!reveal || reveal.nonce === scrolledNonceRef.current) return;
    const key = tableKey(reveal.db, reveal.table);
    if (!visibleKeys.includes(key)) return;
    const node = Array.from(treeRef.current?.querySelectorAll<HTMLElement>("[data-table-node]") ?? []).find(
      (el) => el.dataset.tableNode === key
    );
    scrolledNonceRef.current = reveal.nonce;
    node?.scrollIntoView({ block: "nearest" });
  }, [reveal, visibleKeys]);

  /** 右键命中选中集则整集生效,否则先重置为单选该表(与结果网格同语义)。 */
  const contextTargets = useCallback(
    (database: string, table: string): TableRef[] => {
      if (selectedVisibleKeys.includes(tableKey(database, table)) && selectedRefs.length > 0) return selectedRefs;
      return [{ database, table }];
    },
    [selectedRefs, selectedVisibleKeys]
  );

  const handleConfirmAction = async () => {
    if (!confirmAction || !tabMeta?.assetId) return;
    const { type, targets } = confirmAction;
    setExecutingAction(true);

    // 逐表一条语句,每条带自己的 database —— 选中集可以跨库。单表失败不中断其余。
    const succeeded: TableRef[] = [];
    let errorMsg = "";
    for (const target of targets) {
      try {
        // 语句由驱动生成;生成失败的表不执行任何语句。
        const statements = await (type === "drop" ? GetDropTableStatements : GetTruncateTableStatements)(
          tabMeta.assetId,
          target.database,
          target.table
        );
        for (const sql of statements) {
          await ExecuteSQL(tabMeta.assetId, sql, target.database);
        }
        succeeded.push(target);
      } catch (err) {
        errorMsg += `${target.database}.${target.table}: ${String(err)}\n`;
      }
    }

    if (succeeded.length > 0) {
      // 批量时报成功的张数(名字列表在确认框里刚看过);单张仍报表名。
      notifySuccess(
        succeeded.length > 1
          ? t(type === "drop" ? "query.dropTablesSuccess" : "query.truncateTablesSuccess", {
              count: succeeded.length,
            })
          : t(type === "drop" ? "query.dropTableSuccess" : "query.truncateTableSuccess", {
              table: succeeded[0].table,
            })
      );
      if (type === "drop") {
        const droppedKeys = succeeded.map((ref) => tableKey(ref.database, ref.table));
        setSelectedKeys((prev) => prev.filter((key) => !droppedKeys.includes(key)));
        for (const database of Array.from(new Set(succeeded.map((ref) => ref.database)))) {
          await refreshTables(tabId, database);
        }
      }
    }
    if (errorMsg) toast.error(errorMsg.trim());

    setExecutingAction(false);
    setConfirmAction(null);
  };

  if (!dbState) return null;

  if (driverUnavailable) {
    return (
      <div className="flex flex-col h-full">
        <div className="flex items-center px-2 py-1.5 border-b border-border shrink-0">
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {t("query.databases")}
          </span>
        </div>
        <DriverUnavailableNotice driver={driver} />
      </div>
    );
  }

  const { expandedDbs, error: dbError } = dbState;
  const schemaAware = driverHasCapability(driverDescriptor, "schemas");
  // 侧栏工具栏「新建表」的目标:选中的表所在的库,没有选中时取第一个展开的库,再没有时取默认库。
  const toolbarTarget = defaultCreateTarget({
    selected: selectedRefs[0],
    databases: dbState.databases,
    expandedDbs,
    defaultDatabase,
    schemas: schemaAware,
  });
  const driversLoading = driversState.status === "loading";
  const loadingDbs = dbState.loadingDbs || driversLoading;
  const error = driversState.status === "error" ? driversState.error : dbError;
  const renderTableItem = (db: string, tbl: string, label = tbl) => {
    const key = tableKey(db, tbl);
    const isSelected = selectedVisibleKeys.includes(key);
    // 右键会先把选中集调整好(命中则保留、未命中则重置为这一张),菜单内容随后渲染,
    // 所以这里按当前选中集算出的作用范围就是菜单实际生效的范围。
    const targets = contextTargets(db, tbl);
    const isBatch = targets.length > 1;
    return (
      <ContextMenu key={tbl}>
        <ContextMenuTrigger className="block w-full">
          <div
            data-table-node={key}
            data-selected={isSelected ? "true" : undefined}
            className={`flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer transition-colors duration-150 ${
              isSelected ? "bg-accent text-accent-foreground" : "hover:bg-accent"
            }`}
            onClick={(e) => selectTable(db, tbl, e)}
            onContextMenu={() => {
              if (!selectedVisibleKeys.includes(key)) selectTable(db, tbl);
            }}
            onDoubleClick={() => {
              selectTable(db, tbl);
              openTableTab(tabId, db, tbl);
            }}
          >
            <Table2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{label}</span>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem
            onClick={() => {
              for (const target of targets) openTableTab(tabId, target.database, target.table);
            }}
          >
            <Table2 className="h-3.5 w-3.5" />
            {isBatch ? t("query.openTables", { count: targets.length }) : t("query.openTable")}
          </ContextMenuItem>
          {!access.canAlter && (
            <ContextMenuItem disabled={isBatch} onClick={() => openDesignerTab(tabId, db, tbl, "view")}>
              <Columns3 className="h-3.5 w-3.5" />
              {t("query.viewStructure")}
            </ContextMenuItem>
          )}
          {access.canAlter && (
            <ContextMenuItem disabled={isBatch} onClick={() => openDesignerTab(tabId, db, tbl, "design")}>
              <Wrench className="h-3.5 w-3.5" />
              {t("query.alterTable")}
            </ContextMenuItem>
          )}
          <ContextMenuItem
            disabled={isBatch}
            onClick={() => {
              if (!tabMeta?.assetId) return;
              GetStarterQuery(tabMeta.assetId, db, tbl, 100).then(
                (starter) => openSqlTab(tabId, db, starter),
                (e) => toast.error(String(e))
              );
            }}
          >
            <Search className="h-3.5 w-3.5" />
            {t("query.newSql")}
          </ContextMenuItem>
          {canCreateTable && (
            <ContextMenuItem
              data-testid="table-new-table"
              onClick={() => openNewTable(targetOfTable(db, tbl, schemaAware))}
            >
              <Plus className="h-3.5 w-3.5" />
              {t("query.newTable")}
              <ContextMenuShortcut className="font-mono">
                {t("query.newMenu.in", { name: targetLabel(targetOfTable(db, tbl, schemaAware)) })}
              </ContextMenuShortcut>
            </ContextMenuItem>
          )}
          {(can("truncate_table") || can("drop_table")) && <ContextMenuSeparator />}
          {can("truncate_table") && (
            <ContextMenuItem
              variant="destructive"
              data-testid="table-truncate"
              onClick={() => setConfirmAction({ type: "truncate", targets })}
            >
              <Eraser className="h-3.5 w-3.5" />
              {isBatch ? t("query.truncateTables", { count: targets.length }) : t("query.truncateTable")}
            </ContextMenuItem>
          )}
          {can("drop_table") && (
            <ContextMenuItem
              variant="destructive"
              data-testid="table-drop"
              onClick={() => setConfirmAction({ type: "drop", targets })}
            >
              <Trash2 className="h-3.5 w-3.5" />
              {isBatch ? t("query.dropTables", { count: targets.length }) : t("query.dropTable")}
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      </ContextMenu>
    );
  };

  return (
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-2 py-1.5 border-b border-border shrink-0">
        <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t("query.databases")}
        </span>
        <div className="flex gap-0.5">
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={() => {
              if (showFilter) changeFilter("");
              setShowFilter((v) => !v);
            }}
            title={t("query.filterTables")}
          >
            <Search className={`h-3.5 w-3.5 ${showFilter ? "text-foreground" : ""}`} />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            data-testid="database-new-sql-button"
            onClick={() => openSqlTab(tabId)}
            title={t("query.newSql")}
            aria-label={t("query.newSql")}
          >
            <SquarePen className="h-3.5 w-3.5" />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="h-6 w-auto gap-0.5 px-1"
                data-testid="database-new-menu"
                title={t("query.newMenu.label")}
                aria-label={t("query.newMenu.label")}
              >
                <Plus className="h-3.5 w-3.5" />
                <ChevronDown className="size-2.5 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-48">
              {canCreateTable && toolbarTarget && (
                <DropdownMenuItem
                  data-testid="database-new-menu-table"
                  className="text-xs"
                  onClick={() => openNewTable(toolbarTarget)}
                >
                  <Table2 className="h-3.5 w-3.5" />
                  {t("query.newTable")}
                  <DropdownMenuShortcut className="font-mono">
                    {t("query.newMenu.in", { name: targetLabel(toolbarTarget) })}
                  </DropdownMenuShortcut>
                </DropdownMenuItem>
              )}
              <DropdownMenuItem
                data-testid="database-new-menu-sql"
                className="text-xs"
                onClick={() => openSqlTab(tabId)}
              >
                <SquarePen className="h-3.5 w-3.5" />
                {t("query.newSql")}
              </DropdownMenuItem>
              {can("create_database") && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    data-testid="database-new-menu-database"
                    className="text-xs"
                    onClick={() => setShowCreateDatabase(true)}
                  >
                    <Database className="h-3.5 w-3.5" />
                    {t("query.newMenu.database")}
                  </DropdownMenuItem>
                </>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={() => loadDatabases(tabId)}
            title={t("query.refreshTree")}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Filter input */}
      {showFilter && (
        <div className="border-b px-2 py-1.5 shrink-0">
          <div className="relative">
            <Search className="absolute left-2 top-1/2 size-3 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              className="h-7 pl-7 text-xs"
              placeholder={t("query.filterTables")}
              value={filter}
              onChange={(e) => changeFilter(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  changeFilter("");
                  setShowFilter(false);
                }
              }}
            />
          </div>
        </div>
      )}

      {/* Error message */}
      {error && (
        <div className="flex items-start gap-2 border-b border-destructive/20 bg-destructive/10 px-2 py-2 text-xs text-destructive">
          <AlertCircle className="size-3.5 shrink-0 mt-0.5" />
          <span className="break-all">{error}</span>
        </div>
      )}

      {/* Tree */}
      <ScrollArea className="flex-1 min-h-0">
        <div ref={treeRef} className="p-1 space-y-0.5">
          {loadingDbs ? (
            <div className="flex items-center justify-center py-4">
              <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
            </div>
          ) : visibleDbs.length === 0 ? (
            <div className="text-xs text-muted-foreground text-center py-4">
              {filterLower ? t("query.noMatch") : t("query.databases")}
            </div>
          ) : (
            visibleDbs.map(({ db, dbMatch, tables: dbTables, schemas }) => {
              const isExpanded = filterLower ? true : expandedDbs.includes(db);
              const isLoadingTables = dbState.loadingTables[db] === true;
              const isTablesOpen = filterLower ? true : (openTables[db] ?? true);
              const hasTables = !!dbTables && (dbTables.length > 0 || (!!schemas && schemas.length > 0));
              const tableCount = (dbTables?.length ?? 0) + (schemas?.reduce((n, g) => n + g.tables.length, 0) ?? 0);

              return (
                <div key={db}>
                  {/* Database node with context menu */}
                  <ContextMenu>
                    <ContextMenuTrigger className="block w-full">
                      <div
                        className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent transition-colors duration-150"
                        onClick={() => {
                          if (filterLower) return;
                          if (isExpanded) dropSelectionUnder(`${db}.`);
                          toggleDbExpand(tabId, db);
                        }}
                      >
                        {isExpanded ? (
                          <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
                        ) : (
                          <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
                        )}
                        <Database className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                        <span className="truncate">{db}</span>
                      </div>
                    </ContextMenuTrigger>
                    <ContextMenuContent>
                      <ContextMenuItem onClick={() => openSqlTab(tabId, db)}>
                        <Search className="h-3.5 w-3.5" />
                        {t("query.newSql")}
                      </ContextMenuItem>
                      {canCreateTable && (
                        <ContextMenuItem
                          data-testid="database-add-table"
                          onClick={() => openNewTable({ database: db })}
                        >
                          <Plus className="h-3.5 w-3.5" />
                          {t("query.newTable")}
                        </ContextMenuItem>
                      )}
                      <ContextMenuSeparator />
                      <ContextMenuItem
                        onClick={() => {
                          dropSelectionUnder(`${db}.`);
                          refreshTables(tabId, db);
                        }}
                      >
                        <RefreshCw className="h-3.5 w-3.5" />
                        {t("query.refreshTables")}
                      </ContextMenuItem>
                    </ContextMenuContent>
                  </ContextMenu>

                  {/* Tables */}
                  {isExpanded && (
                    <div className="ml-3">
                      {isLoadingTables ? (
                        <div className="flex items-center gap-1.5 px-2 py-1">
                          <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />
                        </div>
                      ) : (
                        <>
                          {/* Tables group node */}
                          <ContextMenu>
                            <ContextMenuTrigger className="block w-full">
                              <div
                                data-testid={`tree-table-group-${db}`}
                                className="group/tables flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent transition-colors duration-150"
                                onClick={() => {
                                  if (filterLower) return;
                                  if (isTablesOpen) dropSelectionUnder(`${db}.`);
                                  setOpenTables((prev) => ({ ...prev, [db]: !(prev[db] ?? true) }));
                                }}
                              >
                                {isTablesOpen ? (
                                  <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
                                ) : (
                                  <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
                                )}
                                <Table2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                                <span className="truncate text-muted-foreground">
                                  {t("query.objTables")} ({tableCount})
                                </span>
                                {canCreateTable && (
                                  <Button
                                    variant="ghost"
                                    size="icon-xs"
                                    data-testid="tree-table-group-new-table"
                                    className="ml-auto size-5 opacity-0 group-hover/tables:opacity-100 focus-visible:opacity-100"
                                    title={t("query.newTableIn", { name: db })}
                                    aria-label={t("query.newTableIn", { name: db })}
                                    onClick={(e) => {
                                      e.stopPropagation();
                                      openNewTable({ database: db });
                                    }}
                                  >
                                    <Plus />
                                  </Button>
                                )}
                              </div>
                            </ContextMenuTrigger>
                            <ContextMenuContent>
                              {canCreateTable && (
                                <ContextMenuItem
                                  data-testid="table-group-new-table"
                                  onClick={() => openNewTable({ database: db })}
                                >
                                  <Plus className="h-3.5 w-3.5" />
                                  {t("query.newTable")}
                                </ContextMenuItem>
                              )}
                              <ContextMenuItem
                                onClick={() => {
                                  dropSelectionUnder(`${db}.`);
                                  refreshTables(tabId, db);
                                }}
                              >
                                <RefreshCw className="h-3.5 w-3.5" />
                                {t("query.refreshTables")}
                              </ContextMenuItem>
                            </ContextMenuContent>
                          </ContextMenu>
                          {isTablesOpen && (
                            <div className="ml-3">
                              {!hasTables ? (
                                <div className="flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground">
                                  <span className="italic">
                                    {filterLower && !dbMatch ? t("query.noMatch") : t("query.noTables")}
                                  </span>
                                  {canCreateTable && !filterLower && (
                                    <>
                                      <span aria-hidden>·</span>
                                      <button
                                        type="button"
                                        data-testid="tree-empty-new-table"
                                        className="text-primary hover:underline"
                                        onClick={() => openNewTable({ database: db })}
                                      >
                                        {t("query.newTable")}
                                      </button>
                                    </>
                                  )}
                                </div>
                              ) : schemaAware && schemas ? (
                                <>
                                  {dbTables.map((tbl) => renderTableItem(db, tbl))}
                                  {schemas.map((group) => {
                                    const expandedSchemas = dbState.expandedSchemas[db] || [];
                                    const isSchemaExpanded = filterLower
                                      ? true
                                      : expandedSchemas.includes(group.schema);
                                    return (
                                      <div key={group.schema}>
                                        <div
                                          className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent transition-colors duration-150"
                                          onClick={() => {
                                            if (filterLower) return;
                                            if (isSchemaExpanded) dropSelectionUnder(`${db}.${group.schema}.`);
                                            toggleSchemaExpand(tabId, db, group.schema);
                                          }}
                                        >
                                          {isSchemaExpanded ? (
                                            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
                                          ) : (
                                            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
                                          )}
                                          <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                                          <span className="truncate">{group.schema}</span>
                                        </div>
                                        {isSchemaExpanded && (
                                          <div className="ml-3">
                                            {group.tables.map((tbl) =>
                                              renderTableItem(db, tbl.qualifiedName, tbl.name)
                                            )}
                                          </div>
                                        )}
                                      </div>
                                    );
                                  })}
                                </>
                              ) : (
                                dbTables.map((tbl) => renderTableItem(db, tbl))
                              )}
                            </div>
                          )}
                          {!filterLower && tabMeta?.assetId && OBJECT_BROWSER_CAPABILITIES.some(can) ? (
                            <ObjectBrowserSection tabId={tabId} assetId={tabMeta.assetId} database={db} />
                          ) : null}
                        </>
                      )}
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>
      </ScrollArea>

      <CreateDatabaseDialog
        open={showCreateDatabase}
        onOpenChange={setShowCreateDatabase}
        assetId={tabMeta?.assetId ?? 0}
        defaultDatabase={defaultDatabase}
        options={driverDescriptor?.createDatabaseOptions ?? []}
        onSuccess={() => loadDatabases(tabId)}
      />

      <ConfirmDialog
        open={!!confirmAction}
        onOpenChange={(open) => {
          if (!open && !executingAction) setConfirmAction(null);
        }}
        title={t(confirmAction?.type === "drop" ? "query.dropTableConfirmTitle" : "query.truncateTableConfirmTitle")}
        description={
          <div className="space-y-2">
            <p>
              {t(confirmAction?.type === "drop" ? "query.dropTableConfirmDesc" : "query.truncateTableConfirmDesc", {
                count: confirmAction?.targets.length ?? 0,
              })}
            </p>
            <ul className="max-h-40 space-y-1 overflow-auto rounded-md border border-border bg-muted/30 p-2 font-mono text-xs">
              {confirmAction?.targets.map((target) => (
                <li key={tableKey(target.database, target.table)}>{`${target.database}.${target.table}`}</li>
              ))}
            </ul>
            <p className="text-xs text-muted-foreground">{t("query.batchTableActionNote")}</p>
          </div>
        }
        cancelText={t("action.cancel")}
        confirmText={
          (confirmAction?.targets.length ?? 0) > 1
            ? t(confirmAction?.type === "drop" ? "query.dropTables" : "query.truncateTables", {
                count: confirmAction?.targets.length ?? 0,
              })
            : t(confirmAction?.type === "drop" ? "query.dropTable" : "query.truncateTable")
        }
        onConfirm={handleConfirmAction}
      />
    </div>
  );
}
