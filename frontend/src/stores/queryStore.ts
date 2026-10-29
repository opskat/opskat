import { create } from "zustand";
import { GetTableMetadata, ListDatabaseObjects, ListDatabases, ListTables } from "../../wailsjs/go/query/Query";
import { RedisGetKeyDetail, RedisListDatabases, RedisScanKeys } from "../../wailsjs/go/redis/Redis";
import { ListMongoDatabases, ListMongoCollections } from "../../wailsjs/go/query/Query";
import { asset_entity, query_svc } from "../../wailsjs/go/models";
import { useTabStore, registerTabCloseHook, registerTabRestoreHook, type QueryTabMeta } from "./tabStore";
import { useAssetStore } from "./assetStore";
import {
  OBJECT_BROWSER_CAPABILITIES,
  driverHasCapability,
  findDatabaseDriver,
  loadDatabaseDrivers,
} from "../lib/dbDrivers";

// --- Types ---

export interface QueryTab {
  id: string; // "query:{assetId}"
  assetId: number;
  assetName: string;
  assetIcon: string;
  assetType: "database" | "redis" | "mongodb" | "kafka" | "k8s" | "etcd" | "oss";
  driver?: string; // "mysql" | "postgresql" | "sqlite" | "mssql"
  defaultDatabase?: string;
  redisDatabase?: number;
  redisScanPageSize?: number;
  redisKeySeparator?: string;
  redisMode?: "standalone" | "cluster" | "sentinel";
}

/**
 * 表设计器标签。view:只读查看结构;design:设计已有的表;create:在 database(有 schema 的驱动还有 schema,
 * 空表示驱动的默认 schema)里新建表(还没有表名)。每张已有的表只开一个(id 按库与表),草稿不持久化:
 * 恢复后重新读取表结构,新建表回到预置的样子。
 */
export type DesignerInnerTab =
  | { id: string; type: "designer"; mode: "view" | "design"; database: string; table: string }
  | { id: string; type: "designer"; mode: "create"; database: string; schema?: string };

export type InnerTab =
  | { id: string; type: "table"; database: string; table: string; pendingLoad?: boolean }
  | DesignerInnerTab
  | {
      id: string;
      type: "sql";
      title: string;
      sql?: string;
      selectedDb?: string;
      editorHeight?: number;
      history?: string[];
    };

export interface DatabaseTabState {
  databases: string[];
  tables: Record<string, string[]>; // db -> table[]
  loadingTables: Record<string, boolean>; // db -> isLoading
  expandedDbs: string[];
  expandedSchemas: Record<string, string[]>; // db -> schema[]
  loadingDbs: boolean;
  innerTabs: InnerTab[];
  activeInnerTabId: string | null;
  error: string | null;
  tableMeta?: Record<string, query_svc.TableMetadata>; // "db.table" -> metadata
  objects?: Record<string, query_svc.DatabaseObjects>; // db -> objects
  /** 「在左侧树中定位」请求;nonce 递增让同一张表的重复请求也能再次触发树的响应。不持久化。 */
  revealRequest?: { nonce: number; db: string; table: string };
  /** 已分配过的最大 SQL 标签序号;只增不减,关闭标签后序号不复用。 */
  sqlTabSeq?: number;
  /** 有未提交修改的内层标签(由各 TableDataTab 上报)。不持久化。 */
  dirtyInnerTabIds?: string[];
  /**
   * 涉及有未提交修改的标签、等待用户决定的关闭请求。不持久化。
   * 只有一个脏标签时由该 TableDataTab 弹三选项对话框(可提交后再关),多个时由 DatabasePanel 弹批量对话框;
   * 决定前一个都不关,确认后 forceCloseInnerTabs 关掉 ids 全部(closeOuter 时连外层数据库标签一起关)。
   */
  closeRequest?: InnerTabCloseRequest;
}

export interface InnerTabCloseRequest {
  ids: string[];
  dirtyIds: string[];
  closeOuter: boolean;
}

const REDIS_PAGE_SIZE = 100;

export interface RedisKeyInfo {
  type: string;
  ttl: number;
  size?: number;
  value: unknown;
  total: number; // LLEN/HLEN/SCARD/ZCARD, -1 for string
  valueCursor: string; // HSCAN/SSCAN cursor
  valueOffset: number; // LRANGE/ZRANGE next offset
  hasMoreValues: boolean;
  loadingMore: boolean;
  /** Cluster mode: the slot the key hashes to and the master currently serving it. */
  slot?: number;
  node?: string;
}

export interface RedisClusterMasterKeyCount {
  addr: string;
  slots: string;
  keys: number;
  reachable: boolean;
}

export interface RedisClusterUnreachableNode {
  addr: string;
  slots: string;
}

export interface RedisTabState {
  currentDb: number;
  scanCursor: string;
  keys: string[];
  keyFilter: string;
  selectedKey: string | null;
  keyInfo: RedisKeyInfo | null;
  loadingKeys: boolean;
  hasMore: boolean;
  dbKeyCounts: Record<number, number>;
  scanRequestId?: number;
  keyDetailRequestId?: number;
  openKeyTabs?: string[];
  activeRedisKey?: string | null;
  removedKey?: string;
  removedKeySeq?: number;
  error: string | null;
  /** Cluster mode: footer scan-range selection — "" means all masters, else a master addr. */
  scanNode?: string;
  /** Cluster mode: how many / how many total masters the last scan covered. */
  scannedMasters?: number;
  totalMasters?: number;
  /** Cluster mode: masters the last scan could not reach, with their slot ranges. */
  unreachableMasters?: RedisClusterUnreachableNode[];
  /** Cluster mode: per-master key counts for the scan-range selector (from RedisListDatabases). */
  masterKeyCounts?: RedisClusterMasterKeyCount[];
}

export type MongoInnerTab =
  | { id: string; type: "collection"; database: string; collection: string; pendingLoad?: boolean }
  | {
      id: string;
      type: "query";
      title: string;
      database?: string;
      collection?: string;
      operation?: string;
      queryText?: string;
      editorHeight?: number;
    };

export interface MongoDBTabState {
  databases: string[];
  collections: Record<string, string[]>;
  expandedDbs: string[];
  activeDatabase: string | null;
  innerTabs: MongoInnerTab[];
  activeInnerTabId: string | null;
  error: string | null;
}

interface QueryState {
  dbStates: Record<string, DatabaseTabState>;
  redisStates: Record<string, RedisTabState>;
  mongoStates: Record<string, MongoDBTabState>;

  openQueryTab: (asset: asset_entity.Asset, opts?: { initialSQL?: string; initialMongo?: string }) => void;

  // Database actions
  loadDatabases: (tabId: string) => Promise<void>;
  loadTables: (tabId: string, database: string) => Promise<void>;
  refreshTables: (tabId: string, database: string) => Promise<void>;
  toggleDbExpand: (tabId: string, database: string) => void;
  toggleSchemaExpand: (tabId: string, database: string, schema: string) => void;
  revealTable: (tabId: string, database: string, table: string) => void;
  openTableTab: (tabId: string, database: string, table: string) => void;
  /** 打开某张表的设计器标签;这张表已有设计器标签时切过去(不改它的模式)。 */
  openDesignerTab: (tabId: string, database: string, table: string, mode: "view" | "design") => void;
  /**
   * 设计器保存时改了表名:标签跟随新名字(id、表名;当前标签、脏标记与挂起的关闭请求里的 id 一并换掉)。
   * 新名字已有设计器标签时去掉那个旧的。
   */
  renameDesignerTab: (tabId: string, innerTabId: string, table: string) => void;
  /**
   * 打开在 database(与 schema)里新建表的设计器标签。同一目标已有还没动过的新建表标签时切过去,否则另开一个。
   */
  openCreateTableTab: (tabId: string, database: string, schema?: string) => void;
  /** 新建表的目标库(与 schema)改了。 */
  retargetCreateTab: (tabId: string, innerTabId: string, database: string, schema?: string) => void;
  /**
   * 新建表成功:标签换成这张表的设计标签(id、当前标签、脏标记与挂起的关闭请求里的 id 一并换掉)。这张表
   * 已有设计器标签时去掉那个旧的。
   */
  completeCreateTab: (tabId: string, innerTabId: string, table: string) => void;
  openSqlTab: (tabId: string, database?: string, sql?: string) => void;
  closeInnerTab: (tabId: string, innerTabId: string) => void;
  closeOtherInnerTabs: (tabId: string, innerTabId: string) => void;
  closeRightInnerTabs: (tabId: string, innerTabId: string) => void;
  setInnerTabDirty: (tabId: string, innerTabId: string, dirty: boolean) => void;
  /** 关闭内层标签的唯一入口:都没有未提交修改时直接关,否则挂起 closeRequest 等用户决定。 */
  requestCloseInnerTabs: (tabId: string, ids: string[], opts?: { closeOuter?: boolean }) => void;
  forceCloseInnerTabs: (tabId: string, ids: string[], closeOuter?: boolean) => void;
  cancelCloseRequest: (tabId: string) => void;
  setActiveInnerTab: (tabId: string, innerTabId: string) => void;
  updateInnerTab: (tabId: string, innerTabId: string, patch: Record<string, unknown>) => void;
  markTableTabLoaded: (tabId: string, innerTabId: string) => void;
  addSqlHistory: (tabId: string, innerTabId: string, sql: string) => void;
  loadTableMeta: (tabId: string, database: string, table: string) => Promise<void>;
  loadObjects: (tabId: string, database: string) => Promise<void>;

  // Redis actions
  scanKeys: (tabId: string, reset?: boolean) => Promise<void>;
  selectRedisDb: (tabId: string, db: number) => Promise<void>;
  selectRedisScanNode: (tabId: string, node: string) => Promise<void>;
  selectKey: (tabId: string, key: string) => Promise<void>;
  loadMoreValues: (tabId: string) => Promise<void>;
  setKeyFilter: (tabId: string, pattern: string) => void;
  loadDbKeyCounts: (tabId: string) => Promise<void>;
  clearSelectedKey: (tabId: string, key?: string) => void;
  activateRedisOverview: (tabId: string) => void;
  activateRedisKeyTab: (tabId: string, key: string) => void;
  closeRedisKeyTab: (tabId: string, key: string) => void;
  removeKey: (tabId: string, key: string) => void;

  // MongoDB actions
  loadMongoDatabases: (tabId: string) => Promise<void>;
  loadMongoCollections: (tabId: string, database: string) => Promise<void>;
  toggleMongoDbExpand: (tabId: string, database: string) => void;
  openCollectionTab: (tabId: string, database: string, collection: string) => void;
  openMongoQueryTab: (tabId: string, database?: string, collection?: string) => void;
  closeMongoInnerTab: (tabId: string, innerTabId: string) => void;
  setActiveMongoInnerTab: (tabId: string, innerTabId: string) => void;
  updateMongoInnerTab: (tabId: string, innerTabId: string, patch: Record<string, unknown>) => void;
  markMongoCollectionTabLoaded: (tabId: string, innerTabId: string) => void;
}

// --- Helpers ---

function makeTabId(assetId: number) {
  return `query-${assetId}`;
}

/** 新建表标签 id 的序号(与时间一起保证不与恢复出来的标签重复)。 */
let createTabSeq = 0;

/**
 * 把一个内层标签换成 next(id 可以不同):当前标签、脏标记与挂起的关闭请求里的 id 一并换掉;已有与 next 同 id
 * 的其他标签时去掉它。
 */
function replaceInnerTab(state: DatabaseTabState, innerTabId: string, next: InnerTab): DatabaseTabState {
  const swap = (id: string) => (id === innerTabId ? next.id : id);
  const others = (id: string) => id === innerTabId || id !== next.id;
  return {
    ...state,
    innerTabs: state.innerTabs.filter((t) => others(t.id)).map((t) => (t.id === innerTabId ? next : t)),
    activeInnerTabId: state.activeInnerTabId && swap(state.activeInnerTabId),
    dirtyInnerTabIds: state.dirtyInnerTabIds?.filter(others).map(swap),
    closeRequest: state.closeRequest && {
      ...state.closeRequest,
      ids: state.closeRequest.ids.filter(others).map(swap),
      dirtyIds: state.closeRequest.dirtyIds.filter(others).map(swap),
    },
  };
}

/** 已有表的设计器标签 id:每张表一个。 */
export function designerTabId(database: string, table: string) {
  return `designer:${database}.${table}`;
}

function defaultDbState(): DatabaseTabState {
  return {
    databases: [],
    tables: {},
    loadingTables: {},
    expandedDbs: [],
    expandedSchemas: {},
    loadingDbs: false,
    innerTabs: [],
    activeInnerTabId: null,
    error: null,
    tableMeta: {},
    objects: {},
    sqlTabSeq: 0,
  };
}

function defaultRedisState(options: { database?: number } = {}): RedisTabState {
  return {
    currentDb: Math.max(0, options.database || 0),
    scanCursor: "0",
    keys: [],
    keyFilter: "*",
    selectedKey: null,
    keyInfo: null,
    loadingKeys: false,
    hasMore: true,
    dbKeyCounts: {},
    scanRequestId: 0,
    keyDetailRequestId: 0,
    openKeyTabs: [],
    activeRedisKey: null,
    error: null,
    scanNode: "",
  };
}

function getRedisOpenKeyTabs(state: RedisTabState) {
  return state.openKeyTabs ?? (state.selectedKey ? [state.selectedKey] : []);
}

function getRedisActiveKey(state: RedisTabState) {
  return state.activeRedisKey === undefined ? state.selectedKey : state.activeRedisKey;
}

function defaultMongoState(): MongoDBTabState {
  return {
    databases: [],
    collections: {},
    expandedDbs: [],
    activeDatabase: null,
    innerTabs: [],
    activeInnerTabId: null,
    error: null,
  };
}

function toRedisMatchPattern(pattern: string): string {
  return pattern.trim() || "*";
}

function hasRedisMatchWildcard(pattern: string): boolean {
  return pattern.includes("*") || pattern.includes("?") || pattern.includes("[");
}

export interface RedisStreamEntry {
  id: string;
  fields: Record<string, string>;
}

interface RedisKeyDetailResult {
  key: string;
  type: string;
  ttl: number;
  size: number;
  total: number;
  value: unknown;
  valueCursor: string;
  valueOffset: number;
  hasMoreValues: boolean;
  slot?: number;
  node?: string;
}

function normalizeRedisDetailValue(type: string, value: unknown): unknown {
  if (type === "hash" && Array.isArray(value)) {
    return value.map((entry) => {
      if (Array.isArray(entry)) return [String(entry[0] ?? ""), String(entry[1] ?? "")] as [string, string];
      const obj = entry as { field?: unknown; value?: unknown };
      return [String(obj.field ?? ""), String(obj.value ?? "")] as [string, string];
    });
  }
  if (type === "zset" && Array.isArray(value)) {
    return value.map((entry) => {
      if (Array.isArray(entry)) return [String(entry[0] ?? ""), String(entry[1] ?? "0")] as [string, string];
      const obj = entry as { member?: unknown; score?: unknown };
      return [String(obj.member ?? ""), String(obj.score ?? "0")] as [string, string];
    });
  }
  if (type === "stream" && Array.isArray(value)) {
    return value.map((entry) => {
      const obj = entry as { id?: unknown; fields?: Record<string, string> };
      return { id: String(obj.id ?? ""), fields: obj.fields || {} } as RedisStreamEntry;
    });
  }
  return value;
}

function toRedisKeyInfo(detail: RedisKeyDetailResult): RedisKeyInfo {
  return {
    type: detail.type,
    ttl: detail.ttl,
    size: detail.size,
    value: normalizeRedisDetailValue(detail.type, detail.value),
    total: detail.total,
    valueCursor: detail.valueCursor,
    valueOffset: detail.valueOffset,
    hasMoreValues: detail.hasMoreValues,
    loadingMore: false,
    slot: detail.slot,
    node: detail.node,
  };
}

// --- Store ---

/** Returns the set of asset IDs that have an open query tab. */
export function getQueryActiveAssetIds(): Set<number> {
  const tabs = useTabStore.getState().tabs;
  const ids = new Set<number>();
  for (const tab of tabs) {
    if (tab.type !== "query") continue;
    ids.add((tab.meta as QueryTabMeta).assetId);
  }
  return ids;
}

// Helper: get query tab info from tabStore
function getQueryTabFromTabStore(tabId: string): QueryTab | undefined {
  const tab = useTabStore.getState().tabs.find((t) => t.id === tabId);
  if (!tab || tab.type !== "query") return undefined;
  const m = tab.meta as import("./tabStore").QueryTabMeta;
  return {
    id: tab.id,
    assetId: m.assetId,
    assetName: m.assetName,
    assetIcon: m.assetIcon,
    assetType: m.assetType,
    driver: m.driver,
    defaultDatabase: m.defaultDatabase,
    redisDatabase: m.redisDatabase,
    redisScanPageSize: m.redisScanPageSize,
    redisKeySeparator: m.redisKeySeparator,
    redisMode: m.redisMode,
  };
}

/** 资产所用驱动的描述;驱动没有注册时为 undefined。描述加载失败则抛出,由调用方当作该次加载的失败。 */
async function describeTabDriver(tab: QueryTab) {
  const drivers = await loadDatabaseDrivers();
  return tab.driver ? findDatabaseDriver(drivers, tab.driver) : undefined;
}

function schemaNamesFromTables(tables: string[]): string[] {
  const seen = new Set<string>();
  for (const table of tables) {
    const dot = table.indexOf(".");
    if (dot <= 0) continue;
    seen.add(table.slice(0, dot));
  }
  return Array.from(seen);
}

function reconcileExpandedSchemas(
  existing: string[] | undefined,
  nextSchemas: string[],
  shouldGroupBySchema: boolean
): string[] | undefined {
  if (!shouldGroupBySchema) return undefined;
  if (!existing) return nextSchemas;
  const available = new Set(nextSchemas);
  return existing.filter((schema) => available.has(schema));
}

export const useQueryStore = create<QueryState>((set, get) => ({
  dbStates: {},
  redisStates: {},
  mongoStates: {},

  openQueryTab: (asset, opts) => {
    const tabId = makeTabId(asset.ID);
    const tabStore = useTabStore.getState();

    // If already open, activate and optionally inject initial content
    if (tabStore.tabs.some((t) => t.id === tabId)) {
      tabStore.activateTab(tabId);
      if (asset.Type === "database" && opts?.initialSQL) {
        get().openSqlTab(tabId, undefined, opts.initialSQL);
      } else if (asset.Type === "mongodb" && opts?.initialMongo) {
        get().openMongoQueryTab(tabId);
        const mongoState = get().mongoStates[tabId];
        const innerId = mongoState?.activeInnerTabId;
        if (innerId) {
          get().updateMongoInnerTab(tabId, innerId, { queryText: opts.initialMongo });
        }
      }
      return;
    }

    let driver: string | undefined;
    let defaultDatabase: string | undefined;
    let redisDatabase: number | undefined;
    let redisScanPageSize: number | undefined;
    let redisKeySeparator: string | undefined;
    let redisMode: "standalone" | "cluster" | "sentinel" | undefined;
    try {
      const cfg = JSON.parse(asset.Config || "{}");
      driver = cfg.driver;
      if (asset.Type === "redis") {
        redisDatabase = Math.max(0, Number(cfg.database) || 0);
        redisScanPageSize = Math.max(0, Number(cfg.scan_page_size) || 0) || undefined;
        redisKeySeparator = typeof cfg.key_separator === "string" ? cfg.key_separator : undefined;
        redisMode = cfg.mode === "cluster" || cfg.mode === "sentinel" ? cfg.mode : "standalone";
      } else {
        defaultDatabase = cfg.database;
      }
    } catch {
      /* ignore */
    }

    const assetPath = useAssetStore.getState().getAssetPath(asset);
    tabStore.openTab({
      id: tabId,
      type: "query",
      label: assetPath,
      icon: asset.Icon || undefined,
      meta: {
        type: "query",
        assetId: asset.ID,
        assetName: asset.Name,
        assetIcon: asset.Icon || "",
        assetType: asset.Type as "database" | "redis" | "mongodb" | "kafka" | "k8s" | "etcd" | "oss",
        driver,
        defaultDatabase,
        redisDatabase,
        redisScanPageSize,
        redisKeySeparator,
        redisMode,
      },
    });

    if (asset.Type === "database") {
      set((s) => ({
        dbStates: { ...s.dbStates, [tabId]: defaultDbState() },
      }));
      if (opts?.initialSQL) {
        get().openSqlTab(tabId, undefined, opts.initialSQL);
      }
    } else if (asset.Type === "mongodb") {
      set((s) => ({
        mongoStates: { ...s.mongoStates, [tabId]: defaultMongoState() },
      }));
      if (opts?.initialMongo) {
        get().openMongoQueryTab(tabId);
        const mongoState = get().mongoStates[tabId];
        const innerId = mongoState?.activeInnerTabId;
        if (innerId) {
          get().updateMongoInnerTab(tabId, innerId, { queryText: opts.initialMongo });
        }
      }
    } else if (asset.Type === "redis") {
      set((s) => ({
        redisStates: { ...s.redisStates, [tabId]: defaultRedisState({ database: redisDatabase }) },
      }));
    }
  },

  // --- Database ---

  loadDatabases: async (tabId) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: { ...s.dbStates[tabId], loadingDbs: true },
      },
    }));

    try {
      const databases = await ListDatabases(tab.assetId);

      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: { ...s.dbStates[tabId], databases, loadingDbs: false, error: null },
        },
      }));

      // Also refresh tables for already-expanded databases that still exist,
      // otherwise the top-level refresh button leaves stale table lists.
      const expanded = get().dbStates[tabId]?.expandedDbs ?? [];
      await Promise.all(expanded.filter((db) => databases.includes(db)).map((db) => get().refreshTables(tabId, db)));
    } catch (err) {
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: { ...s.dbStates[tabId], loadingDbs: false, error: String(err) },
        },
      }));
    }
  },

  loadTables: async (tabId, database) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...s.dbStates[tabId],
          loadingTables: { ...s.dbStates[tabId].loadingTables, [database]: true },
        },
      },
    }));

    try {
      const descriptor = await describeTabDriver(tab);
      const tables = await ListTables(tab.assetId, database);

      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: (() => {
            const state = s.dbStates[tabId];
            const schemas = schemaNamesFromTables(tables);
            const nextExpanded = reconcileExpandedSchemas(
              state.expandedSchemas[database],
              schemas,
              driverHasCapability(descriptor, "schemas")
            );
            return {
              ...state,
              tables: { ...state.tables, [database]: tables },
              loadingTables: { ...state.loadingTables, [database]: false },
              expandedSchemas:
                nextExpanded === undefined
                  ? state.expandedSchemas
                  : { ...state.expandedSchemas, [database]: nextExpanded },
            };
          })(),
        },
      }));
    } catch (err) {
      // Reset table list to [] (not undefined) so the UI exits the loading
      // state instead of showing an infinite spinner.
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: {
            ...s.dbStates[tabId],
            tables: { ...s.dbStates[tabId].tables, [database]: [] },
            loadingTables: { ...s.dbStates[tabId].loadingTables, [database]: false },
            error: s.dbStates[tabId]?.error || String(err),
          },
        },
      }));
    }
  },

  refreshTables: async (tabId, database) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    await get().loadTables(tabId, database);
    await get().loadObjects(tabId, database);
  },

  toggleDbExpand: (tabId, database) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const isExpanded = state.expandedDbs.includes(database);
    const expanded = isExpanded ? state.expandedDbs.filter((d) => d !== database) : [...state.expandedDbs, database];
    if (!isExpanded && !state.tables[database]) {
      // Load tables if not loaded
      get().loadTables(tabId, database);
    }
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: { ...s.dbStates[tabId], expandedDbs: expanded },
      },
    }));
  },

  toggleSchemaExpand: (tabId, database, schema) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const current = state.expandedSchemas[database] || [];
    const isExpanded = current.includes(schema);
    const expanded = isExpanded ? current.filter((s) => s !== schema) : [...current, schema];
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...s.dbStates[tabId],
          expandedSchemas: { ...s.dbStates[tabId].expandedSchemas, [database]: expanded },
        },
      },
    }));
  },

  revealTable: (tabId, database, table) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    if (!state.tables[database] && !state.loadingTables[database]) {
      get().loadTables(tabId, database);
    }
    set((s) => {
      const cur = s.dbStates[tabId];
      return {
        dbStates: {
          ...s.dbStates,
          [tabId]: {
            ...cur,
            expandedDbs: cur.expandedDbs.includes(database) ? cur.expandedDbs : [...cur.expandedDbs, database],
            revealRequest: { nonce: (cur.revealRequest?.nonce ?? 0) + 1, db: database, table },
          },
        },
      };
    });
  },

  openTableTab: (tabId, database, table) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const innerId = `table:${database}.${table}`;
    if (state.innerTabs.some((t) => t.id === innerId)) {
      set((s) => ({
        dbStates: { ...s.dbStates, [tabId]: { ...state, activeInnerTabId: innerId } },
      }));
      return;
    }
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: [...state.innerTabs, { id: innerId, type: "table", database, table }],
          activeInnerTabId: innerId,
        },
      },
    }));
  },

  openDesignerTab: (tabId, database, table, mode) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const innerId = designerTabId(database, table);
    const exists = state.innerTabs.some((t) => t.id === innerId);
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: exists
            ? state.innerTabs
            : [...state.innerTabs, { id: innerId, type: "designer", mode, database, table }],
          activeInnerTabId: innerId,
        },
      },
    }));
  },

  renameDesignerTab: (tabId, innerTabId, table) => {
    const state = get().dbStates[tabId];
    const tab = state?.innerTabs.find((t) => t.id === innerTabId);
    if (!state || tab?.type !== "designer" || tab.mode === "create") return;
    const next = { ...tab, id: designerTabId(tab.database, table), table };
    set((s) => ({ dbStates: { ...s.dbStates, [tabId]: replaceInnerTab(state, innerTabId, next) } }));
  },

  openCreateTableTab: (tabId, database, schema) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const untouched = state.innerTabs.find(
      (t) =>
        t.type === "designer" &&
        t.mode === "create" &&
        t.database === database &&
        (t.schema ?? "") === (schema ?? "") &&
        !state.dirtyInnerTabIds?.includes(t.id)
    );
    const tab: InnerTab = untouched ?? {
      id: `create:${Date.now().toString(36)}.${++createTabSeq}`,
      type: "designer",
      mode: "create",
      database,
      ...(schema ? { schema } : {}),
    };
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: untouched ? state.innerTabs : [...state.innerTabs, tab],
          activeInnerTabId: tab.id,
        },
      },
    }));
  },

  retargetCreateTab: (tabId, innerTabId, database, schema) => {
    const state = get().dbStates[tabId];
    const tab = state?.innerTabs.find((t) => t.id === innerTabId);
    if (!state || tab?.type !== "designer" || tab.mode !== "create") return;
    const next: InnerTab = { id: tab.id, type: "designer", mode: "create", database, ...(schema ? { schema } : {}) };
    set((s) => ({ dbStates: { ...s.dbStates, [tabId]: replaceInnerTab(state, innerTabId, next) } }));
  },

  completeCreateTab: (tabId, innerTabId, table) => {
    const state = get().dbStates[tabId];
    const tab = state?.innerTabs.find((t) => t.id === innerTabId);
    if (!state || tab?.type !== "designer" || tab.mode !== "create") return;
    const next: InnerTab = {
      id: designerTabId(tab.database, table),
      type: "designer",
      mode: "design",
      database: tab.database,
      table,
    };
    set((s) => ({ dbStates: { ...s.dbStates, [tabId]: replaceInnerTab(state, innerTabId, next) } }));
  },

  openSqlTab: (tabId, database?, sql?) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const count = (state.sqlTabSeq ?? 0) + 1;
    const innerId = `sql:${Date.now()}`;
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          sqlTabSeq: count,
          innerTabs: [
            ...state.innerTabs,
            { id: innerId, type: "sql", title: `SQL ${count}`, sql, selectedDb: database },
          ],
          activeInnerTabId: innerId,
        },
      },
    }));
  },

  closeInnerTab: (tabId, innerTabId) => {
    get().requestCloseInnerTabs(tabId, [innerTabId]);
  },

  closeOtherInnerTabs: (tabId, innerTabId) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const ids = state.innerTabs.filter((t) => t.id !== innerTabId).map((t) => t.id);
    get().requestCloseInnerTabs(tabId, ids);
  },

  closeRightInnerTabs: (tabId, innerTabId) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const idx = state.innerTabs.findIndex((t) => t.id === innerTabId);
    if (idx < 0) return;
    const ids = state.innerTabs.slice(idx + 1).map((t) => t.id);
    get().requestCloseInnerTabs(tabId, ids);
  },

  setInnerTabDirty: (tabId, innerTabId, dirty) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const current = state.dirtyInnerTabIds ?? [];
    if (current.includes(innerTabId) === dirty) return;
    const next = dirty ? [...current, innerTabId] : current.filter((id) => id !== innerTabId);
    set((s) => ({ dbStates: { ...s.dbStates, [tabId]: { ...state, dirtyInnerTabIds: next } } }));
    // 外层数据库标签借 tabStore 的未保存守卫:有脏内层标签时关外层会挂起 pendingCloseTabId,由 DatabasePanel 接管。
    useTabStore.getState().setTabUnsaved(tabId, next.length > 0);
  },

  requestCloseInnerTabs: (tabId, ids, opts) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const closeOuter = opts?.closeOuter ?? false;
    const dirtyIds = ids.filter((id) => state.dirtyInnerTabIds?.includes(id));
    if (dirtyIds.length === 0) {
      get().forceCloseInnerTabs(tabId, ids, closeOuter);
      return;
    }
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          closeRequest: { ids, dirtyIds, closeOuter },
          // 单个脏标签就地决定:切到它,让用户看着那页修改做选择。
          activeInnerTabId: dirtyIds.length === 1 ? dirtyIds[0] : state.activeInnerTabId,
        },
      },
    }));
  },

  forceCloseInnerTabs: (tabId, ids, closeOuter = false) => {
    if (closeOuter) {
      useTabStore.getState().forceCloseTab(tabId);
      return;
    }
    const state = get().dbStates[tabId];
    if (!state) return;
    const closing = new Set(ids);
    const innerTabs = state.innerTabs.filter((t) => !closing.has(t.id));
    let activeInnerTabId = state.activeInnerTabId;
    if (activeInnerTabId && closing.has(activeInnerTabId)) {
      // 当前标签被关:优先落到它右侧最近的幸存标签,没有再找左侧。
      const idx = state.innerTabs.findIndex((t) => t.id === activeInnerTabId);
      const right = state.innerTabs.slice(idx + 1).find((t) => !closing.has(t.id));
      const left = state.innerTabs
        .slice(0, idx)
        .reverse()
        .find((t) => !closing.has(t.id));
      activeInnerTabId = (right ?? left)?.id ?? null;
    }
    const dirtyInnerTabIds = (state.dirtyInnerTabIds ?? []).filter((id) => !closing.has(id));
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: { ...state, innerTabs, activeInnerTabId, dirtyInnerTabIds, closeRequest: undefined },
      },
    }));
    useTabStore.getState().setTabUnsaved(tabId, dirtyInnerTabIds.length > 0);
  },

  cancelCloseRequest: (tabId) => {
    const state = get().dbStates[tabId];
    if (!state?.closeRequest) return;
    set((s) => ({ dbStates: { ...s.dbStates, [tabId]: { ...state, closeRequest: undefined } } }));
  },

  setActiveInnerTab: (tabId, innerTabId) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: { ...state, activeInnerTabId: innerTabId },
      },
    }));
  },

  updateInnerTab: (tabId, innerTabId, patch) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: state.innerTabs.map((t) => (t.id === innerTabId ? ({ ...t, ...patch } as InnerTab) : t)),
        },
      },
    }));
  },

  markTableTabLoaded: (tabId, innerTabId) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: state.innerTabs.map((t) =>
            t.id === innerTabId && t.type === "table" ? { ...t, pendingLoad: false } : t
          ),
        },
      },
    }));
  },

  addSqlHistory: (tabId, innerTabId, sql) => {
    const state = get().dbStates[tabId];
    if (!state) return;
    const trimmed = sql.trim();
    if (!trimmed) return;
    set((s) => ({
      dbStates: {
        ...s.dbStates,
        [tabId]: {
          ...state,
          innerTabs: state.innerTabs.map((t) => {
            if (t.id !== innerTabId || t.type !== "sql") return t;
            const prev = t.history || [];
            const next = [trimmed, ...prev.filter((h) => h !== trimmed)].slice(0, 30);
            return { ...t, history: next };
          }),
        },
      },
    }));
  },

  loadTableMeta: async (tabId, database, table) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;
    try {
      const meta = await GetTableMetadata(tab.assetId, database, table);
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: {
            ...s.dbStates[tabId],
            tableMeta: { ...(s.dbStates[tabId].tableMeta ?? {}), [`${database}.${table}`]: meta },
          },
        },
      }));
    } catch (err) {
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: { ...s.dbStates[tabId], error: String(err) },
        },
      }));
    }
  },

  loadObjects: async (tabId, database) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;
    try {
      const descriptor = await describeTabDriver(tab);
      const listed = await ListDatabaseObjects(tab.assetId, database);
      // 驱动没有声明的那类对象不显示。
      const objects = new query_svc.DatabaseObjects(listed);
      for (const group of OBJECT_BROWSER_CAPABILITIES) {
        if (!driverHasCapability(descriptor, group)) objects[group] = [];
      }
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: {
            ...s.dbStates[tabId],
            objects: { ...(s.dbStates[tabId].objects ?? {}), [database]: objects },
          },
        },
      }));
    } catch (err) {
      set((s) => ({
        dbStates: {
          ...s.dbStates,
          [tabId]: { ...s.dbStates[tabId], error: String(err) },
        },
      }));
    }
  },

  // --- Redis ---

  scanKeys: async (tabId, reset) => {
    const tab = getQueryTabFromTabStore(tabId);
    const state = get().redisStates[tabId];
    if (!tab || !state) return;
    if (!reset && state.loadingKeys) return;

    const cursor = reset ? "0" : state.scanCursor;
    if (!reset && cursor === "0" && state.keys.length > 0) return;

    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: { ...state, loadingKeys: true, scanRequestId: (state.scanRequestId || 0) + 1 },
      },
    }));
    const requestId = (state.scanRequestId || 0) + 1;
    const pattern = toRedisMatchPattern(state.keyFilter);

    try {
      const result = await RedisScanKeys({
        assetId: tab.assetId,
        db: state.currentDb,
        cursor,
        match: pattern,
        type: "",
        count: tab.redisScanPageSize || 200,
        exact: !hasRedisMatchWildcard(pattern),
        node: state.scanNode || undefined,
      });

      set((s) => ({
        redisStates: {
          ...s.redisStates,
          [tabId]:
            s.redisStates[tabId]?.scanRequestId === requestId
              ? {
                  ...s.redisStates[tabId],
                  scanCursor: result.cursor || "0",
                  keys: reset ? result.keys || [] : [...s.redisStates[tabId].keys, ...(result.keys || [])],
                  hasMore: !!result.hasMore,
                  loadingKeys: false,
                  error: null,
                  scannedMasters: result.scannedMasters,
                  totalMasters: result.totalMasters,
                  unreachableMasters: result.unreachable,
                }
              : s.redisStates[tabId],
        },
      }));
    } catch (err) {
      set((s) => ({
        redisStates: {
          ...s.redisStates,
          [tabId]:
            s.redisStates[tabId]?.scanRequestId === requestId
              ? { ...s.redisStates[tabId], loadingKeys: false, error: String(err) }
              : s.redisStates[tabId],
        },
      }));
    }
  },

  selectRedisDb: async (tabId, db) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    const prev = get().redisStates[tabId];
    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: {
          ...defaultRedisState(),
          currentDb: db,
          keyFilter: prev?.keyFilter || "*",
          dbKeyCounts: prev?.dbKeyCounts || {},
        },
      },
    }));

    get().scanKeys(tabId, true);
  },

  selectRedisScanNode: async (tabId, node) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    const prev = get().redisStates[tabId];
    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: {
          ...defaultRedisState({ database: prev?.currentDb }),
          scanNode: node,
          keyFilter: prev?.keyFilter || "*",
          dbKeyCounts: prev?.dbKeyCounts || {},
          masterKeyCounts: prev?.masterKeyCounts,
        },
      },
    }));

    get().scanKeys(tabId, true);
  },

  selectKey: async (tabId, key) => {
    const tab = getQueryTabFromTabStore(tabId);
    const state = get().redisStates[tabId];
    if (!tab || !state) return;
    const db = state.currentDb;
    const requestId = (state.keyDetailRequestId || 0) + 1;

    set((s) => {
      const openKeyTabs = getRedisOpenKeyTabs(s.redisStates[tabId]);
      return {
        redisStates: {
          ...s.redisStates,
          [tabId]: {
            ...s.redisStates[tabId],
            selectedKey: key,
            keyInfo: null,
            keyDetailRequestId: requestId,
            openKeyTabs: openKeyTabs.includes(key) ? openKeyTabs : [...openKeyTabs, key],
            activeRedisKey: key,
          },
        },
      };
    });

    try {
      const detail = await RedisGetKeyDetail({
        assetId: tab.assetId,
        db,
        key,
        cursor: "",
        offset: 0,
        count: REDIS_PAGE_SIZE,
      });

      set((s) => {
        const current = s.redisStates[tabId];
        if (
          !current ||
          current.keyDetailRequestId !== requestId ||
          current.selectedKey !== key ||
          current.currentDb !== db
        ) {
          return { redisStates: s.redisStates };
        }
        return {
          redisStates: {
            ...s.redisStates,
            [tabId]: {
              ...current,
              keyInfo: toRedisKeyInfo(detail as RedisKeyDetailResult),
            },
          },
        };
      });
    } catch {
      /* ignore */
    }
  },

  loadMoreValues: async (tabId) => {
    const tab = getQueryTabFromTabStore(tabId);
    const state = get().redisStates[tabId];
    if (!tab || !state?.keyInfo || !state.selectedKey || !state.keyInfo.hasMoreValues || state.keyInfo.loadingMore)
      return;

    const key = state.selectedKey;
    const info = state.keyInfo;
    const db = state.currentDb;
    const requestId = (state.keyDetailRequestId || 0) + 1;

    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: {
          ...s.redisStates[tabId],
          keyDetailRequestId: requestId,
          keyInfo: { ...info, loadingMore: true },
        },
      },
    }));

    try {
      let newValue: unknown = info.value;
      let newCursor = info.valueCursor;
      let newOffset = info.valueOffset;
      let newHasMore = false;

      const detail = await RedisGetKeyDetail({
        assetId: tab.assetId,
        db,
        key,
        cursor: info.valueCursor,
        offset: info.valueOffset,
        count: REDIS_PAGE_SIZE,
      });
      const next = toRedisKeyInfo(detail as RedisKeyDetailResult);
      newValue =
        info.type === "hash" || info.type === "zset"
          ? [...(info.value as [string, string][]), ...(next.value as [string, string][])]
          : info.type === "stream"
            ? [...(info.value as RedisStreamEntry[]), ...(next.value as RedisStreamEntry[])]
            : [...(info.value as string[]), ...((next.value as string[]) || [])];
      newCursor = next.valueCursor;
      newOffset = next.valueOffset;
      newHasMore = next.hasMoreValues;

      set((s) => {
        const current = s.redisStates[tabId];
        if (
          !current ||
          current.keyDetailRequestId !== requestId ||
          current.selectedKey !== key ||
          current.currentDb !== db
        ) {
          return { redisStates: s.redisStates };
        }
        return {
          redisStates: {
            ...s.redisStates,
            [tabId]: {
              ...current,
              keyInfo: {
                ...info,
                value: newValue,
                valueCursor: newCursor,
                valueOffset: newOffset,
                hasMoreValues: newHasMore,
                loadingMore: false,
              },
            },
          },
        };
      });
    } catch {
      set((s) => {
        const current = s.redisStates[tabId];
        if (
          !current ||
          current.keyDetailRequestId !== requestId ||
          current.selectedKey !== key ||
          current.currentDb !== db
        ) {
          return { redisStates: s.redisStates };
        }
        return {
          redisStates: {
            ...s.redisStates,
            [tabId]: { ...current, keyInfo: { ...info, loadingMore: false } },
          },
        };
      });
    }
  },

  setKeyFilter: (tabId, pattern) => {
    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: { ...s.redisStates[tabId], keyFilter: pattern || "*" },
      },
    }));
  },

  loadDbKeyCounts: async (tabId) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    try {
      const databases = await RedisListDatabases(tab.assetId);
      const counts: Record<number, number> = {};
      let masterKeyCounts: RedisClusterMasterKeyCount[] | undefined;
      for (const db of databases || []) {
        counts[db.db] = db.keys;
        if (db.masters) masterKeyCounts = db.masters;
      }
      set((s) => ({
        redisStates: {
          ...s.redisStates,
          [tabId]: { ...s.redisStates[tabId], dbKeyCounts: counts, masterKeyCounts },
        },
      }));
    } catch (err) {
      set((s) => ({
        redisStates: {
          ...s.redisStates,
          [tabId]: { ...s.redisStates[tabId], error: s.redisStates[tabId]?.error || String(err) },
        },
      }));
    }
  },

  clearSelectedKey: (tabId, key) => {
    const state = get().redisStates[tabId];
    if (!state || (key && state.selectedKey !== key)) return;
    const selectedKey = state.selectedKey;
    set((s) => {
      const current = s.redisStates[tabId];
      const activeRedisKey = getRedisActiveKey(current);
      return {
        redisStates: {
          ...s.redisStates,
          [tabId]: {
            ...current,
            selectedKey: null,
            keyInfo: null,
            activeRedisKey: selectedKey === null || activeRedisKey === selectedKey ? null : activeRedisKey,
            keyDetailRequestId: (current.keyDetailRequestId || 0) + 1,
          },
        },
      };
    });
  },

  activateRedisOverview: (tabId) => {
    const state = get().redisStates[tabId];
    if (!state) return;
    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: { ...s.redisStates[tabId], activeRedisKey: null },
      },
    }));
  },

  activateRedisKeyTab: (tabId, key) => {
    const state = get().redisStates[tabId];
    if (!state || !getRedisOpenKeyTabs(state).includes(key)) return;
    if (state.selectedKey !== key) {
      void get().selectKey(tabId, key);
      return;
    }
    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: { ...s.redisStates[tabId], activeRedisKey: key },
      },
    }));
  },

  closeRedisKeyTab: (tabId, key) => {
    const state = get().redisStates[tabId];
    if (!state) return;

    const openKeyTabs = getRedisOpenKeyTabs(state);
    const nextOpenKeyTabs = openKeyTabs.filter((item) => item !== key);
    const activeRedisKey = getRedisActiveKey(state);
    const closingActiveKey = activeRedisKey === key;
    const currentIndex = openKeyTabs.indexOf(key);
    const fallbackKey = closingActiveKey
      ? (nextOpenKeyTabs[Math.min(currentIndex, nextOpenKeyTabs.length - 1)] ?? null)
      : activeRedisKey;
    const shouldLoadFallbackKey = Boolean(fallbackKey && (closingActiveKey || state.selectedKey === key));

    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: {
          ...s.redisStates[tabId],
          openKeyTabs: nextOpenKeyTabs,
          activeRedisKey: fallbackKey,
          selectedKey: s.redisStates[tabId].selectedKey === key ? fallbackKey : s.redisStates[tabId].selectedKey,
          keyInfo: s.redisStates[tabId].selectedKey === key ? null : s.redisStates[tabId].keyInfo,
          keyDetailRequestId:
            s.redisStates[tabId].selectedKey === key
              ? (s.redisStates[tabId].keyDetailRequestId || 0) + 1
              : s.redisStates[tabId].keyDetailRequestId,
        },
      },
    }));

    if (fallbackKey && shouldLoadFallbackKey && state.selectedKey !== fallbackKey) {
      void get().selectKey(tabId, fallbackKey);
    }
  },

  removeKey: (tabId, key) => {
    const state = get().redisStates[tabId];
    if (!state) return;
    const openKeyTabs = getRedisOpenKeyTabs(state);
    const nextOpenKeyTabs = openKeyTabs.filter((item) => item !== key);
    const activeRedisKey = getRedisActiveKey(state);
    const removingActiveKey = activeRedisKey === key;
    const currentIndex = openKeyTabs.indexOf(key);
    const fallbackKey = removingActiveKey
      ? (nextOpenKeyTabs[Math.min(currentIndex, nextOpenKeyTabs.length - 1)] ?? null)
      : activeRedisKey;
    const shouldLoadFallbackKey = Boolean(fallbackKey && (removingActiveKey || state.selectedKey === key));

    set((s) => ({
      redisStates: {
        ...s.redisStates,
        [tabId]: {
          ...s.redisStates[tabId],
          keys: s.redisStates[tabId].keys.filter((k) => k !== key),
          openKeyTabs: nextOpenKeyTabs,
          activeRedisKey: fallbackKey,
          selectedKey: s.redisStates[tabId].selectedKey === key ? fallbackKey : s.redisStates[tabId].selectedKey,
          keyInfo: s.redisStates[tabId].selectedKey === key ? null : s.redisStates[tabId].keyInfo,
          keyDetailRequestId:
            s.redisStates[tabId].selectedKey === key
              ? (s.redisStates[tabId].keyDetailRequestId || 0) + 1
              : s.redisStates[tabId].keyDetailRequestId,
          removedKey: key,
          removedKeySeq: (s.redisStates[tabId].removedKeySeq || 0) + 1,
        },
      },
    }));

    if (fallbackKey && shouldLoadFallbackKey && state.selectedKey !== fallbackKey) {
      void get().selectKey(tabId, fallbackKey);
    }
  },

  // --- MongoDB ---

  loadMongoDatabases: async (tabId) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    try {
      const result = await ListMongoDatabases(tab.assetId);
      const databases: string[] = JSON.parse(result);
      set((s) => ({
        mongoStates: {
          ...s.mongoStates,
          [tabId]: { ...s.mongoStates[tabId], databases, error: null },
        },
      }));

      // Also refresh collections for already-loaded databases that still exist,
      // otherwise the top-level refresh button leaves stale collection lists.
      const loaded = Object.keys(get().mongoStates[tabId]?.collections ?? {});
      await Promise.all(
        loaded.filter((db) => databases.includes(db)).map((db) => get().loadMongoCollections(tabId, db))
      );
    } catch (err) {
      set((s) => ({
        mongoStates: {
          ...s.mongoStates,
          [tabId]: { ...s.mongoStates[tabId], error: s.mongoStates[tabId]?.error || String(err) },
        },
      }));
    }
  },

  loadMongoCollections: async (tabId, database) => {
    const tab = getQueryTabFromTabStore(tabId);
    if (!tab) return;

    try {
      const result = await ListMongoCollections(tab.assetId, database);
      const collections: string[] = JSON.parse(result);
      set((s) => ({
        mongoStates: {
          ...s.mongoStates,
          [tabId]: {
            ...s.mongoStates[tabId],
            collections: { ...s.mongoStates[tabId].collections, [database]: collections },
          },
        },
      }));
    } catch (err) {
      set((s) => ({
        mongoStates: {
          ...s.mongoStates,
          [tabId]: { ...s.mongoStates[tabId], error: s.mongoStates[tabId]?.error || String(err) },
        },
      }));
    }
  },

  toggleMongoDbExpand: (tabId, database) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    const isExpanded = state.expandedDbs.includes(database);
    const expanded = isExpanded ? state.expandedDbs.filter((d) => d !== database) : [...state.expandedDbs, database];
    if (!isExpanded && !state.collections[database]) {
      get().loadMongoCollections(tabId, database);
    }
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: {
          ...s.mongoStates[tabId],
          expandedDbs: expanded,
          // 展开某个库时把它当作"当前库"，方便新开 Query Tab 时继承
          activeDatabase: !isExpanded ? database : s.mongoStates[tabId].activeDatabase,
        },
      },
    }));
  },

  openCollectionTab: (tabId, database, collection) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    const innerId = `collection:${database}.${collection}`;
    if (state.innerTabs.some((t) => t.id === innerId)) {
      set((s) => ({
        mongoStates: {
          ...s.mongoStates,
          [tabId]: { ...s.mongoStates[tabId], activeInnerTabId: innerId },
        },
      }));
      return;
    }
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: {
          ...s.mongoStates[tabId],
          innerTabs: [...state.innerTabs, { id: innerId, type: "collection", database, collection }],
          activeInnerTabId: innerId,
        },
      },
    }));
  },

  openMongoQueryTab: (tabId, database?, collection?) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    const count = state.innerTabs.filter((t) => t.type === "query").length + 1;
    const innerId = `mongo-query:${Date.now()}`;
    const resolvedDb = database ?? state.activeDatabase ?? undefined;
    const queryText = collection ? `db.${collection}.find({})` : "";
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: {
          ...s.mongoStates[tabId],
          activeDatabase: resolvedDb ?? s.mongoStates[tabId].activeDatabase,
          innerTabs: [
            ...state.innerTabs,
            {
              id: innerId,
              type: "query",
              title: `Query ${count}`,
              database: resolvedDb,
              collection,
              queryText,
            },
          ],
          activeInnerTabId: innerId,
        },
      },
    }));
  },

  closeMongoInnerTab: (tabId, innerTabId) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    const idx = state.innerTabs.findIndex((t) => t.id === innerTabId);
    const newTabs = state.innerTabs.filter((t) => t.id !== innerTabId);
    let newActive = state.activeInnerTabId;
    if (newActive === innerTabId) {
      const neighbor = state.innerTabs[idx + 1] || state.innerTabs[idx - 1];
      newActive = neighbor?.id || null;
    }
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: { ...s.mongoStates[tabId], innerTabs: newTabs, activeInnerTabId: newActive },
      },
    }));
  },

  setActiveMongoInnerTab: (tabId, innerTabId) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: { ...s.mongoStates[tabId], activeInnerTabId: innerTabId },
      },
    }));
  },

  updateMongoInnerTab: (tabId, innerTabId, patch) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: {
          ...state,
          innerTabs: state.innerTabs.map((t) => (t.id === innerTabId ? ({ ...t, ...patch } as MongoInnerTab) : t)),
        },
      },
    }));
  },

  markMongoCollectionTabLoaded: (tabId, innerTabId) => {
    const state = get().mongoStates[tabId];
    if (!state) return;
    set((s) => ({
      mongoStates: {
        ...s.mongoStates,
        [tabId]: {
          ...state,
          innerTabs: state.innerTabs.map((t) =>
            t.id === innerTabId && t.type === "collection" ? { ...t, pendingLoad: false } : t
          ),
        },
      },
    }));
  },
}));

// === Persistence ===
//
// Caches sidebar metadata (database / table / collection lists, expanded
// state, inner tabs, sql history, editor height) so the sidebar is ready
// immediately on reload. Query results are NOT cached; table / collection
// inner tabs are restored with pendingLoad = true so the user must click
// to re-fetch the current page — avoiding a burst of queries on startup.

const QUERY_STORE_KEY = "query_store_v1";

interface PersistedDbState {
  databases: string[];
  tables: Record<string, string[]>;
  expandedDbs: string[];
  expandedSchemas?: Record<string, string[]>;
  innerTabs: InnerTab[];
  activeInnerTabId: string | null;
  sqlTabSeq?: number;
}

interface PersistedMongoState {
  databases: string[];
  collections: Record<string, string[]>;
  expandedDbs: string[];
  innerTabs: MongoInnerTab[];
  activeInnerTabId: string | null;
}

interface PersistedQueryStore {
  dbStates: Record<string, PersistedDbState>;
  mongoStates: Record<string, PersistedMongoState>;
}

function stripDbState(s: DatabaseTabState): PersistedDbState {
  return {
    databases: s.databases,
    tables: s.tables,
    expandedDbs: s.expandedDbs,
    expandedSchemas: s.expandedSchemas,
    innerTabs: s.innerTabs,
    activeInnerTabId: s.activeInnerTabId,
    sqlTabSeq: s.sqlTabSeq,
  };
}

function stripMongoState(s: MongoDBTabState): PersistedMongoState {
  return {
    databases: s.databases,
    collections: s.collections,
    expandedDbs: s.expandedDbs,
    innerTabs: s.innerTabs,
    activeInnerTabId: s.activeInnerTabId,
  };
}

function loadPersistedQueryStore(): PersistedQueryStore {
  try {
    const raw = localStorage.getItem(QUERY_STORE_KEY);
    if (!raw) return { dbStates: {}, mongoStates: {} };
    const parsed = JSON.parse(raw) as Partial<PersistedQueryStore>;
    return {
      dbStates: parsed.dbStates || {},
      mongoStates: parsed.mongoStates || {},
    };
  } catch {
    return { dbStates: {}, mongoStates: {} };
  }
}

function savePersistedQueryStore() {
  const state = useQueryStore.getState();
  const data: PersistedQueryStore = {
    dbStates: {},
    mongoStates: {},
  };
  for (const [tabId, s] of Object.entries(state.dbStates)) {
    data.dbStates[tabId] = stripDbState(s);
  }
  for (const [tabId, s] of Object.entries(state.mongoStates)) {
    data.mongoStates[tabId] = stripMongoState(s);
  }
  try {
    localStorage.setItem(QUERY_STORE_KEY, JSON.stringify(data));
  } catch {
    /* storage full — ignore */
  }
}

let _persistReady = false;
let _saveTimer: ReturnType<typeof setTimeout> | null = null;

useQueryStore.subscribe((state, prevState) => {
  if (!_persistReady) return;
  if (state.dbStates === prevState.dbStates && state.mongoStates === prevState.mongoStates) return;
  // Debounce: SQL editor / MongoDB query editor writes on every keystroke.
  if (_saveTimer) clearTimeout(_saveTimer);
  _saveTimer = setTimeout(() => {
    _saveTimer = null;
    savePersistedQueryStore();
  }, 300);
});

// === Close Hook: clean up when tabStore closes a query tab ===

registerTabCloseHook((tab) => {
  if (tab.type !== "query") return;
  useQueryStore.setState((s) => {
    const newDbStates = { ...s.dbStates };
    delete newDbStates[tab.id];
    const newRedisStates = { ...s.redisStates };
    delete newRedisStates[tab.id];
    const newMongoStates = { ...s.mongoStates };
    delete newMongoStates[tab.id];
    return { dbStates: newDbStates, redisStates: newRedisStates, mongoStates: newMongoStates };
  });
});

// === Restore Hook: initialize query tab states ===

/** 持久化状态缺少序号计数时,按现存「SQL N」标签的最大 N 初始化,避免与它们重号。 */
function maxSqlTitleNumber(innerTabs: InnerTab[]): number {
  let max = 0;
  for (const t of innerTabs) {
    const m = t.type === "sql" ? /^SQL (\d+)$/.exec(t.title) : null;
    if (m) max = Math.max(max, Number(m[1]));
  }
  return max;
}

registerTabRestoreHook("query", (tabs) => {
  const persisted = loadPersistedQueryStore();
  // Drop persisted entries whose tab is no longer open
  const openIds = new Set(tabs.map((t) => t.id));

  const dbStates: Record<string, DatabaseTabState> = {};
  const redisStates: Record<string, RedisTabState> = {};
  const mongoStates: Record<string, MongoDBTabState> = {};

  for (const tab of tabs) {
    const m = tab.meta as QueryTabMeta;
    if (m.assetType === "database") {
      const saved = persisted.dbStates[tab.id];
      if (saved) {
        dbStates[tab.id] = {
          ...defaultDbState(),
          databases: saved.databases || [],
          tables: saved.tables || {},
          expandedDbs: saved.expandedDbs || [],
          expandedSchemas: saved.expandedSchemas || {},
          innerTabs: (saved.innerTabs || []).map((it) => (it.type === "table" ? { ...it, pendingLoad: true } : it)),
          activeInnerTabId: saved.activeInnerTabId ?? null,
          sqlTabSeq: saved.sqlTabSeq ?? maxSqlTitleNumber(saved.innerTabs || []),
        };
      } else {
        dbStates[tab.id] = defaultDbState();
      }
    } else if (m.assetType === "mongodb") {
      const saved = persisted.mongoStates[tab.id];
      if (saved) {
        mongoStates[tab.id] = {
          ...defaultMongoState(),
          databases: saved.databases || [],
          collections: saved.collections || {},
          expandedDbs: saved.expandedDbs || [],
          innerTabs: (saved.innerTabs || []).map((it) =>
            it.type === "collection" ? { ...it, pendingLoad: true } : it
          ),
          activeInnerTabId: saved.activeInnerTabId ?? null,
        };
      } else {
        mongoStates[tab.id] = defaultMongoState();
      }
    } else if (m.assetType === "redis") {
      redisStates[tab.id] = defaultRedisState({ database: m.redisDatabase });
    }
  }

  useQueryStore.setState({ dbStates, redisStates, mongoStates });
  _persistReady = true;

  // Drop stale persisted entries (tabs no longer open) by writing the current
  // trimmed state back.
  const hasStale =
    Object.keys(persisted.dbStates).some((id) => !openIds.has(id)) ||
    Object.keys(persisted.mongoStates).some((id) => !openIds.has(id));
  if (hasStale) savePersistedQueryStore();
});
