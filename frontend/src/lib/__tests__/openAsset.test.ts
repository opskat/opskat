import { describe, it, expect, beforeEach, vi } from "vitest";
import type { asset_entity } from "../../../wailsjs/go/models";

const openQueryTab = vi.fn();
const connect = vi.fn().mockResolvedValue("conn-1");
const openTab = vi.fn();
const activateTab = vi.fn();

vi.mock("@/stores/queryStore", () => ({ useQueryStore: { getState: () => ({ openQueryTab }) } }));
vi.mock("@/stores/terminalStore", () => ({ useTerminalStore: { getState: () => ({ connect }) } }));
vi.mock("@/stores/tabStore", () => ({ useTabStore: { getState: () => ({ tabs: [], openTab, activateTab }) } }));
vi.mock("@/extension", () => ({
  useExtensionStore: { getState: () => ({ getExtensionForAssetType: () => undefined }) },
}));

import { openAssetConnection } from "../openAsset";

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
});
