import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, act } from "@testing-library/react";

// 顶层 mock 覆盖 setup.ts 里默认 mockResolvedValue(undefined) 的版本，给本测试一份
// 真实形态的返回。EtcdListPrefix 至少要给 "/" 返回一条叶子，方便后面 KeyDetail 路径用。
vi.mock("../../wailsjs/go/etcd/Etcd", () => ({
  EtcdExec: vi.fn().mockResolvedValue({
    op: "get",
    count: 1,
    kvs: [
      {
        key: "/x",
        value: "v",
        modRevision: 1,
        createRevision: 1,
        version: 1,
        lease: 0,
      },
    ],
    revision: 1,
  }),
  EtcdListPrefix: vi.fn().mockResolvedValue({
    dirs: [],
    leaves: [],
    truncated: false,
  }),
  EtcdTestConnection: vi.fn().mockResolvedValue(undefined),
  Cleanup: vi.fn().mockResolvedValue(undefined),
  Startup: vi.fn().mockResolvedValue(undefined),
}));

import { EtcdPanel } from "@/components/query/EtcdPanel";
import { useTabStore } from "@/stores/tabStore";
import { useEtcdStore } from "@/stores/etcdStore";

function resetStores() {
  useEtcdStore.setState({
    treeCache: new Map(),
    truncatedAt: new Map(),
    queryHistory: [],
    lastResult: null,
  });
  useTabStore.setState({
    tabs: [
      {
        id: "t1",
        type: "query",
        label: "etcd",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "etcd",
          assetIcon: "",
          assetType: "etcd",
        },
      },
    ],
    activeTabId: "t1",
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any);
}

describe("EtcdPanel", () => {
  beforeEach(async () => {
    resetStores();
    const { EtcdExec, EtcdListPrefix } = await import("../../wailsjs/go/etcd/Etcd");
    (EtcdExec as ReturnType<typeof vi.fn>).mockClear();
    (EtcdListPrefix as ReturnType<typeof vi.fn>).mockClear();
  });

  it("renders left tree pane and two view tabs (tree + query)", async () => {
    render(<EtcdPanel tabId="t1" />);
    // 至少 2 个 role=tab —— tree 视图按钮 + query 视图按钮
    expect(screen.getAllByRole("tab").length).toBeGreaterThanOrEqual(2);
    // 树侧栏的 filter input 会出现
    expect(screen.getByPlaceholderText("etcd.tree.filterPlaceholder")).toBeInTheDocument();
  });

  it("switches to query view and executes a get from the query bar", async () => {
    const { EtcdExec } = await import("../../wailsjs/go/etcd/Etcd");
    render(<EtcdPanel tabId="t1" />);

    // 切到 query tab(顺序：tree, query)
    const tabs = screen.getAllByRole("tab");
    fireEvent.click(tabs[1]);

    const input = screen.getByTestId("etcd-query-input");
    fireEvent.change(input, { target: { value: "get /x" } });
    fireEvent.click(screen.getByTestId("etcd-query-execute"));

    await waitFor(() => {
      expect(EtcdExec).toHaveBeenCalled();
    });
    // 结果表渲染了
    await waitFor(() => {
      expect(screen.getByTestId("etcd-result-table")).toBeInTheDocument();
    });
    // ExecRequest.Op = "get",Key = "/x"
    const firstCall = (EtcdExec as ReturnType<typeof vi.fn>).mock.calls[0]?.[0];
    expect(firstCall?.Op).toBe("get");
    expect(firstCall?.Key).toBe("/x");
  });

  it("prompts ConfirmDialog before running a destructive put, and aborts on cancel", async () => {
    const { EtcdExec } = await import("../../wailsjs/go/etcd/Etcd");
    render(<EtcdPanel tabId="t1" />);

    fireEvent.click(screen.getAllByRole("tab")[1]);
    fireEvent.change(screen.getByTestId("etcd-query-input"), {
      target: { value: "put /flags/x true" },
    });
    fireEvent.click(screen.getByTestId("etcd-query-execute"));

    // ConfirmDialog 用 Radix AlertDialog → role="alertdialog"
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "action.cancel" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "action.confirm" })).toBeInTheDocument();

    // 此时还没真正调用 EtcdExec —— 后端调用必须在用户确认之后
    expect(EtcdExec).not.toHaveBeenCalled();

    // 点击 Cancel —— description body 里有 i18n key,Cancel 按钮没有显式 cancelText,所以用 role=button 选第一个 footer 按钮
    // 直接走 Esc 关弹窗,等价用户取消
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    // 取消后仍然没调用 EtcdExec
    expect(EtcdExec).not.toHaveBeenCalled();
  });

  it("shows exec-command confirm copy (not delete copy) for destructive query commands", async () => {
    render(<EtcdPanel tabId="t1" />);

    fireEvent.click(screen.getAllByRole("tab")[1]);
    fireEvent.change(screen.getByTestId("etcd-query-input"), {
      target: { value: "del /locks/ --prefix" },
    });
    fireEvent.click(screen.getByTestId("etcd-query-execute"));

    const dialog = await screen.findByRole("alertdialog");
    // execCommand 分支：标题/正文必须用 execConfirm* key,不能套用 deleteConfirm*
    expect(dialog).toHaveTextContent("etcd.query.execConfirmTitle");
    expect(dialog).toHaveTextContent("etcd.query.execConfirmBody");
    expect(dialog).not.toHaveTextContent("etcd.query.deleteConfirmTitle");
  });

  it("executes put after confirming the destructive dialog", async () => {
    const { EtcdExec } = await import("../../wailsjs/go/etcd/Etcd");
    render(<EtcdPanel tabId="t1" />);

    fireEvent.click(screen.getAllByRole("tab")[1]);
    fireEvent.change(screen.getByTestId("etcd-query-input"), {
      target: { value: "put /flags/x true" },
    });
    fireEvent.click(screen.getByTestId("etcd-query-execute"));

    await screen.findByRole("alertdialog");
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "action.confirm" }));
    });

    await waitFor(() => {
      expect(EtcdExec).toHaveBeenCalled();
    });
    const req = (EtcdExec as ReturnType<typeof vi.fn>).mock.calls[0]?.[0];
    expect(req?.Op).toBe("put");
    expect(req?.Key).toBe("/flags/x");
    expect(req?.Value).toBe("true");
  });
});
