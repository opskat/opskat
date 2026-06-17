import { useEffect, useMemo, useState } from "react";
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
  const [confirmAction, setConfirmAction] = useState<{
    type: "drop" | "truncate";
    database: string;
    table: string;
  } | null>(null);
  const [executingAction, setExecutingAction] = useState(false);

  const tab = useTabStore((s) => s.tabs.find((t) => t.id === tabId));
  const tabMeta = tab?.meta as QueryTabMeta | undefined;
  const driver = tabMeta?.driver;
  const defaultDatabase = tabMeta?.defaultDatabase ?? "";

  const dbState = dbStates[tabId];
  const [filter, setFilter] = useState("");
  const [showFilter, setShowFilter] = useState(false);
  const [selected, setSelected] = useState<{ db: string; table: string } | null>(null);

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

  const handleConfirmAction = async () => {
    if (!confirmAction || !tabMeta?.assetId) return;
    const { type, database, table } = confirmAction;
    const qualified = quoteTableRef(database, table, driver);
    const sql =
      type === "drop"
        ? `DROP TABLE ${qualified}`
        : driver === "sqlite"
          ? `DELETE FROM ${qualified}`
          : `TRUNCATE TABLE ${qualified}`;
    setExecutingAction(true);
    try {
      await ExecuteSQL(tabMeta.assetId, sql, database);
      notifySuccess(t(type === "drop" ? "query.dropTableSuccess" : "query.truncateTableSuccess", { table }));
      if (type === "drop") {
        if (selected?.db === database && selected?.table === table) setSelected(null);
        await refreshTables(tabId, database);
      }
      setConfirmAction(null);
    } catch (err) {
      toast.error(String(err));
    } finally {
      setExecutingAction(false);
    }
  };

  if (!dbState) return null;

  const { expandedDbs, loadingDbs, error } = dbState;
  const renderTableItem = (db: string, tbl: string, label = tbl) => {
    const isSelected = selected?.db === db && selected?.table === tbl;
    return (
      <ContextMenu key={tbl}>
        <ContextMenuTrigger className="block w-full">
          <div
            className={`flex items-center gap-1.5 rounded-md px-2 py-1 text-xs cursor-pointer transition-colors duration-150 ${
              isSelected ? "bg-accent text-accent-foreground" : "hover:bg-accent"
            }`}
            onClick={() => setSelected({ db, table: tbl })}
            onDoubleClick={() => {
              setSelected({ db, table: tbl });
              openTableTab(tabId, db, tbl);
            }}
          >
            <Table2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{label}</span>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onClick={() => openTableTab(tabId, db, tbl)}>
            <Table2 className="h-3.5 w-3.5" />
            {t("query.openTable")}
          </ContextMenuItem>
          <ContextMenuItem
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
            onClick={() => {
              const tableName = quoteTableRef(db, tbl, driver);
              openSqlTab(tabId, db, buildStarterSelectSql(tableName, driver, 100));
            }}
          >
            <Search className="h-3.5 w-3.5" />
            {t("query.newSql")}
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            variant="destructive"
            onClick={() => setConfirmAction({ type: "truncate", database: db, table: tbl })}
          >
            <Eraser className="h-3.5 w-3.5" />
            {t("query.truncateTable")}
          </ContextMenuItem>
          <ContextMenuItem
            variant="destructive"
            onClick={() => setConfirmAction({ type: "drop", database: db, table: tbl })}
          >
            <Trash2 className="h-3.5 w-3.5" />
            {t("query.dropTable")}
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
                      ) : !dbTables || (dbTables.length === 0 && (!schemas || schemas.length === 0)) ? (
                        <div className="px-2 py-1 text-xs text-muted-foreground italic">
                          {filterLower && !dbMatch ? t("query.noMatch") : t("query.noTables")}
                        </div>
                      ) : schemaAware && schemas ? (
                        <>
                          {dbTables.map((tbl) => renderTableItem(db, tbl))}
                          {schemas.map((group) => {
                            const expandedSchemas = dbState.expandedSchemas[db] || [];
                            const isSchemaExpanded = filterLower ? true : expandedSchemas.includes(group.schema);
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
                                    {group.tables.map((tbl) => renderTableItem(db, tbl.qualifiedName, tbl.name))}
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

      <ConfirmDialog
        open={!!confirmAction}
        onOpenChange={(open) => {
          if (!open && !executingAction) setConfirmAction(null);
        }}
        title={t(confirmAction?.type === "drop" ? "query.dropTableConfirmTitle" : "query.truncateTableConfirmTitle")}
        description={t(
          confirmAction?.type === "drop" ? "query.dropTableConfirmDesc" : "query.truncateTableConfirmDesc",
          { table: confirmAction?.table ?? "" }
        )}
        cancelText={t("action.cancel")}
        confirmText={t(confirmAction?.type === "drop" ? "query.dropTable" : "query.truncateTable")}
        onConfirm={handleConfirmAction}
      />
    </div>
  );
}
