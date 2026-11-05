import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { Profiler, useState, type ProfilerOnRenderCallback } from "react";
import { Server } from "lucide-react";
import { TooltipProvider, pinyinMatch } from "@opskat/ui";
import { AssetTree } from "@/components/layout/AssetTree";
import { useAssetStore } from "@/stores/assetStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { useTabStore } from "@/stores/tabStore";
import { ListAssets, ListGroups } from "../../wailsjs/go/system/System";
import { asset_entity, group_entity } from "../../wailsjs/go/models";
import { profiled } from "./renderCount";

// Every asset row renders exactly one EntityIcon with the Server fallback (group rows use
// Folder), so `assetRowRenders` counts re-renders of already-mounted asset rows
// (AssetRowContent); first renders of newly mounted rows are not counted.
const probe = vi.hoisted(() => ({ assetRowRenders: 0, seq: 0, serverIcon: null as unknown }));
vi.mock("@/components/asset/AssetIcon", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/components/asset/AssetIcon")>();
  const seen = new Set<number>();
  function CountingEntityIcon(props: Parameters<typeof actual.EntityIcon>[0]) {
    const [id] = useState(() => ++probe.seq);
    if (props.fallback === probe.serverIcon) {
      if (seen.has(id)) probe.assetRowRenders += 1;
      else seen.add(id);
    }
    return <actual.EntityIcon {...props} />;
  }
  return { ...actual, EntityIcon: CountingEntityIcon };
});
probe.serverIcon = Server;

const ROOT_GROUPS = 10;
const CHILDREN_PER_ROOT = 9; // 10 + 90 = 100 groups
const ASSETS_PER_GROUP = 10; // 1000 assets

let parentIdReads = 0;

function seed() {
  const groups: group_entity.Group[] = [];
  let id = 1;
  for (let r = 0; r < ROOT_GROUPS; r++) {
    const rootId = id++;
    groups.push(new group_entity.Group({ ID: rootId, Name: `机房${r}`, ParentID: 0, Icon: "", SortOrder: rootId }));
    for (let c = 0; c < CHILDREN_PER_ROOT; c++) {
      const childId = id++;
      groups.push(
        new group_entity.Group({ ID: childId, Name: `业务${r}-${c}`, ParentID: rootId, Icon: "", SortOrder: childId })
      );
    }
  }
  for (const g of groups) {
    const parentID = g.ParentID;
    Object.defineProperty(g, "ParentID", {
      get() {
        parentIdReads += 1;
        return parentID;
      },
      enumerable: true,
    });
  }
  const assets: asset_entity.Asset[] = [];
  let assetId = 1;
  for (const g of groups) {
    for (let i = 0; i < ASSETS_PER_GROUP; i++) {
      const n = assetId++;
      assets.push(
        new asset_entity.Asset({
          ID: n,
          Name: n % 2 ? `服务器${n}` : `web-${n}`,
          Type: "ssh",
          GroupID: g.ID,
          Icon: "",
          Status: 1,
          SortOrder: n,
        })
      );
    }
  }
  return { groups, assets };
}

async function mountTree() {
  const { groups, assets } = seed();
  vi.mocked(ListAssets).mockResolvedValue(assets);
  vi.mocked(ListGroups).mockResolvedValue(groups);
  useAssetStore.setState({
    assets,
    groups,
    selectedAssetId: null,
    selectedGroupId: null,
    collapsedGroupIds: [],
    initialized: true,
    loading: false,
  });
  const props = {
    collapsed: false,
    onAddAsset: vi.fn(),
    onAddGroup: vi.fn(),
    onEditGroup: vi.fn(),
    onGroupDetail: vi.fn(),
    onEditAsset: vi.fn(),
    onCopyAsset: vi.fn(),
    onConnectAsset: vi.fn(),
    onSelectAsset: vi.fn(),
  };
  const { tree, counter } = profiled(<AssetTree {...props} />);
  const durations: number[] = [];
  const onRender: ProfilerOnRenderCallback = (_id, _phase, actualDuration) => {
    durations.push(actualDuration);
  };
  parentIdReads = 0;
  const utils = render(
    <TooltipProvider>
      <Profiler id="timing" onRender={onRender}>
        {tree}
      </Profiler>
    </TooltipProvider>
  );
  const mountParentIdReads = parentIdReads;
  // Let the mount-time fetchAssets/fetchGroups settle before measuring.
  await waitFor(() => expect(useAssetStore.getState().loading).toBe(false));
  await act(async () => {});
  counter.commits = 0;
  probe.assetRowRenders = 0;
  return { ...utils, counter, durations, groups, assets, mountParentIdReads };
}

// setup.ts reports a 4000px box for every observed element; give the sidebar a realistic
// 900px-tall viewport so the virtualizer mounts what a real window would.
class SidebarResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element) {
    const box = [{ inlineSize: 280, blockSize: 900 }];
    this.callback(
      [{ target, borderBoxSize: box, contentBoxSize: box } as unknown as ResizeObserverEntry],
      this as unknown as ResizeObserver
    );
  }
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal("ResizeObserver", SidebarResizeObserver);

describe("AssetTree render cost (1000 assets / 100 groups)", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useTerminalStore.setState({ tabData: {}, connectingAssetIds: new Set() });
  });

  it("does not re-render when an asset-store field it does not use changes", async () => {
    const { counter } = await mountTree();
    act(() => useAssetStore.setState({ selectedGroupId: 42 }));
    expect(counter.commits).toBe(0);
  });

  it("does not re-render when terminal tab data changes without changing active assets", async () => {
    const { counter } = await mountTree();
    act(() =>
      useTerminalStore.setState({
        tabData: { orphan: { panes: {} } as never },
      })
    );
    expect(counter.commits).toBe(0);
  });

  it("collapsing one group does not re-render asset rows of other groups", async () => {
    await mountTree();
    fireEvent.click(screen.getByText("机房0"));
    expect(useAssetStore.getState().collapsedGroupIds).toEqual([1]);
    expect(probe.assetRowRenders).toBe(0);
  });

  it("mounts only the rows near the viewport, and search still reaches rows further down", async () => {
    const { container, assets } = await mountTree();
    const mountedAssetRows = () => container.querySelectorAll('[data-asset-tree-row^="asset-"]').length;
    expect(mountedAssetRows()).toBeGreaterThan(0);
    expect(mountedAssetRows()).toBeLessThan(assets.length / 2);
    expect(screen.queryByText("web-1000")).toBeNull();
    fireEvent.change(screen.getByPlaceholderText("asset.search"), { target: { value: "web-1000" } });
    expect(screen.getByText("web-1000")).toBeInTheDocument();
  });

  it("selecting an asset re-renders only the rows whose selection changed", async () => {
    await mountTree();
    // Assets 11 and 12 are the first rows of the first child group, so both are on screen.
    expect(screen.getByText("服务器11")).toBeInTheDocument();
    expect(screen.getByText("web-12")).toBeInTheDocument();
    act(() => useAssetStore.setState({ selectedAssetId: 11 }));
    expect(probe.assetRowRenders).toBe(1);
    probe.assetRowRenders = 0;
    act(() => useAssetStore.setState({ selectedAssetId: 12 }));
    expect(probe.assetRowRenders).toBe(2);
  });

  it("reads each group's parent O(G) times per render, not O(G²)", async () => {
    const { mountParentIdReads, groups } = await mountTree();
    expect(mountParentIdReads).toBeLessThanOrEqual(4 * groups.length);
  });

  it("shows subtree totals on group headers and the ungrouped count on the ungrouped header", async () => {
    const parent = new group_entity.Group({ ID: 1, Name: "父组", ParentID: 0, Icon: "", SortOrder: 1 });
    const child = new group_entity.Group({ ID: 2, Name: "子组", ParentID: 1, Icon: "", SortOrder: 2 });
    const mk = (id: number, groupId: number) =>
      new asset_entity.Asset({ ID: id, Name: `a${id}`, Type: "ssh", GroupID: groupId, Icon: "", Status: 1 });
    const assets = [mk(1, 1), mk(2, 2), mk(3, 2), mk(4, 0), mk(5, 0)];
    vi.mocked(ListAssets).mockResolvedValue(assets);
    vi.mocked(ListGroups).mockResolvedValue([parent, child]);
    useAssetStore.setState({ assets, groups: [parent, child], collapsedGroupIds: [], initialized: true });
    render(
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
    const header = (name: string) => screen.getByText(name).closest("[data-asset-tree-row]")!;
    expect(header("父组")).toHaveTextContent(/3$/);
    expect(header("子组")).toHaveTextContent(/2$/);
    expect(header("asset.ungrouped")).toHaveTextContent(/2$/);
  });

  it("measures search typing and one group toggle commit time", async () => {
    await waitFor(() => expect(pinyinMatch("服务器", "fwq")).toBe(true));
    const { durations } = await mountTree();
    // Timings are noisy on a loaded machine: repeat each step and report the minimum
    // (closest to the uncontended cost) and the median.
    const ROUNDS = 5;
    const samples = new Map<string, number[]>();
    const measure = (label: string, run: () => void) => {
      durations.length = 0;
      act(run);
      const list = samples.get(label) ?? [];
      list.push(durations.reduce((a, b) => a + b, 0));
      samples.set(label, list);
    };
    const input = screen.getByPlaceholderText("asset.search");
    for (let round = 0; round < ROUNDS; round++) {
      measure("search 'f'", () => fireEvent.change(input, { target: { value: "f" } }));
      measure("search 'fw'", () => fireEvent.change(input, { target: { value: "fw" } }));
      measure("search 'fwq'", () => fireEvent.change(input, { target: { value: "fwq" } }));
      measure("search clear", () => fireEvent.change(input, { target: { value: "" } }));
      measure("collapse group", () => fireEvent.click(screen.getByText("机房0")));
      measure("expand group", () => fireEvent.click(screen.getByText("机房0")));
    }
    for (const [label, list] of samples) {
      const sorted = [...list].sort((a, b) => a - b);
      const min = sorted[0].toFixed(1);
      const median = sorted[Math.floor(sorted.length / 2)].toFixed(1);
      process.stdout.write(`[R18] ${label}: min ${min} ms, median ${median} ms over ${ROUNDS}\n`);
    }
  });
});
