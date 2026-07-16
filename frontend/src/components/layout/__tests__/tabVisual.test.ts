import { describe, expect, it } from "vitest";
import { Folder, MessageSquare, Server, Settings } from "lucide-react";
import type { Tab } from "@/stores/tabStore";
import { resolveTabVisual } from "../tabVisual";

function tab(overrides: Partial<Tab>): Tab {
  return {
    id: "tab",
    type: "terminal",
    label: "Tab",
    meta: { type: "terminal", assetId: 1, assetName: "Tab", assetIcon: "", host: "h", port: 22, username: "u" },
    ...overrides,
  } as Tab;
}

describe("resolveTabVisual", () => {
  it("uses stable fallbacks for AI, asset, and group tabs", () => {
    expect(resolveTabVisual(tab({ type: "ai", meta: { type: "ai", conversationId: null, title: "AI" } })).Icon).toBe(
      MessageSquare
    );
    expect(resolveTabVisual(tab({ type: "query", meta: { type: "query", assetId: 1 } as Tab["meta"] })).Icon).toBe(
      Server
    );
    expect(
      resolveTabVisual(tab({ type: "info", meta: { type: "info", targetType: "group", targetId: 1, name: "Group" } }))
        .Icon
    ).toBe(Folder);
  });

  it("uses builtin page icons and the server fallback for extension pages", () => {
    expect(resolveTabVisual(tab({ type: "page", meta: { type: "page", pageId: "settings" } })).Icon).toBe(Settings);
    expect(resolveTabVisual(tab({ type: "page", meta: { type: "page", pageId: "extension" } })).Icon).toBe(Server);
  });

  it("returns one canonical color for both the icon and active indicator", () => {
    const visual = resolveTabVisual(tab({ icon: "server#ff0000" }));
    expect(visual.iconStyle).toEqual({ color: "#ff0000" });
    expect(visual.indicatorColor).toBe("#ff0000");
  });
});
