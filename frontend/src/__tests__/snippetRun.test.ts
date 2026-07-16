import { beforeEach, describe, expect, it, vi } from "vitest";
import type { asset_entity } from "../../wailsjs/go/models";
import { isRunnableCategoryId, runSnippetOnAsset } from "../components/snippet/snippetRun";
import { registerSnippetRunner } from "../lib/snippetRunners";

const mocks = vi.hoisted(() => ({
  connect: vi.fn(),
  openQueryTab: vi.fn(),
  writeSSH: vi.fn(),
  terminalState: { tabData: {} as Record<string, unknown> },
  tabState: { tabs: [] as unknown[] },
}));

vi.mock("../stores/terminalStore", () => ({
  useTerminalStore: { getState: () => ({ ...mocks.terminalState, connect: mocks.connect }) },
}));

vi.mock("../stores/tabStore", () => ({
  useTabStore: { getState: () => mocks.tabState },
}));

vi.mock("../stores/queryStore", () => ({
  useQueryStore: { getState: () => ({ openQueryTab: mocks.openQueryTab }) },
}));

vi.mock("../../wailsjs/go/ssh/SSH", () => ({ WriteSSH: mocks.writeSSH }));

type Cat = { id: string; assetType: string };

const CATEGORIES: Cat[] = [
  { id: "shell", assetType: "ssh" },
  { id: "sql", assetType: "database" },
  { id: "mongo", assetType: "mongodb" },
  { id: "redis", assetType: "redis" },
  { id: "k8s", assetType: "k8s" },
  { id: "prompt", assetType: "" },
];

function asset(type: string, id = 7): asset_entity.Asset {
  return { ID: id, Type: type, Name: `${type}-asset` } as asset_entity.Asset;
}

describe("isRunnableCategoryId", () => {
  it("is true only for categories backed by a registered snippet runner", () => {
    expect(isRunnableCategoryId("shell", CATEGORIES)).toBe(true);
    expect(isRunnableCategoryId("sql", CATEGORIES)).toBe(true);
    expect(isRunnableCategoryId("mongo", CATEGORIES)).toBe(true);
    expect(isRunnableCategoryId("redis", CATEGORIES)).toBe(false);
    expect(isRunnableCategoryId("k8s", CATEGORIES)).toBe(false);
    expect(isRunnableCategoryId("prompt", CATEGORIES)).toBe(false);
  });

  it("is false for an unknown category id", () => {
    expect(isRunnableCategoryId("does-not-exist", CATEGORIES)).toBe(false);
  });
});

describe("runSnippetOnAsset", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.terminalState.tabData = {};
    mocks.tabState.tabs = [];
  });

  it("writes into an existing connected SSH pane without opening another connection", async () => {
    mocks.tabState.tabs = [{ id: "session-7", type: "terminal", meta: { assetId: 7 } }];
    mocks.terminalState.tabData = {
      "session-7": { activePaneId: "pane-1", panes: { "pane-1": { connected: true } } },
    };

    await runSnippetOnAsset(asset("ssh"), "echo hello");

    expect(mocks.writeSSH).toHaveBeenCalledWith("pane-1", btoa("echo hello"));
    expect(mocks.connect).not.toHaveBeenCalled();
  });

  it("opens a new SSH connection with initial input when no connected pane exists", async () => {
    const target = asset("ssh");

    await runSnippetOnAsset(target, "uptime");

    expect(mocks.connect).toHaveBeenCalledWith(target, "", false, { initialInput: "uptime" });
    expect(mocks.writeSSH).not.toHaveBeenCalled();
  });

  it("opens database snippets as initial SQL", async () => {
    const target = asset("database");

    await runSnippetOnAsset(target, "select 1");

    expect(mocks.openQueryTab).toHaveBeenCalledWith(target, { initialSQL: "select 1" });
  });

  it("opens MongoDB snippets as initial Mongo queries", async () => {
    const target = asset("mongodb");

    await runSnippetOnAsset(target, "db.users.find({})");

    expect(mocks.openQueryTab).toHaveBeenCalledWith(target, { initialMongo: "db.users.find({})" });
  });

  it("rejects asset types without a registered runner", async () => {
    await expect(runSnippetOnAsset(asset("redis"), "GET key")).rejects.toThrow(
      "snippetRun: unsupported asset type redis"
    );
  });

  it("dispatches newly registered asset types without changing the caller", async () => {
    const runner = vi.fn().mockResolvedValue(undefined);
    registerSnippetRunner("extension-shell", runner);
    const target = asset("extension-shell");

    await runSnippetOnAsset(target, "hello extension");

    expect(runner).toHaveBeenCalledWith(target, "hello extension");
    expect(isRunnableCategoryId("extension", [{ id: "extension", assetType: "extension-shell" }])).toBe(true);
  });
});
