import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { OpsctlApprovalDialog } from "../components/approval/OpsctlApprovalDialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { RespondOpsctlApproval } from "../../wailsjs/go/opsctl/Opsctl";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

// 连续审批：opsctl 脚本一条接一条发审批，下一条在同一组按钮下原地换入。

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

function emit(handlers: Map<string, (data: unknown) => void>, event: string, data: unknown) {
  const handler = handlers.get(event);
  if (!handler) throw new Error(`${event} handler not registered`);
  act(() => handler(data));
}

function single(id: string, overrides: Record<string, unknown> = {}) {
  return {
    confirm_id: id,
    kind: "single",
    type: "exec",
    asset_id: 1,
    asset_name: "web-1",
    command: `echo ${id}`,
    session_id: "session-1",
    ...overrides,
  };
}

const shownCommand = () => screen.getByTestId("approval-command-text").textContent;
const position = () => screen.queryByTestId("opsctl-approval-position");

describe("OpsctlApprovalDialog 连续审批", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(RespondOpsctlApproval).mockResolvedValue(undefined);
  });

  it("队列多于一条时显示第 n 条 / 共 m 条，作答后序号前进", async () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    emit(handlers, "opsctl:approval", single("a"));
    expect(position()).toBeNull();

    emit(handlers, "opsctl:approval", single("b"));
    emit(handlers, "opsctl:approval", single("c"));
    expect(position()).toHaveAttribute("data-current", "1");
    expect(position()).toHaveAttribute("data-total", "3");

    fireEvent.click(screen.getByTestId("opsctl-approval-allow"), { detail: 1 });
    await waitFor(() => expect(shownCommand()).toBe("echo b"));
    expect(position()).toHaveAttribute("data-current", "2");
    expect(position()).toHaveAttribute("data-total", "3");
  });

  it("初始与换入下一条后焦点都在「拒绝」", async () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    emit(handlers, "opsctl:approval", single("a"));
    emit(handlers, "opsctl:approval", single("b"));
    await waitFor(() => expect(screen.getByTestId("opsctl-approval-deny")).toHaveFocus());

    const allow = screen.getByTestId("opsctl-approval-allow");
    allow.focus();
    fireEvent.click(allow, { detail: 1 });

    await waitFor(() => expect(shownCommand()).toBe("echo b"));
    await waitFor(() => expect(screen.getByTestId("opsctl-approval-deny")).toHaveFocus());
  });

  it("双击的第二下不作答换入的下一条", async () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));
    emit(handlers, "opsctl:approval", single("b"));

    fireEvent.click(screen.getByTestId("opsctl-approval-allow"), { detail: 1 });
    await waitFor(() => expect(shownCommand()).toBe("echo b"));
    fireEvent.click(screen.getByTestId("opsctl-approval-allow"), { detail: 2 });

    expect(RespondOpsctlApproval).toHaveBeenCalledTimes(1);
    expect(vi.mocked(RespondOpsctlApproval).mock.calls[0][0]).toBe("a");
    expect(shownCommand()).toBe("echo b");
  });

  it("双击「记住并允许」不会把进入记住模式的那一下变成永久授权", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));

    fireEvent.click(screen.getByText("ai.approvalRememberAndAllow"), { detail: 1 });
    fireEvent.click(screen.getByText("ai.approvalRememberAndAllow"), { detail: 2 });

    expect(RespondOpsctlApproval).not.toHaveBeenCalled();
    expect(screen.getByTestId("approval-remember-pattern")).toBeInTheDocument();
  });

  it("调用方放弃等待时移除对应条目：当前条换入下一条，最后一条关闭弹窗", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));
    emit(handlers, "opsctl:batch-approval", {
      confirm_id: "batch_1",
      session_id: "s",
      items: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "echo batch" }],
    });
    emit(handlers, "opsctl:grant-approval", {
      session_id: "grant-1",
      items: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "uptime" }],
    });

    // 不是当前条：只是从队列里消失
    emit(handlers, "opsctl:approval-closed", { confirm_id: "batch_1" });
    expect(shownCommand()).toBe("echo a");
    expect(position()).toHaveAttribute("data-total", "2");

    // 当前条：换入下一条（grant 以 session id 作为 confirm id）
    emit(handlers, "opsctl:approval-closed", { confirm_id: "a" });
    expect(screen.getByText("opsctlApproval.grantTitle")).toBeInTheDocument();

    emit(handlers, "opsctl:approval-closed", { confirm_id: "grant-1" });
    expect(screen.queryByTestId("opsctl-approval-dialog")).toBeNull();
    expect(RespondOpsctlApproval).not.toHaveBeenCalled();
  });

  it("应答被后端拒绝时提示原因，条目留着可以改正或拒绝", async () => {
    vi.mocked(RespondOpsctlApproval).mockRejectedValueOnce("invalid approval response: pattern must keep the action");
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));

    fireEvent.click(screen.getByTestId("opsctl-approval-allow"), { detail: 1 });

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("opsctlApproval.respondFailed", {
        description: "invalid approval response: pattern must keep the action",
      })
    );
    expect(shownCommand()).toBe("echo a");
    expect(screen.getByTestId("opsctl-approval-allow")).toBeEnabled();

    fireEvent.click(screen.getByTestId("opsctl-approval-deny"), { detail: 1 });
    await waitFor(() => expect(screen.queryByTestId("opsctl-approval-dialog")).toBeNull());
    expect(vi.mocked(RespondOpsctlApproval).mock.calls[1][1].decision).toBe("deny");
  });

  it("拒绝也失败（审批已不存在）时仍移除条目并提示，不会卡住队列", async () => {
    vi.mocked(RespondOpsctlApproval).mockRejectedValueOnce("approval a is no longer pending");
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));

    fireEvent.click(screen.getByTestId("opsctl-approval-deny"), { detail: 1 });

    await waitFor(() => expect(screen.queryByTestId("opsctl-approval-dialog")).toBeNull());
    expect(toast.error).toHaveBeenCalledWith("opsctlApproval.respondFailed", {
      description: "approval a is no longer pending",
    });
  });

  it("记住模式可以退出，回到普通三按钮，仅本次允许不带记住的模式", async () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    emit(handlers, "opsctl:approval", single("a"));

    fireEvent.click(screen.getByText("ai.approvalRememberAndAllow"), { detail: 1 });
    fireEvent.change(screen.getByTestId("approval-remember-pattern"), { target: { value: "echo *" } });
    fireEvent.click(screen.getByTestId("approval-remember-cancel"), { detail: 1 });

    expect(screen.queryByTestId("approval-remember-pattern")).toBeNull();
    expect(screen.getByTestId("opsctl-approval-deny")).toBeInTheDocument();
    expect(screen.getByText("ai.approvalRememberAndAllow")).toBeInTheDocument();
    expect(screen.getByTestId("opsctl-approval-allow")).toHaveTextContent("ai.approvalAllowOnce");

    // 再进入记住模式时重新预填，而不是沿用退出前的编辑
    fireEvent.click(screen.getByText("ai.approvalRememberAndAllow"), { detail: 1 });
    expect(screen.getByTestId("approval-remember-pattern")).toHaveValue("echo a");
    fireEvent.click(screen.getByTestId("approval-remember-cancel"), { detail: 1 });

    fireEvent.click(screen.getByTestId("opsctl-approval-allow"), { detail: 1 });
    await waitFor(() => expect(RespondOpsctlApproval).toHaveBeenCalledTimes(1));
    const resp = vi.mocked(RespondOpsctlApproval).mock.calls[0][1];
    expect(resp.decision).toBe("allow");
    expect(resp.edited_items).toBeUndefined();
  });
});
