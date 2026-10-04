import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { OpsctlApprovalDialog } from "../components/approval/OpsctlApprovalDialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { RespondOpsctlApproval } from "../../wailsjs/go/opsctl/Opsctl";

// opsctl:approval 事件处理器按事件名捕获，测试里直接调用模拟后端 EventsEmit。
function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

function fireSingleApproval(handlers: Map<string, (data: unknown) => void>, overrides: Record<string, unknown> = {}) {
  const handler = handlers.get("opsctl:approval");
  if (!handler) throw new Error("opsctl:approval handler not registered");
  act(() => {
    handler({
      confirm_id: "opsctl_1",
      kind: "single",
      type: "exec",
      asset_id: 1,
      asset_name: "web-1",
      command: "ls -la",
      session_id: "",
      ...overrides,
    });
  });
}

function fireBatchApproval(handlers: Map<string, (data: unknown) => void>, items: unknown[]) {
  const handler = handlers.get("opsctl:batch-approval");
  if (!handler) throw new Error("opsctl:batch-approval handler not registered");
  act(() => {
    handler({ confirm_id: "batch_1", items, session_id: "session-1" });
  });
}

function fireGrantApproval(
  handlers: Map<string, (data: unknown) => void>,
  items: unknown[],
  overrides: Record<string, unknown> = {}
) {
  const handler = handlers.get("opsctl:grant-approval");
  if (!handler) throw new Error("opsctl:grant-approval handler not registered");
  act(() => {
    handler({ session_id: "grant-1", items, description: "批量授权", ...overrides });
  });
}

// 一条递归/通配 opsctl cp 展开出的批量审批项：每条都是独立主体（D17）。detail 是
// 唯一携带"两端基点"的地方——internal/app/opsctl/approval.go 的 handleBatchApproval
// 把 approval.BatchItem.Detail 原样转发进 `opsctl:batch-approval` 事件（cp.go 给每条都填了
// 同一句 "cp src → dst"；batch.go 的 exec/sql/redis 混合批不产出，item.Detail 留空）。
function batchItems(n: number, detail?: string) {
  return Array.from({ length: n }, (_, i) => ({
    type: "cp",
    asset_id: (i % 2) + 1,
    asset_name: i % 2 === 0 ? "web-01" : "s3-prod",
    command: `/var/log/app-${i}.log`,
    detail,
  }));
}

describe("OpsctlApprovalDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("删除审批（后端 type=delete）不提供「记住」入口——删除不可 grant", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, {
      kind: "delete",
      type: "delete",
      command: 'delete asset "web-9" (type=ssh)',
      session_id: "session-1",
    });

    // 「记住」是通往 allowAll 的唯一入口，删除审批必须没有它
    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    // deny/allow 仍然存在
    expect(screen.getByText("opsctlApproval.deny")).toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("普通命令审批（type=exec）仍显示「记住」入口——正向对照", () => {
    // 与上一条的否定断言配对：若有人把 gate 误改窄，这条会先变红。
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type: "exec", session_id: "session-1" });

    expect(screen.getByText("opsctlApproval.remember")).toBeInTheDocument();
  });

  it("命令与详情正文可选中复制", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { command: "uname -a", detail: "target: web-1" });

    expect(screen.getByText("uname -a")).toHaveClass("select-text");
    expect(screen.getByText("target: web-1")).toHaveClass("select-text");
  });

  it("扩展审批（带 action/resource）显示动作、资源，请求详情可折叠", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, {
      type: "acme-store",
      command: "list_objects --bucket=prod",
      action: "object.write",
      resource: "prod-bucket",
      detail: '{\n  "tool": "list_objects"\n}',
    });

    expect(screen.getByText("object.write")).toBeInTheDocument();
    expect(screen.getByText("prod-bucket")).toBeInTheDocument();
    const summary = screen.getByText("ai.approvalRequestDetail");
    expect(summary.closest("details")).not.toBeNull();
  });

  it("普通命令（无 action/resource）不显示动作/资源行，详情不折叠", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { command: "uname -a", detail: "target: web-1" });

    expect(screen.queryByText("ai.approvalActionLabel")).not.toBeInTheDocument();
    expect(screen.queryByText("ai.approvalResourceLabel")).not.toBeInTheDocument();
    expect(screen.queryByText("ai.approvalRequestDetail")).not.toBeInTheDocument();
  });

  it("未修改 remember pattern 时不伪造 edited_items，保留后端的系统主体收窄", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, {
      type: "oss",
      command: "object.read mybucket/secrets*",
      session_id: "session-1",
    });
    fireEvent.click(screen.getByText("opsctlApproval.remember"));
    fireEvent.click(screen.getByText("opsctlApproval.approve"));

    const response = vi.mocked(RespondOpsctlApproval).mock.calls[0]?.[1];
    expect(response?.decision).toBe("allowAll");
    expect(response?.edited_items).toBeUndefined();
  });

  it("实际修改 remember pattern 时仍发送完整 edited_items", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, {
      type: "oss",
      command: "object.read mybucket/secrets*",
      session_id: "session-1",
    });
    fireEvent.click(screen.getByText("opsctlApproval.remember"));
    fireEvent.change(screen.getByPlaceholderText("opsctlApproval.patternPlaceholder"), {
      target: { value: "object.read mybucket/safe/*" },
    });
    fireEvent.click(screen.getByText("opsctlApproval.approve"));

    const response = vi.mocked(RespondOpsctlApproval).mock.calls[0]?.[1];
    expect(response?.edited_items).toHaveLength(1);
    expect(response?.edited_items?.[0].command).toBe("object.read mybucket/safe/*");
  });

  it("扩展审批（type=ext_tool）不提供 remember/allowAll", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "extension", type: "ext_tool", session_id: "session-1" });

    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("普通一次性审批（kind=once）不提供 remember/allowAll", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "once", type: "create", session_id: "session-1" });

    expect(screen.queryByText("opsctlApproval.remember")).not.toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.allow")).toBeInTheDocument();
  });

  it("删除审批的标题与 ApprovalBlock 保持一致的措辞（复用同一 key，不新造文案）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { kind: "delete", type: "delete", session_id: "session-1" });

    expect(screen.getByText("ai.approvalDeleteTitle")).toBeInTheDocument();
  });

  it.each([
    ["delete", "lucide-trash2"],
    ["etcd", "lucide-database"],
    ["k8s", "lucide-boxes"],
    ["cp", "lucide-file-up"],
  ])("TypeBadge 为 type=%s 渲染对应图标（不回落到终端图标）", (type, iconClass) => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type, session_id: "session-1" });

    // Dialog 内容经 Radix portal 挂到 document.body 下，不在 render() 返回的 container 里。
    expect(document.body.querySelector(`.${iconClass}`)).not.toBeNull();
    expect(document.body.querySelector(".lucide-terminal")).toBeNull();
  });

  it("TypeBadge 为 type=oss 渲染专属徽章图标（复用 S3 品牌图标，不回落到终端图标）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireSingleApproval(handlers, { type: "oss", session_id: "session-1" });

    const badge = screen.getByText("OSS").closest("span");
    expect(badge).not.toBeNull();
    const svg = badge!.querySelector("svg");
    expect(svg).not.toBeNull();
    // S3 品牌图标经 Iconify 渲染，不带 lucide-react 统一加的 "lucide-<kebab-name>" class，
    // 这个 class 的缺席就是"没有落到 Terminal 兜底"的可观察信号。
    expect(svg!.getAttribute("class") || "").not.toMatch(/lucide/);
  });
});

// O3：Radix 把 Overlay 与 Content 各包一层 Presence+Portal，退场动画时长不同（overlay 先结束）。
// 队列清空 → open=false → 紧接着第二条审批到达时，若 overlay 已卸载而 content 仍在退场，
// 重新挂载的 overlay 会追加到 body 末尾，排在 content 之后（同为 z-50）盖住新弹窗按钮。
// happy-dom 没有真实 CSS 动画，这里让 getComputedStyle 按 data-state 报告动画名，并手动派发
// animationend，把两个 Presence 的退场时序固定下来。
describe("OpsctlApprovalDialog 背靠背审批（O3）", () => {
  it("overlay 先于 content 退场完毕时紧接第二条审批：overlay 仍在 content 之前，不盖住按钮", () => {
    const realGetComputedStyle = window.getComputedStyle.bind(window);
    vi.spyOn(window, "getComputedStyle").mockImplementation(((el: Element, pseudo?: string | null) => {
      const style = realGetComputedStyle(el, pseudo);
      const slot = el.getAttribute("data-slot");
      if (slot !== "dialog-overlay" && slot !== "dialog-content") return style;
      // Presence 持有这个对象并在之后反复读取，所以动画名必须在读取时按 data-state 现算。
      return new Proxy(style, {
        get: (target, prop) => {
          if (prop !== "animationName") return Reflect.get(target, prop);
          const state = el.getAttribute("data-state");
          return state === "closed" ? "test-out" : state === "open" ? "test-in" : "none";
        },
      });
    }) as typeof window.getComputedStyle);

    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    const overlay = () => document.body.querySelector('[data-slot="dialog-overlay"]');

    fireSingleApproval(handlers, { confirm_id: "opsctl_1", command: "create asset" });
    fireEvent.click(screen.getByTestId("opsctl-approval-allow"));
    // overlay 的退场先结束（若实现让它还挂着退场）；第二条审批在 content 仍存活时到达
    const leaving = overlay();
    if (leaving) {
      act(() => {
        fireEvent.animationEnd(leaving, { animationName: "test-out" });
      });
    }

    fireSingleApproval(handlers, { confirm_id: "opsctl_2", command: "exec ls" });

    const nodes = Array.from(
      document.body.querySelectorAll('[data-slot="dialog-overlay"],[data-slot="dialog-content"]')
    );
    const overlays = nodes.filter((n) => n.getAttribute("data-slot") === "dialog-overlay");
    const contents = nodes.filter((n) => n.getAttribute("data-slot") === "dialog-content");
    expect(overlays).toHaveLength(1);
    expect(contents).toHaveLength(1);
    // 后来的同级同 z-index 盖在前面的上方：overlay 必须排在 content 之前
    expect(overlays[0].compareDocumentPosition(contents[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByText("exec ls")).toBeInTheDocument();
    vi.restoreAllMocks();
  });
});

// 递归/通配 cp 一次性送来上百条 ApprovalItem，原样铺开没法读——超过 10 条时折叠为一行
// 摘要，展开后仍是全部具体主体。折叠只是呈现，批的还是那 N 条主体（D17）。
describe("OpsctlApprovalDialog 批量审批折叠（kind=batch，D17）", () => {
  it("恰好 10 条不折叠：全部主体直接可见，没有折叠摘要", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(10));

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-9.log")).toBeVisible();
  });

  it("11 条时折叠为一行摘要（含条数与两端基点），具体主体默认不可见", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(11, "cp web-01:/var/log → s3-prod:/bucket/logs/"));

    const summary = screen.getByTestId("opsctl-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "11");
    expect(summary.textContent).toContain("cp web-01:/var/log → s3-prod:/bucket/logs/");
    expect(screen.getByText("/var/log/app-0.log")).not.toBeVisible();
  });

  it("展开折叠摘要后，11 条具体主体全部可见——折叠只是呈现，不是新的授权范围（D17）", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(11, "cp web-01:/var/log → s3-prod:/bucket/logs/"));

    fireEvent.click(screen.getByTestId("opsctl-approval-batch-summary"));

    for (let i = 0; i < 11; i++) {
      expect(screen.getByText(`/var/log/app-${i}.log`)).toBeVisible();
    }
  });

  it("200 条（D19 上限）同样折叠，展开后 200 条全部可见，一条不少", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(200, "cp web-01:/var/log → s3-prod:/bucket/logs/"));

    const summary = screen.getByTestId("opsctl-approval-batch-summary");
    expect(summary).toHaveAttribute("data-count", "200");

    fireEvent.click(summary);
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
    expect(screen.getByText("/var/log/app-199.log")).toBeVisible();
  });

  it("1 条批量审批不折叠", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireBatchApproval(handlers, batchItems(1));

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("/var/log/app-0.log")).toBeVisible();
  });

  // batch verb（exec/sql/redis 混合批）同样走 opsctl:batch-approval，但 cmd/opsctl/command/
  // batch.go 建 BatchItem 时不填 Detail——它的条目分属不同资产/类型，没有 cp 那种"两端基点"
  // 可摘要。折叠是为 cp 设计的：collapse 掉一句读不出内容的"N 项已折叠"，Approve 按钮却还
  // 活着，比展示 11 条异构命令更危险。detail 是 payload 里现成的判据，与 ApprovalBlock 同源。
  it("batch verb 混合批没有 detail 摘要，超过 10 条也不折叠——各条目没有可摘要的共同点", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    const items = Array.from({ length: 11 }, (_, i) => ({
      type: i % 2 === 0 ? "exec" : "sql",
      asset_id: i + 1,
      asset_name: `web-${i}`,
      command: `do something ${i}`,
    }));
    fireBatchApproval(handlers, items);

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText("do something 0")).toBeVisible();
    expect(screen.getByText("do something 10")).toBeVisible();
  });

  // 折叠只对 kind=batch 生效，理由是 grant 的每条都要能编辑。这个对话框正是编辑真的发生的
  // 地方（grant 事件建的队列项 editable=true，每条渲染成一个 Textarea），所以那条守卫必须
  // 在这里也有断言：ApprovalBlock 的同名用例守不住这个组件。
  it("grant 审批哪怕超过 10 条也不折叠——每条都要能编辑", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    fireGrantApproval(
      handlers,
      Array.from({ length: 12 }, (_, i) => ({
        type: "exec",
        asset_id: i + 1,
        asset_name: `web-${i}`,
        command: `cat /var/log/app-${i}.log`,
      }))
    );

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getAllByDisplayValue(/cat \/var\/log\/app-/)).toHaveLength(12);
  });

  it.each([
    ["DELETE /api/dashboards/uid/k8s", "opsctlApproval.genericHttp"],
    ["secret:token", "opsctlApproval.genericSecret"],
    ["aws s3 ls", "opsctlApproval.genericCommand"],
  ])("通用资产审批按匹配对象标出操作类型：%s", (command, label) => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireSingleApproval(handlers, {
      type: "generic",
      asset_name: "grafana-prod",
      command,
      detail: "HTTP request: DELETE https://grafana.internal:3000/api/dashboards/uid/k8s",
    });
    expect(screen.getByTestId("approval-type-badge")).toHaveTextContent(label);
    expect(screen.getByText(/https:\/\/grafana\.internal:3000/)).toBeInTheDocument();
  });

  // opsctl batch 给通用资产条目各自带上渲染后的目标地址（cmd/opsctl/command/batch.go 的
  // batchItemApprovalDetail）；各条目标不同，不能当成 cp 那种共享摘要折叠起来。
  it("批量审批里通用资产条目的目标各异时超过 10 条也不折叠，逐条显示目标", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);

    const items = Array.from({ length: 11 }, (_, i) => ({
      type: "generic",
      asset_id: i + 1,
      asset_name: `grafana-${i}`,
      command: `GET /api/${i}`,
      detail: `HTTP request: GET https://grafana-${i}.internal/api/${i}`,
    }));
    fireBatchApproval(handlers, items);

    expect(screen.queryByTestId("opsctl-approval-batch-summary")).not.toBeInTheDocument();
    expect(screen.getByText(/https:\/\/grafana-0\.internal\/api\/0/)).toBeVisible();
    expect(screen.getByText(/https:\/\/grafana-10\.internal\/api\/10/)).toBeVisible();
  });
});
