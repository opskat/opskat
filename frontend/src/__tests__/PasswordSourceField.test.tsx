import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import userEvent, { PointerEventsCheckLevel } from "@testing-library/user-event";
import { toast } from "sonner";
import { PasswordSourceField } from "../components/asset/PasswordSourceField";
import { credential_entity } from "../../wailsjs/go/models";
import { GetAssetPassword } from "../../wailsjs/go/system/System";

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

function makeCred(id: number, username: string): credential_entity.Credential {
  return { id, name: `cred-${id}`, username, type: "password" } as credential_entity.Credential;
}

// Radix Select renders SelectValue as a <span pointer-events:none> inside its trigger,
// so userEvent has to skip its pointer-events check before it can click the trigger.
function renderField(overrides: Partial<React.ComponentProps<typeof PasswordSourceField>> = {}) {
  const user = userEvent.setup({ pointerEventsCheck: PointerEventsCheckLevel.Never });
  const props: React.ComponentProps<typeof PasswordSourceField> = {
    source: "managed",
    onSourceChange: vi.fn(),
    password: "",
    onPasswordChange: vi.fn(),
    credentialId: 0,
    onCredentialIdChange: vi.fn(),
    managedPasswords: [makeCred(1, "alice"), makeCred(2, ""), makeCred(3, "bob")],
    onUsernameChange: vi.fn(),
    ...overrides,
  };
  return { ...render(<PasswordSourceField {...props} />), props, user };
}

describe("PasswordSourceField reveal", () => {
  it("已保存密码解密失败时保留隐藏态并向用户报错", async () => {
    vi.mocked(GetAssetPassword).mockRejectedValueOnce(new Error("keychain unavailable"));
    const { props, user } = renderField({ source: "inline", hasExistingPassword: true, editAssetId: 42 });
    const input = screen.getByDisplayValue("") as HTMLInputElement;

    await user.click(screen.getByRole("button", { name: "action.showSecret" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Error: keychain unavailable"));
    expect(input.type).toBe("password");
    expect(props.onPasswordChange).not.toHaveBeenCalled();
  });
});

describe("PasswordSourceField username 联动", () => {
  it("选中带 username 的密钥 → 触发 onUsernameChange", async () => {
    const { props, user } = renderField();

    await user.click(screen.getByText("asset.selectPasswordPlaceholder"));
    await user.click(screen.getByRole("option", { name: "cred-1 (alice)" }));

    expect(props.onCredentialIdChange).toHaveBeenCalledWith(1);
    expect(props.onUsernameChange).toHaveBeenCalledWith("alice");
  });

  it("选中 username 为空的密钥 → 不触发 onUsernameChange", async () => {
    const { props, user } = renderField();

    await user.click(screen.getByText("asset.selectPasswordPlaceholder"));
    await user.click(screen.getByRole("option", { name: "cred-2" }));

    expect(props.onCredentialIdChange).toHaveBeenCalledWith(2);
    expect(props.onUsernameChange).not.toHaveBeenCalled();
  });

  it("初次挂载（即使 credentialId 已有初值）→ 不触发 onUsernameChange", () => {
    const onUsernameChange = vi.fn();
    renderField({ credentialId: 1, onUsernameChange });
    expect(onUsernameChange).not.toHaveBeenCalled();
  });
});
