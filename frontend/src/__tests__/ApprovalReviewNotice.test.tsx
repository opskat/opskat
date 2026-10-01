import { describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { ApprovalBlock } from "../components/approval/ApprovalBlock";
import { OpsctlApprovalDialog } from "../components/approval/OpsctlApprovalDialog";
import type { ContentBlock } from "../stores/aiStore";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// 辅助审批下审核没通过的命令转人工确认时，审批项带着审核结果，人要能看到为什么要确认。
const review = { mode: "assisted" as const, outcome: "reject", failed: ["disruptive"], model: "jev-1.13.0" };

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

describe("审批里显示模型审核结果", () => {
  it("AI 对话的单条审批", () => {
    const block = {
      type: "approval",
      status: "pending_confirm",
      confirmId: "confirm-1",
      approvalKind: "single",
      approvalItems: [{ type: "exec", asset_id: 1, asset_name: "web-01", command: "systemctl restart nginx", review }],
    } as ContentBlock;
    render(<ApprovalBlock block={block} />);
    expect(screen.getByTestId("review-notice")).toHaveTextContent("commandReview.summary.reject");
  });

  it("AI 对话的批量审批", () => {
    const block = {
      type: "approval",
      status: "pending_confirm",
      confirmId: "confirm-1",
      approvalKind: "batch",
      approvalItems: [
        { type: "exec", asset_id: 1, asset_name: "web-01", command: "ls" },
        { type: "exec", asset_id: 1, asset_name: "web-01", command: "systemctl restart nginx", review },
      ],
    } as ContentBlock;
    render(<ApprovalBlock block={block} />);
    expect(screen.getAllByTestId("review-notice")).toHaveLength(1);
  });

  it("opsctl 单条审批事件里的审核结果", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    act(() => {
      handlers.get("opsctl:approval")?.({
        confirm_id: "opsctl_1",
        kind: "single",
        type: "exec",
        asset_id: 1,
        asset_name: "web-01",
        command: "systemctl restart nginx",
        session_id: "",
        source: "opsctl",
        extension: "",
        review,
      });
    });
    expect(screen.getByTestId("review-notice")).toHaveTextContent("commandReview.summary.reject");
  });

  it("opsctl 批量审批事件里的审核结果", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    act(() => {
      handlers.get("opsctl:batch-approval")?.({
        confirm_id: "batch_1",
        session_id: "s",
        items: [
          { type: "exec", asset_id: 1, asset_name: "web-01", command: "ls" },
          { type: "exec", asset_id: 2, asset_name: "web-02", command: "systemctl stop nginx", review },
        ],
      });
    });
    expect(screen.getAllByTestId("review-notice")).toHaveLength(1);
  });
});
