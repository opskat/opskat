import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { AssetTree } from "@/components/layout/AssetTree";
import { useAssetStore } from "@/stores/assetStore";
import { ASSET_SIDEBAR_ATTR } from "@/lib/assetRef";
import { asset_entity, group_entity } from "../../wailsjs/go/models";

// The asset-reference shortcut is scoped to the asset sidebar, so the sidebar root
// must carry the marker the predicate looks for and must take focus when the user
// presses a non-interactive part of it (an asset row, a group row, empty space) —
// otherwise the shortcut would never fire from the surface that owns it.

function makeGroup(id: number, name: string): group_entity.Group {
  return new group_entity.Group({ ID: id, Name: name, ParentID: 0, Icon: "", SortOrder: id, Status: 1 });
}

function makeAsset(id: number, name: string, groupId: number): asset_entity.Asset {
  return new asset_entity.Asset({ ID: id, Name: name, Type: "ssh", GroupID: groupId, Icon: "", Status: 1 });
}

function renderTree() {
  return render(
    <TooltipProvider>
      <AssetTree
        collapsed={false}
        onAddAsset={vi.fn()}
        onAddGroup={vi.fn()}
        onEditGroup={vi.fn()}
        onGroupDetail={vi.fn()}
        onEditAsset={vi.fn()}
        onCopyAsset={vi.fn()}
        onConnectAsset={vi.fn()}
        onSelectAsset={vi.fn()}
      />
    </TooltipProvider>
  );
}

describe("AssetTree sidebar focus surface", () => {
  beforeEach(() => {
    useAssetStore.setState({
      assets: [makeAsset(101, "Asset A", 1)],
      groups: [makeGroup(1, "Folder A")],
      selectedAssetId: null,
      collapsedGroupIds: [],
      initialized: true,
      loading: false,
    });
  });

  it("marks the sidebar root as the asset-reference surface", () => {
    const { container } = renderTree();
    expect(container.querySelector(`[${ASSET_SIDEBAR_ATTR}]`)).not.toBeNull();
  });

  it("takes focus when an asset row is pressed", () => {
    const { container } = renderTree();
    const sidebar = container.querySelector(`[${ASSET_SIDEBAR_ATTR}]`) as HTMLElement;

    fireEvent.mouseDown(screen.getByText("Asset A"));

    expect(document.activeElement).toBe(sidebar);
  });

  it("leaves focus in the search field", () => {
    renderTree();
    const input = screen.getByPlaceholderText("asset.search");
    input.focus();

    fireEvent.mouseDown(input);

    expect(document.activeElement).toBe(input);
  });
});
