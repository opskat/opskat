import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { CredentialManager } from "@/components/settings/CredentialManager";
import { ListCredentials, ListAgentSources } from "../../../../wailsjs/go/system/System";
import { credential_entity } from "../../../../wailsjs/go/models";

function renderManager() {
  return render(
    <TooltipProvider>
      <CredentialManager />
    </TooltipProvider>
  );
}

describe("CredentialManager load states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ListAgentSources).mockResolvedValue([]);
  });

  it("shows loading, not the empty state, while the request is pending", async () => {
    vi.mocked(ListCredentials).mockReturnValue(new Promise(() => {}));
    renderManager();
    expect(await screen.findByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("sshKey.empty")).not.toBeInTheDocument();
  });

  it("shows the failure reason and retry instead of the empty state when credentials fail to load", async () => {
    vi.mocked(ListCredentials).mockRejectedValueOnce(new Error("vault locked"));
    renderManager();
    expect(await screen.findByText(/vault locked/)).toBeInTheDocument();
    expect(screen.queryByText("sshKey.empty")).not.toBeInTheDocument();

    vi.mocked(ListCredentials).mockResolvedValue([
      { id: 1, name: "recovered", type: "password", username: "u", description: "" } as credential_entity.Credential,
    ]);
    await userEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("recovered")).toBeInTheDocument();
  });

  it("shows the failure when agent sources fail to load", async () => {
    vi.mocked(ListCredentials).mockResolvedValue([]);
    vi.mocked(ListAgentSources).mockRejectedValueOnce(new Error("agent down"));
    renderManager();
    expect(await screen.findByText(/agent down/)).toBeInTheDocument();
    expect(screen.queryByText("sshKey.empty")).not.toBeInTheDocument();
  });

  it("shows the empty state after a successful empty load", async () => {
    vi.mocked(ListCredentials).mockResolvedValue([]);
    renderManager();
    expect(await screen.findByText("sshKey.empty")).toBeInTheDocument();
  });
});
