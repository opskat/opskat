import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, render, screen, fireEvent, within } from "@testing-library/react";
import { PolicyGroupManager } from "@/components/asset/PolicyGroupManager";
import { ListPolicyGroups } from "../../wailsjs/go/system/System";

// The manager only exposed a fixed set of built-in tabs (SSH/Database/Redis/MongoDB/Kafka/etcd);
// OSS had no tab, so there was no way to reach the "Create" flow for an OSS policy group at all.
describe("PolicyGroupManager OSS support", () => {
  beforeEach(() => {
    vi.mocked(ListPolicyGroups).mockResolvedValue([] as never);
  });

  it("offers an OSS tab", async () => {
    render(<PolicyGroupManager open onOpenChange={() => {}} />);

    // react-i18next is mocked to identity in tests, so the tab (which reuses the existing
    // asset.typeOSS key — the same one the asset-type picker already uses for "OSS", per the
    // "query" tab's labelKey precedent) renders the key itself rather than translated text.
    expect(await screen.findByText("asset.typeOSS")).toBeInTheDocument();
  });

  // react-i18next is mocked to identity, so the rendered label text equals the i18n key asked for.
  // The three rows cover the whole policyType→label lookup: the new entry (oss), the pre-existing
  // per-type entry the lookup replaced a ternary for (kafka), and the generic fallback every other
  // list-shaped tab still uses (command). Any of them regresses if the table is edited carelessly;
  // "oss" additionally fails if the tab were given the query shape (allow_types/deny_types, which
  // has no *PolicyAllowList label at all).
  it.each([
    ["oss", "asset.ossPolicyAllowList", "asset.ossPolicyDenyList"],
    ["kafka", "asset.kafkaPolicyAllowList", "asset.kafkaPolicyDenyList"],
    ["command", "asset.cmdPolicyAllowList", "asset.cmdPolicyDenyList"],
  ])("creating a group under the %s tab uses that type's allow/deny labels", async (tab, allowLabel, denyLabel) => {
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab={tab} />);

    fireEvent.click(await screen.findByText("asset.policyGroup.create"));

    expect(screen.getByText(allowLabel)).toBeInTheDocument();
    expect(screen.getByText(denyLabel)).toBeInTheDocument();
  });
});

describe("PolicyGroupManager load and delete", () => {
  it("shows the failure reason with retry instead of the empty hint when the list fails to load", async () => {
    vi.mocked(ListPolicyGroups).mockRejectedValue(new Error("policy db down"));
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab="command" />);

    expect(await screen.findByText(/policy db down/)).toBeInTheDocument();
    expect(screen.queryByText("asset.policyGroup.empty")).not.toBeInTheDocument();

    vi.mocked(ListPolicyGroups).mockResolvedValue([] as never);
    fireEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("asset.policyGroup.empty")).toBeInTheDocument();
  });

  it("ignores the previous tab's list when it answers after the current tab's", async () => {
    const answers: Record<string, (v: unknown) => void> = {};
    vi.mocked(ListPolicyGroups).mockImplementation(
      (policyType: string) => new Promise((resolve) => (answers[policyType] = resolve)) as never
    );
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab="command" />);
    await vi.waitFor(() => expect(answers.command).toBeDefined());
    fireEvent.click(screen.getByText("asset.typeOSS"));
    await vi.waitFor(() => expect(answers.oss).toBeDefined());

    await act(async () =>
      answers.oss([{ id: "custom:oss", name: "oss-group", description: "", builtin: false, policyType: "oss" }])
    );
    await act(async () =>
      answers.command([{ id: "custom:cmd", name: "cmd-group", description: "", builtin: false, policyType: "command" }])
    );

    expect(screen.getByText("oss-group")).toBeInTheDocument();
    expect(screen.queryByText("cmd-group")).toBeNull();
    expect(screen.queryByTestId("async-state-loading")).toBeNull();
  });

  it("shows loading, not the empty hint, before the list returns", async () => {
    vi.mocked(ListPolicyGroups).mockReturnValue(new Promise(() => {}));
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab="command" />);
    expect(await screen.findByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("asset.policyGroup.empty")).not.toBeInTheDocument();
  });

  it("labels the delete confirmation button with the delete verb", async () => {
    vi.mocked(ListPolicyGroups).mockResolvedValue([
      { id: "custom:1", name: "mine", description: "", builtin: false, policyType: "command", policy: "{}" },
    ] as never);
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab="command" />);
    const row = (await screen.findByText("mine")).closest("div.cursor-pointer") as HTMLElement;
    fireEvent.click(within(row).getByRole("button"));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByRole("button", { name: "action.delete" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "action.confirm" })).toBeNull();
  });
});
