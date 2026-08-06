import { describe, it, expect, vi } from "vitest";
import type { ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { AgentSourceRow } from "@/components/settings/AgentSourceRow";
import type { AgentSourceRuntimeStatus } from "@/components/settings/agentSource";
import { system, ssh_agent_svc } from "../../../../wailsjs/go/models";

function makeSource(id = 1, type = "unix_socket"): system.AgentSourceSummary {
  return { id, name: `agent-${id}`, endpoint_type: type, description: "" } as system.AgentSourceSummary;
}

function makeIdentity(
  fp: string,
  overrides: Partial<ssh_agent_svc.IdentitySummary> = {}
): ssh_agent_svc.IdentitySummary {
  return { fingerprint: fp, type: "ssh-ed25519", comment: "work-key", usages: 0, ...overrides };
}

function renderRow(props: Partial<ComponentProps<typeof AgentSourceRow>> = {}) {
  const base: ComponentProps<typeof AgentSourceRow> = {
    source: makeSource(1),
    status: "ok",
    identityCount: 1,
    expanded: false,
    identities: null,
    identitiesLoading: false,
    identitiesError: null,
    onToggle: vi.fn(),
    onRefresh: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    onCopyPublicKey: vi.fn(),
  };
  const handlers = render(
    <TooltipProvider>
      <AgentSourceRow {...base} {...props} />
    </TooltipProvider>
  );
  return { ...handlers, base };
}

describe("AgentSourceRow", () => {
  it("shows name, the SSH Agent type and the endpoint-kind label", () => {
    renderRow({ source: makeSource(1, "environment") });
    expect(screen.getByText("agent-1")).toBeInTheDocument();
    expect(screen.getByText("SSH Agent")).toBeInTheDocument();
    expect(screen.getByText("agentSource.kindEnvironment")).toBeInTheDocument();
  });

  it.each<[AgentSourceRuntimeStatus, string]>([
    ["loading", "agentSource.statusLoading"],
    ["ok", "agentSource.statusOkWithCount"],
    ["empty", "agentSource.statusEmpty"],
    ["unavailable", "agentSource.statusUnavailable"],
    ["unsupported", "agentSource.statusUnsupported"],
  ])("renders a distinct runtime-status label for %s", (status, labelKey) => {
    const { unmount } = renderRow({ status, identityCount: status === "ok" ? 3 : undefined });
    expect(screen.getByText(labelKey)).toBeInTheDocument();
    unmount();
  });

  it("shows the unavailable hint and disables expand when the source is unreachable", () => {
    renderRow({ status: "unavailable" });
    expect(screen.getByText("agentSource.unavailableHint")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "agentSource.expand" })).toBeDisabled();
  });

  it("disables expand while probing or on unsupported platforms", () => {
    const { unmount } = renderRow({ status: "loading" });
    expect(screen.getByRole("button", { name: "agentSource.expand" })).toBeDisabled();
    unmount();
    renderRow({ status: "unsupported" });
    expect(screen.getByRole("button", { name: "agentSource.expand" })).toBeDisabled();
  });

  it("renders expanded identities with a selectable fingerprint and labels", () => {
    renderRow({
      expanded: true,
      identities: [makeIdentity("SHA256:fp1", { type: "ssh-rsa", comment: "home-key", usages: 2 })],
    });
    const fp = screen.getByText("SHA256:fp1");
    expect(fp).toHaveClass("select-text");
    expect(screen.getByText("home-key")).toBeInTheDocument();
    expect(screen.getByText("ssh-rsa")).toBeInTheDocument();
    expect(screen.getByText("agentSource.usage")).toBeInTheDocument();
  });

  it("shows an empty message when a reachable agent has no identities", () => {
    renderRow({ expanded: true, identities: [] });
    expect(screen.getByText("agentSource.emptyIdentities")).toBeInTheDocument();
  });

  it("copies a public key via the per-identity copy button", async () => {
    const onCopy = vi.fn();
    renderRow({
      expanded: true,
      identities: [makeIdentity("SHA256:fp1")],
      onCopyPublicKey: onCopy,
    });
    await userEvent.click(screen.getByRole("button", { name: "agentSource.copyPublicKey" }));
    expect(onCopy).toHaveBeenCalledWith("SHA256:fp1");
  });

  it("gives every icon-only action a localized aria-label", () => {
    renderRow({ expanded: true, identities: [makeIdentity("SHA256:fp1")] });
    expect(screen.getByRole("button", { name: "agentSource.refresh" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "action.edit" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "action.delete" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "agentSource.collapse" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "agentSource.copyPublicKey" })).toBeInTheDocument();
  });
});
