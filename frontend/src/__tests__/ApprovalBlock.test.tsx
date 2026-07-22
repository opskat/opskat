import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ApprovalBlock } from "../components/approval/ApprovalBlock";
import type { ContentBlock } from "../stores/aiStore";

function cpBlock(): ContentBlock {
  return {
    type: "approval",
    status: "pending_confirm",
    confirmId: "confirm-1",
    approvalKind: "single",
    approvalItems: [
      {
        type: "cp",
        asset_id: 1,
        asset_name: "web-01",
        command: "/etc/cron.d/backup",
        detail: "upload /tmp/payload → /etc/cron.d/backup",
      },
    ],
  } as ContentBlock;
}

function renderApproval(overrides: Partial<ContentBlock>) {
  const block: ContentBlock = {
    type: "approval",
    status: "pending_confirm",
    confirmId: "confirm-1",
    approvalKind: "single",
    approvalItems: [],
    ...overrides,
  } as ContentBlock;
  render(<ApprovalBlock block={block} />);
}

describe("ApprovalBlock", () => {
  it("renders a cp approval with its remote path as the subject", () => {
    render(<ApprovalBlock block={cpBlock()} />);

    expect(screen.getByText("CP")).toBeInTheDocument();
    expect(screen.getByText("web-01")).toBeInTheDocument();
    expect(screen.getByText("/etc/cron.d/backup")).toBeInTheDocument();
  });

  it("shows the transfer detail so the local side is visible too", () => {
    render(<ApprovalBlock block={cpBlock()} />);

    expect(screen.getByText(/upload \/tmp\/payload/)).toBeInTheDocument();
  });

  it("删除审批不提供「全部允许」——删除不可 grant", () => {
    renderApproval({
      approvalKind: "delete",
      approvalItems: [{ type: "delete", asset_id: 1, asset_name: "web-9", command: 'delete asset "web-9" (type=ssh)' }],
    });

    expect(screen.getByTestId("ai-approval-block")).toHaveAttribute("data-approval-kind", "delete");
    // 「记住」开关是通往 allowAll 的唯一入口，删除审批必须没有它
    expect(screen.queryByTestId("ai-approval-remember")).not.toBeInTheDocument();
    expect(screen.queryByText(/allow all|全部允许/i)).not.toBeInTheDocument();
  });
});
