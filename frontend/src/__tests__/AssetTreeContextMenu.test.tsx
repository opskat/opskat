import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { AssetTree } from "@/components/layout/AssetTree";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { ListAssets, ListGroups } from "../../wailsjs/go/system/System";
import { asset_entity, group_entity } from "../../wailsjs/go/models";

function makeGroup(id: number, name: string): group_entity.Group {
  return new group_entity.Group({
    ID: id,
    Name: name,
    ParentID: 0,
    Icon: "",
    SortOrder: id,
    Status: 1,
  });
}

function makeAsset(id: number, name: string, groupId: number, type = "ssh"): asset_entity.Asset {
  return new asset_entity.Asset({
    ID: id,
    Name: name,
    Type: type,
    GroupID: groupId,
    Icon: "",
    Status: 1,
  });
}

function renderTree({
  onAddAsset = vi.fn(),
  onGroupDetail = vi.fn(),
  onConnectAsset = vi.fn(),
  onReconnectAsset,
  onOpenFileManager,
}: {
  onAddAsset?: (groupId?: number) => void;
  onGroupDetail?: (group: group_entity.Group) => void;
  onConnectAsset?: (asset: asset_entity.Asset) => void;
  onReconnectAsset?: (asset: asset_entity.Asset) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
} = {}) {
  return render(
    <TooltipProvider>
      <AssetTree
        collapsed={false}
        onAddAsset={onAddAsset}
        onAddGroup={vi.fn()}
        onEditGroup={vi.fn()}
        onGroupDetail={onGroupDetail}
        onEditAsset={vi.fn()}
        onCopyAsset={vi.fn()}
        onConnectAsset={onConnectAsset}
        onReconnectAsset={onReconnectAsset}
        onSelectAsset={vi.fn()}
        onOpenFileManager={onOpenFileManager}
      />
    </TooltipProvider>
  );
}

describe("AssetTree context menu", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useTerminalStore.setState({ connectingAssetIds: new Set() });
    useAssetStore.setState({
      assets: [makeAsset(101, "Asset A", 1), makeAsset(102, "Asset B", 2)],
      groups: [makeGroup(1, "Folder A"), makeGroup(2, "Folder B")],
      selectedAssetId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
  });

  it("opens the group context menu for the second folder", async () => {
    renderTree();

    fireEvent.contextMenu(screen.getByText("Folder B"));

    expect(await screen.findByText("asset.editGroupSettings")).toBeInTheDocument();
    expect(screen.queryByText("asset.renameGroup")).not.toBeInTheDocument();
    expect(screen.getAllByRole("menu")).toHaveLength(1);
  });

  it("updates the group context menu target when switching folders", async () => {
    const onAddAsset = vi.fn();
    renderTree({ onAddAsset });

    fireEvent.contextMenu(screen.getByText("Folder A"));
    fireEvent.contextMenu(screen.getByText("Folder B"));
    fireEvent.click(within(await screen.findByRole("menu")).getByText("asset.addAsset"));

    expect(onAddAsset).toHaveBeenCalledTimes(1);
    expect(onAddAsset).toHaveBeenLastCalledWith(2);
  });

  it("opens a context menu for the ungrouped bucket", async () => {
    useAssetStore.setState({
      assets: [makeAsset(101, "Asset A", 1), makeAsset(102, "Root Asset", 0)],
      groups: [makeGroup(1, "Folder A")],
      selectedAssetId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
    const onAddAsset = vi.fn();
    renderTree({ onAddAsset });

    fireEvent.contextMenu(screen.getByText("asset.ungrouped"));
    fireEvent.click(within(await screen.findByRole("menu")).getByText("asset.addAsset"));

    expect(onAddAsset).toHaveBeenCalledTimes(1);
    expect(onAddAsset).toHaveBeenLastCalledWith(0);
  });

  it("shows disabled group actions for the ungrouped bucket", async () => {
    useAssetStore.setState({
      assets: [makeAsset(101, "Asset A", 1), makeAsset(102, "Root Asset", 0)],
      groups: [makeGroup(1, "Folder A")],
      selectedAssetId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
    renderTree();

    fireEvent.contextMenu(screen.getByText("asset.ungrouped"));
    const menu = await screen.findByRole("menu");

    expect(menu).toHaveTextContent("asset.editGroupSettings");
    expect(within(menu).getByText("asset.editGroupSettings").closest('[role="menuitem"]')).toHaveAttribute(
      "data-disabled",
      "true"
    );
    expect(within(menu).getByText("asset.moveDown").closest('[role="menuitem"]')).toHaveAttribute(
      "data-disabled",
      "true"
    );
  });

  it("uses the full asset row as the drag handle", () => {
    renderTree();

    const assetRow = screen.getByText("Asset A").closest("[data-asset-tree-row]");

    expect(assetRow).toHaveAttribute("role", "button");
  });

  it("does not render rename or open in tab in group context menu", async () => {
    renderTree();

    fireEvent.contextMenu(screen.getByText("Folder A"));
    const menu = await screen.findByRole("menu");

    expect(menu).toHaveTextContent("asset.editGroupSettings");
    expect(menu).not.toHaveTextContent("asset.renameGroup");
    expect(menu).not.toHaveTextContent("action.openInTab");
  });

  // 分组详情页放着分组的命令权限和权限模式，右键菜单是它唯一的入口。
  it("opens group details from the group context menu", async () => {
    const onGroupDetail = vi.fn();
    renderTree({ onGroupDetail });

    fireEvent.contextMenu(screen.getByText("Folder B"));
    fireEvent.click(within(await screen.findByRole("menu")).getByText("asset.groupDetailTitle"));

    expect(onGroupDetail).toHaveBeenCalledTimes(1);
    expect(onGroupDetail.mock.calls[0][0]).toMatchObject({ ID: 2, Name: "Folder B" });
  });

  it("disables group details for the ungrouped bucket", async () => {
    useAssetStore.setState({ assets: [makeAsset(102, "Root Asset", 0)], groups: [] });
    renderTree();

    fireEvent.contextMenu(screen.getByText("asset.ungrouped"));
    const item = within(await screen.findByRole("menu")).getByText("asset.groupDetailTitle");
    expect(item.closest('[role="menuitem"]')).toHaveAttribute("data-disabled", "true");
  });

  it("shows the file-manager action for ssh assets", async () => {
    renderTree({ onOpenFileManager: vi.fn() });

    fireEvent.contextMenu(screen.getByText("Asset A"));
    const menu = await screen.findByRole("menu");

    expect(menu).toHaveTextContent("sftp.fileManager");
  });

  it("hides the file-manager action for non-ssh assets (registry capability, not type-string)", async () => {
    useAssetStore.setState({
      assets: [makeAsset(201, "Redis Asset", 1, "redis")],
      groups: [makeGroup(1, "Folder A")],
      selectedAssetId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
    renderTree({ onOpenFileManager: vi.fn() });

    fireEvent.contextMenu(screen.getByText("Redis Asset"));
    const menu = await screen.findByRole("menu");

    expect(menu).not.toHaveTextContent("sftp.fileManager");
  });

  it("shows a copy-reference action next to clone", async () => {
    renderTree();

    fireEvent.contextMenu(screen.getByText("Asset A"));
    const menu = await screen.findByRole("menu");

    expect(menu).toHaveTextContent("asset.copyRef");
    expect(menu).toHaveTextContent("action.copy");
  });

  it("keeps Connect enabled while connecting and shows Reconnect only when an open tab exists", async () => {
    const assets = [makeAsset(101, "Asset A", 1), makeAsset(102, "Asset B", 2)];
    const groups = [makeGroup(1, "Folder A"), makeGroup(2, "Folder B")];
    vi.mocked(ListAssets).mockResolvedValueOnce(assets);
    vi.mocked(ListGroups).mockResolvedValueOnce(groups);
    useAssetStore.setState({ assets, groups });

    const onConnectAsset = vi.fn();
    const onReconnectAsset = vi.fn();
    useTerminalStore.setState({ connectingAssetIds: new Set([101]) });
    renderTree({ onConnectAsset, onReconnectAsset });

    // Without an open tab, Reconnect is hidden; Connect remains clickable even while connecting (#380).
    fireEvent.contextMenu(screen.getByText("Asset A"));
    let menu = await screen.findByRole("menu");
    expect(within(menu).queryByTestId("asset-context-reconnect")).not.toBeInTheDocument();
    const connectItem = within(menu).getByText("asset.connect").closest('[role="menuitem"]')!;
    expect(connectItem).not.toHaveAttribute("data-disabled", "true");
    fireEvent.click(connectItem);
    expect(onConnectAsset).toHaveBeenCalledTimes(1);

    // Once a tab is open for the asset, Reconnect appears and triggers onReconnectAsset even while connecting.
    useTabStore.setState({
      tabs: [
        {
          id: "conn-101",
          type: "terminal",
          label: "Asset A",
          meta: {
            type: "terminal",
            assetId: 101,
            assetName: "Asset A",
            assetIcon: "",
            host: "h",
            port: 22,
            username: "root",
          },
        },
      ],
      activeTabId: "conn-101",
    });

    fireEvent.contextMenu(screen.getByText("Asset A"));
    menu = await screen.findByRole("menu");
    const reconnectItem = within(menu).getByTestId("asset-context-reconnect");
    expect(reconnectItem).not.toHaveAttribute("data-disabled", "true");
    fireEvent.click(reconnectItem);
    expect(onReconnectAsset).toHaveBeenCalledTimes(1);
    expect(onReconnectAsset.mock.calls[0][0]).toMatchObject({ ID: 101 });
  });
});
