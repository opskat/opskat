import { describe, it, expect, beforeEach, vi } from "vitest";
import type { asset_entity } from "../../../wailsjs/go/models";

const openQueryTab = vi.fn();
const connect = vi.fn().mockResolvedValue("conn-1");
const openTab = vi.fn();
const activateTab = vi.fn();

vi.mock("@/stores/queryStore", () => ({ useQueryStore: { getState: () => ({ openQueryTab }) } }));
vi.mock("@/stores/terminalStore", () => ({ useTerminalStore: { getState: () => ({ connect }) } }));
vi.mock("@/stores/tabStore", () => ({ useTabStore: { getState: () => ({ tabs: [], openTab, activateTab }) } }));
import { openAssetConnection } from "../openAsset";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";

function makeAsset(id: number, name: string, type: string): asset_entity.Asset {
  return {
    ID: id,
    Name: name,
    Type: type,
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: "",
    CmdPolicy: "",
    SortOrder: 0,
    sshTunnelId: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  };
}

describe("openAssetConnection", () => {
  beforeEach(() => vi.clearAllMocks());

  it("opens a query tab for a database asset", async () => {
    await openAssetConnection(makeAsset(1, "db", "database"));
    expect(openQueryTab).toHaveBeenCalledTimes(1);
  });

  it("connects a terminal for an ssh asset", async () => {
    await openAssetConnection(makeAsset(2, "web", "ssh"));
    expect(connect).toHaveBeenCalledTimes(1);
  });

  it("opens a page tab for an rdp asset", async () => {
    await openAssetConnection(makeAsset(3, "desktop", "rdp"));

    expect(openTab).toHaveBeenCalledWith({
      id: "rdp-3",
      type: "page",
      label: "desktop",
      icon: "monitor",
      meta: { type: "page", pageId: "rdp", assetId: 3 },
    });
    expect(connect).not.toHaveBeenCalled();
  });

  // k8s 的派发曾经是一个写死的 `asset.Type === "k8s"` 分支，而它的注册表定义里写着
  // connectAction: "terminal" —— 定义与行为对不上。现在只有注册表一条来源。
  it("opens the cluster page for a k8s asset, from the registry alone", async () => {
    await openAssetConnection(makeAsset(4, "prod", "k8s"));

    expect(openTab).toHaveBeenCalledWith({
      id: "k8s-4",
      type: "page",
      label: "prod",
      icon: "kubernetes",
      meta: { type: "page", pageId: "k8s-cluster", assetId: 4 },
    });
    expect(connect).not.toHaveBeenCalled();
  });

  it("opens an extension's connect page for its asset type", async () => {
    registerExtensionAssetTypes("acme", {
      name: "acme",
      version: "1.0.0",
      icon: "cloud",
      i18n: { displayName: "Acme", description: "" },
      assetTypes: [{ type: "acme-store", i18n: { name: "Acme" } }],
      frontend: {
        entry: "index.js",
        styles: "",
        pages: [{ id: "browser", slot: "asset.connect", i18n: { name: "Browser" }, component: "Browser" }],
      },
    } as ExtManifest);

    await openAssetConnection(makeAsset(5, "bucket", "acme-store"));

    expect(openTab).toHaveBeenCalledWith({
      id: "ext-acme-browser-5",
      type: "page",
      label: "bucket",
      icon: "cloud",
      meta: { type: "page", pageId: "browser", assetId: 5, extensionName: "acme" },
    });
    unregisterExtensionAssetTypes("acme");
  });

  it("does nothing for a type no extension provides (uninstalled extension asset)", async () => {
    await openAssetConnection(makeAsset(6, "orphan", "gone-type"));
    expect(openTab).not.toHaveBeenCalled();
    expect(connect).not.toHaveBeenCalled();
    expect(openQueryTab).not.toHaveBeenCalled();
  });
});
