/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect } from "vitest";
import { resolveSnippetTarget } from "../lib/snippetTarget";
import type { Tab } from "../stores/tabStore";
import type { asset_entity, snippet_entity, snippet_svc } from "../../wailsjs/go/models";

type Asset = asset_entity.Asset;

const CATEGORIES = [
  { id: "shell", assetType: "ssh", label: "Shell", source: "builtin" },
  { id: "sql", assetType: "database", label: "SQL", source: "builtin" },
  { id: "mongo", assetType: "mongodb", label: "Mongo", source: "builtin" },
  { id: "prompt", assetType: "", label: "Prompt", source: "builtin" },
] as snippet_svc.Category[];

function snippet(category: string): snippet_entity.Snippet {
  return { ID: 1, Name: "s", Category: category, Content: "echo hi", Source: "user" } as any;
}

function asset(id: number, type: string): Asset {
  return { ID: id, Name: `asset-${id}`, Type: type } as any;
}

function terminalTab(assetId: number): Tab {
  return {
    id: "t1",
    type: "terminal",
    label: "term",
    meta: { type: "terminal", assetId, assetName: "x", assetIcon: "", host: "h", port: 22, username: "u" },
  } as Tab;
}

function queryTab(assetId: number, assetType: string): Tab {
  return {
    id: "q1",
    type: "query",
    label: "query",
    meta: { type: "query", assetId, assetName: "x", assetIcon: "", assetType: assetType as any },
  } as Tab;
}

function aiTab(): Tab {
  return { id: "a1", type: "ai", label: "ai", meta: { type: "ai", conversationId: null, title: "ai" } } as Tab;
}

describe("resolveSnippetTarget", () => {
  it("shell snippet + active terminal tab whose asset is known → active", () => {
    const a = asset(7, "ssh");
    const res = resolveSnippetTarget({
      snippet: snippet("shell"),
      activeTab: terminalTab(7),
      assetsById: new Map([[7, a]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "active", asset: a });
  });

  it("sql snippet + active database query tab whose asset is known → active", () => {
    const a = asset(9, "database");
    const res = resolveSnippetTarget({
      snippet: snippet("sql"),
      activeTab: queryTab(9, "database"),
      assetsById: new Map([[9, a]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "active", asset: a });
  });

  it("sql snippet + active redis query tab → pick (type mismatch)", () => {
    const res = resolveSnippetTarget({
      snippet: snippet("sql"),
      activeTab: queryTab(9, "redis"),
      assetsById: new Map([[9, asset(9, "redis")]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "pick" });
  });

  it("shell snippet + matching terminal tab but asset missing from store → pick", () => {
    const res = resolveSnippetTarget({
      snippet: snippet("shell"),
      activeTab: terminalTab(7),
      assetsById: new Map(),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "pick" });
  });

  it("no active tab → pick", () => {
    const res = resolveSnippetTarget({
      snippet: snippet("shell"),
      activeTab: undefined,
      assetsById: new Map([[7, asset(7, "ssh")]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "pick" });
  });

  it("active tab of an unrelated kind (ai) → pick", () => {
    const res = resolveSnippetTarget({
      snippet: snippet("shell"),
      activeTab: aiTab(),
      assetsById: new Map([[7, asset(7, "ssh")]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "pick" });
  });

  it("snippet whose category has no asset type (prompt) → pick", () => {
    const res = resolveSnippetTarget({
      snippet: snippet("prompt"),
      activeTab: terminalTab(7),
      assetsById: new Map([[7, asset(7, "ssh")]]),
      categories: CATEGORIES,
    });
    expect(res).toEqual({ kind: "pick" });
  });
});
