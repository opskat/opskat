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
});
