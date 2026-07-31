import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
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
