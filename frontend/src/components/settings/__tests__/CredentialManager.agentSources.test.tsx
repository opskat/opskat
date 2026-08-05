import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { CredentialManager } from "@/components/settings/CredentialManager";
import { notifyCopied } from "@/lib/notify";
import {
  ListCredentials,
  ListAgentSources,
  ProbeSavedAgentSource,
  InspectAgentSource,
  CopyAgentSourcePublicKey,
  GetAgentSourceUsage,
  DeleteAgentSource,
  DiscoverAgentSourceCandidates,
  ProbeAgentSource,
  CreateAgentSource,
} from "../../../../wailsjs/go/system/System";
import { credential_entity, system, ssh_agent_svc } from "../../../../wailsjs/go/models";

vi.mock("@/lib/notify", () => ({
  notifyCopied: vi.fn(),
  notifySuccess: vi.fn(),
}));

function rowEl(name: string): HTMLElement {
  const node = screen.getByText(name).closest(".rounded-lg");
  if (!node) throw new Error(`no row found for ${name}`);
  return node as HTMLElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(ListCredentials).mockResolvedValue([
    {
      id: 1,
      name: "prod-password",
      type: "password",
      username: "deploy",
      description: "",
    } as credential_entity.Credential,
    {
      id: 2,
      name: "deploy-key",
      type: "ssh_key",
      keyType: "ed25519",
      fingerprint: "SHA256:abc",
      comment: "deploy@prod",
    } as credential_entity.Credential,
  ]);
  vi.mocked(ListAgentSources).mockResolvedValue([
    { id: 11, name: "1Password · 工作", endpoint_type: "unix_socket", description: "" } as system.AgentSourceSummary,
    { id: 12, name: "系统 SSH Agent", endpoint_type: "environment", description: "" } as system.AgentSourceSummary,
  ]);
  vi.mocked(ProbeSavedAgentSource).mockImplementation(async (id) =>
    id === 11 ? { status: "ok", identity_count: 2 } : { status: "unavailable" }
  );
  vi.mocked(InspectAgentSource).mockResolvedValue(
    new ssh_agent_svc.InspectResult({
      source_id: 11,
      usages: 1,
      identities: [
        { fingerprint: "SHA256:fp1", type: "ssh-ed25519", comment: "work", usages: 1 },
        { fingerprint: "SHA256:fp2", type: "ssh-rsa", comment: "home", usages: 0 },
      ],
    })
  );
  vi.mocked(DiscoverAgentSourceCandidates).mockResolvedValue([
    { endpoint_type: "environment", endpoint: "SSH_AUTH_SOCK" } as ssh_agent_svc.Candidate,
  ]);
  vi.mocked(ProbeAgentSource).mockResolvedValue({ status: "ok", identity_count: 2 });
  vi.mocked(CopyAgentSourcePublicKey).mockResolvedValue("ssh-ed25519 AAAA work");
  vi.mocked(GetAgentSourceUsage).mockResolvedValue(0);
  vi.mocked(DeleteAgentSource).mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined), readText: vi.fn() },
  });
});

function renderManager() {
  return render(
    <TooltipProvider>
      <CredentialManager />
    </TooltipProvider>
  );
}

describe("CredentialManager agent sources", () => {
  it("mixes agent sources into the credentials list without a page-level type label", async () => {
    renderManager();
    expect(await screen.findByText("prod-password")).toBeInTheDocument();
    expect(screen.getByText("deploy-key")).toBeInTheDocument();
    expect(screen.getByText("1Password · 工作")).toBeInTheDocument();
    expect(screen.getByText("系统 SSH Agent")).toBeInTheDocument();
    // 混排列表内没有页面级凭据类型标题
    expect(screen.queryByRole("heading")).not.toBeInTheDocument();
    // 顶层"添加 SSH Agent"按钮
    expect(screen.getByRole("button", { name: "agentSource.add" })).toBeInTheDocument();
  });

  it("lazily loads identities only when a source row is expanded", async () => {
    renderManager();
    await screen.findByText("1Password · 工作");
    expect(InspectAgentSource).not.toHaveBeenCalled();

    await userEvent.click(screen.getAllByRole("button", { name: "agentSource.expand" })[0]);
    await waitFor(() => expect(InspectAgentSource).toHaveBeenCalledWith(11));
    expect(await screen.findByText("SHA256:fp1")).toBeInTheDocument();
    expect(screen.getByText("work")).toBeInTheDocument();
    expect(screen.getByText("ssh-ed25519")).toBeInTheDocument();
  });

  it("renders the loaded fingerprint as selectable text", async () => {
    renderManager();
    await screen.findByText("1Password · 工作");
    await userEvent.click(screen.getAllByRole("button", { name: "agentSource.expand" })[0]);
    expect(await screen.findByText("SHA256:fp1")).toHaveClass("select-text");
  });

  it("copies a public key to the clipboard and notifies", async () => {
    renderManager();
    await screen.findByText("1Password · 工作");
    await userEvent.click(screen.getAllByRole("button", { name: "agentSource.expand" })[0]);
    await screen.findByText("SHA256:fp1");
    await userEvent.click(screen.getAllByRole("button", { name: "agentSource.copyPublicKey" })[0]);
    await waitFor(() => expect(CopyAgentSourcePublicKey).toHaveBeenCalledWith(11, "SHA256:fp1"));
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("ssh-ed25519 AAAA work");
    expect(notifyCopied).toHaveBeenCalled();
  });

  it("confirms deletion and reports when the source is referenced by assets", async () => {
    vi.mocked(GetAgentSourceUsage).mockResolvedValue(3);
    renderManager();
    await screen.findByText("1Password · 工作");
    await userEvent.click(within(rowEl("1Password · 工作")).getByRole("button", { name: "action.delete" }));
    await waitFor(() => expect(GetAgentSourceUsage).toHaveBeenCalledWith(11));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/agentSource.deleteConfirmUsage/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: "action.delete" }));
    await waitFor(() => expect(DeleteAgentSource).toHaveBeenCalledWith(11));
  });

  it("opens a blank create dialog from the toolbar Add button", async () => {
    renderManager();
    await userEvent.click(screen.getByRole("button", { name: "agentSource.add" }));
    expect(await screen.findByLabelText("agentSource.name")).toHaveValue("");
    expect(screen.getByLabelText("agentSource.endpointEnv")).toHaveValue("");
    expect(screen.getByRole("button", { name: "action.save" })).toBeDisabled();
  });

  it("prefills the dialog from a detected candidate", async () => {
    renderManager();
    await screen.findByText("agentSource.detected");
    await userEvent.click(screen.getByRole("button", { name: "agentSource.addCandidate" }));
    expect(await screen.findByLabelText("agentSource.name")).toHaveValue("SSH_AUTH_SOCK");
    expect(screen.getByLabelText("agentSource.endpointEnv")).toHaveValue("SSH_AUTH_SOCK");
  });

  it("saves a new source through CreateAgentSource without requiring a probe", async () => {
    vi.mocked(ProbeAgentSource).mockResolvedValue({ status: "unavailable" });
    renderManager();
    await screen.findByText("1Password · 工作");
    await userEvent.click(screen.getByRole("button", { name: "agentSource.add" }));
    await userEvent.type(await screen.findByLabelText("agentSource.name"), "my-agent");
    await userEvent.type(screen.getByLabelText("agentSource.endpointEnv"), "SSH_AUTH_SOCK");

    await userEvent.click(screen.getByRole("button", { name: "agentSource.test" }));
    await screen.findByText("agentSource.testFail");
    expect(CreateAgentSource).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "action.save" }));
    await waitFor(() =>
      expect(CreateAgentSource).toHaveBeenCalledWith({
        name: "my-agent",
        endpoint_type: "environment",
        endpoint: "SSH_AUTH_SOCK",
        description: "",
      })
    );
  });
});
