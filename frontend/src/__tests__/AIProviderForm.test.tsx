import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AIProviderForm, type AIProviderFormProps } from "@/components/ai/AIProviderForm";
import { FetchAIModels } from "../../wailsjs/go/ai/AI";
import { ai } from "../../wailsjs/go/models";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}));

// spec Decision 7（AI Provider DTO/form 测试行）：表单只有 apiKey，默认视觉隐藏，
// 眼睛展示/隐藏同一原值，fetch/save 均使用该原值；不生成 masked 副本、不意外清空。
const ORIGINAL_KEY = "sk-abc1234567890secretXYZ";

const baseInitial = {
  name: "test",
  type: "openai",
  apiBase: "https://api.openai.com/v1",
  apiKey: ORIGINAL_KEY,
  model: "gpt-4o",
  maxOutputTokens: 0,
  contextWindow: 0,
  reasoningEffort: "none" as const,
};

function renderForm(props: Partial<AIProviderFormProps> = {}) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<AIProviderForm initialValues={baseInitial} onSave={onSave} showTypeSelector={false} {...props} />);
  return { onSave };
}

describe("AIProviderForm apiKey", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(FetchAIModels).mockResolvedValue([]);
  });

  it("编辑默认隐藏：type=password 且保留返回的原始 apiKey", () => {
    renderForm();
    const input = screen.getByLabelText("settings.apiKey") as HTMLInputElement;
    expect(input.type).toBe("password");
    expect(input.value).toBe(ORIGINAL_KEY);
  });

  it("眼睛切换显示/隐藏同一原值，不生成 masked 副本也不清空", async () => {
    const user = userEvent.setup();
    renderForm();
    const input = screen.getByLabelText("settings.apiKey") as HTMLInputElement;

    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    expect(input.type).toBe("text");
    expect(input.value).toBe(ORIGINAL_KEY);

    await user.click(screen.getByRole("button", { name: "action.hideSecret" }));
    expect(input.type).toBe("password");
    expect(input.value).toBe(ORIGINAL_KEY);
  });

  it("fetch 模型使用当前表单持有的同一原始值", async () => {
    const user = userEvent.setup();
    vi.mocked(FetchAIModels).mockResolvedValue([
      ai.AIModelInfo.createFrom({ id: "gpt-4o", maxOutputTokens: 100, contextWindow: 200 }),
    ]);
    renderForm();
    await user.click(screen.getByRole("button", { name: "settings.fetchModels" }));
    await waitFor(() => expect(FetchAIModels).toHaveBeenCalledTimes(1));
    expect(vi.mocked(FetchAIModels).mock.calls[0][0]).toMatchObject({
      type: "openai",
      apiBase: "https://api.openai.com/v1",
      apiKey: ORIGINAL_KEY,
    });
  });

  it("save 把同一原始 apiKey 传给 onSave", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm();
    await user.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].apiKey).toBe(ORIGINAL_KEY);
  });
});

describe("AIProviderForm 自定义请求头", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(FetchAIModels).mockResolvedValue([]);
  });

  function renderWithHeaders(extraHeaders: { name: string; value: string }[]) {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <AIProviderForm initialValues={{ ...baseInitial, extraHeaders }} onSave={onSave} showTypeSelector={false} />
    );
    return { onSave };
  }

  it("未配置时折叠且不显示计数", () => {
    renderWithHeaders([]);
    const toggle = screen.getByRole("button", { name: /settings.extraHeaders/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByLabelText("settings.extraHeaderName")).not.toBeInTheDocument();
  });

  it("折叠态显示已配置条目数", () => {
    renderWithHeaders([
      { name: "x-opencode-session", value: "{{session}}" },
      { name: "X-Trace", value: "t-9" },
    ]);
    expect(screen.getByTestId("extra-headers-count")).toHaveTextContent("2");
  });

  it("展开后能改值并随保存一起提交", async () => {
    const user = userEvent.setup();
    const { onSave } = renderWithHeaders([{ name: "x-opencode-session", value: "" }]);

    await user.click(screen.getByRole("button", { name: /settings.extraHeaders/ }));
    // userEvent.type 把 "{{" 当转义序列，占位符用 paste 原样送进去
    await user.click(screen.getByLabelText("settings.extraHeaderValue"));
    await user.paste("{{session}}");
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].extraHeaders).toEqual([{ name: "x-opencode-session", value: "{{session}}" }]);
  });

  it("添加与删除行", async () => {
    const user = userEvent.setup();
    const { onSave } = renderWithHeaders([{ name: "X-Keep", value: "1" }]);

    await user.click(screen.getByRole("button", { name: /settings.extraHeaders/ }));
    await user.click(screen.getByRole("button", { name: "settings.extraHeaderAdd" }));
    expect(screen.getAllByLabelText("settings.extraHeaderName")).toHaveLength(2);

    await user.click(screen.getAllByRole("button", { name: "settings.extraHeaderRemove" })[0]);
    await user.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].extraHeaders).toEqual([]);
  });

  it("重名（大小写不敏感）禁用保存，只标后出现的那条", async () => {
    const user = userEvent.setup();
    renderWithHeaders([
      { name: "x-opencode-session", value: "a" },
      { name: "X-Opencode-Session", value: "b" },
    ]);
    await user.click(screen.getByRole("button", { name: /settings.extraHeaders/ }));

    expect(screen.getByRole("button", { name: "action.save" })).toBeDisabled();
    expect(screen.getAllByText("settings.extraHeaderDuplicate")).toHaveLength(1);
  });

  it("OpsKat 自己设置的头禁用保存", async () => {
    const user = userEvent.setup();
    renderWithHeaders([{ name: "Authorization", value: "Bearer x" }]);
    await user.click(screen.getByRole("button", { name: /settings.extraHeaders/ }));

    expect(screen.getByRole("button", { name: "action.save" })).toBeDisabled();
    expect(screen.getByText(/settings.extraHeaderReserved/)).toBeInTheDocument();
  });

  it("名称为空的条目不计入计数也不阻塞保存", async () => {
    const user = userEvent.setup();
    const { onSave } = renderWithHeaders([{ name: "", value: "" }]);
    expect(screen.queryByTestId("extra-headers-count")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
  });
});
