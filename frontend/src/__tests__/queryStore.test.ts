import { describe, it, expect, beforeEach, vi } from "vitest";
import { useTabStore } from "../stores/tabStore";
import { useQueryStore } from "../stores/queryStore";
import { useAssetStore } from "../stores/assetStore";
import { asset_entity, query_svc } from "../../wailsjs/go/models";
import { ExecuteSQL, ListDatabaseObjects, ListDatabases, ListTables } from "../../wailsjs/go/query/Query";
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
