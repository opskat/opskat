import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AuditLogPage } from "@/components/audit/AuditLogPage";
import { useAssetStore } from "@/stores/assetStore";
import {
  ListAuditDeciderGroups,
  ListAuditFacets,
  ListAuditLogs,
  ListAuditSessionGrants,
  ListAuditSessions,
} from "../../wailsjs/go/system/System";
import { GetSSHPoolConnections } from "../../wailsjs/go/ssh/SSH";
import type { audit_svc } from "../../wailsjs/go/models";

// 每个用例都要的后端：筛选项与决策方分组（分组定义在后端，前端只按它反查）
beforeEach(() => {
  vi.mocked(ListAuditFacets).mockResolvedValue({
    tools: ["exec", "cp"],
    deciders: ["policy", "user", "exec_gate"],
  } as never);
  vi.mocked(ListAuditDeciderGroups).mockResolvedValue([
    { id: "policy", sources: ["policy_allow", "policy_deny"] },
    { id: "user", sources: ["user_allow", "user_deny"] },
    { id: "exec_gate", sources: ["exec_gate_blocked"] },
    { id: "none", sources: [""] },
  ] as never);
});

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

    await user.click(within(row as HTMLTableRowElement).getByText("echo selectable-command"));

    const detail = screen.getByRole("complementary", { name: "audit.detail" });
    // 请求是 JSON 时缩进显示
    const request = within(detail).getByText((_, el) => el?.textContent === '{\n  "request": "selectable"\n}');
    expect(request).toHaveClass("select-text");
    expect(within(detail).getByText('{"response":"selectable"}')).toHaveClass("select-text");
    expect(within(detail).getByText("selectable-error")).toHaveClass("select-text");
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
            '{"mode":"autopilot","outcome":"reject","failed":["disruptive"],' +
            '"scores":{"disruptive":0.91},"attempts":2,"failed_names":{"disruptive":{"en":"Interrupts services"}}}',
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);

    const user = userEvent.setup();
    render(<AuditLogPage />);

    const tool = await screen.findByText("reviewed-tool");
    const row = tool.closest("tr") as HTMLTableRowElement;
    expect(within(row).getByText("✗ audit.decisionSources.autopilot")).toBeInTheDocument();

    await user.click(within(row).getByText("systemctl stop nginx"));
    expect(screen.getByTestId("review-notice")).toHaveTextContent("commandReview.withMode");

    // 只列未通过题目的评分
    const scores = screen.getByTestId("review-scores");
    expect(scores).toHaveTextContent("commandReview.scores");
    const disruptive = within(scores)
      .getByText(/Interrupts services/)
      .closest("li") as HTMLElement;
    expect(disruptive).toHaveTextContent("0.91");
    expect(disruptive).toHaveAttribute("data-failed", "true");
    expect(within(scores).getAllByRole("listitem")).toHaveLength(1);

    // 第一次请求超时、自动重试过的，详情里写明请求了几次
    expect(screen.getByTestId("review-attempts")).toHaveTextContent("commandReview.retried");
  });

  it("shows which review extension judged the command, with its question names", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        {
          ID: 4,
          Source: "opsctl",
          ToolName: "ext-reviewed-tool",
          AssetID: 9,
          AssetName: "web-01",
          Command: "rm -rf /data",
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
            '{"mode":"autopilot","outcome":"reject","failed":["data_loss"],"scores":{"data_loss":0.9},' +
            '"extension":"jev-command-review","version":"1.2.0","failed_names":{"data_loss":{"en":"Loses data"}}}',
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);

    const user = userEvent.setup();
    render(<AuditLogPage />);

    const tool = await screen.findByText("ext-reviewed-tool");
    await user.click(within(tool.closest("tr") as HTMLTableRowElement).getByText("rm -rf /data"));
    expect(screen.getByTestId("review-reviewer")).toHaveTextContent("commandReview.reviewedBy");
    expect(within(screen.getByTestId("review-scores")).getByText(/Loses data/)).toBeInTheDocument();
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
    expect(within(reviewed).getByText("✓ audit.decisionSources.user")).toBeInTheDocument();
    expect(within(reviewed).getByTestId("review-mark")).toHaveAccessibleName("commandReview.withMode");

    const ruled = screen.getByText("rule-allowed").closest("tr") as HTMLTableRowElement;
    expect(within(ruled).queryByTestId("review-mark")).toBeNull();
  });
});

describe("AuditLogPage load states", () => {
  beforeEach(() => {
    vi.mocked(ListAuditSessions).mockResolvedValue([]);
  });

  it("shows the failure reason with retry instead of the empty state when logs fail to load", async () => {
    vi.mocked(ListAuditLogs).mockRejectedValueOnce(new Error("audit db locked"));
    render(<AuditLogPage />);
    expect(await screen.findByText(/audit db locked/)).toBeInTheDocument();
    expect(screen.queryByText("audit.empty")).not.toBeInTheDocument();

    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [], total: 0 } as never);
    await userEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("audit.empty")).toBeInTheDocument();
  });

  it("does not keep showing the previous rows next to a failed reload", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValueOnce({
      items: [{ ID: 1, ToolName: "exec", AssetName: "web-01", Command: "uptime", Request: "{}", Createtime: 1 }],
      total: 1,
    } as never);
    render(<AuditLogPage />);
    expect(await screen.findByText("web-01")).toBeInTheDocument();

    vi.mocked(ListAuditLogs).mockRejectedValueOnce(new Error("audit db locked"));
    await userEvent.click(document.querySelector(".lucide-refresh-cw")!.closest("button")!);

    expect(await screen.findByText(/audit db locked/)).toBeInTheDocument();
    expect(screen.queryByText("web-01")).not.toBeInTheDocument();
  });

  it("shows loading while a logs retry is in flight", async () => {
    vi.mocked(ListAuditLogs).mockRejectedValueOnce(new Error("audit db locked"));
    render(<AuditLogPage />);
    await screen.findByText(/audit db locked/);

    vi.mocked(ListAuditLogs).mockReturnValue(new Promise(() => {}));
    await userEvent.click(screen.getByTestId("async-state-retry"));
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText(/audit db locked/)).not.toBeInTheDocument();
  });

  it("shows loading while a pool retry is in flight", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [], total: 0 } as never);
    vi.mocked(GetSSHPoolConnections).mockRejectedValueOnce(new Error("pool down"));
    render(<AuditLogPage />);
    await userEvent.click(await screen.findByRole("tab", { name: /audit.tabPool/ }));
    await screen.findByText(/pool down/);

    vi.mocked(GetSSHPoolConnections).mockReturnValue(new Promise(() => {}));
    await userEvent.click(screen.getByTestId("async-state-retry"));
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText(/pool down/)).not.toBeInTheDocument();
  });

  it("shows loading, not the empty state, while logs are pending", async () => {
    vi.mocked(ListAuditLogs).mockReturnValue(new Promise(() => {}));
    render(<AuditLogPage />);
    expect(await screen.findByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("audit.empty")).not.toBeInTheDocument();
  });

  it("shows the failure reason instead of the empty pool when the pool fails to load", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [], total: 0 } as never);
    vi.mocked(GetSSHPoolConnections).mockRejectedValueOnce(new Error("pool down"));
    render(<AuditLogPage />);
    await userEvent.click(await screen.findByRole("tab", { name: /audit.tabPool/ }));
    expect(await screen.findByText(/pool down/)).toBeInTheDocument();
    expect(screen.queryByText("audit.poolEmpty")).not.toBeInTheDocument();
  });

  it("shows translated decision names instead of internal identifiers", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        {
          ID: 1,
          ToolName: "exec",
          AssetName: "a",
          Command: "ls",
          Success: 1,
          Decision: "allow",
          DecisionSource: "grant_allow",
          Createtime: 1,
        },
      ],
      total: 1,
    } as never);
    render(<AuditLogPage />);
    expect(await screen.findByText("✓ audit.decisionSources.grant")).toBeInTheDocument();
  });

  it("translates short-circuit and batch decision sources too", async () => {
    const row = (id: number, decision: string, source: string) => ({
      ID: id,
      ToolName: `tool-${id}`,
      AssetName: "a",
      Command: "ls",
      Success: 1,
      Decision: decision,
      DecisionSource: source,
      Createtime: id,
    });
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        row(1, "deny", "exec_gate_blocked"),
        row(2, "allow", "batch_complete"),
        row(3, "deny", "batch_partial_failure"),
      ],
      total: 3,
    } as never);
    render(<AuditLogPage />);
    const rowOf = async (tool: string) => (await screen.findByText(tool)).closest("tr") as HTMLTableRowElement;
    expect(within(await rowOf("tool-1")).getByText("✗ audit.decisionSources.execGateBlocked")).toBeInTheDocument();
    expect(within(await rowOf("tool-2")).getByText("✓ audit.decisionSources.batchComplete")).toBeInTheDocument();
    expect(within(await rowOf("tool-3")).getByText("✗ audit.decisionSources.batchPartialFailure")).toBeInTheDocument();
  });
});

describe("AuditLogPage filters", () => {
  const log = (id: number, over: Record<string, unknown> = {}) => ({
    ID: id,
    Source: "opsctl",
    ToolName: "exec",
    AssetID: 9,
    AssetName: "web-01",
    Command: `cmd-${id}`,
    Request: "{}",
    Result: "",
    Error: "",
    Success: 1,
    SessionID: "",
    Decision: "allow",
    DecisionSource: "policy_allow",
    MatchedPattern: "",
    Createtime: id,
    ...over,
  });
  const lastQuery = () => vi.mocked(ListAuditLogs).mock.calls.at(-1)![0] as audit_svc.Query;

  beforeEach(() => {
    vi.mocked(ListAuditLogs).mockReset();
    vi.mocked(ListAuditSessions).mockResolvedValue([]);
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [log(1), log(2, { AssetID: 12, AssetName: "db-01" })],
      total: 2,
    } as never);
  });

  it("filters by a header option and shows it as a removable condition", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    await user.click(screen.getByTestId("audit-filter-source"));
    await user.click(await screen.findByRole("button", { name: "audit.sources.ai" }));

    await waitFor(() => expect(lastQuery().source).toBe("ai"));
    const conditions = screen.getByTestId("audit-conditions");
    expect(conditions).toHaveTextContent("audit.source:audit.sources.ai");
    expect(screen.getByTestId("audit-filter-source")).toHaveAttribute("aria-pressed", "true");

    await user.click(within(conditions).getByRole("button", { name: "audit.removeCondition" }));
    await waitFor(() => expect(lastQuery().source).toBe(""));
    expect(screen.queryByTestId("audit-conditions")).not.toBeInTheDocument();
  });

  it("offers the tools and deciders that occur in the time range", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    await user.click(screen.getByTestId("audit-filter-tool"));
    await user.click(await screen.findByRole("button", { name: "cp" }));
    await waitFor(() => expect(lastQuery().tool_name).toBe("cp"));

    await user.click(screen.getByTestId("audit-filter-decision"));
    await user.click(await screen.findByRole("button", { name: "audit.decisions.deny" }));
    await user.click(await screen.findByRole("button", { name: "audit.deciders.user" }));
    await waitFor(() => expect(lastQuery()).toMatchObject({ decision: "deny", decider: "user", tool_name: "cp" }));
  });

  it("shows the failure with retry inside the popover when the options fail to load", async () => {
    vi.mocked(ListAuditFacets).mockRejectedValueOnce(new Error("facets down"));
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    await user.click(screen.getByTestId("audit-filter-tool"));
    expect(await screen.findByText(/facets down/)).toBeInTheDocument();
    await user.click(screen.getByTestId("async-state-retry"));
    expect(await within(await screen.findByRole("dialog")).findByRole("button", { name: "exec" })).toBeInTheDocument();
  });

  it("adds a condition when a cell value is clicked", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [
        log(1),
        log(2, { AssetID: 12, AssetName: "db-01", Success: 0, Decision: "deny", DecisionSource: "user_deny" }),
      ],
      total: 2,
    } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    const row1 = (await screen.findByText("cmd-1")).closest("tr") as HTMLTableRowElement;
    const row2 = screen.getByText("cmd-2").closest("tr") as HTMLTableRowElement;

    await user.click(within(row1).getByRole("button", { name: "web-01" }));
    await waitFor(() => expect(lastQuery().asset_ids).toEqual([9]));
    // 资产是追加，不是替换
    await user.click(within(row2).getByRole("button", { name: "db-01" }));
    await waitFor(() => expect(lastQuery().asset_ids).toEqual([9, 12]));

    // 决策标签同时设置放行/拒绝与它所属的决策方分组
    await user.click(within(row2).getByText("✗ audit.decisionSources.user"));
    await waitFor(() => expect(lastQuery()).toMatchObject({ decision: "deny", decider: "user" }));

    await user.click(within(row2).getByRole("button", { name: "audit.failed" }));
    await waitFor(() => expect(lastQuery().result).toBe("failed"));

    await user.click(within(row1).getByRole("button", { name: "audit.sources.opsctl" }));
    await user.click(within(row1).getByRole("button", { name: "exec" }));
    await waitFor(() => expect(lastQuery()).toMatchObject({ source: "opsctl", tool_name: "exec" }));
    expect(screen.getByTestId("audit-conditions")).toHaveTextContent("web-01, db-01");
  });

  it("selects every asset of a group from the asset header", async () => {
    useAssetStore.setState({
      assets: [
        { ID: 11, Name: "web-a", Type: "ssh", GroupID: 1, Status: 1 },
        { ID: 12, Name: "web-b", Type: "ssh", GroupID: 1, Status: 1 },
        { ID: 21, Name: "db-a", Type: "ssh", GroupID: 0, Status: 1 },
      ],
      groups: [{ ID: 1, Name: "prod", ParentID: 0 }],
    } as unknown as Parameters<typeof useAssetStore.setState>[0]);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    await user.click(screen.getByTestId("audit-filter-asset"));
    await user.click(await screen.findByRole("checkbox", { name: "prod" }));

    await waitFor(() => expect([...lastQuery().asset_ids].sort()).toEqual([11, 12]));
    expect(screen.getByTestId("audit-conditions")).toHaveTextContent("web-a, web-b");
  });

  it("searches the keyword only after typing pauses", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");
    const calls = vi.mocked(ListAuditLogs).mock.calls.length;

    await user.type(screen.getByRole("textbox", { name: "audit.searchPlaceholder" }), "nginx");
    // 逐字输入期间不查询
    expect(vi.mocked(ListAuditLogs).mock.calls.length).toBe(calls);
    await waitFor(() => expect(lastQuery().keyword).toBe("nginx"));
    expect(vi.mocked(ListAuditLogs).mock.calls.length).toBe(calls + 1);
  });

  it("returns to the first page when a condition changes", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 45 } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    await user.click(screen.getByRole("button", { name: "audit.nextPage" }));
    await waitFor(() => expect(lastQuery().offset).toBe(20));

    await user.click(within(screen.getByText("cmd-1").closest("tr")!).getByRole("button", { name: "exec" }));
    await waitFor(() => expect(lastQuery()).toMatchObject({ tool_name: "exec", offset: 0 }));
  });

  it("explains an empty filtered result and clears every condition, including the keyword", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");

    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [], total: 0 } as never);
    await user.type(screen.getByRole("textbox", { name: "audit.searchPlaceholder" }), "zzz");
    expect(await screen.findByText("audit.emptyFiltered")).toBeInTheDocument();
    expect(screen.queryByText("audit.empty")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "audit.clearAllFilters" }));
    await waitFor(() => expect(lastQuery().keyword).toBe(""));
    expect(screen.getByRole("textbox", { name: "audit.searchPlaceholder" })).toHaveValue("");
  });
});

describe("AuditLogPage detail drawer", () => {
  const log = (id: number, over: Record<string, unknown> = {}) => ({
    ID: id,
    Source: "opsctl",
    ToolName: "exec",
    AssetID: 9,
    AssetName: "web-01",
    Command: `cmd-${id}`,
    Request: `{"n":${id}}`,
    Result: `out-${id}`,
    Error: "",
    Success: 1,
    SessionID: `session-${id}`,
    Decision: "allow",
    DecisionSource: "policy_allow",
    MatchedPattern: "",
    Createtime: id,
    ...over,
  });
  const drawer = () => screen.queryByRole("complementary", { name: "audit.detail" });
  const lastQuery = () => vi.mocked(ListAuditLogs).mock.calls.at(-1)![0] as audit_svc.Query;

  beforeEach(() => {
    vi.mocked(ListAuditLogs).mockReset();
    vi.mocked(ListAuditSessions).mockResolvedValue([]);
    vi.mocked(ListAuditLogs).mockResolvedValue({
      items: [log(3), log(2, { Success: 0, Error: "exit status 1" }), log(1)],
      total: 3,
    } as never);
  });

  it("opens from a row click, highlights the row, and steps through the page with the arrow keys", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    const row3 = (await screen.findByText("cmd-3")).closest("tr") as HTMLTableRowElement;

    await user.click(within(row3).getByText("cmd-3"));
    expect(within(drawer()!).getByText("out-3")).toBeInTheDocument();
    expect(row3).toHaveAttribute("aria-selected", "true");
    expect(within(drawer()!).getByRole("button", { name: "audit.prevRecord" })).toBeDisabled();

    await user.keyboard("{ArrowDown}");
    expect(within(drawer()!).getByText("exit status 1")).toBeInTheDocument();
    await user.keyboard("{ArrowDown}");
    expect(within(drawer()!).getByText("out-1")).toBeInTheDocument();
    // 到本页末尾就停住，不翻页
    expect(within(drawer()!).getByRole("button", { name: "audit.nextRecord" })).toBeDisabled();
    await user.keyboard("{ArrowDown}");
    expect(within(drawer()!).getByText("out-1")).toBeInTheDocument();

    await user.keyboard("{ArrowUp}");
    expect(within(drawer()!).getByText("exit status 1")).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(drawer()).not.toBeInTheDocument();
  });

  it("ignores the arrow keys while typing in the search box", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByText("cmd-3"));

    await user.click(screen.getByRole("textbox", { name: "audit.searchPlaceholder" }));
    await user.keyboard("{ArrowDown}");
    expect(within(drawer()!).getByText("out-3")).toBeInTheDocument();
  });

  it("does not open when a filterable value in the row is clicked", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    const row = (await screen.findByText("cmd-3")).closest("tr") as HTMLTableRowElement;

    await user.click(within(row).getByRole("button", { name: "web-01" }));
    expect(drawer()).not.toBeInTheDocument();
  });

  it("turns detail values into filters and copies blocks", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByText("cmd-3"));

    const commandBlock = within(drawer()!).getByText("cmd-3").closest("section") as HTMLElement;
    await user.click(within(commandBlock).getByRole("button", { name: "audit.copyField" }));
    // userEvent.setup() 装了自己的剪贴板替身，读回来即可
    expect(await navigator.clipboard.readText()).toBe("cmd-3");

    await user.click(within(drawer()!).getByRole("button", { name: "session-3" }));
    await waitFor(() => expect(lastQuery().session_id).toBe("session-3"));
  });

  it("closes when the selected record is no longer in the results", async () => {
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByText("cmd-3"));
    expect(drawer()).toBeInTheDocument();

    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 1 } as never);
    await user.click(within(screen.getByText("cmd-1").closest("tr")!).getByRole("button", { name: "exec" }));
    await waitFor(() => expect(lastQuery().tool_name).toBe("exec"));
    await waitFor(() => expect(screen.queryByText("cmd-3")).not.toBeInTheDocument());
    expect(drawer()).not.toBeInTheDocument();
  });

  it("shows the error summary when hovering a failed result", async () => {
    render(<AuditLogPage />);
    const row2 = (await screen.findByText("cmd-2")).closest("tr") as HTMLTableRowElement;
    expect(within(row2).getByRole("button", { name: "audit.failed" })).toHaveAttribute("title", "exit status 1");
  });
});

describe("AuditLogPage paging, loading and context", () => {
  const log = (id: number, over: Record<string, unknown> = {}) => ({
    ID: id,
    Source: "opsctl",
    ToolName: "exec",
    AssetID: 9,
    AssetName: "web-01",
    Command: `cmd-${id}`,
    Request: "{}",
    Result: "",
    Error: "",
    Success: 1,
    SessionID: "s-1",
    Decision: "allow",
    DecisionSource: "policy_allow",
    MatchedPattern: "",
    Createtime: id,
    ...over,
  });
  const lastQuery = () => vi.mocked(ListAuditLogs).mock.calls.at(-1)![0] as audit_svc.Query;
  const deferred = <T,>() => {
    let resolve!: (v: T) => void;
    const promise = new Promise<T>((r) => (resolve = r));
    return { promise, resolve };
  };

  beforeEach(() => {
    vi.mocked(ListAuditLogs).mockReset();
    vi.mocked(ListAuditSessions).mockResolvedValue([]);
    vi.mocked(ListAuditSessionGrants).mockResolvedValue([]);
  });

  it("shows the record range and jumps to a typed page, clamped to the last one", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 1234 } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");
    expect(screen.getByTestId("audit-range")).toHaveTextContent("audit.range");
    expect(screen.getByRole("button", { name: "audit.firstPage" })).toBeDisabled();

    const pageInput = screen.getByRole("textbox", { name: "audit.pageNumber" });
    await user.clear(pageInput);
    await user.type(pageInput, "999{Enter}");
    // 1234 条 / 每页 20 = 62 页，越界钳到最后一页
    await waitFor(() => expect(lastQuery()).toMatchObject({ offset: 61 * 20, limit: 20 }));
    expect(pageInput).toHaveValue("62");

    await user.click(screen.getByRole("button", { name: "audit.firstPage" }));
    await waitFor(() => expect(lastQuery().offset).toBe(0));
    await user.click(screen.getByRole("button", { name: "audit.lastPage" }));
    await waitFor(() => expect(lastQuery().offset).toBe(61 * 20));
  });

  it("changing the page size goes back to the first page", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 1234 } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await screen.findByText("cmd-1");
    await user.click(screen.getByRole("button", { name: "audit.nextPage" }));
    await waitFor(() => expect(lastQuery().offset).toBe(20));

    await user.click(screen.getByRole("combobox", { name: "audit.pageSize" }));
    // 选项依次是 20 / 50 / 100 条每页
    await user.click((await screen.findAllByRole("option", { name: "audit.perPage" }))[1]);
    await waitFor(() => expect(lastQuery()).toMatchObject({ offset: 0, limit: 50 }));
  });

  it("keeps the current rows dimmed with a progress bar while the next query runs", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValueOnce({ items: [log(1)], total: 1 } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    const row = (await screen.findByText("cmd-1")).closest("tr") as HTMLTableRowElement;

    const pending = deferred<unknown>();
    vi.mocked(ListAuditLogs).mockReturnValueOnce(pending.promise as never);
    await user.click(within(row).getByRole("button", { name: "exec" }));

    expect(screen.getByText("cmd-1")).toBeInTheDocument();
    expect(row.closest("tbody")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("progressbar")).toBeInTheDocument();

    pending.resolve({ items: [log(2)], total: 1 });
    expect(await screen.findByText("cmd-2")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("never lets an earlier query that answers late replace the latest result", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValueOnce({ items: [log(1)], total: 1 } as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    const row = (await screen.findByText("cmd-1")).closest("tr") as HTMLTableRowElement;

    const slow = deferred<unknown>();
    const fast = deferred<unknown>();
    vi.mocked(ListAuditLogs)
      .mockReturnValueOnce(slow.promise as never)
      .mockReturnValueOnce(fast.promise as never);
    await user.click(within(row).getByRole("button", { name: "exec" }));
    await user.click(within(row).getByRole("button", { name: "audit.sources.opsctl" }));

    fast.resolve({ items: [log(3, { Command: "latest" })], total: 1 });
    expect(await screen.findByText("latest")).toBeInTheDocument();
    slow.resolve({ items: [log(2, { Command: "stale" })], total: 1 });
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByText("stale")).not.toBeInTheDocument();
    expect(screen.getByText("latest")).toBeInTheDocument();
  });

  it("summarises every grant of the selected session, not just the current page", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 1 } as never);
    vi.mocked(ListAuditSessionGrants).mockResolvedValue([
      { asset_id: 9, asset_name: "web-01", patterns: "systemctl status *", createtime: 1 },
      { asset_id: 12, asset_name: "db-01", patterns: "SELECT *", createtime: 2 },
    ] as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByText("cmd-1"));
    await user.click(
      within(screen.getByRole("complementary", { name: "audit.detail" })).getByRole("button", { name: "s-1" })
    );

    const grants = await screen.findByTestId("audit-session-grants");
    expect(ListAuditSessionGrants).toHaveBeenLastCalledWith("s-1");
    expect(grants).toHaveTextContent("web-01:systemctl status *");
    expect(grants).toHaveTextContent("db-01:SELECT *");
  });

  it("shows a retry when the session grants fail to load", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [log(1)], total: 1 } as never);
    vi.mocked(ListAuditSessionGrants).mockRejectedValueOnce(new Error("grants down"));
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByText("cmd-1"));
    await user.click(
      within(screen.getByRole("complementary", { name: "audit.detail" })).getByRole("button", { name: "s-1" })
    );

    expect(await screen.findByText(/grants down/)).toBeInTheDocument();
    vi.mocked(ListAuditSessionGrants).mockResolvedValue([
      { asset_id: 9, asset_name: "web-01", patterns: "uptime", createtime: 1 },
    ] as never);
    await user.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByTestId("audit-session-grants")).toHaveTextContent("uptime");
  });

  it("names the pooled assets, and marks ones that no longer exist", async () => {
    vi.mocked(ListAuditLogs).mockResolvedValue({ items: [], total: 0 } as never);
    useAssetStore.setState({
      assets: [{ ID: 11, Name: "web-a", Type: "ssh", GroupID: 0, Status: 1 }],
      groups: [],
    } as unknown as Parameters<typeof useAssetStore.setState>[0]);
    vi.mocked(GetSSHPoolConnections).mockResolvedValue([
      { asset_id: 11, ref_count: 2, last_used: 1 },
      { asset_id: 99, ref_count: 0, last_used: 1 },
    ] as never);
    const user = userEvent.setup();
    render(<AuditLogPage />);
    await user.click(await screen.findByRole("tab", { name: /audit.tabPool/ }));

    const live = (await screen.findByText("web-a")).closest("tr") as HTMLTableRowElement;
    expect(live).toHaveTextContent("#11");
    const gone = screen.getByText("audit.poolDeletedAsset").closest("tr") as HTMLTableRowElement;
    expect(gone).toHaveTextContent("#99");
  });
});
