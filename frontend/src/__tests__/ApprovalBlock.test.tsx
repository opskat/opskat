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

  it("普通命令审批（kind=single）显示「记住」入口", () => {
    // 与上一条的否定断言配对：若有人把渲染条件误改窄成只剩 local_tool，
    // 这条会先变红——上一条本来就断言它不存在，单靠它捕不住这个回归。
    renderApproval({
      approvalKind: "single",
      approvalItems: [{ type: "exec", asset_id: 1, asset_name: "web-1", command: "ls -la" }],
    });

    expect(screen.getByTestId("ai-approval-remember")).toBeInTheDocument();
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
