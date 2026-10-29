import { describe, it, expect, beforeEach, vi } from "vitest";
import { useTabStore } from "../stores/tabStore";
import { useQueryStore } from "../stores/queryStore";
import { useAssetStore } from "../stores/assetStore";
import { asset_entity, query_svc } from "../../wailsjs/go/models";
import {
  ExecuteSQL,
  ListDatabaseObjects,
  ListDatabases,
  ListMongoCollections,
  ListMongoDatabases,
  ListTables,
} from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, FAKE_DRIVER, provideDrivers } from "./dbDriverFixtures";
import { RedisGetKeyDetail } from "../../wailsjs/go/redis/Redis";
import { RedisListDatabases, RedisScanKeys } from "../../wailsjs/go/redis/Redis";

function makeDatabaseAsset(id: number, name = `DB ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "database",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ driver: "mysql", database: "testdb", host: "10.0.0.1", port: 3306 }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

function makeSQLiteAsset(id: number, name = `SQLite ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "database",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ driver: "sqlite", path: "/tmp/app.db" }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

function makePostgreSQLAsset(id: number, name = `PostgreSQL ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "database",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ driver: "postgresql", database: "admdb", host: "127.0.0.1", port: 5432 }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

function makeMSSQLAsset(id: number, name = `MSSQL ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "database",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ driver: "mssql", host: "10.0.0.1", port: 1433, username: "sa" }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

function makeRedisAsset(id: number, name = `Redis ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "redis",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ host: "10.0.0.1", port: 6379 }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

function makeMongoAsset(id: number, name = `Mongo ${id}`): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: "mongodb",
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify({ host: "10.0.0.1", port: 27017 }),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

describe("queryStore.openQueryTab", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/DB");
  });

  it("should open a new database query tab", () => {
    const asset = makeDatabaseAsset(1);
    useQueryStore.getState().openQueryTab(asset);

    const tabs = useTabStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].id).toBe("query-1");
    expect(tabs[0].type).toBe("query");

    const meta = tabs[0].meta as { assetType: string; driver: string };
    expect(meta.assetType).toBe("database");
    expect(meta.driver).toBe("mysql");

    // Should initialize dbStates
    expect(useQueryStore.getState().dbStates["query-1"]).toBeDefined();
    expect(useQueryStore.getState().redisStates["query-1"]).toBeUndefined();
  });

  it("should open a new redis query tab", () => {
    const asset = makeRedisAsset(10);
    useQueryStore.getState().openQueryTab(asset);

    const tabs = useTabStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].id).toBe("query-10");

    // Should initialize redisStates
    expect(useQueryStore.getState().redisStates["query-10"]).toBeDefined();
    expect(useQueryStore.getState().dbStates["query-10"]).toBeUndefined();
  });

  it("should reuse existing tab for same asset", () => {
    const asset = makeDatabaseAsset(1);
    useQueryStore.getState().openQueryTab(asset);
    useQueryStore.getState().openQueryTab(asset);

    // Only one tab should exist
    expect(useTabStore.getState().tabs).toHaveLength(1);
    // Should activate it
    expect(useTabStore.getState().activeTabId).toBe("query-1");
  });

  it("should activate existing tab instead of creating duplicate", () => {
    const db1 = makeDatabaseAsset(1);
    const db2 = makeDatabaseAsset(2);

    useQueryStore.getState().openQueryTab(db1);
    useQueryStore.getState().openQueryTab(db2);
    expect(useTabStore.getState().activeTabId).toBe("query-2");

    // Open db1 again — should switch to it, not create new
    useQueryStore.getState().openQueryTab(db1);
    expect(useTabStore.getState().activeTabId).toBe("query-1");
    expect(useTabStore.getState().tabs).toHaveLength(2);
  });

  it("should allow different assets to open separate tabs", () => {
    useQueryStore.getState().openQueryTab(makeDatabaseAsset(1));
    useQueryStore.getState().openQueryTab(makeDatabaseAsset(2));
    useQueryStore.getState().openQueryTab(makeRedisAsset(3));

    expect(useTabStore.getState().tabs).toHaveLength(3);
    expect(useQueryStore.getState().dbStates["query-1"]).toBeDefined();
    expect(useQueryStore.getState().dbStates["query-2"]).toBeDefined();
    expect(useQueryStore.getState().redisStates["query-3"]).toBeDefined();
  });

  it("should open a new mongodb query tab", () => {
    const asset = makeMongoAsset(20);
    useQueryStore.getState().openQueryTab(asset);

    const tabs = useTabStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].id).toBe("query-20");
    expect(tabs[0].type).toBe("query");

    const meta = tabs[0].meta as { assetType: string };
    expect(meta.assetType).toBe("mongodb");

    // Should initialize mongoStates, not db/redis
    expect(useQueryStore.getState().mongoStates["query-20"]).toBeDefined();
    expect(useQueryStore.getState().dbStates["query-20"]).toBeUndefined();
    expect(useQueryStore.getState().redisStates["query-20"]).toBeUndefined();
  });

  it("should reuse existing mongodb query tab", () => {
    const asset = makeMongoAsset(20);
    useQueryStore.getState().openQueryTab(asset);
    useQueryStore.getState().openQueryTab(asset);

    // Only one tab should exist
    expect(useTabStore.getState().tabs).toHaveLength(1);
    // Should activate it
    expect(useTabStore.getState().activeTabId).toBe("query-20");
  });
});

describe("queryStore redis actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/Redis");
    useQueryStore.getState().openQueryTab(makeRedisAsset(10));
  });

  it("scans redis keys through typed binding", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValue({ cursor: "5", keys: ["a", "b"], hasMore: true });

    await useQueryStore.getState().scanKeys("query-10", true);

    expect(RedisScanKeys).toHaveBeenCalledWith({
      assetId: 10,
      db: 0,
      cursor: "0",
      match: "*",
      type: "",
      count: 200,
      exact: false,
    });
    const state = useQueryStore.getState().redisStates["query-10"];
    expect(state.keys).toEqual(["a", "b"]);
    expect(state.scanCursor).toBe("5");
    expect(state.hasMore).toBe(true);
  });

  it("ignores stale redis scan responses after a newer search starts", async () => {
    let resolveFirst!: (value: { cursor: string; keys: string[]; hasMore: boolean }) => void;
    let resolveSecond!: (value: { cursor: string; keys: string[]; hasMore: boolean }) => void;
    vi.mocked(RedisScanKeys)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          })
      )
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveSecond = resolve;
          })
      );

    const first = useQueryStore.getState().scanKeys("query-10", true);
    useQueryStore.getState().setKeyFilter("query-10", "root");
    const second = useQueryStore.getState().scanKeys("query-10", true);

    resolveSecond({ cursor: "0", keys: ["root:user"], hasMore: false });
    await second;
    expect(useQueryStore.getState().redisStates["query-10"].keys).toEqual(["root:user"]);

    resolveFirst({ cursor: "5", keys: ["old:key"], hasMore: true });
    await first;
    expect(useQueryStore.getState().redisStates["query-10"].keys).toEqual(["root:user"]);
    expect(useQueryStore.getState().redisStates["query-10"].scanCursor).toBe("0");
  });

  it("uses exact matching for plain redis key search", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValue({ cursor: "0", keys: [], hasMore: false });

    useQueryStore.getState().setKeyFilter("query-10", "2fe43136-1b38-43c3-b4bf-82b19c66c7bf");
    await useQueryStore.getState().scanKeys("query-10", true);

    expect(RedisScanKeys).toHaveBeenCalledWith(
      expect.objectContaining({
        match: "2fe43136-1b38-43c3-b4bf-82b19c66c7bf",
        exact: true,
      })
    );
  });

  it("preserves explicit redis match patterns", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValue({ cursor: "0", keys: [], hasMore: false });

    useQueryStore.getState().setKeyFilter("query-10", "common:*");
    await useQueryStore.getState().scanKeys("query-10", true);

    expect(RedisScanKeys).toHaveBeenCalledWith(expect.objectContaining({ match: "common:*" }));
  });

  it("loads selected key detail through typed binding", async () => {
    vi.mocked(RedisGetKeyDetail).mockResolvedValue({
      key: "user:1",
      type: "hash",
      ttl: 120,
      size: 42,
      total: 1,
      value: [{ field: "name", value: "Ada" }],
      valueCursor: "0",
      valueOffset: 0,
      hasMoreValues: false,
    });

    await useQueryStore.getState().selectKey("query-10", "user:1");

    expect(RedisGetKeyDetail).toHaveBeenCalledWith({
      assetId: 10,
      db: 0,
      key: "user:1",
      cursor: "",
      offset: 0,
      count: 100,
    });
    const info = useQueryStore.getState().redisStates["query-10"].keyInfo;
    expect(info?.type).toBe("hash");
    expect(info?.ttl).toBe(120);
    expect(info?.value).toEqual([["name", "Ada"]]);
  });

  it("clears the active redis key when the selected key is cleared", async () => {
    vi.mocked(RedisGetKeyDetail).mockResolvedValue({
      key: "user:1",
      type: "string",
      ttl: -1,
      size: 3,
      total: -1,
      value: "Ada",
      valueCursor: "0",
      valueOffset: 0,
      hasMoreValues: false,
    });

    await useQueryStore.getState().selectKey("query-10", "user:1");
    useQueryStore.getState().clearSelectedKey("query-10", "user:1");

    const state = useQueryStore.getState().redisStates["query-10"];
    expect(state.selectedKey).toBeNull();
    expect(state.activeRedisKey).toBeNull();
    expect(state.openKeyTabs).toEqual(["user:1"]);
  });

  it("ignores stale selected key detail responses", async () => {
    let resolveFirst!: (value: Awaited<ReturnType<typeof RedisGetKeyDetail>>) => void;
    let resolveSecond!: (value: Awaited<ReturnType<typeof RedisGetKeyDetail>>) => void;
    vi.mocked(RedisGetKeyDetail)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveFirst = resolve;
          })
      )
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveSecond = resolve;
          })
      );

    const first = useQueryStore.getState().selectKey("query-10", "user:old");
    const second = useQueryStore.getState().selectKey("query-10", "user:new");

    resolveSecond({
      key: "user:new",
      type: "string",
      ttl: -1,
      size: 3,
      total: -1,
      value: "new",
      valueCursor: "0",
      valueOffset: 0,
      hasMoreValues: false,
    });
    await second;

    resolveFirst({
      key: "user:old",
      type: "string",
      ttl: -1,
      size: 3,
      total: -1,
      value: "old",
      valueCursor: "0",
      valueOffset: 0,
      hasMoreValues: false,
    });
    await first;

    const state = useQueryStore.getState().redisStates["query-10"];
    expect(state.selectedKey).toBe("user:new");
    expect(state.keyInfo?.value).toBe("new");
  });

  it("ignores stale load-more responses after selecting another key", async () => {
    let resolveMore!: (value: Awaited<ReturnType<typeof RedisGetKeyDetail>>) => void;
    vi.mocked(RedisGetKeyDetail).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveMore = resolve;
        })
    );
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-10": {
          ...s.redisStates["query-10"],
          selectedKey: "list:old",
          keyInfo: {
            type: "list",
            ttl: -1,
            total: 3,
            value: ["a"],
            valueCursor: "0",
            valueOffset: 1,
            hasMoreValues: true,
            loadingMore: false,
          },
        },
      },
    }));

    const loadMore = useQueryStore.getState().loadMoreValues("query-10");
    useQueryStore.setState((s) => ({
      redisStates: {
        ...s.redisStates,
        "query-10": {
          ...s.redisStates["query-10"],
          selectedKey: "list:new",
          keyInfo: {
            type: "list",
            ttl: -1,
            total: 1,
            value: ["z"],
            valueCursor: "0",
            valueOffset: 1,
            hasMoreValues: false,
            loadingMore: false,
          },
        },
      },
    }));

    resolveMore({
      key: "list:old",
      type: "list",
      ttl: -1,
      size: 3,
      total: 3,
      value: ["b", "c"],
      valueCursor: "0",
      valueOffset: 3,
      hasMoreValues: false,
    });
    await loadMore;

    const state = useQueryStore.getState().redisStates["query-10"];
    expect(state.selectedKey).toBe("list:new");
    expect(state.keyInfo?.value).toEqual(["z"]);
  });

  it("loads db key counts through typed binding", async () => {
    vi.mocked(RedisListDatabases).mockResolvedValue([
      { db: 0, keys: 2, expires: 1, avgTtl: 10 },
      { db: 3, keys: 7, expires: 0, avgTtl: 0 },
    ]);

    await useQueryStore.getState().loadDbKeyCounts("query-10");

    expect(RedisListDatabases).toHaveBeenCalledWith(10);
    expect(useQueryStore.getState().redisStates["query-10"].dbKeyCounts).toEqual({ 0: 2, 3: 7 });
  });

  it("uses redis browser options from asset config", async () => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    const asset = makeRedisAsset(11);
    asset.Config = JSON.stringify({ host: "10.0.0.1", port: 6379, database: 3, scan_page_size: 500 });
    vi.mocked(RedisScanKeys).mockResolvedValue({ cursor: "0", keys: [], hasMore: false });

    useQueryStore.getState().openQueryTab(asset);
    await useQueryStore.getState().scanKeys("query-11", true);

    expect(useQueryStore.getState().redisStates["query-11"].currentDb).toBe(3);
    expect(RedisScanKeys).toHaveBeenCalledWith(
      expect.objectContaining({
        assetId: 11,
        db: 3,
        count: 500,
      })
    );
  });

  it("uses wildcard pattern matching for redis key search", async () => {
    vi.mocked(RedisScanKeys).mockResolvedValue({
      cursor: "0",
      keys: [],
      hasMore: false,
    });

    useQueryStore.getState().setKeyFilter("query-10", "dispatcher*");

    await useQueryStore.getState().scanKeys("query-10", true);

    expect(RedisScanKeys).toHaveBeenCalledWith(
      expect.objectContaining({
        match: "dispatcher*",
        exact: false,
      })
    );
  });
});

describe("queryStore database actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/SQLite");
    useQueryStore.getState().openQueryTab(makeSQLiteAsset(30));
  });

  it("lists SQLite databases through the structured binding and shows what the driver returns", async () => {
    vi.mocked(ListDatabases).mockResolvedValueOnce(["main"]);

    await useQueryStore.getState().loadDatabases("query-30");

    expect(ListDatabases).toHaveBeenCalledWith(30);
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect(useQueryStore.getState().dbStates["query-30"].databases).toEqual(["main"]);
  });

  it("lists SQLite tables through the structured binding", async () => {
    vi.mocked(ListTables).mockResolvedValueOnce(["users", "orders"]);

    await useQueryStore.getState().loadTables("query-30", "main");

    expect(ListTables).toHaveBeenCalledWith(30, "main");
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect(useQueryStore.getState().dbStates["query-30"].tables.main).toEqual(["users", "orders"]);
  });

  it("reports a database listing failure in the same place as before", async () => {
    vi.mocked(ListDatabases).mockRejectedValueOnce("boom");

    await useQueryStore.getState().loadDatabases("query-30");

    expect(useQueryStore.getState().dbStates["query-30"].error).toBe("boom");
    expect(useQueryStore.getState().dbStates["query-30"].loadingDbs).toBe(false);
  });

  it("refreshes SQLite object browser data with the table list", async () => {
    vi.mocked(ListTables).mockResolvedValueOnce(["users", "orders"]);
    vi.mocked(ListDatabaseObjects).mockResolvedValueOnce({
      views: [{ name: "v_orders", type: "view" }],
      procedures: [],
      functions: [],
      triggers: [{ name: "trg_orders_ai", type: "trigger" }],
    } as unknown as query_svc.DatabaseObjects);

    await useQueryStore.getState().refreshTables("query-30", "main");

    expect(ListDatabaseObjects).toHaveBeenCalledWith(30, "main");
    expect(useQueryStore.getState().dbStates["query-30"].objects?.main).toEqual({
      views: [{ name: "v_orders", type: "view" }],
      procedures: [],
      functions: [],
      triggers: [{ name: "trg_orders_ai", type: "trigger" }],
    });
  });
});

describe("queryStore PostgreSQL database actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/PostgreSQL");
    useQueryStore.getState().openQueryTab(makePostgreSQLAsset(35));
  });

  it("loads PostgreSQL tables from non-system schemas as schema.table", async () => {
    vi.mocked(ListTables).mockResolvedValueOnce(["adm.ads_audit", "public.users"]);

    await useQueryStore.getState().loadTables("query-35", "admdb");

    expect(ListTables).toHaveBeenCalledWith(35, "admdb");
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect(useQueryStore.getState().dbStates["query-35"].tables.admdb).toEqual(["adm.ads_audit", "public.users"]);
    expect(useQueryStore.getState().dbStates["query-35"].expandedSchemas.admdb).toEqual(["adm", "public"]);
  });

  it("preserves still-existing PostgreSQL schema expansion state on refresh", async () => {
    useQueryStore.setState((s) => ({
      dbStates: {
        ...s.dbStates,
        "query-35": {
          ...s.dbStates["query-35"],
          expandedSchemas: { admdb: ["adm"] },
        },
      },
    }));
    vi.mocked(ListTables).mockResolvedValueOnce(["adm.ads_audit", "reporting.events"]);

    await useQueryStore.getState().loadTables("query-35", "admdb");

    expect(useQueryStore.getState().dbStates["query-35"].tables.admdb).toEqual(["adm.ads_audit", "reporting.events"]);
    expect(useQueryStore.getState().dbStates["query-35"].expandedSchemas.admdb).toEqual(["adm"]);
  });

  it("toggles PostgreSQL schema expansion", () => {
    useQueryStore.setState((s) => ({
      dbStates: {
        ...s.dbStates,
        "query-35": {
          ...s.dbStates["query-35"],
          expandedSchemas: { admdb: ["adm"] },
        },
      },
    }));

    useQueryStore.getState().toggleSchemaExpand("query-35", "admdb", "public");
    expect(useQueryStore.getState().dbStates["query-35"].expandedSchemas.admdb).toEqual(["adm", "public"]);

    useQueryStore.getState().toggleSchemaExpand("query-35", "admdb", "adm");
    expect(useQueryStore.getState().dbStates["query-35"].expandedSchemas.admdb).toEqual(["public"]);
  });
});

describe("queryStore MSSQL database actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/MSSQL");
    useQueryStore.getState().openQueryTab(makeMSSQLAsset(40));
  });

  it("lists MSSQL databases through the structured binding, showing the driver's list as-is", async () => {
    vi.mocked(ListDatabases).mockResolvedValueOnce(["appdb"]);

    await useQueryStore.getState().loadDatabases("query-40");

    expect(ListDatabases).toHaveBeenCalledWith(40);
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect(useQueryStore.getState().dbStates["query-40"].databases).toEqual(["appdb"]);
  });

  it("lists MSSQL tables as schema.table through the structured binding", async () => {
    vi.mocked(ListTables).mockResolvedValueOnce(["dbo.users", "sales.orders"]);

    await useQueryStore.getState().loadTables("query-40", "appdb");

    expect(ListTables).toHaveBeenCalledWith(40, "appdb");
    expect(ExecuteSQL).not.toHaveBeenCalled();
    expect(useQueryStore.getState().dbStates["query-40"].tables.appdb).toEqual(["dbo.users", "sales.orders"]);
    expect(useQueryStore.getState().dbStates["query-40"].expandedSchemas.appdb).toEqual(["dbo", "sales"]);
  });
});

describe("queryStore with a driver unknown to the frontend", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers([FAKE_DRIVER]);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/FakeDB");
    useQueryStore.getState().openQueryTab({
      ...makeDatabaseAsset(50),
      Config: JSON.stringify({ driver: "fakedb", host: "h", port: 7777 }),
    } as asset_entity.Asset);
  });

  it("presents the databases and tables the driver returned, as-is", async () => {
    vi.mocked(ListDatabases).mockResolvedValueOnce(["alpha", "beta"]);
    vi.mocked(ListTables).mockResolvedValueOnce(["t1", "t2"]);

    await useQueryStore.getState().loadDatabases("query-50");
    await useQueryStore.getState().loadTables("query-50", "alpha");

    const state = useQueryStore.getState().dbStates["query-50"];
    expect(state.databases).toEqual(["alpha", "beta"]);
    expect(state.tables.alpha).toEqual(["t1", "t2"]);
    expect(ExecuteSQL).not.toHaveBeenCalled();
  });
});

describe("queryStore MongoDB actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/Mongo");
    useQueryStore.getState().openQueryTab(makeMongoAsset(20));
  });

  function seedLoadedCollections(collections: Record<string, string[]>, expandedDbs: string[]) {
    useQueryStore.setState((s) => ({
      mongoStates: {
        ...s.mongoStates,
        "query-20": { ...s.mongoStates["query-20"], databases: Object.keys(collections), collections, expandedDbs },
      },
    }));
  }

  it("refreshes the collection lists of already-loaded databases when databases are reloaded", async () => {
    seedLoadedCollections({ app: ["users"], logs: ["audit"] }, ["app"]);
    vi.mocked(ListMongoDatabases).mockResolvedValueOnce(JSON.stringify(["app", "logs"]));
    const fresh: Record<string, string[]> = { app: ["users", "associated_vulnerability"], logs: ["audit", "access"] };
    vi.mocked(ListMongoCollections).mockImplementation(async (_assetId, db) => JSON.stringify(fresh[db]));

    await useQueryStore.getState().loadMongoDatabases("query-20");

    const state = useQueryStore.getState().mongoStates["query-20"];
    expect(state.collections.app).toEqual(["users", "associated_vulnerability"]);
    expect(state.collections.logs).toEqual(["audit", "access"]);
  });

  it("only reloads collections for loaded databases that still exist", async () => {
    seedLoadedCollections({ app: ["users"], dropped: ["tmp"] }, ["app", "dropped"]);
    vi.mocked(ListMongoDatabases).mockResolvedValueOnce(JSON.stringify(["app", "neverOpened"]));
    vi.mocked(ListMongoCollections).mockResolvedValue(JSON.stringify(["users", "orders"]));

    await useQueryStore.getState().loadMongoDatabases("query-20");

    expect(ListMongoCollections).toHaveBeenCalledTimes(1);
    expect(ListMongoCollections).toHaveBeenCalledWith(20, "app");
    expect(useQueryStore.getState().mongoStates["query-20"].collections.neverOpened).toBeUndefined();
  });
});

describe("queryStore.revealTable", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/SQLite");
    useQueryStore.getState().openQueryTab(makeSQLiteAsset(30));
  });

  it("expands the database once and loads its tables when missing", async () => {
    vi.mocked(ListTables).mockResolvedValue(["users"]);

    useQueryStore.getState().revealTable("query-30", "main", "users");
    const first = useQueryStore.getState().dbStates["query-30"];
    expect(first.expandedDbs).toContain("main");
    expect(first.revealRequest).toMatchObject({ db: "main", table: "users" });
    await vi.waitFor(() => expect(ListTables).toHaveBeenCalledWith(30, "main"));

    useQueryStore.getState().revealTable("query-30", "main", "users");
    const second = useQueryStore.getState().dbStates["query-30"];
    expect(second.expandedDbs.filter((d) => d === "main")).toHaveLength(1);
  });
});

describe("queryStore inner tab closing and SQL numbering", () => {
  const innerIds = () => useQueryStore.getState().dbStates["query-40"].innerTabs.map((t) => t.id);

  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/SQLite");
    useQueryStore.getState().openQueryTab(makeSQLiteAsset(40));
  });

  function seed() {
    const s = useQueryStore.getState();
    s.openTableTab("query-40", "main", "a");
    s.openTableTab("query-40", "main", "b");
    s.openTableTab("query-40", "main", "c");
  }

  it("never reuses an SQL tab number after closing tabs", () => {
    const s = useQueryStore.getState();
    s.openSqlTab("query-40");
    s.openSqlTab("query-40");
    const [first, second] = useQueryStore.getState().dbStates["query-40"].innerTabs;
    s.closeInnerTab("query-40", second.id);
    s.closeInnerTab("query-40", first.id);
    s.openSqlTab("query-40");
    const titles = useQueryStore
      .getState()
      .dbStates["query-40"].innerTabs.map((t) => (t.type === "sql" ? t.title : ""));
    expect(titles).toEqual(["SQL 3"]);
  });

  it("closeOtherInnerTabs keeps only the given tab and activates it", () => {
    seed();
    useQueryStore.getState().closeOtherInnerTabs("query-40", "table:main.b");
    expect(innerIds()).toEqual(["table:main.b"]);
    expect(useQueryStore.getState().dbStates["query-40"].activeInnerTabId).toBe("table:main.b");
  });

  it("closeRightInnerTabs drops tabs to the right and moves active onto the anchor if it was closed", () => {
    seed();
    expect(useQueryStore.getState().dbStates["query-40"].activeInnerTabId).toBe("table:main.c");
    useQueryStore.getState().closeRightInnerTabs("query-40", "table:main.a");
    expect(innerIds()).toEqual(["table:main.a"]);
    expect(useQueryStore.getState().dbStates["query-40"].activeInnerTabId).toBe("table:main.a");
  });

  it("closeRightInnerTabs keeps the active tab when it survives", () => {
    seed();
    useQueryStore.getState().setActiveInnerTab("query-40", "table:main.a");
    useQueryStore.getState().closeRightInnerTabs("query-40", "table:main.b");
    expect(innerIds()).toEqual(["table:main.a", "table:main.b"]);
    expect(useQueryStore.getState().dbStates["query-40"].activeInnerTabId).toBe("table:main.a");
  });

  describe("with unsaved edits", () => {
    const db = () => useQueryStore.getState().dbStates["query-40"];

    it("marks the outer tab unsaved while any inner tab has edits", () => {
      seed();
      const s = useQueryStore.getState();
      s.setInnerTabDirty("query-40", "table:main.a", true);
      s.setInnerTabDirty("query-40", "table:main.b", true);
      s.setInnerTabDirty("query-40", "table:main.a", false);
      expect(useTabStore.getState().unsavedTabIds).toContain("query-40");
      s.setInnerTabDirty("query-40", "table:main.b", false);
      expect(useTabStore.getState().unsavedTabIds).not.toContain("query-40");
    });

    it("holds the close of an edited tab for a decision and brings it to the front", () => {
      seed();
      useQueryStore.getState().setInnerTabDirty("query-40", "table:main.a", true);
      useQueryStore.getState().closeInnerTab("query-40", "table:main.a");
      expect(innerIds()).toEqual(["table:main.a", "table:main.b", "table:main.c"]);
      expect(db().activeInnerTabId).toBe("table:main.a");
      expect(db().closeRequest).toEqual({ ids: ["table:main.a"], dirtyIds: ["table:main.a"], closeOuter: false });
    });

    it("a batch close with edits closes nothing until decided; cancel keeps every tab", () => {
      seed();
      useQueryStore.getState().setInnerTabDirty("query-40", "table:main.b", true);
      useQueryStore.getState().closeOtherInnerTabs("query-40", "table:main.c");
      expect(innerIds()).toEqual(["table:main.a", "table:main.b", "table:main.c"]);
      useQueryStore.getState().cancelCloseRequest("query-40");
      expect(db().closeRequest).toBeUndefined();
      expect(innerIds()).toEqual(["table:main.a", "table:main.b", "table:main.c"]);
    });

    it("forceCloseInnerTabs closes the requested tabs and forgets their edits", () => {
      seed();
      useQueryStore.getState().setInnerTabDirty("query-40", "table:main.b", true);
      useQueryStore.getState().closeOtherInnerTabs("query-40", "table:main.c");
      const { ids, closeOuter } = db().closeRequest!;
      useQueryStore.getState().forceCloseInnerTabs("query-40", ids, closeOuter);
      expect(innerIds()).toEqual(["table:main.c"]);
      expect(db().activeInnerTabId).toBe("table:main.c");
      expect(db().closeRequest).toBeUndefined();
      expect(useTabStore.getState().unsavedTabIds).not.toContain("query-40");
    });

    it("forceCloseInnerTabs with closeOuter closes the database tab itself", () => {
      seed();
      useQueryStore.getState().setInnerTabDirty("query-40", "table:main.b", true);
      useQueryStore.getState().forceCloseInnerTabs("query-40", ["table:main.b"], true);
      expect(useTabStore.getState().tabs.map((t) => t.id)).not.toContain("query-40");
      expect(useQueryStore.getState().dbStates["query-40"]).toBeUndefined();
    });
  });
});

describe("queryStore 设计器标签", () => {
  const tabs = () => useQueryStore.getState().dbStates["query-41"].innerTabs;
  const active = () => useQueryStore.getState().dbStates["query-41"].activeInnerTabId;

  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/SQLite");
    useQueryStore.getState().openQueryTab(makeSQLiteAsset(41));
  });

  it("每张表只开一个:再次打开切到已有的那个", () => {
    const s = useQueryStore.getState();
    s.openDesignerTab("query-41", "main", "users", "view");
    s.openTableTab("query-41", "main", "users");
    s.openDesignerTab("query-41", "main", "users", "view");

    const designers = tabs().filter((t) => t.type === "designer");
    expect(designers).toEqual([
      { id: "designer:main.users", type: "designer", mode: "view", database: "main", table: "users" },
    ]);
    expect(active()).toBe("designer:main.users");
    // 与同一张表的数据标签互不影响。
    expect(tabs().map((t) => t.id)).toEqual(["designer:main.users", "table:main.users"]);
  });

  it("不同的表各开一个", () => {
    const s = useQueryStore.getState();
    s.openDesignerTab("query-41", "main", "users", "view");
    s.openDesignerTab("query-41", "main", "orders", "view");
    expect(tabs().map((t) => t.id)).toEqual(["designer:main.users", "designer:main.orders"]);
    expect(active()).toBe("designer:main.orders");
  });

  it("改名后设计器标签跟随新表名:id、表名、当前标签、脏标记与挂起的关闭请求都换成新的", () => {
    const s = useQueryStore.getState();
    s.openDesignerTab("query-41", "main", "users", "design");
    s.openSqlTab("query-41");
    s.setActiveInnerTab("query-41", "designer:main.users");
    s.setInnerTabDirty("query-41", "designer:main.users", true);
    s.requestCloseInnerTabs(
      "query-41",
      tabs().map((t) => t.id)
    );

    useQueryStore.getState().renameDesignerTab("query-41", "designer:main.users", "people");

    const state = useQueryStore.getState().dbStates["query-41"];
    expect(state.innerTabs[0]).toEqual({
      id: "designer:main.people",
      type: "designer",
      mode: "design",
      database: "main",
      table: "people",
    });
    expect(state.innerTabs).toHaveLength(2);
    expect(state.activeInnerTabId).toBe("designer:main.people");
    expect(state.dirtyInnerTabIds).toEqual(["designer:main.people"]);
    expect(state.closeRequest?.ids).toEqual(["designer:main.people", state.innerTabs[1].id]);
    expect(state.closeRequest?.dirtyIds).toEqual(["designer:main.people"]);
    // 再从树上打开新名字的设计器,切到的就是这个标签。
    useQueryStore.getState().openDesignerTab("query-41", "main", "people", "design");
    expect(tabs().filter((t) => t.type === "designer")).toHaveLength(1);
  });

  it("改名时新名字已有一个设计器标签:去掉那个旧的,不出现两个同 id 的标签", () => {
    const s = useQueryStore.getState();
    s.openDesignerTab("query-41", "main", "people", "view");
    s.openDesignerTab("query-41", "main", "users", "design");
    useQueryStore.getState().renameDesignerTab("query-41", "designer:main.users", "people");
    expect(tabs()).toEqual([
      { id: "designer:main.people", type: "designer", mode: "design", database: "main", table: "people" },
    ]);
  });
});

describe("queryStore 新建表标签", () => {
  const state = () => useQueryStore.getState().dbStates["query-42"];
  const creates = () => state().innerTabs.filter((t) => t.type === "designer" && t.mode === "create");

  beforeEach(() => {
    vi.clearAllMocks();
    provideDrivers(BUILTIN_DRIVERS);
    useTabStore.setState({ tabs: [], activeTabId: null });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockReturnValue("Test/SQLite");
    useQueryStore.getState().openQueryTab(makeSQLiteAsset(42));
  });

  it("在目标库(与 schema)打开一个新建表标签并切过去", () => {
    useQueryStore.getState().openCreateTableTab("query-42", "appdb", "sales");
    expect(creates()).toEqual([
      { id: expect.stringMatching(/^create:/), type: "designer", mode: "create", database: "appdb", schema: "sales" },
    ]);
    expect(state().activeInnerTabId).toBe(creates()[0].id);
  });

  it("同一目标还没动过的新建表标签直接切过去;动过的、或目标不同的另开一个", () => {
    const s = useQueryStore.getState();
    s.openCreateTableTab("query-42", "appdb");
    const first = creates()[0].id;
    s.openSqlTab("query-42");
    s.openCreateTableTab("query-42", "appdb");
    expect(creates()).toHaveLength(1);
    expect(state().activeInnerTabId).toBe(first);

    s.openCreateTableTab("query-42", "other");
    expect(creates()).toHaveLength(2);

    s.setInnerTabDirty("query-42", first, true);
    useQueryStore.getState().openCreateTableTab("query-42", "appdb");
    expect(creates()).toHaveLength(3);
    expect(new Set(creates().map((t) => t.id)).size).toBe(3);
  });

  it("建好后标签换成这张表的设计标签:id、当前标签与脏标记跟着换,原有的同表设计标签去掉", () => {
    const s = useQueryStore.getState();
    s.openDesignerTab("query-42", "appdb", "users", "view");
    s.openCreateTableTab("query-42", "appdb");
    const id = creates()[0].id;
    s.setInnerTabDirty("query-42", id, true);

    useQueryStore.getState().completeCreateTab("query-42", id, "users");

    expect(state().innerTabs).toEqual([
      { id: "designer:appdb.users", type: "designer", mode: "design", database: "appdb", table: "users" },
    ]);
    expect(state().activeInnerTabId).toBe("designer:appdb.users");
    expect(state().dirtyInnerTabIds).toEqual(["designer:appdb.users"]);
  });
});

describe("queryStore SQL numbering across persist and restore", () => {
  const sqlTab = (n: number) => ({ id: `sql:${n}`, type: "sql", title: `SQL ${n}` });
  const tabsJson = JSON.stringify({
    tabs: [
      {
        id: "query-50",
        type: "query",
        label: "db",
        meta: { type: "query", assetId: 50, assetName: "db", assetIcon: "", assetType: "database", driver: "sqlite" },
      },
    ],
    activeTabId: "query-50",
  });

  async function restore(persisted: object) {
    vi.resetModules();
    localStorage.clear();
    localStorage.setItem("tab_store", tabsJson);
    localStorage.setItem("query_store_v1", JSON.stringify({ dbStates: { "query-50": persisted }, mongoStates: {} }));
    const mod = await import("../stores/queryStore");
    return mod.useQueryStore;
  }
  const base = { databases: [], tables: {}, expandedDbs: [], activeInnerTabId: null };

  it("restores the persisted counter so closed numbers stay retired", async () => {
    const store = await restore({ ...base, innerTabs: [sqlTab(1)], sqlTabSeq: 5 });
    store.getState().openSqlTab("query-50");
    expect(store.getState().dbStates["query-50"].innerTabs.map((t) => (t.type === "sql" ? t.title : ""))).toContain(
      "SQL 6"
    );
  });

  it("derives the counter from existing titles when an old persisted state has none", async () => {
    const store = await restore({ ...base, innerTabs: [sqlTab(2), sqlTab(7)] });
    store.getState().openSqlTab("query-50");
    expect(store.getState().dbStates["query-50"].innerTabs.map((t) => (t.type === "sql" ? t.title : ""))).toContain(
      "SQL 8"
    );
  });

  it("设计器标签随持久化恢复,恢复后不带任何草稿", async () => {
    const designer = { id: "designer:main.users", type: "designer", mode: "view", database: "main", table: "users" };
    const store = await restore({ ...base, innerTabs: [designer], activeInnerTabId: designer.id });
    store.getState().openSqlTab("query-50");
    await vi.waitFor(() => {
      const saved = JSON.parse(localStorage.getItem("query_store_v1")!);
      // 前面用例的旧模块实例可能还有防抖保存没落地,按内容等这一次的保存。
      expect(saved.dbStates["query-50"].innerTabs.map((t: { id: string }) => t.id)).toEqual([
        designer.id,
        expect.stringMatching(/^sql:/),
      ]);
    });

    const saved = JSON.parse(localStorage.getItem("query_store_v1")!);
    const restored = await restore(saved.dbStates["query-50"]);
    expect(restored.getState().dbStates["query-50"].innerTabs[0]).toEqual(designer);
  });

  it("keeps a closed number retired across a persist and restore round trip", async () => {
    const store = await restore({ ...base, innerTabs: [sqlTab(1)], sqlTabSeq: 3 });
    store.getState().openSqlTab("query-50");
    // 关掉全部 SQL 标签:恢复后若按现存标题推算会回到 SQL 1,保留序号才是 SQL 5。
    store.getState().closeOtherInnerTabs("query-50", "sql:1");
    store.getState().closeInnerTab("query-50", "sql:1");
    await vi.waitFor(() => {
      const saved = JSON.parse(localStorage.getItem("query_store_v1")!);
      expect(saved.dbStates["query-50"].innerTabs).toEqual([]);
    });

    const saved = JSON.parse(localStorage.getItem("query_store_v1")!);
    const restored = await restore(saved.dbStates["query-50"]);
    restored.getState().openSqlTab("query-50");
    expect(restored.getState().dbStates["query-50"].innerTabs.map((t) => (t.type === "sql" ? t.title : ""))).toEqual([
      "SQL 5",
    ]);
  });
});
