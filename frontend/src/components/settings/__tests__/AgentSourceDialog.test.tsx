import { describe, it, expect, beforeEach, vi } from "vitest";
import type { ComponentProps } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { AgentSourceDialog } from "@/components/settings/AgentSourceDialog";
import { ProbeAgentSource } from "../../../../wailsjs/go/system/System";

function renderDialog(props: Partial<ComponentProps<typeof AgentSourceDialog>> = {}) {
  const base: ComponentProps<typeof AgentSourceDialog> = {
    open: true,
    mode: "create",
    initial: null,
    onOpenChange: vi.fn(),
    onSaved: vi.fn().mockResolvedValue(undefined),
  };
  return render(
    <TooltipProvider>
      <AgentSourceDialog {...base} {...props} />
    </TooltipProvider>
  );
}

describe("AgentSourceDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ProbeAgentSource).mockResolvedValue({ status: "ok", identity_count: 3 });
  });

  it("keeps save disabled until name and endpoint structure are complete", async () => {
    renderDialog();
    const user = userEvent.setup();
    const save = screen.getByRole("button", { name: "action.save" });
    expect(save).toBeDisabled();

    await user.type(screen.getByLabelText("agentSource.name"), "system");
    // 端点为空的 blank create → 仍禁用
    expect(save).toBeDisabled();

    await user.type(screen.getByLabelText("agentSource.endpointEnv"), "SSH_AUTH_SOCK");
    expect(save).toBeEnabled();
  });

  it("rejects structurally invalid endpoints until fixed", async () => {
    renderDialog();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("agentSource.name"), "x");
    const endpoint = screen.getByLabelText("agentSource.endpointEnv");
    await user.type(endpoint, "1BAD NAME");
    expect(screen.getByRole("button", { name: "action.save" })).toBeDisabled();
    await user.clear(endpoint);
    await user.type(endpoint, "SSH_AUTH_SOCK");
    expect(screen.getByRole("button", { name: "action.save" })).toBeEnabled();
  });

  it("disables a platform-incompatible endpoint type when creating manually", async () => {
    renderDialog();
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox"));
    const option = (await screen.findAllByText("agentSource.kindWindowsNamedPipe"))
      .map((el) => el.closest('[role="option"]'))
      .find(Boolean);
    // 测试环境 platform=other → windows_named_pipe 不兼容
    expect(option).toHaveAttribute("aria-disabled", "true");
    expect(option).toHaveAttribute("data-disabled");
  });

  it("preserves an imported platform-incompatible type when editing", async () => {
    renderDialog({
      mode: "edit",
      initial: {
        name: "win-agent",
        type: "windows_named_pipe",
        endpoint: "\\\\.\\pipe\\openssh-ssh-agent",
        description: "imported",
      },
    });
    expect(screen.getByLabelText("agentSource.name")).toHaveValue("win-agent");
    expect(screen.getByLabelText("agentSource.endpointWindows")).toHaveValue("\\\\.\\pipe\\openssh-ssh-agent");
    const user = userEvent.setup();
    await user.click(screen.getByRole("combobox"));
    const option = (await screen.findAllByText("agentSource.kindWindowsNamedPipe"))
      .map((el) => el.closest('[role="option"]'))
      .find(Boolean);
    expect(option).not.toHaveAttribute("data-disabled");
  });

  it("prefills fields from an initial draft (detected candidate)", () => {
    renderDialog({
      initial: { name: "SSH_AUTH_SOCK", type: "environment", endpoint: "SSH_AUTH_SOCK", description: "" },
    });
    expect(screen.getByLabelText("agentSource.name")).toHaveValue("SSH_AUTH_SOCK");
    expect(screen.getByLabelText("agentSource.endpointEnv")).toHaveValue("SSH_AUTH_SOCK");
  });

  it("probes without persisting and still allows saving after a failed probe", async () => {
    const onSaved = vi.fn().mockResolvedValue(undefined);
    renderDialog({ onSaved });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("agentSource.name"), "my-agent");
    await user.type(screen.getByLabelText("agentSource.endpointEnv"), "SSH_AUTH_SOCK");

    vi.mocked(ProbeAgentSource).mockResolvedValue({ status: "unavailable" });
    await user.click(screen.getByRole("button", { name: "agentSource.test" }));
    await waitFor(() => expect(ProbeAgentSource).toHaveBeenCalledWith("environment", "SSH_AUTH_SOCK"));
    expect(screen.getByText("agentSource.testFail")).toBeInTheDocument();
    // 探测只读，不触发保存
    expect(onSaved).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() =>
      expect(onSaved).toHaveBeenCalledWith({
        name: "my-agent",
        endpoint_type: "environment",
        endpoint: "SSH_AUTH_SOCK",
        description: "",
      })
    );
  });

  it("shows the identity count after a successful probe", async () => {
    renderDialog();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("agentSource.name"), "x");
    await user.type(screen.getByLabelText("agentSource.endpointEnv"), "SSH_AUTH_SOCK");
    await user.click(screen.getByRole("button", { name: "agentSource.test" }));
    await waitFor(() => expect(screen.getByText("agentSource.testSuccess")).toBeInTheDocument());
  });
});
