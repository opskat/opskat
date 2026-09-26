import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { OpsctlApprovalDialog } from "../components/approval/OpsctlApprovalDialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { RespondOpsctlApproval } from "../../wailsjs/go/opsctl/Opsctl";

// 标题要点名是哪个扩展的页面：插值参数必须可观察，所以本文件的 t 把参数一并渲染出来
// （同 ActiveTasksQuitDialog.test.tsx 的写法），全局 setup 的 t 会把它们丢掉。
vi.mock("react-i18next", () => {
  const t = (key: string, params?: Record<string, unknown>) => (params ? `${key}(${JSON.stringify(params)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return {
    useTranslation: () => ({ t, i18n }),
    initReactI18next: { type: "3rdParty", init: vi.fn() },
  };
});

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

// 后端 internal/app/opsctl 的 awaitSingleApproval 为一次分类过的扩展调用发出的事件形状：
// command 是 exec DSL 原文，remember_pattern 是"始终允许"真正落库的 <action>:<resource>
// （资源已按 grant 的 glob 转义）。
function fireClassifiedApproval(
  handlers: Map<string, (data: unknown) => void>,
  overrides: Record<string, unknown> = {}
) {
  const handler = handlers.get("opsctl:approval");
  if (!handler) throw new Error("opsctl:approval handler not registered");
  act(() => {
    handler({
      confirm_id: "opsctl_ext_1",
      kind: "single",
      type: "esverify",
      asset_id: 3,
      asset_name: "es-logs",
      command: "request --body='' --method=DELETE --path=/logs-app",
      detail: '{\n  "tool": "request"\n}',
      action: "delete",
      resource: "logs-app",
      remember_pattern: "delete:logs-app",
      session_id: "ext_page_run_asset_3",
      source: "extension_page",
      extension: "ES Verify",
      ...overrides,
    });
  });
}

describe("OpsctlApprovalDialog — 分类审批的「记住」编辑器", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("预填并编辑实际落库的 action:resource，而不是命令串", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers);

    fireEvent.click(screen.getByText("opsctlApproval.remember"));

    const input = screen.getByTestId("approval-remember-pattern");
    expect(input).toHaveValue("delete:logs-app");
    expect(screen.queryByDisplayValue(/--method=DELETE/)).not.toBeInTheDocument();
  });

  it("未修改时不发送 edited_items", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers);

    fireEvent.click(screen.getByText("opsctlApproval.remember"));
    fireEvent.click(screen.getByText("opsctlApproval.approve"));

    const response = vi.mocked(RespondOpsctlApproval).mock.calls[0]?.[1];
    expect(response?.decision).toBe("allowAll");
    expect(response?.edited_items).toBeUndefined();
  });

  it("编辑后的 action:resource-glob 原样作为 edited_items 发回", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers);

    fireEvent.click(screen.getByText("opsctlApproval.remember"));
    fireEvent.change(screen.getByTestId("approval-remember-pattern"), { target: { value: "delete:logs-*" } });
    fireEvent.click(screen.getByText("opsctlApproval.approve"));

    const response = vi.mocked(RespondOpsctlApproval).mock.calls[0]?.[1];
    expect(response?.decision).toBe("allowAll");
    expect(response?.edited_items).toHaveLength(1);
    expect(response?.edited_items?.[0].command).toBe("delete:logs-*");
  });

  it.each([
    ["清空", ""],
    ["去掉动作", ":logs-app"],
    ["换成别的动作", "get:logs-app"],
    ["只剩动作、没有资源段", "delete"],
  ])("%s时提示错误且不能提交", (_label, value) => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers);

    fireEvent.click(screen.getByText("opsctlApproval.remember"));
    fireEvent.change(screen.getByTestId("approval-remember-pattern"), { target: { value } });

    expect(screen.getByTestId("approval-remember-pattern-error")).toHaveTextContent(
      'opsctlApproval.classificationPatternActionRequired({"action":"delete"})'
    );
    const approve = screen.getByText("opsctlApproval.approve");
    expect(approve).toBeDisabled();
    fireEvent.click(approve);
    expect(RespondOpsctlApproval).not.toHaveBeenCalled();
  });

  it("未分类的审批仍预填命令串，沿用命令模式编辑器", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    // awaitSingleApproval 的事件是 map 字面量，未分类时 action/resource/remember_pattern
    // 照样发出，只是空串——不是缺省字段。
    fireClassifiedApproval(handlers, {
      type: "exec",
      command: "cat /var/log/app.log",
      action: "",
      resource: "",
      remember_pattern: "",
      source: "opsctl",
      extension: "",
    });

    fireEvent.click(screen.getByText("opsctlApproval.remember"));

    expect(screen.getByTestId("approval-remember-pattern")).toHaveValue("cat /var/log/app.log");
    expect(screen.getByText("opsctlApproval.patternLabel")).toBeInTheDocument();
    fireEvent.change(screen.getByTestId("approval-remember-pattern"), { target: { value: "cat /var/log/*" } });
    expect(screen.queryByTestId("approval-remember-pattern-error")).not.toBeInTheDocument();
  });
});

describe("OpsctlApprovalDialog — 审批来源", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("扩展页面发起的审批标题点名扩展页面，不再是 CLI 审批", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers);

    expect(screen.getByText('opsctlApproval.extensionPageTitle({"extension":"ES Verify"})')).toBeInTheDocument();
    expect(screen.getByText('opsctlApproval.extensionPageDescription({"extension":"ES Verify"})')).toBeInTheDocument();
    expect(screen.queryByText("opsctlApproval.title")).not.toBeInTheDocument();
    expect(screen.queryByText("opsctlApproval.description")).not.toBeInTheDocument();
  });

  it("opsctl 发起的审批保持原标题", () => {
    const handlers = captureHandlers();
    render(<OpsctlApprovalDialog />);
    fireClassifiedApproval(handlers, { source: "opsctl", extension: "" });

    expect(screen.getByText("opsctlApproval.title")).toBeInTheDocument();
    expect(screen.getByText("opsctlApproval.description")).toBeInTheDocument();
  });
});
