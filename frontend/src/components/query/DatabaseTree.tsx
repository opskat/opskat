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
  ConfirmDialog,
} from "@opskat/ui";
import { ExecuteSQL } from "../../../wailsjs/go/query/Query";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
import { CreateDatabaseDialog } from "./CreateDatabaseDialog";
import { CreateTableDialog } from "./CreateTableDialog";
import { AlterTableDialog } from "./AlterTableDialog";
import { TableStructureDialog } from "./TableStructureDialog";
import { ObjectBrowserSection } from "./ObjectBrowserSection";
import { buildStarterSelectSql, quoteTableRef } from "@/lib/tableSql";

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

function isSchemaAwareDriver(driver: string | undefined): boolean {
  return driver === "postgresql" || driver === "mssql";
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

export function DatabaseTree({ tabId }: DatabaseTreeProps) {
  const { t } = useTranslation();
  const { dbStates, loadDatabases, toggleDbExpand, toggleSchemaExpand, openTableTab, openSqlTab, refreshTables } =
    useQueryStore();
  const [showCreateDatabase, setShowCreateDatabase] = useState(false);
  const [showCreateTable, setShowCreateTable] = useState(false);
  const [createTableDatabase, setCreateTableDatabase] = useState("");
  const [showAlterTable, setShowAlterTable] = useState(false);
  const [alterDatabase, setAlterDatabase] = useState("");
  const [alterTableName, setAlterTableName] = useState("");
  const [structureTarget, setStructureTarget] = useState<{ db: string; table: string } | null>(null);
  const [confirmAction, setConfirmAction] = useState<{
    type: "drop" | "truncate";
    targets: TableRef[];
  } | null>(null);
  const [executingAction, setExecutingAction] = useState(false);

  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));
  const tabMeta = tab?.meta as QueryTabMeta | undefined;
  const driver = tabMeta?.driver;
  const defaultDatabase = tabMeta?.defaultDatabase ?? "";

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
  useEffect(() => {
    if (dbState && dbState.databases.length === 0 && !dbState.loadingDbs) {
      loadDatabases(tabId);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabId]);

  const filterLower = filter.trim().toLowerCase();

  const visibleDbs = useMemo(() => {
    if (!dbState) return [];
    const schemaAware = isSchemaAwareDriver(driver);
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
  }, [dbState, driver, filterLower]);

  // 树上此刻真正可见的表节点,顺序与渲染顺序一致 —— Shift 取区间和筛选后剪枝都以它为准。
  const visibleTableRefs = useMemo(() => {
    const refs: TableRef[] = [];
    for (const { db, tables, schemas } of visibleDbs) {
      const expanded = filterLower ? true : (dbState?.expandedDbs ?? []).includes(db);
      if (!expanded) continue;
      const tablesOpen = filterLower ? true : (openTables[db] ?? true);
      if (!tablesOpen) continue;
      for (const table of tables ?? []) refs.push({ database: db, table });
      for (const group of schemas ?? []) {
        for (const node of group.tables) refs.push({ database: db, table: node.qualifiedName });
      }
    }
    return refs;
  }, [dbState?.expandedDbs, filterLower, openTables, visibleDbs]);

  const visibleKeys = useMemo(
    () => visibleTableRefs.map((ref) => tableKey(ref.database, ref.table)),
    [visibleTableRefs]
  );

  // 折叠、筛选、刷新都会改变可见集合;不再可见的节点从选中集里剔除。
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
      const qualified = quoteTableRef(target.database, target.table, driver);
      const sql =
        type === "drop"
          ? `DROP TABLE ${qualified}`
          : driver === "sqlite"
            ? `DELETE FROM ${qualified}`
            : `TRUNCATE TABLE ${qualified}`;
      try {
        await ExecuteSQL(tabMeta.assetId, sql, target.database);
        succeeded.push(target);
      } catch (err) {
        errorMsg += `${target.database}.${target.table}: ${String(err)}\n`;
      }
    }

    if (succeeded.length > 0) {
      notifySuccess(
        t(type === "drop" ? "query.dropTableSuccess" : "query.truncateTableSuccess", {
          table: succeeded.map((ref) => `${ref.database}.${ref.table}`).join(", "),
          count: succeeded.length,
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

  const { expandedDbs, loadingDbs, error } = dbState;
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
          <ContextMenuItem disabled={isBatch} onClick={() => setStructureTarget({ db, table: tbl })}>
            <Columns3 className="h-3.5 w-3.5" />
            {t("query.viewStructure")}
          </ContextMenuItem>
          <ContextMenuItem
            disabled={isBatch}
            onClick={() => {
              setAlterDatabase(db);
              setAlterTableName(tbl);
              setShowAlterTable(true);
            }}
          >
            <Wrench className="h-3.5 w-3.5" />
            {t("query.alterTable")}
          </ContextMenuItem>
          <ContextMenuItem
            disabled={isBatch}
            onClick={() => {
              const tableName = quoteTableRef(db, tbl, driver);
              openSqlTab(tabId, db, buildStarterSelectSql(tableName, driver, 100));
            }}
          >
            <Search className="h-3.5 w-3.5" />
            {t("query.newSql")}
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" onClick={() => setConfirmAction({ type: "truncate", targets })}>
            <Eraser className="h-3.5 w-3.5" />
            {isBatch ? t("query.truncateTables", { count: targets.length }) : t("query.truncateTable")}
          </ContextMenuItem>
          <ContextMenuItem variant="destructive" onClick={() => setConfirmAction({ type: "drop", targets })}>
            <Trash2 className="h-3.5 w-3.5" />
            {isBatch ? t("query.dropTables", { count: targets.length }) : t("query.dropTable")}
          </ContextMenuItem>
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
              setShowFilter((v) => {
                if (v) setFilter("");
                return !v;
              });
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
          {driver !== "sqlite" && (
            <Button
              variant="ghost"
              size="icon"
              className="h-6 w-6"
              onClick={() => setShowCreateDatabase(true)}
              title={t("query.createDatabase")}
              aria-label={t("query.createDatabase")}
            >
              <Plus className="h-3.5 w-3.5" />
            </Button>
          )}
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
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setFilter("");
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
        <div className="p-1 space-y-0.5">
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
              const schemaAware = isSchemaAwareDriver(driver);
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
                      <ContextMenuItem
                        onClick={() => {
                          setCreateTableDatabase(db);
                          setShowCreateTable(true);
                        }}
                      >
                        <Table2 className="h-3.5 w-3.5" />
                        {t("query.addTable")}
                      </ContextMenuItem>
                      <ContextMenuSeparator />
                      <ContextMenuItem onClick={() => refreshTables(tabId, db)}>
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
                          <div
                            className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer hover:bg-accent transition-colors duration-150"
                            onClick={() => {
                              if (filterLower) return;
                              setOpenTables((prev) => ({ ...prev, [db]: !(prev[db] ?? false) }));
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
                          </div>
                          {isTablesOpen && (
                            <div className="ml-3">
                              {!hasTables ? (
                                <div className="px-2 py-1 text-xs text-muted-foreground italic">
                                  {filterLower && !dbMatch ? t("query.noMatch") : t("query.noTables")}
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
                          {!filterLower && tabMeta?.assetId ? (
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
        driver={driver}
        onSuccess={() => loadDatabases(tabId)}
      />

      <CreateTableDialog
        open={showCreateTable}
        onOpenChange={(open) => {
          setShowCreateTable(open);
          if (!open) setCreateTableDatabase("");
        }}
        assetId={tabMeta?.assetId ?? 0}
        database={createTableDatabase || defaultDatabase}
        driver={driver}
        onSuccess={() => {
          const targetDb = createTableDatabase || defaultDatabase;
          if (targetDb) {
            refreshTables(tabId, targetDb);
          }
          setShowCreateTable(false);
          setCreateTableDatabase("");
        }}
      />

      <AlterTableDialog
        open={showAlterTable}
        onOpenChange={(open) => {
          setShowAlterTable(open);
          if (!open) {
            setAlterDatabase("");
            setAlterTableName("");
          }
        }}
        assetId={tabMeta?.assetId ?? 0}
        database={alterDatabase || defaultDatabase}
        table={alterTableName}
        driver={driver}
        onSuccess={(nextTableName) => {
          const targetDb = alterDatabase || defaultDatabase;
          if (targetDb) {
            refreshTables(tabId, targetDb);
          }
          if (nextTableName && targetDb) {
            openTableTab(tabId, targetDb, nextTableName);
          }
          setShowAlterTable(false);
          setAlterDatabase("");
          setAlterTableName("");
        }}
      />

      <TableStructureDialog
        open={structureTarget !== null}
        onOpenChange={(open) => {
          if (!open) setStructureTarget(null);
        }}
        tabId={tabId}
        database={structureTarget?.db ?? ""}
        table={structureTarget?.table ?? ""}
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
