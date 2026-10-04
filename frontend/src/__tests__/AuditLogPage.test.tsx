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

  it("shows Autopilot rejections with their model review", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        {
          ID: 3,
          Source: "opsctl",
          ToolName: "reviewed-tool",
          AssetID: 9,
          AssetName: "web-01",
          Command: "systemctl stop nginx",
          Request: "{}",
          Result: "",
          Error: "command denied by policy",
          Success: 0,
          ConversationID: 0,
          GrantSessionID: "",
          SessionID: "s",
          Decision: "deny",
          DecisionSource: "autopilot_deny",
          MatchedPattern: "",
          Review:
            '{"mode":"autopilot","outcome":"reject","failed":["disruptive"],"model":"jev-1.13.0",' +
            '"scores":{"destructive":0.02,"disruptive":0.91,"remote_code":0.01},"threshold":0.2,"attempts":2}',
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);

    const user = userEvent.setup();
    render(<AuditLogPage />);

    const tool = await screen.findByText("reviewed-tool");
    const row = tool.closest("tr") as HTMLTableRowElement;
    expect(within(row).getByText("✗ autopilot")).toBeInTheDocument();

    await user.click(within(row).getByRole("button"));
    expect(screen.getByTestId("review-notice")).toHaveTextContent("commandReview.withMode");

    // 每道题的评分，没过阈值的那题标出来
    const scores = screen.getByTestId("review-scores");
    expect(scores).toHaveTextContent("commandReview.scores");
    const disruptive = within(scores).getByText("commandReview.scoreName.disruptive").closest("li") as HTMLElement;
    expect(disruptive).toHaveTextContent("0.91");
    expect(disruptive).toHaveAttribute("data-failed", "true");
    const destructive = within(scores).getByText("commandReview.scoreName.destructive").closest("li") as HTMLElement;
    expect(destructive).toHaveTextContent("0.02");
    expect(destructive).toHaveAttribute("data-failed", "false");

    // 第一次请求超时、自动重试过的，详情里写明请求了几次
    expect(screen.getByTestId("review-attempts")).toHaveTextContent("commandReview.retried");
  });

  // 辅助审批审核没通过、转人工的记录，决策来源是 user_allow，列表里要靠审核标志看出它被模型审过。
  it("marks every model-reviewed row in the list, including ones a human decided", async () => {
    const row = (id: number, tool: string, source: string, review: string) => ({
      ID: id,
      Source: "opsctl",
      ToolName: tool,
      AssetID: 9,
      AssetName: "web-01",
      Command: "systemctl restart nginx",
      Request: "{}",
      Result: "",
      Error: "",
      Success: 1,
      ConversationID: 0,
      GrantSessionID: "",
      SessionID: "s",
      Decision: "allow",
      DecisionSource: source,
      MatchedPattern: "",
      Review: review,
      Createtime: id,
    });
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        row(1, "assisted-to-human", "user_allow", '{"mode":"assisted","outcome":"reject","failed":["disruptive"]}'),
        row(2, "rule-allowed", "policy_allow", ""),
      ],
      total: 2,
    } as never);

    render(<AuditLogPage />);

    const reviewed = (await screen.findByText("assisted-to-human")).closest("tr") as HTMLTableRowElement;
    expect(within(reviewed).getByText("✓ user")).toBeInTheDocument();
    expect(within(reviewed).getByTestId("review-mark")).toHaveAccessibleName("commandReview.withMode");

    const ruled = screen.getByText("rule-allowed").closest("tr") as HTMLTableRowElement;
    expect(within(ruled).queryByTestId("review-mark")).toBeNull();
  });
});
