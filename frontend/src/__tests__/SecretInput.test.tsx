import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SecretInput } from "@/components/SecretInput";

function renderInput(props: Partial<React.ComponentProps<typeof SecretInput>> = {}) {
  return render(<SecretInput value="s3cr3t" onChange={() => {}} aria-label="Secret" {...props} />);
}

describe("SecretInput", () => {
  it("默认隐藏：type=password 且保留原值", () => {
    renderInput();
    const input = screen.getByLabelText("Secret") as HTMLInputElement;
    expect(input.type).toBe("password");
    expect(input.value).toBe("s3cr3t");
  });

  it("点击眼睛显示同一原值（type=text，value 不变，label 切为隐藏）", async () => {
    const user = userEvent.setup();
    renderInput();
    const input = screen.getByLabelText("Secret") as HTMLInputElement;
    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    expect(input.type).toBe("text");
    expect(input.value).toBe("s3cr3t");
    expect(screen.getByRole("button", { name: "action.hideSecret" })).toBeInTheDocument();
  });

  it("再次点击眼睛恢复隐藏", async () => {
    const user = userEvent.setup();
    renderInput();
    const input = screen.getByLabelText("Secret") as HTMLInputElement;
    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    await user.click(screen.getByRole("button", { name: "action.hideSecret" }));
    expect(input.type).toBe("password");
    expect(input.value).toBe("s3cr3t");
  });

  it("眼睛按钮可访问：有可访问名称且 aria-pressed 随状态切换", async () => {
    const user = userEvent.setup();
    renderInput();
    const btn = screen.getByRole("button", { name: "action.showSecret" });
    expect(btn).toHaveAttribute("aria-pressed", "false");
    await user.click(btn);
    expect(screen.getByRole("button", { name: "action.hideSecret" })).toHaveAttribute("aria-pressed", "true");
  });

  it("支持现有 Input props：placeholder / disabled / className / autoComplete / onChange", () => {
    const onChange = vi.fn();
    renderInput({ placeholder: "p", disabled: true, className: "custom-cls", autoComplete: "new-password", onChange });
    const input = screen.getByLabelText("Secret") as HTMLInputElement;
    expect(input).toHaveAttribute("placeholder", "p");
    expect(input).toBeDisabled();
    expect(input).toHaveClass("custom-cls");
    expect(input).toHaveAttribute("autocomplete", "new-password");
    expect(input.type).toBe("password");
  });

  it("右侧附加 action 渲染并可点击，不影响眼睛切换", async () => {
    const user = userEvent.setup();
    const onAction = vi.fn();
    renderInput({
      actions: (
        <button type="button" onClick={onAction}>
          Generate
        </button>
      ),
    });
    await user.click(screen.getByRole("button", { name: "Generate" }));
    expect(onAction).toHaveBeenCalledTimes(1);
    // 眼睛按钮仍然可用
    const input = screen.getByLabelText("Secret") as HTMLInputElement;
    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    expect(input.type).toBe("text");
  });

  it("受控模式：onRevealChange 收到切换后的布尔值", async () => {
    const user = userEvent.setup();
    const onRevealChange = vi.fn();
    renderInput({ reveal: false, onRevealChange });
    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    expect(onRevealChange).toHaveBeenCalledWith(true);
  });

  it("reveal 属性单独传入时决定只读显示态", () => {
    renderInput({ reveal: true });
    expect(screen.getByLabelText("Secret")).toHaveAttribute("type", "text");
    expect(screen.getByRole("button", { name: "action.hideSecret" })).toBeDisabled();
  });

  it("只监听 onRevealChange 时保留内部切换行为", async () => {
    const user = userEvent.setup();
    const onRevealChange = vi.fn();
    renderInput({ onRevealChange });
    const input = screen.getByLabelText("Secret");

    await user.click(screen.getByRole("button", { name: "action.showSecret" }));

    expect(onRevealChange).toHaveBeenCalledWith(true);
    expect(input).toHaveAttribute("type", "text");
  });

  it("revealLoading 时显示加载态且不触发切换", async () => {
    const user = userEvent.setup();
    const onRevealChange = vi.fn();
    renderInput({ reveal: false, onRevealChange, revealLoading: true });
    await user.click(screen.getByRole("button", { name: "action.showSecret" }));
    expect(onRevealChange).not.toHaveBeenCalled();
  });
});
