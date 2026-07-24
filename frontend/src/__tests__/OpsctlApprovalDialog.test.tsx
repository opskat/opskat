import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
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
});
