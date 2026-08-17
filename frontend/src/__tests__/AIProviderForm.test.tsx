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
    await waitFor(() =>
      expect(FetchAIModels).toHaveBeenCalledWith("openai", "https://api.openai.com/v1", ORIGINAL_KEY)
    );
  });

  it("save 把同一原始 apiKey 传给 onSave", async () => {
    const user = userEvent.setup();
    const { onSave } = renderForm();
    await user.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].apiKey).toBe(ORIGINAL_KEY);
  });
});
