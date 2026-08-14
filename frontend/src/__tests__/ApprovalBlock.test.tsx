import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ApprovalBlock } from "../components/approval/ApprovalBlock";
import type { ContentBlock } from "../stores/aiStore";
import { RespondAIApproval } from "../../wailsjs/go/ai/AI";

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

// 一条递归/通配 cp 展开出的批量审批项：每条都是独立主体（D17），detail 是这次传输
// 唯一携带"两端基点"的地方（checkAccessBatch 给每条都填了同一句 "cp src → dst"）。
function batchItems(n: number, detail?: string) {
  return Array.from({ length: n }, (_, i) => ({
    type: "cp",
    asset_id: (i % 2) + 1,
    asset_name: i % 2 === 0 ? "web-01" : "s3-prod",
    command: `/var/log/app-${i}.log`,
    detail,
  }));
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
  beforeEach(() => {
    vi.clearAllMocks();
  });

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

  it("普通命令审批（kind=single）显示「记住」入口", () => {
    // 与上一条的否定断言配对：若有人把渲染条件误改窄成只剩 local_tool，
    // 这条会先变红——上一条本来就断言它不存在，单靠它捕不住这个回归。
    renderApproval({
      approvalKind: "single",
      approvalItems: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "ls -la" }],
    });

    expect(screen.getByTestId("ai-approval-remember")).toBeInTheDocument();
  });

  it("未修改 remember pattern 时不伪造 edited_items，保留后端的系统主体收窄", () => {
    renderApproval({
      approvalKind: "single",
      approvalItems: [{ type: "oss", asset_id: 1, asset_name: "s3-prod", command: "object.read mybucket/secrets*" }],
    });

    fireEvent.click(screen.getByTestId("ai-approval-remember"));
    fireEvent.click(screen.getByTestId("ai-approval-allow-all"));

    const response = vi.mocked(RespondAIApproval).mock.calls[0]?.[1];
    expect(response?.decision).toBe("allowAll");
    expect(response?.edited_items).toBeUndefined();
  });

  it("实际修改 remember pattern 时仍发送完整 edited_items", () => {
    renderApproval({
      approvalKind: "single",
      approvalItems: [{ type: "oss", asset_id: 1, asset_name: "s3-prod", command: "object.read mybucket/secrets*" }],
    });

    fireEvent.click(screen.getByTestId("ai-approval-remember"));
    fireEvent.change(screen.getByPlaceholderText("opsctlApproval.patternPlaceholder"), {
      target: { value: "object.read mybucket/safe/*" },
    });
    fireEvent.click(screen.getByTestId("ai-approval-allow-all"));

    const response = vi.mocked(RespondAIApproval).mock.calls[0]?.[1];
    expect(response?.edited_items).toHaveLength(1);
    expect(response?.edited_items?.[0].command).toBe("object.read mybucket/safe/*");
  });

  it("扩展审批不提供 remember/allowAll", () => {
    renderApproval({
      approvalKind: "extension",
      approvalItems: [
        {
          type: "ext_tool",
          asset_id: 0,
          asset_name: "",
          command: 'oss.delete_objects {"bucket":"prod"}',
        },
      ],
    });

    expect(screen.getByTestId("ai-approval-block")).toHaveAttribute("data-approval-kind", "extension");
    expect(screen.queryByTestId("ai-approval-remember")).not.toBeInTheDocument();
    expect(screen.queryByTestId("ai-approval-allow-all")).not.toBeInTheDocument();
    expect(screen.getByTestId("ai-approval-allow")).toBeInTheDocument();
  });

  it("oss 审批项有自己的徽章图标，不回落到通用的终端图标", () => {
    renderApproval({
      approvalKind: "single",
      approvalItems: [{ type: "oss", asset_id: 1, asset_name: "s3-prod", command: "object.read mybucket/logs/" }],
    });

    const badge = screen.getByText("OSS").closest("span");
    expect(badge).not.toBeNull();
    const svg = badge!.querySelector("svg");
    expect(svg).not.toBeNull();
    // lucide-react 图标统一带 "lucide-<kebab-name>" class；OSS 复用的是 S3 品牌图标（Iconify），
    // 不是这套 lucide 图标里的任何一个，因此不该带这个 class —— 这与它没有落到 Terminal 兜底是同一件事。
    expect(svg!.getAttribute("class") || "").not.toMatch(/lucide/);
  });

  it("删除分组的审批项在徽标行也要显示分组名（没有 asset_name，只有 group_name）", () => {
    renderApproval({
      approvalKind: "delete",
      approvalItems: [
        {
          type: "delete",
          asset_id: 0,
          asset_name: "",
          group_id: 7,
          group_name: "生产环境",
          command: 'delete group "生产环境" (assets move to ungrouped)',
        },
      ],
    });

    // "生产环境" 只在命令文本里出现是不够的——徽标行必须也能查到（同一个名字在文档里出现两次也没关系，
    // getAllByText 至少要命中一次；这里直接用 getByText 加上 selector 限定徽标行更精确会更啰嗦，
    // 用 getAllByText 更贴近"徽标行是否显示得出名字"这个问题本身）。
    const matches = screen.getAllByText("生产环境");
    expect(matches.length).toBeGreaterThanOrEqual(1);
  });

  it("删除审批的不可逆警告无需展开即可见", () => {
    renderApproval({
      approvalKind: "delete",
      approvalItems: [
        {
          type: "delete",
          asset_id: 1,
          asset_name: "web-9",
          command: 'delete asset "web-9" (type=ssh)',
          detail: "此操作不可撤销，连接会被断开。",
        },
      ],
    });

    // 不做任何点击/展开操作，警告文本必须直接可见——藏在一次点击之后就等于没警告。
    expect(screen.getByText("此操作不可撤销，连接会被断开。")).toBeVisible();
  });
});

// 递归/通配 cp 一次性送来上百条 ApprovalItem，原样铺开没法读——超过 10 条时折叠为一行
// 摘要（条数 + 两端基点），展开后仍是全部具体主体。折叠只是呈现，批的还是那 N 条主体（D17），
// 因此只对 kind=batch 生效——grant 的每条都要能编辑，折叠会让人够不着编辑框。
describe("ApprovalBlock 批量审批折叠（kind=batch，D17）", () => {
  it("恰好 10 条不折叠：全部主体直接可见，没有折叠摘要", () => {
    renderApproval({ approvalKind: "batch", approvalItems: batchItems(10) });

    expect(screen.queryByTestId("ai-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-9.log")).toBeVisible();
  });

  it("11 条时折叠为一行摘要（含条数与两端基点），具体主体默认不可见", () => {
    renderApproval({
      approvalKind: "batch",
      approvalItems: batchItems(11, "cp web-01:/var/log → s3-prod:/bucket/logs/"),
    });

    const summary = screen.getByTestId("ai-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "11");
    expect(summary.textContent).toContain("cp web-01:/var/log → s3-prod:/bucket/logs/");
    expect(screen.getByText("/var/log/app-0.log")).not.toBeVisible();
  });

  it("展开折叠摘要后，11 条具体主体全部可见——折叠只是呈现，不是新的授权范围（D17）", () => {
    renderApproval({
      approvalKind: "batch",
      approvalItems: batchItems(11, "cp web-01:/var/log → s3-prod:/bucket/logs/"),
    });

    fireEvent.click(screen.getByTestId("ai-approval-batch-summary"));

    for (let i = 0; i < 11; i++) {
      expect(screen.getByText(`/var/log/app-${i}.log`)).toBeVisible();
    }
  });

  it("200 条（D19 上限）同样折叠，展开后 200 条全部可见，一条不少", () => {
    renderApproval({
      approvalKind: "batch",
      approvalItems: batchItems(200, "cp web-01:/var/log → s3-prod:/bucket/logs/"),
    });

    const summary = screen.getByTestId("ai-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "200");

    fireEvent.click(summary);
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-199.log")).toBeVisible();
  });

  it("1 条批量审批不折叠", () => {
    renderApproval({ approvalKind: "batch", approvalItems: batchItems(1) });

    expect(screen.queryByTestId("ai-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
  });

  it("grant 审批哪怕超过 10 条也不折叠——每条都要能编辑，折叠只对 batch 生效", () => {
    const items = Array.from({ length: 12 }, (_, i) => ({
      type: "exec",
      asset_id: i + 1,
      asset_name: `web-${i}`,
      command: `cat /var/log/app-${i}.log`,
    }));
    renderApproval({ approvalKind: "grant", approvalItems: items });

    expect(screen.queryByTestId("ai-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getAllByDisplayValue(/cat \/var\/log\/app-/)).toHaveLength(12);
  });

  // batch_exec（exec/sql/redis/mongo 混合批）同样是 kind=batch，但 tool_handler_batch.go
  // 建 item 时不填 Detail——它的条目分属不同资产/工具，没有 cp 那种"两端基点"可摘要。
  // 折叠是为 cp 设计的：collapse 掉一句读不出内容的"N 项已折叠"，Approve 按钮却还活着，
  // 比展示 11 条异构命令更危险。detail 是 payload 里现成的、可靠的判据（cp 每条都填
  // 同一句非空摘要，batch_exec 从不填）——不新增字段，只是不再对没有它的 batch 折叠。
  it("batch_exec 没有 detail 摘要，超过 10 条也不折叠——各条目没有可摘要的共同点", () => {
    const items = Array.from({ length: 11 }, (_, i) => ({
      type: i % 2 === 0 ? "exec" : "sql",
      asset_id: i + 1,
      asset_name: `web-${i}`,
      command: `do something ${i}`,
    }));
    renderApproval({ approvalKind: "batch", approvalItems: items });

    expect(screen.queryByTestId("ai-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("do something 0")).toBeVisible();
    expect(screen.getByText("do something 10")).toBeVisible();
  });
});

// 审批主体被后端脱敏（command/detail 含 <redacted>）时，UI 必须隐藏 remember/allowAll
// 与 pattern 编辑器——<redacted> 不能成为授权 pattern，原始秘密也不能经编辑响应回传
// （spec Approval safety / Compatibility）。后端同时拒绝伪造的 allowAll/edited_items。
describe("ApprovalBlock 脱敏主体（spec Approval safety）", () => {
  it("single 脱敏主体不提供「记住」入口，allow-once 仍可用", () => {
    renderApproval({
      approvalKind: "single",
      approvalRedacted: true,
      approvalItems: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "mysql --password=<redacted>" }],
    });

    expect(screen.queryByTestId("ai-approval-remember")).not.toBeInTheDocument();
    expect(screen.queryByTestId("ai-approval-allow-all")).not.toBeInTheDocument();
    expect(screen.getByTestId("ai-approval-allow")).toBeInTheDocument();
    expect(screen.getByTestId("ai-approval-deny")).toBeInTheDocument();
  });

  it("literal <redacted> text does not disable grant controls without the backend flag", () => {
    renderApproval({
      approvalKind: "single",
      approvalRedacted: false,
      approvalItems: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "printf '<redacted>\\n'" }],
    });

    expect(screen.getByTestId("ai-approval-remember")).toBeInTheDocument();
  });

  it("grant 脱敏主体只允许拒绝，不提供编辑框或无效的批准动作", () => {
    renderApproval({
      approvalKind: "grant",
      approvalRedacted: true,
      approvalItems: [
        {
          type: "exec",
          asset_id: 1,
          asset_name: "web-1",
          command: "uptime",
          detail: "-----BEGIN PRIVATE KEY----- <redacted>",
        },
      ],
    });

    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.getByText("uptime")).toBeInTheDocument();
    expect(screen.queryByText("ai.approvalApprove")).not.toBeInTheDocument();
    expect(screen.getByTestId("ai-approval-deny")).toBeInTheDocument();
  });
});
