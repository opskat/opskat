import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { AssetTree } from "@/components/layout/AssetTree";
import { useAssetStore } from "@/stores/assetStore";
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
  onOpenFileManager,
}: {
  onAddAsset?: (groupId?: number) => void;
  onOpenFileManager?: (asset: asset_entity.Asset) => void;
} = {}) {
  return render(
    <TooltipProvider>
      <AssetTree
        collapsed={false}
        onAddAsset={onAddAsset}
        onAddGroup={vi.fn()}
        onEditGroup={vi.fn()}
        onGroupDetail={vi.fn()}
        onEditAsset={vi.fn()}
        onCopyAsset={vi.fn()}
        onConnectAsset={vi.fn()}
        onSelectAsset={vi.fn()}
        onOpenFileManager={onOpenFileManager}
      />
    </TooltipProvider>
  );
}

describe("AssetTree context menu", () => {
  beforeEach(() => {
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

  it("does not render group detail or open in tab in group context menu", async () => {
    renderTree();

    fireEvent.contextMenu(screen.getByText("Folder A"));
    const menu = await screen.findByRole("menu");

    expect(menu).toHaveTextContent("asset.editGroupSettings");
    expect(menu).not.toHaveTextContent("asset.renameGroup");
    expect(menu).not.toHaveTextContent("asset.groupDetail");
    expect(menu).not.toHaveTextContent("action.openInTab");
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
});
