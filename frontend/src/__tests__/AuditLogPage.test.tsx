import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AuditLogPage } from "@/components/audit/AuditLogPage";
import { ListAuditLogs, ListAuditSessions } from "../../wailsjs/go/system/System";

describe("AuditLogPage result status", () => {
  beforeEach(() => {
    vi.mocked(ListAuditSessions).mockResolvedValue([]);
  });

  it("renders a denied, unsuccessful audit row with the failure icon", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        {
          ID: 1,
          Source: "opsctl",
          ToolName: "cp",
          AssetID: 7,
          AssetName: "controlled-sftp",
          Command: "cp /tmp/payload.bin → controlled-sftp:/srv/payload.bin",
          Request: "{}",
          Result: "",
          Error: "operation denied: user denied",
          Success: 0,
          ConversationID: 0,
          GrantSessionID: "",
          SessionID: "opsctl-cp-deny",
          Decision: "deny",
          DecisionSource: "user_deny",
          MatchedPattern: "",
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);

    render(<AuditLogPage />);

    expect(await screen.findByText("cp")).toBeInTheDocument();
    expect(screen.getByLabelText("audit.failed")).toBeInTheDocument();
    expect(screen.queryByLabelText("audit.success")).not.toBeInTheDocument();
  });

  it("lets users select audit data and detail payloads for keyboard copy", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        {
          ID: 2,
          Source: "opsctl",
          ToolName: "selectable-tool",
          AssetID: 8,
          AssetName: "selectable-asset",
          Command: "echo selectable-command",
          Request: '{"request":"selectable"}',
          Result: '{"response":"selectable"}',
          Error: "selectable-error",
          Success: 0,
          ConversationID: 0,
          GrantSessionID: "",
          SessionID: "selectable-session",
          Decision: "deny",
          DecisionSource: "policy_deny",
          MatchedPattern: "selectable-pattern",
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);

    const user = userEvent.setup();
    render(<AuditLogPage />);

    const tool = await screen.findByText("selectable-tool");
    const row = tool.closest("tr");
    expect(row?.parentElement).toHaveClass("select-text");

    await user.click(within(row as HTMLTableRowElement).getByRole("button"));

    const request = screen.getByText('{"request":"selectable"}');
    const detail = request.closest('[role="dialog"]')?.querySelector(".select-text");
    expect(detail).toBeInTheDocument();
    expect(request).toHaveClass("select-text");
    expect(screen.getByText('{"response":"selectable"}')).toHaveClass("select-text");
    expect(screen.getByText("selectable-error")).toHaveClass("select-text");
  });
});
