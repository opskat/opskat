import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetForm } from "@/components/asset/AssetForm";
import { asset_entity } from "../../../../wailsjs/go/models";
import { CreateAsset, GetSSHConnectionSettings, ListAssets } from "../../../../wailsjs/go/system/System";

const mocks = vi.hoisted(() => ({ openSettingsTab: vi.fn() }));

vi.mock("@/stores/tabStore", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/stores/tabStore")>();
  return { ...actual, openSettingsTab: mocks.openSettingsTab };
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(CreateAsset).mockResolvedValue(undefined as never);
  vi.mocked(ListAssets).mockResolvedValue([] as never);
  vi.mocked(GetSSHConnectionSettings).mockResolvedValue(undefined as never);
});

async function typeName(value: string) {
  await userEvent.type(await screen.findByTestId("asset-form-name-input"), value);
}

describe("AssetForm discards edits only after confirmation", () => {
  it("closes at once on Esc, the close button and cancel while nothing was changed", async () => {
    for (const close of [
      () => userEvent.keyboard("{Escape}"),
      () => userEvent.click(screen.getByRole("button", { name: /close/i })),
      () => userEvent.click(screen.getByRole("button", { name: "action.cancel" })),
    ]) {
      const onOpenChange = vi.fn();
      const { unmount } = render(<AssetForm open onOpenChange={onOpenChange} />);
      await screen.findByTestId("asset-form-name-input");
      await close();
      expect(onOpenChange).toHaveBeenCalledWith(false);
      expect(screen.queryByText("asset.discardTitle")).toBeNull();
      unmount();
    }
  });

  it.each([
    ["Esc", () => userEvent.keyboard("{Escape}")],
    ["the close button", () => userEvent.click(screen.getByRole("button", { name: /close/i }))],
    ["cancel", () => userEvent.click(screen.getByRole("button", { name: "action.cancel" }))],
  ])("asks first when %s is used after a change, and keeps the form on 'keep editing'", async (_, close) => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await typeName("prod-web");
    await close();

    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "asset.discardKeepEditing" }));
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByTestId("asset-form-name-input")).toHaveValue("prod-web");

    await close();
    await userEvent.click(await screen.findByRole("button", { name: "asset.discardConfirm" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("treats a changed config field as a change", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.1");
    await userEvent.click(screen.getByRole("button", { name: "action.cancel" }));
    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it.each([
    ["redis", "redis-host-input"],
    ["vnc", "vnc-host-input"],
  ])("treats a changed %s config field as a change", async (type, hostTestId) => {
    const onOpenChange = vi.fn();
    const asset = new asset_entity.Asset({
      ID: 9,
      Name: "cache-01",
      Type: type,
      Config: JSON.stringify({ host: "10.0.0.1", port: type === "redis" ? 6379 : 5900 }),
    });
    render(<AssetForm open onOpenChange={onOpenChange} editAsset={asset} />);
    await userEvent.type(await screen.findByTestId(hostTestId), "0");
    await userEvent.keyboard("{Escape}");
    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it("does not count the global SSH keep-alive default as a change", async () => {
    vi.mocked(GetSSHConnectionSettings).mockResolvedValue({ keepAliveIntervalSeconds: 60 } as never);
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await screen.findByTestId("ssh-host-input");
    await waitFor(() => expect(GetSSHConnectionSettings).toHaveBeenCalled());
    await act(async () => {});
    await userEvent.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(screen.queryByText("asset.discardTitle")).toBeNull();
  });

  it("confirms before 'manage custom types' leaves the form, and only then navigates", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await typeName("prod-web");
    await userEvent.click(screen.getByTestId("asset-type-picker"));
    await userEvent.click(await screen.findByTestId("asset-type-manage-custom"));

    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(mocks.openSettingsTab).not.toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "asset.discardConfirm" }));
    expect(mocks.openSettingsTab).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("leaves straight away through 'manage custom types' when nothing was changed", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await userEvent.click(await screen.findByTestId("asset-type-picker"));
    await userEvent.click(await screen.findByTestId("asset-type-manage-custom"));
    expect(mocks.openSettingsTab).toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe("AssetForm counts credential input as a change", () => {
  const sshAsset = new asset_entity.Asset({
    ID: 7,
    Name: "web-01",
    Type: "ssh",
    Config: JSON.stringify({ host: "10.0.0.1", port: 22, username: "root", auth_type: "password" }),
  });

  it("asks before Esc discards a typed new password", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} editAsset={sshAsset} />);
    await screen.findByTestId("ssh-host-input");
    await userEvent.type(document.querySelector<HTMLInputElement>('input[type="password"]')!, "s3cret");
    await userEvent.keyboard("{Escape}");
    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it("asks when only the password source is switched to a managed credential", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} editAsset={sshAsset} />);
    await screen.findByTestId("ssh-host-input");
    await userEvent.click(screen.getByTestId("password-source-managed"));
    await userEvent.keyboard("{Escape}");
    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it("closes at once when the credential section was only opened", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} editAsset={sshAsset} />);
    await screen.findByTestId("ssh-host-input");
    await userEvent.keyboard("{Escape}");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

describe("AssetForm keeps common fields across a type switch", () => {
  async function switchTo(optionTestId: string) {
    await userEvent.click(screen.getByTestId("asset-type-picker"));
    await userEvent.click(await screen.findByTestId(optionTestId));
  }

  it("carries name, host, port and username to the new type", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await typeName("jump-01");
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.7");
    const port = screen.getByTestId("ssh-port-input");
    await userEvent.clear(port);
    await userEvent.type(port, "2222");
    const username = screen.getByTestId("ssh-username-input");
    await userEvent.clear(username);
    await userEvent.type(username, "ops");

    await switchTo("asset-type-option-rdp");

    expect(await screen.findByTestId("rdp-host-input")).toHaveValue("10.0.0.7");
    expect(screen.getByTestId("rdp-port-input")).toHaveValue(2222);
    expect(screen.getByTestId("rdp-username-input")).toHaveValue("ops");
    expect(screen.getByTestId("asset-form-name-input")).toHaveValue("jump-01");
  });

  it("carries host and username from a redis form to the new type", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await switchTo("asset-type-option-redis");
    await userEvent.type(await screen.findByTestId("redis-host-input"), "10.0.0.9");
    await switchTo("asset-type-option-ssh");
    expect(await screen.findByTestId("ssh-host-input")).toHaveValue("10.0.0.9");
  });

  it("carries host and username from a vnc form to the new type", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await switchTo("asset-type-option-vnc");
    await userEvent.type(await screen.findByTestId("vnc-host-input"), "10.0.0.8");
    await userEvent.type(screen.getByTestId("vnc-username-input"), "viewer");
    await switchTo("asset-type-option-ssh");
    expect(await screen.findByTestId("ssh-host-input")).toHaveValue("10.0.0.8");
    expect(screen.getByTestId("ssh-username-input")).toHaveValue("viewer");
  });

  it("still asks before discarding a host that was carried back to the original type", async () => {
    const onOpenChange = vi.fn();
    render(<AssetForm open onOpenChange={onOpenChange} />);
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.7");
    await switchTo("asset-type-option-rdp");
    await screen.findByTestId("rdp-host-input");
    await switchTo("asset-type-option-ssh");
    expect(await screen.findByTestId("ssh-host-input")).toHaveValue("10.0.0.7");

    await userEvent.keyboard("{Escape}");

    expect(await screen.findByText("asset.discardTitle")).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it("does not show a rejected save's errors on a fresh section after switching back", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await typeName("jump-01");
    const submit = await screen.findByTestId("asset-form-submit");
    await waitFor(() => expect(submit).toBeEnabled());
    await userEvent.click(submit);
    await waitFor(() => expect(screen.getByTestId("ssh-host-input")).toHaveAttribute("aria-invalid", "true"));

    await switchTo("asset-type-option-redis");
    await screen.findByTestId("redis-host-input");
    await switchTo("asset-type-option-ssh");

    expect(await screen.findByTestId("ssh-host-input")).not.toHaveAttribute("aria-invalid");
  });

  it("does not bring back a host the user cleared in a later type", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.7");
    await switchTo("asset-type-option-redis");
    await userEvent.clear(await screen.findByTestId("redis-host-input"));
    await switchTo("asset-type-option-vnc");
    expect(await screen.findByTestId("vnc-host-input")).toHaveValue("");
  });

  it("does not drag the old type's default port along", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.7");
    await switchTo("asset-type-option-rdp");
    expect(await screen.findByTestId("rdp-port-input")).toHaveValue(3389);
  });

  async function saveRdpWithIcon(pickIcon: boolean) {
    render(<AssetForm open onOpenChange={vi.fn()} />);
    await typeName("win-01");
    await userEvent.type(await screen.findByTestId("ssh-host-input"), "10.0.0.7");
    if (pickIcon) {
      await userEvent.click(
        screen.getByTestId("asset-form-dialog").querySelector<HTMLElement>('button[role="combobox"][aria-label]')!
      );
      const grid = await waitFor(() => {
        const buttons = document.querySelectorAll<HTMLButtonElement>("button.h-8.w-8");
        if (buttons.length === 0) throw new Error("icon grid not open");
        return buttons[buttons.length - 1];
      });
      fireEvent.click(grid);
    }
    await switchTo("asset-type-option-rdp");
    await screen.findByTestId("rdp-host-input");
    await userEvent.click(screen.getByTestId("asset-form-submit"));
    await waitFor(() => expect(CreateAsset).toHaveBeenCalledTimes(1));
    return vi.mocked(CreateAsset).mock.calls[0][0].Icon;
  }

  it("keeps a manually chosen icon across the switch but swaps an untouched one for the new type's default", async () => {
    const untouched = await saveRdpWithIcon(false);
    expect(untouched).toBe("monitor-up");
  });

  it("does not replace an icon the user picked", async () => {
    const picked = await saveRdpWithIcon(true);
    expect(picked).not.toBe("monitor-up");
    expect(picked).not.toBe("server");
  });
});

describe("AssetForm copy mode", () => {
  const source = new asset_entity.Asset({
    ID: 0,
    Name: "web-01 - copy",
    Type: "ssh",
    Icon: "server",
    Config: JSON.stringify({ host: "10.0.0.1", port: 22, username: "root", auth_type: "password" }),
  });

  it("shows a copy title with the type still visible, and saves as a new asset", async () => {
    render(<AssetForm open onOpenChange={vi.fn()} editAsset={source} copy />);
    expect(screen.getByRole("heading")).toHaveTextContent("asset.copyTitle");
    expect(screen.getByRole("heading")).not.toHaveTextContent("action.edit");
    expect(screen.getByTestId("asset-type-picker")).toBeDisabled();
    expect(screen.getByTestId("asset-type-picker")).toHaveTextContent("nav.ssh");
    await screen.findByTestId("ssh-host-input");
    await userEvent.click(screen.getByTestId("asset-form-submit"));
    await waitFor(() => expect(CreateAsset).toHaveBeenCalledTimes(1));
  });
});
