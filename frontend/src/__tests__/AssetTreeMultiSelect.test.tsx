import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, within, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { TooltipProvider } from "@opskat/ui";
import { AssetTree } from "@/components/layout/AssetTree";
import { GroupDialog } from "@/components/asset/GroupDialog";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity, group_entity } from "../../wailsjs/go/models";
import { CreateGroup, DeleteAssets, ListAssets, ListGroups, ReorderAssets } from "../../wailsjs/go/system/System";

function makeGroup(id: number, name: string, parentId = 0, permissionMode = ""): group_entity.Group {
  return new group_entity.Group({
    ID: id,
    Name: name,
    ParentID: parentId,
    Icon: "",
    SortOrder: id * 10,
    permissionMode,
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
    SortOrder: id * 10,
  });
}

function renderTree(overrides: Partial<React.ComponentProps<typeof AssetTree>> = {}) {
  const props: React.ComponentProps<typeof AssetTree> = {
    collapsed: false,
    onAddAsset: vi.fn(),
    onAddGroup: vi.fn(),
    onEditGroup: vi.fn(),
    onGroupDetail: vi.fn(),
    onEditAsset: vi.fn(),
    onCopyAsset: vi.fn(),
    onConnectAsset: vi.fn(),
    onSelectAsset: (asset) => useAssetStore.getState().selectAsset(asset.ID),
    ...overrides,
  };
  const utils = render(
    <TooltipProvider>
      <AssetTree {...props} />
    </TooltipProvider>
  );
  return { ...utils, props };
}

describe("AssetTree multi-select, batch actions & directory sub-group creation", () => {
  const defaultGroups = [makeGroup(1, "Folder A"), makeGroup(2, "Folder B", 0, "autopilot")];
  const defaultAssets = [
    makeAsset(101, "web-1", 1),
    makeAsset(102, "web-2", 1),
    makeAsset(103, "db-1", 2, "database"),
    makeAsset(104, "notes-1", 2, "generic"),
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ListAssets).mockResolvedValue(defaultAssets);
    vi.mocked(ListGroups).mockResolvedValue(defaultGroups);
    useAssetStore.setState({
      assets: defaultAssets,
      groups: defaultGroups,
      selectedAssetId: null,
      selectedAssetIds: [],
      selectedAssetIdSet: new Set(),
      selectionAnchorId: null,
      selectedGroupId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
  });

  it("supports Click, Ctrl/Cmd+Click toggle, Shift+Click range selection, and Esc/blank clear", async () => {
    renderTree();

    fireEvent.click(screen.getByText("web-1"));
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101]);
    expect(screen.queryByTestId("asset-selection-bar")).not.toBeInTheDocument();

    // Shift+Click selects visible range across groups (skipping group headers)
    fireEvent.click(screen.getByText("db-1"), { shiftKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102, 103]);
    expect(screen.getByTestId("asset-selection-bar")).toBeInTheDocument();

    // Ctrl+Click toggles an item off
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 103]);

    // Esc clears selection when sidebar is focused
    const sidebar = screen.getByTestId("asset-tree");
    fireEvent.keyDown(sidebar, { key: "Escape" });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([]);
    expect(screen.queryByTestId("asset-selection-bar")).not.toBeInTheDocument();

    // Cmd+A selects all visible assets, clicking blank space clears selection
    fireEvent.keyDown(sidebar, { key: "a", ctrlKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102, 103, 104]);
    fireEvent.click(sidebar.querySelector('[data-slot="scroll-area"]') ?? sidebar.lastElementChild!);
    expect(useAssetStore.getState().selectedAssetIds).toEqual([]);
  });

  it("suppresses double-click connect when modifier key is held", () => {
    const onConnectAsset = vi.fn();
    renderTree({ onConnectAsset });

    fireEvent.doubleClick(screen.getByText("web-1"), { ctrlKey: true });
    fireEvent.doubleClick(screen.getByText("web-1"), { shiftKey: true });
    expect(onConnectAsset).not.toHaveBeenCalled();

    fireEvent.doubleClick(screen.getByText("web-1"));
    expect(onConnectAsset).toHaveBeenCalledTimes(1);
  });

  it("auto-prunes hidden selected assets when collapsing a group or filtering", async () => {
    renderTree();
    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("db-1"), { shiftKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102, 103]);

    // Collapsing Folder A hides web-1 & web-2 -> pruned from selection
    fireEvent.click(screen.getByText("Folder A"));
    await waitFor(() => {
      expect(useAssetStore.getState().selectedAssetIds).toEqual([103]);
    });

    // Expanding back & filtering by "web-1" prunes db-1
    fireEvent.click(screen.getByText("Folder A"));
    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("db-1"), { ctrlKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 103]);

    fireEvent.change(screen.getByTestId("asset-tree-search"), { target: { value: "web-1" } });
    await waitFor(() => {
      expect(useAssetStore.getState().selectedAssetIds).toEqual([101]);
    });
  });

  it("keeps multi-selection on right-click inside selection and resets when right-clicking outside", async () => {
    renderTree();
    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });

    // Right-click inside selection -> batch menu
    fireEvent.contextMenu(screen.getByText("web-2"));
    const batchMenu = await screen.findByRole("menu");
    expect(within(batchMenu).getByTestId("batch-context-connect")).toBeInTheDocument();
    expect(within(batchMenu).getByTestId("batch-context-move")).toBeInTheDocument();
    expect(within(batchMenu).getByTestId("batch-context-copy-ref")).toBeInTheDocument();
    expect(within(batchMenu).getByTestId("batch-context-delete")).toBeInTheDocument();
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102]);

    // Right-click outside selection -> resets to single selection & opens single-asset menu
    fireEvent.contextMenu(screen.getByText("db-1"));
    const singleMenu = await screen.findByRole("menu");
    expect(within(singleMenu).getByTestId("asset-context-edit")).toBeInTheDocument();
    expect(within(singleMenu).queryByTestId("batch-context-move")).not.toBeInTheDocument();
    expect(useAssetStore.getState().selectedAssetIds).toEqual([103]);
  });

  it("confirms batch connect only when > 5 connectable assets are selected (5 vs 6 & mixed generic)", async () => {
    const mixedAssets = [
      ...Array.from({ length: 6 }, (_, i) => makeAsset(201 + i, `srv-${i + 1}`, 1, "ssh")),
      makeAsset(207, "doc-1", 1, "generic"),
      makeAsset(208, "doc-2", 1, "generic"),
    ];
    vi.mocked(ListAssets).mockResolvedValue(mixedAssets);
    useAssetStore.setState({ assets: mixedAssets });

    const onConnectAsset = vi.fn();
    renderTree({ onConnectAsset });

    // Only non-connectable generic assets selected -> connect button disabled
    fireEvent.click(screen.getByText("doc-1"));
    fireEvent.click(screen.getByText("doc-2"), { ctrlKey: true });
    expect(screen.getByTestId("selection-bar-connect")).toBeDisabled();

    // Exact boundary = 5 connectable + 2 generic (7 total) -> connects 5 immediately without dialog
    fireEvent.click(screen.getByText("srv-1"));
    fireEvent.click(screen.getByText("srv-5"), { shiftKey: true });
    fireEvent.click(screen.getByText("doc-1"), { ctrlKey: true });
    fireEvent.click(screen.getByText("doc-2"), { ctrlKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toHaveLength(7);
    fireEvent.click(screen.getByTestId("selection-bar-connect"));
    await waitFor(() => expect(onConnectAsset).toHaveBeenCalledTimes(5));
    expect(screen.queryByTestId("confirm-batch-connect")).not.toBeInTheDocument();

    // Exact boundary = 6 connectable + 2 generic (8 total) -> prompts ConfirmDialog, connects only 6 connectable
    onConnectAsset.mockClear();
    fireEvent.keyDown(screen.getByTestId("asset-tree"), { key: "a", ctrlKey: true });
    fireEvent.click(screen.getByTestId("selection-bar-connect"));
    expect(onConnectAsset).not.toHaveBeenCalled();

    const confirmBtn = await screen.findByTestId("confirm-batch-connect");
    fireEvent.click(confirmBtn);
    await waitFor(() => expect(onConnectAsset).toHaveBeenCalledTimes(6));
    expect(onConnectAsset.mock.calls.every(([asset]) => asset.Type === "ssh")).toBe(true);
  });

  it("preserves selection when Escape closes batch context menu and ignores shortcuts in search/modals", async () => {
    renderTree();
    const sidebar = screen.getByTestId("asset-tree");

    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102]);

    // Open Batch Context Menu; pressing Escape should close menu WITHOUT clearing multi-selection
    fireEvent.contextMenu(screen.getByText("web-2"));
    await screen.findByRole("menu");
    fireEvent.keyDown(sidebar, { key: "Escape" });
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menu")).not.toBeInTheDocument());
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102]);

    // Pressing Escape inside search input does not clear selection; Delete inside search input does not open confirm
    const searchInput = screen.getByTestId("asset-tree-search");
    fireEvent.keyDown(searchInput, { key: "Escape" });
    fireEvent.keyDown(searchInput, { key: "Delete" });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102]);
    expect(screen.queryByTestId("confirm-batch-delete-assets")).not.toBeInTheDocument();

    // Open MoveAssetsDialog (portal): shortcuts inside modal must not trigger sidebar clear/delete
    fireEvent.click(screen.getByTestId("selection-bar-move"));
    const moveDialog = await screen.findByTestId("move-assets-dialog");
    fireEvent.keyDown(moveDialog, { key: "Delete" });
    fireEvent.keyDown(moveDialog, { key: "a", ctrlKey: true });
    expect(screen.queryByTestId("confirm-batch-delete-assets")).not.toBeInTheDocument();
    expect(useAssetStore.getState().selectedAssetIds).toEqual([101, 102]);

    // Pressing Escape on sidebar once dialog is closed clears selection
    fireEvent.click(within(moveDialog).getByRole("button", { name: /取消|Cancel/i }));
    await waitFor(() => expect(screen.queryByTestId("move-assets-dialog")).not.toBeInTheDocument());
    fireEvent.keyDown(sidebar, { key: "Escape" });
    expect(useAssetStore.getState().selectedAssetIds).toEqual([]);
  });

  it("keeps MoveAssetsDialog open and shows toast.error without unhandled rejection when batch move fails", async () => {
    const errorSpy = vi.spyOn(toast, "error");
    vi.mocked(ReorderAssets).mockRejectedValueOnce(new Error("move failed"));
    renderTree();

    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });
    fireEvent.click(screen.getByTestId("selection-bar-move"));

    const dialog = await screen.findByTestId("move-assets-dialog");
    fireEvent.click(within(dialog).getByTestId("move-assets-submit"));

    await waitFor(() => {
      expect(errorSpy).toHaveBeenCalledWith("move failed");
    });
    expect(screen.getByTestId("move-assets-dialog")).toBeInTheDocument();
    errorSpy.mockRestore();
  });

  it("clears stale primary and anchor IDs in fetchAssets even when selectedAssetIds is empty", async () => {
    useAssetStore.setState({
      selectedAssetId: 999,
      selectedAssetIds: [],
      selectedAssetIdSet: new Set(),
      selectionAnchorId: 999,
    });
    vi.mocked(ListAssets).mockResolvedValue(defaultAssets);

    await useAssetStore.getState().fetchAssets();
    expect(useAssetStore.getState().selectedAssetId).toBeNull();
    expect(useAssetStore.getState().selectionAnchorId).toBeNull();
  });

  it("moves selected assets via dialog, calls ReorderAssets, and warns when permission_mode becomes more permissive", async () => {
    const warnSpy = vi.spyOn(toast, "warning");
    vi.mocked(ReorderAssets).mockResolvedValue(undefined);
    renderTree();

    // web-1 & web-2 are in Folder A (default); Folder B is autopilot
    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });
    fireEvent.click(screen.getByTestId("selection-bar-move"));

    const dialog = await screen.findByTestId("move-assets-dialog");
    // Open GroupSelect trigger inside MoveAssetsDialog and pick Folder B (popover portal is rendered last)
    const trigger = within(dialog).getByTestId("move-assets-group-select");
    fireEvent.click(trigger);
    const folderBMatches = await screen.findAllByText("Folder B");
    fireEvent.click(folderBMatches[folderBMatches.length - 1]);
    fireEvent.click(within(dialog).getByTestId("move-assets-submit"));

    await waitFor(() => {
      expect(ReorderAssets).toHaveBeenCalledWith([101, 102], 2, 0);
    });
    expect(warnSpy).toHaveBeenCalledTimes(1);
    warnSpy.mockRestore();
  });

  it("batch deletes selected assets via Delete key and ConfirmDialog", async () => {
    vi.mocked(DeleteAssets).mockResolvedValue(undefined);
    renderTree();

    fireEvent.click(screen.getByText("web-1"));
    fireEvent.click(screen.getByText("web-2"), { ctrlKey: true });
    fireEvent.keyDown(screen.getByTestId("asset-tree"), { key: "Delete" });

    const confirmBtn = await screen.findByTestId("confirm-batch-delete-assets");
    fireEvent.click(confirmBtn);

    await waitFor(() => {
      expect(DeleteAssets).toHaveBeenCalledWith([101, 102]);
    });
  });

  it("supports right-clicking a group to create a sub-group and blank area to create a root group", async () => {
    const onAddGroup = vi.fn();
    const onAddAsset = vi.fn();
    renderTree({ onAddGroup, onAddAsset });

    fireEvent.contextMenu(screen.getByText("Folder A"));
    fireEvent.click(within(await screen.findByRole("menu")).getByTestId("group-context-add-subgroup"));
    expect(onAddGroup).toHaveBeenCalledWith(1);

    // Blank area context menu
    const scrollArea = screen.getByTestId("asset-tree").querySelector('[data-slot="scroll-area"]')!;
    fireEvent.contextMenu(scrollArea);
    const blankMenu = await screen.findByRole("menu");
    fireEvent.click(within(blankMenu).getByTestId("blank-context-add-group"));
    expect(onAddGroup).toHaveBeenLastCalledWith(0);
  });

  it("GroupDialog pre-fills defaultParentId and auto-expands collapsed parent after creation", async () => {
    vi.mocked(CreateGroup).mockResolvedValue(undefined);
    useAssetStore.setState({ collapsedGroupIds: [1] });

    render(
      <TooltipProvider>
        <GroupDialog open onOpenChange={vi.fn()} defaultParentId={1} />
      </TooltipProvider>
    );

    fireEvent.change(screen.getByTestId("group-dialog-name-input"), { target: { value: "Child Folder" } });
    fireEvent.click(screen.getByTestId("group-dialog-submit"));

    await waitFor(() => {
      expect(CreateGroup).toHaveBeenCalledWith(expect.objectContaining({ Name: "Child Folder", ParentID: 1 }));
    });
    expect(useAssetStore.getState().collapsedGroupIds).not.toContain(1);
  });
});
