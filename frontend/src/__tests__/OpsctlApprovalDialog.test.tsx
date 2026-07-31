import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { OpsctlApprovalDialog } from "../components/approval/OpsctlApprovalDialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// opsctl:approval 事件处理器按事件名捕获，测试里直接调用模拟后端 EventsEmit。
function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

function fireSingleApproval(handlers: Map<string, (data: unknown) => void>, overrides: Record<string, unknown> = {}) {
  const handler = handlers.get("opsctl:approval");
  if (!handler) throw new Error("opsctl:approval handler not registered");
  act(() => {
    handler({
      confirm_id: "opsctl_1",
      kind: "single",
      type: "exec",
      asset_id: 1,
      asset_name: "web-1",
      command: "ls -la",
      session_id: "",
      ...overrides,
    });
  });
}

function fireBatchApproval(handlers: Map<string, (data: unknown) => void>, items: unknown[]) {
  const handler = handlers.get("opsctl:batch-approval");
  if (!handler) throw new Error("opsctl:batch-approval handler not registered");
  act(() => {
    handler({ confirm_id: "batch_1", items, session_id: "session-1" });
  });
}

// 一条递归/通配 opsctl cp 展开出的批量审批项：每条都是独立主体（D17）。detail 是这次
// 传输唯一携带"两端基点"的地方——checkAccessBatch 给每条都填了同一句 "cp src → dst"。
function batchItems(n: number, detail?: string) {
  return Array.from({ length: n }, (_, i) => ({
    type: "cp",
    asset_id: (i % 2) + 1,
    asset_name: i % 2 === 0 ? "web-01" : "s3-prod",
    command: `/var/log/app-${i}.log`,
    detail,
  }));
}

describe("OpsctlApprovalDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("删除审批（后端 type=delete）不提供「记住」入口——删除不可 grant", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, {
      kind: "delete",
      type: "delete",
      command: 'delete asset "web-9" (type=ssh)',
      session_id: "session-1",
    });

    // 「记住」是通往 allowAll 的唯一入口，删除审批必须没有它
    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    // deny/allow 仍然存在
    expect(screen.getByText("opsctlApproval.deny")).toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("普通命令审批（type=exec）仍显示「记住」入口——正向对照", () => {
    // 与上一条的否定断言配对：若有人把 gate 误改窄，这条会先变红。
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type: "exec", session_id: "session-1" });

    expect(screen.getByText("opsctlApproval.remember")).toBeInTheDocument();
  });

  it("扩展审批（type=ext_tool）不提供 remember/allowAll", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "extension", type: "ext_tool", session_id: "session-1" });

    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("普通一次性审批（kind=once）不提供 remember/allowAll", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "once", type: "create", session_id: "session-1" });

    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("删除审批的标题与 ApprovalBlock 保持一致的措辞（复用同一 key，不新造文案）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "delete", type: "delete", session_id: "session-1" });

    expect(screen.getByText("ai.approvalDeleteTitle")).toBeInTheDocument();
  });

  it.each([
    ["delete", "lucide-trash2"],
    ["etcd", "lucide-database"],
    ["k8s", "lucide-boxes"],
    ["cp", "lucide-file-up"],
  ])("TypeBadge 为 type=%s 渲染对应图标（不回落到终端图标）", (type, iconClass) => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type, session_id: "session-1" });

    // Dialog 内容经 Radix portal 挂到 document.body 下，不在 render() 返回的 container 里。
    expect(document.body.querySelector(`.${iconClass}`)).not.toBeNull();
    expect(document.body.querySelector(".lucide-terminal")).toBeNull();
  });

  it("TypeBadge 为 type=oss 渲染专属徽章图标（复用 S3 品牌图标，不回落到终端图标）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type: "oss", session_id: "session-1" });

    const badge = screen.getByText("OSS").closest("span");
    expect(badge).not.toBeNull();
    const svg = badge!.querySelector("svg");
    expect(svg).not.toBeNull();
    // S3 品牌图标经 Iconify 渲染，不带 lucide-react 统一加的 "lucide-<kebab-name>" class，
    // 这个 class 的缺席就是"没有落到 Terminal 兜底"的可观察信号。
    expect(svg!.getAttribute("class") || "").not.toMatch(/lucide/);
  });
});

// 递归/通配 cp 一次性送来上百条 ApprovalItem，原样铺开没法读——超过 10 条时折叠为一行
// 摘要（条数 + 两端基点），展开后仍是全部具体主体。折叠只是呈现，批的还是那 N 条主体（D17）。
describe("OpsctlApprovalDialog 批量审批折叠（kind=batch，D17）", () => {
  it("恰好 10 条不折叠：全部主体直接可见，没有折叠摘要", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(10));

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-9.log")).toBeVisible();
  });

  it("11 条时折叠为一行摘要（含条数与两端基点），具体主体默认不可见", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(11, "cp web-01:/var/log → s3-prod:/bucket/logs/"));

    const summary = screen.getByTestId("opsctl-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "11");
    expect(summary.textContent).toContain("cp web-01:/var/log → s3-prod:/bucket/logs/");
    expect(screen.getByText("/var/log/app-0.log")).not.toBeVisible();
  });

  it("展开折叠摘要后，11 条具体主体全部可见——折叠只是呈现，不是新的授权范围（D17）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(11));

    fireEvent.click(screen.getByTestId("opsctl-approval-batch-summary"));

    for (let i = 0; i < 11; i++) {
      expect(screen.getByText(`/var/log/app-${i}.log`)).toBeVisible();
    }
  });

  it("200 条（D19 上限）同样折叠，展开后 200 条全部可见，一条不少", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(200));

    const summary = screen.getByTestId("opsctl-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "200");

    fireEvent.click(summary);
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-199.log")).toBeVisible();
  });

  it("1 条批量审批不折叠", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(1));

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
  });
});
