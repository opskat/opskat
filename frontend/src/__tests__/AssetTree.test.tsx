import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { ConnectSSHAsync } from "../../wailsjs/go/ssh/SSH";
import { ListAssets, ListGroups } from "../../wailsjs/go/system/System";
import { AssetTree } from "@/components/layout/AssetTree";
import { useAssetStore } from "../stores/assetStore";
import { useTabStore } from "../stores/tabStore";
import { useTerminalStore } from "../stores/terminalStore";
import { useQueryStore } from "../stores/queryStore";
import { asset_entity, group_entity } from "../../wailsjs/go/models";
import { getAssetType } from "../lib/assetTypes";
import React from "react";

// ---- Minimal AssetList component that replicates the key double-click logic from AssetTree ----
// AssetTree itself has heavy dependencies (icons, pinyin, scroll area, etc.).
// We extract the core interaction logic into a thin test-only component so we can
// verify the click → store → tab flow without mocking every UI dependency.

function AssetList({
  assets,
  onConnectAsset,
  onSelectAsset,
}: {
  assets: asset_entity.Asset[];
  onConnectAsset: (asset: asset_entity.Asset) => void;
  onSelectAsset: (asset: asset_entity.Asset) => void;
}) {
  const clickTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectingAssetIds = useTerminalStore((s) => s.connectingAssetIds);

  return (
    <div>
      {assets.map((asset) => {
        const isConnecting = connectingAssetIds.has(asset.ID);
        return (
          <div
            key={asset.ID}
            data-testid={`asset-${asset.ID}`}
            onClick={() => {
              if (clickTimerRef.current) clearTimeout(clickTimerRef.current);
              clickTimerRef.current = setTimeout(() => {
                clickTimerRef.current = null;
                onSelectAsset(asset);
              }, 200);
            }}
            onDoubleClick={() => {
              if (clickTimerRef.current) {
                clearTimeout(clickTimerRef.current);
                clickTimerRef.current = null;
              }
              const def = getAssetType(asset.Type);
              if (def?.canConnect && (def.connectAction === "query" || !isConnecting)) {
                onConnectAsset(asset);
              } else {
                onSelectAsset(asset);
              }
            }}
          >
            {asset.Name}
          </div>
        );
      })}
    </div>
  );
}

// ---- Replicate App.tsx handleConnectAsset ----

function makeHandleConnectAsset() {
  const errors: string[] = [];
  const handleConnectAsset = async (asset: asset_entity.Asset) => {
    if (asset.Type === "database" || asset.Type === "redis" || asset.Type === "mongodb") {
      useQueryStore.getState().openQueryTab(asset);
      return;
    }
    if (asset.Type !== "ssh") return;
    try {
      await useTerminalStore.getState().connect(asset);
    } catch (e) {
      errors.push(String(e));
    }
  };
  return { handleConnectAsset, errors };
}

function makeHandleConnectAssetInNewTab() {
  const errors: string[] = [];
  const handleConnectAssetInNewTab = async (asset: asset_entity.Asset) => {
    if (asset.Type !== "ssh") return;
    try {
      await useTerminalStore.getState().connect(asset, "", true);
    } catch (e) {
      errors.push(String(e));
    }
  };
  return { handleConnectAssetInNewTab, errors };
}

// ---- Helpers ----

function makeAsset(id: number, type: string, name?: string): asset_entity.Asset {
  return {
    ID: id,
    Name: name || `Asset ${id}`,
    Type: type,
    GroupID: 0,
    Icon: "",
    Tags: "",
    Description: "",
    Config: JSON.stringify(
      type === "ssh"
        ? { host: "10.0.0.1", port: 22, username: "root" }
        : type === "database"
          ? { driver: "mysql", database: "testdb" }
          : { host: "10.0.0.1", port: 6379 }
    ),
    CmdPolicy: "",
    SortOrder: 0,
    Status: 1,
    Createtime: 0,
    Updatetime: 0,
  } as asset_entity.Asset;
}

describe("AssetTree double-click → connection flow", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set() });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    useAssetStore.setState({ assets: [], groups: [] as group_entity.Group[] });
    vi.spyOn(useAssetStore.getState(), "getAssetPath").mockImplementation((a) => a.Name);
    vi.mocked(ConnectSSHAsync).mockReset();
  });

  it("double-click SSH asset opens terminal tab", async () => {
    vi.mocked(ConnectSSHAsync).mockResolvedValue("conn-1");
    const sshAsset = makeAsset(1, "ssh", "Web Server");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    render(<AssetList assets={[sshAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-1"));
    // Wait for async connect
    await vi.waitFor(() => {
      expect(useTabStore.getState().tabs).toHaveLength(1);
    });

    const tab = useTabStore.getState().tabs[0];
    expect(tab.type).toBe("terminal");
    expect(tab.id).toBe("conn-1");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("double-click SSH asset with existing tab activates it without new connection", async () => {
    // Pre-populate an existing connected terminal tab
    useTabStore.setState({
      tabs: [
        {
          id: "session-abc",
          type: "terminal",
          label: "Web Server",
          meta: {
            type: "terminal",
            assetId: 1,
            assetName: "Web Server",
            assetIcon: "",
            host: "10.0.0.1",
            port: 22,
            username: "root",
          },
        },
      ],
      activeTabId: null,
    });

    const sshAsset = makeAsset(1, "ssh", "Web Server");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    render(<AssetList assets={[sshAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-1"));
    await vi.waitFor(() => {
      expect(useTabStore.getState().activeTabId).toBe("session-abc");
    });

    // No new connection, no new tab
    expect(ConnectSSHAsync).not.toHaveBeenCalled();
    expect(useTabStore.getState().tabs).toHaveLength(1);
  });

  it("double-click database asset opens query tab", async () => {
    const dbAsset = makeAsset(2, "database", "MySQL Prod");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    render(<AssetList assets={[dbAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-2"));

    const tabs = useTabStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].id).toBe("query-2");
    expect(tabs[0].type).toBe("query");
    expect(useQueryStore.getState().dbStates["query-2"]).toBeDefined();
  });

  it("double-click database asset with existing tab activates it", async () => {
    const dbAsset = makeAsset(2, "database", "MySQL Prod");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    // Open once
    useQueryStore.getState().openQueryTab(dbAsset);
    expect(useTabStore.getState().tabs).toHaveLength(1);

    // Switch to another tab
    useTabStore.setState({ activeTabId: null });

    const user = userEvent.setup();
    render(<AssetList assets={[dbAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-2"));

    // Should reuse, not create new
    expect(useTabStore.getState().tabs).toHaveLength(1);
    expect(useTabStore.getState().activeTabId).toBe("query-2");
  });

  it("double-click redis asset opens query tab", async () => {
    const redisAsset = makeAsset(3, "redis", "Cache");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    render(<AssetList assets={[redisAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-3"));

    const tabs = useTabStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].id).toBe("query-3");
    expect(useQueryStore.getState().redisStates["query-3"]).toBeDefined();
  });

  it("double-clicking a mongodb asset opens query tab", async () => {
    const mongoAsset = new asset_entity.Asset({
      ID: 30,
      Name: "MongoDB",
      Type: "mongodb",
      Config: JSON.stringify({ host: "localhost", port: 27017 }),
      Status: 1,
    });
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();
    const user = userEvent.setup();

    render(<AssetList assets={[mongoAsset]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);
    await user.dblClick(screen.getByTestId("asset-30"));

    const tabStore = useTabStore.getState();
    expect(tabStore.tabs.some((t) => t.id === "query-30")).toBe(true);
  });

  it("double-click unknown type asset triggers select, not connect", async () => {
    const otherAsset = makeAsset(4, "other", "Unknown");
    const onConnect = vi.fn();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    render(<AssetList assets={[otherAsset]} onConnectAsset={onConnect} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-4"));
    // Double-click on unknown type should call onSelectAsset, not onConnectAsset
    expect(onConnect).not.toHaveBeenCalled();
    expect(onSelect).toHaveBeenCalledWith(otherAsset);
  });

  it("multiple different assets can each open their own tab", async () => {
    vi.mocked(ConnectSSHAsync).mockResolvedValueOnce("conn-1").mockResolvedValueOnce("conn-2");
    const ssh1 = makeAsset(1, "ssh", "Server 1");
    const ssh2 = makeAsset(2, "ssh", "Server 2");
    const db = makeAsset(3, "database", "MySQL");
    const { handleConnectAsset } = makeHandleConnectAsset();
    const onSelect = vi.fn();

    const user = userEvent.setup();
    const { rerender } = render(
      <AssetList assets={[ssh1, ssh2, db]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />
    );

    await user.dblClick(screen.getByTestId("asset-1"));
    await vi.waitFor(() => expect(useTabStore.getState().tabs).toHaveLength(1));

    // Need to rerender because connectingAssetIds changed
    rerender(<AssetList assets={[ssh1, ssh2, db]} onConnectAsset={handleConnectAsset} onSelectAsset={onSelect} />);

    await user.dblClick(screen.getByTestId("asset-2"));
    await vi.waitFor(() => expect(useTabStore.getState().tabs).toHaveLength(2));

    await user.dblClick(screen.getByTestId("asset-3"));
    expect(useTabStore.getState().tabs).toHaveLength(3);

    const types = useTabStore.getState().tabs.map((t) => t.type);
    expect(types).toEqual(["terminal", "terminal", "query"]);
  });

  it("connect in new tab creates new terminal even when existing tab exists", async () => {
    // Pre-populate an existing connected terminal tab
    useTabStore.setState({
      tabs: [
        {
          id: "session-abc",
          type: "terminal",
          label: "Web Server",
          meta: {
            type: "terminal",
            assetId: 1,
            assetName: "Web Server",
            assetIcon: "",
            host: "10.0.0.1",
            port: 22,
            username: "root",
          },
        },
      ],
      activeTabId: "session-abc",
    });
    useTerminalStore.setState({
      tabData: {
        "session-abc": {
          splitTree: { type: "terminal", sessionId: "session-abc" },
          activePaneId: "session-abc",
          panes: {
            "session-abc": { sessionId: "session-abc", transport: "ssh", connected: true, connectedAt: Date.now() },
          },
          directoryFollowMode: "off",
        },
      },
    });

    vi.mocked(ConnectSSHAsync).mockResolvedValue("conn-new");

    const sshAsset = makeAsset(1, "ssh", "Web Server");
    const { handleConnectAssetInNewTab } = makeHandleConnectAssetInNewTab();

    await handleConnectAssetInNewTab(sshAsset);

    await vi.waitFor(() => {
      expect(useTabStore.getState().tabs).toHaveLength(2);
    });

    expect(ConnectSSHAsync).toHaveBeenCalledTimes(1);
    const tabIds = useTabStore.getState().tabs.map((t) => t.id);
    expect(tabIds).toContain("session-abc");
    expect(tabIds).toContain("conn-new");
  });

  it("connect in new tab ignores non-SSH assets", async () => {
    const dbAsset = makeAsset(2, "database", "MySQL");
    const { handleConnectAssetInNewTab } = makeHandleConnectAssetInNewTab();

    await handleConnectAssetInNewTab(dbAsset);

    expect(ConnectSSHAsync).not.toHaveBeenCalled();
    expect(useTabStore.getState().tabs).toHaveLength(0);
  });
});

describe("AssetTree ungrouped virtual folder", () => {
  const renderAssetTree = () =>
    render(
      <TooltipProvider>
        <AssetTree
          collapsed={false}
          onAddAsset={() => {}}
          onAddGroup={() => {}}
          onEditGroup={() => {}}
          onGroupDetail={() => {}}
          onEditAsset={() => {}}
          onCopyAsset={() => {}}
          onConnectAsset={() => {}}
          onSelectAsset={() => {}}
        />
      </TooltipProvider>
    );

  beforeEach(() => {
    localStorage.clear();
    useAssetStore.setState({
      assets: [],
      groups: [],
      selectedAssetId: null,
      selectedGroupId: null,
      collapsedGroupIds: [0],
      loading: false,
      initialized: false,
    });
    useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set() });
    useTabStore.setState({ tabs: [], activeTabId: null });
    vi.mocked(ListAssets).mockResolvedValue([makeAsset(1, "ssh", "Ungrouped Server")]);
    vi.mocked(ListGroups).mockResolvedValue([]);
  });

  it("preserves the persisted virtual-folder collapse on initial load", async () => {
    const user = userEvent.setup();
    renderAssetTree();

    expect(await screen.findByText("asset.ungrouped")).toBeTruthy();
    expect(screen.queryByText("Ungrouped Server")).toBeNull();

    await user.click(screen.getByText("asset.ungrouped"));

    expect(screen.getByText("Ungrouped Server")).toBeTruthy();
  });
});

describe("AssetTree double-click on a connecting asset (#380)", () => {
  beforeEach(() => {
    localStorage.clear();
    useAssetStore.setState({
      assets: [],
      groups: [],
      selectedAssetId: null,
      selectedGroupId: null,
      collapsedGroupIds: [],
      loading: false,
      initialized: false,
    });
    useTabStore.setState({ tabs: [], activeTabId: null });
    vi.mocked(ListAssets).mockResolvedValue([makeAsset(5, "ssh", "Busy Server")]);
    vi.mocked(ListGroups).mockResolvedValue([]);
  });

  it("still asks to connect so the existing connecting tab gets focused", async () => {
    useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set([5]) });
    const onConnect = vi.fn();
    const user = userEvent.setup();
    render(
      <TooltipProvider>
        <AssetTree
          collapsed={false}
          onAddAsset={() => {}}
          onAddGroup={() => {}}
          onEditGroup={() => {}}
          onGroupDetail={() => {}}
          onEditAsset={() => {}}
          onCopyAsset={() => {}}
          onConnectAsset={onConnect}
          onSelectAsset={() => {}}
        />
      </TooltipProvider>
    );

    await user.dblClick(await screen.findByText("Busy Server"));

    expect(onConnect).toHaveBeenCalledTimes(1);
    expect(onConnect.mock.calls[0][0].ID).toBe(5);
  });
});

describe("AssetTree load states", () => {
  const renderTree = () =>
    render(
      <TooltipProvider>
        <AssetTree
          collapsed={false}
          onAddAsset={() => {}}
          onAddGroup={() => {}}
          onEditGroup={() => {}}
          onGroupDetail={() => {}}
          onEditAsset={() => {}}
          onCopyAsset={() => {}}
          onConnectAsset={() => {}}
          onSelectAsset={() => {}}
        />
      </TooltipProvider>
    );

  beforeEach(() => {
    localStorage.clear();
    useAssetStore.setState({
      assets: [],
      groups: [],
      selectedAssetId: null,
      selectedGroupId: null,
      collapsedGroupIds: [],
      loading: false,
      initialized: false,
      loadErrors: { assets: null, groups: null },
    });
  });

  it("shows a local loading state, not the empty state, before the first load returns", async () => {
    vi.mocked(ListAssets).mockReturnValue(new Promise(() => {}));
    vi.mocked(ListGroups).mockReturnValue(new Promise(() => {}));
    renderTree();
    expect(await screen.findByTestId("async-state-loading")).toBeTruthy();
    expect(screen.queryByText("asset.emptyTitle")).toBeNull();
  });

  it("shows the failure reason with retry instead of the empty state, and retry recovers", async () => {
    const user = userEvent.setup();
    vi.mocked(ListAssets).mockRejectedValueOnce(new Error("db locked"));
    vi.mocked(ListGroups).mockResolvedValue([]);
    renderTree();
    expect(await screen.findByText(/db locked/)).toBeTruthy();
    expect(screen.queryByText("asset.emptyTitle")).toBeNull();

    vi.mocked(ListAssets).mockResolvedValue([makeAsset(1, "ssh", "Recovered")]);
    await user.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("Recovered")).toBeTruthy();
  });

  it("shows the empty state after a successful empty load", async () => {
    vi.mocked(ListAssets).mockResolvedValue([]);
    vi.mocked(ListGroups).mockResolvedValue([]);
    renderTree();
    expect(await screen.findByText("asset.emptyTitle")).toBeTruthy();
  });
});

describe("AssetTree session status indicator alignment", () => {
  const renderTree = () =>
    render(
      <TooltipProvider>
        <AssetTree
          collapsed={false}
          onAddAsset={() => {}}
          onAddGroup={() => {}}
          onEditGroup={() => {}}
          onGroupDetail={() => {}}
          onEditAsset={() => {}}
          onCopyAsset={() => {}}
          onConnectAsset={() => {}}
          onSelectAsset={() => {}}
        />
      </TooltipProvider>
    );

  const makeGroupedAsset = (id: number, name: string, groupId: number) =>
    new asset_entity.Asset({
      ID: id,
      Name: name,
      Type: "ssh",
      GroupID: groupId,
      Icon: "",
      Status: 1,
      SortOrder: id,
    });

  const makeGroup = (id: number, name: string, parentId = 0) =>
    new group_entity.Group({
      ID: id,
      Name: name,
      ParentID: parentId,
      Icon: "",
      SortOrder: id,
      Status: 1,
    });

  const getRowInner = (name: string) => {
    const label = screen.getByText(name);
    const rowInner = label.parentElement as HTMLElement;
    expect(rowInner).toBeTruthy();
    return { label, rowInner };
  };

  beforeEach(() => {
    localStorage.clear();
    useTabStore.setState({ tabs: [], activeTabId: null });
    useTerminalStore.setState({ tabData: {}, connections: {}, connectingAssetIds: new Set() });
    useQueryStore.setState({ dbStates: {}, redisStates: {}, mongoStates: {} });
    useAssetStore.setState({
      assets: [],
      groups: [],
      selectedAssetId: null,
      selectedGroupId: null,
      collapsedGroupIds: [],
      loading: false,
      initialized: false,
      loadErrors: { assets: null, groups: null },
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("places the active session indicator to the left of the icon without shifting sibling rows across depths", async () => {
    const rootGroup = makeGroup(1, "hanna", 0);
    const childGroup = makeGroup(2, "wh-local", 1);
    const nestedIdle = makeGroupedAsset(11, "LAN-Dev-113", 2);
    const nestedActive = makeGroupedAsset(12, "LAN-Dev-85", 2);
    const ungroupedActive = makeGroupedAsset(13, "Root-Active", 0);
    const ungroupedIdle = makeGroupedAsset(14, "Root-Idle", 0);

    vi.mocked(ListGroups).mockResolvedValue([rootGroup, childGroup]);
    vi.mocked(ListAssets).mockResolvedValue([nestedIdle, nestedActive, ungroupedActive, ungroupedIdle]);

    useTabStore.setState({
      tabs: [
        {
          id: "sess-12",
          type: "terminal",
          label: "LAN-Dev-85",
          meta: {
            type: "terminal",
            assetId: 12,
            assetName: "LAN-Dev-85",
            assetIcon: "",
            host: "10.0.0.12",
            port: 22,
            username: "root",
          },
        },
        {
          id: "sess-13",
          type: "terminal",
          label: "Root-Active",
          meta: {
            type: "terminal",
            assetId: 13,
            assetName: "Root-Active",
            assetIcon: "",
            host: "10.0.0.13",
            port: 22,
            username: "root",
          },
        },
      ],
      activeTabId: "sess-12",
    });
    useTerminalStore.setState({
      tabData: {
        "sess-12": {
          splitTree: { type: "terminal", sessionId: "sess-12" },
          activePaneId: "sess-12",
          panes: { "sess-12": { sessionId: "sess-12", transport: "ssh", connected: true, connectedAt: Date.now() } },
          directoryFollowMode: "off",
        },
        "sess-13": {
          splitTree: { type: "terminal", sessionId: "sess-13" },
          activePaneId: "sess-13",
          panes: { "sess-13": { sessionId: "sess-13", transport: "ssh", connected: true, connectedAt: Date.now() } },
          directoryFollowMode: "off",
        },
      },
      connections: {},
      connectingAssetIds: new Set(),
    });

    renderTree();

    expect(await screen.findByText("LAN-Dev-85")).toBeTruthy();

    // Nested depth = 2 siblings: active and inactive keep identical 3-slot structure and left offset
    // (paddingLeft 32px + 6px status slot + 6px gap = 44px icon X, unchanged from 20 + 2 * 12).
    const idleNested = getRowInner("LAN-Dev-113");
    const activeNested = getRowInner("LAN-Dev-85");

    expect(idleNested.rowInner.style.paddingLeft).toBe("32px");
    expect(activeNested.rowInner.style.paddingLeft).toBe("32px");
    expect(idleNested.rowInner.children).toHaveLength(3);
    expect(activeNested.rowInner.children).toHaveLength(3);

    const [idleSlot, idleIcon, idleName] = Array.from(idleNested.rowInner.children) as HTMLElement[];
    const [activeSlot, activeIcon, activeName] = Array.from(activeNested.rowInner.children) as HTMLElement[];

    // Nothing sits between icon and asset name on either row.
    expect(idleIcon.nextElementSibling).toBe(idleName);
    expect(activeIcon.nextElementSibling).toBe(activeName);
    expect(idleIcon.tagName.toLowerCase()).toBe("svg");
    expect(activeIcon.tagName.toLowerCase()).toBe("svg");

    // Leading slot reserves identical width for both siblings; only the active row renders the status dot inside it.
    expect(idleSlot.className).toContain("w-1.5");
    expect(idleSlot.className).toContain("shrink-0");
    expect(idleSlot.querySelector(".bg-success")).toBeNull();

    expect(activeSlot.className).toContain("w-1.5");
    expect(activeSlot.className).toContain("shrink-0");
    const nestedDot = activeSlot.querySelector(".bg-success") as HTMLElement | null;
    expect(nestedDot).not.toBeNull();
    expect(nestedDot?.getAttribute("title")).toBe("ssh.session.connected");
    expect(nestedDot?.getAttribute("aria-label")).toBe("ssh.session.connected");

    // Ungrouped depth = 1 siblings: paddingLeft 20px (+ 6px slot + 6px gap = 32px icon X, unchanged from 20 + 1 * 12).
    const activeUngrouped = getRowInner("Root-Active");
    const idleUngrouped = getRowInner("Root-Idle");
    expect(activeUngrouped.rowInner.style.paddingLeft).toBe("20px");
    expect(idleUngrouped.rowInner.style.paddingLeft).toBe("20px");
    expect(activeUngrouped.rowInner.children).toHaveLength(3);
    expect(idleUngrouped.rowInner.children).toHaveLength(3);
    expect(activeUngrouped.rowInner.children[0].querySelector(".bg-success")).not.toBeNull();
    expect(idleUngrouped.rowInner.children[0].querySelector(".bg-success")).toBeNull();
    expect(activeUngrouped.rowInner.children[1].nextElementSibling).toBe(activeUngrouped.label);
  });

  it("keeps the left status slot aligned while connecting and during search filtering", async () => {
    const user = userEvent.setup();
    const rootGroup = makeGroup(1, "prod", 0);
    const connectingOnly = makeGroupedAsset(21, "Web-Connecting", 1);
    const activeAndConnecting = makeGroupedAsset(22, "Web-Active-Connecting", 1);

    vi.mocked(ListGroups).mockResolvedValue([rootGroup]);
    vi.mocked(ListAssets).mockResolvedValue([connectingOnly, activeAndConnecting]);

    useTabStore.setState({
      tabs: [
        {
          id: "sess-22",
          type: "terminal",
          label: "Web-Active-Connecting",
          meta: {
            type: "terminal",
            assetId: 22,
            assetName: "Web-Active-Connecting",
            assetIcon: "",
            host: "10.0.0.22",
            port: 22,
            username: "root",
          },
        },
      ],
      activeTabId: "sess-22",
    });
    useTerminalStore.setState({
      tabData: {
        "sess-22": {
          splitTree: { type: "terminal", sessionId: "sess-22" },
          activePaneId: "sess-22",
          panes: { "sess-22": { sessionId: "sess-22", transport: "ssh", connected: true, connectedAt: Date.now() } },
          directoryFollowMode: "off",
        },
      },
      connections: {},
      connectingAssetIds: new Set([21, 22]),
    });

    renderTree();
    expect(await screen.findByText("Web-Active-Connecting")).toBeTruthy();

    const connOnlyRow = getRowInner("Web-Connecting");
    const activeConnRow = getRowInner("Web-Active-Connecting");

    expect(connOnlyRow.rowInner.style.paddingLeft).toBe("20px");
    expect(activeConnRow.rowInner.style.paddingLeft).toBe("20px");
    expect(connOnlyRow.rowInner.children).toHaveLength(3);
    expect(activeConnRow.rowInner.children).toHaveLength(3);

    // Spinner stays in the icon slot (index 1); status dot stays in the leading slot (index 0).
    expect(connOnlyRow.rowInner.children[0].querySelector(".bg-success")).toBeNull();
    expect(connOnlyRow.rowInner.children[1].getAttribute("class")).toContain("animate-spin");
    expect(activeConnRow.rowInner.children[0].querySelector(".bg-success")).not.toBeNull();
    expect(activeConnRow.rowInner.children[1].getAttribute("class")).toContain("animate-spin");
    expect(activeConnRow.rowInner.children[1].nextElementSibling).toBe(activeConnRow.label);

    // Search filtering preserves the same left status slot and row padding.
    await user.type(screen.getByTestId("asset-tree-search"), "Active");
    const filteredRow = getRowInner("Web-Active-Connecting");
    expect(filteredRow.rowInner.style.paddingLeft).toBe("20px");
    expect(filteredRow.rowInner.children[0].querySelector(".bg-success")).not.toBeNull();
    expect(filteredRow.rowInner.children[1].nextElementSibling).toBe(filteredRow.label);
  });
});
