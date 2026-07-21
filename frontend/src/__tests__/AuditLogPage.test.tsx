import { render, screen } from "@testing-library/react";
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
});
