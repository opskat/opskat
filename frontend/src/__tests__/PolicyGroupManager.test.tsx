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

  it("creating a group under the OSS tab uses the allow/deny object-list shape, not the SQL query shape", async () => {
    render(<PolicyGroupManager open onOpenChange={() => {}} initialTab="oss" />);
    await screen.findByText("asset.typeOSS");

    fireEvent.click(screen.getByText("asset.policyGroup.create"));

    // react-i18next is mocked to identity, so the label text equals the i18n key requested —
    // this fails both if the OSS tab falls back to the generic "cmdPolicy*" labels (pre-fix
    // ternary only special-cased "kafka") and if it were wrongly given the query shape
    // (allow_types/deny_types, which has no *PolicyAllowList label at all).
    expect(screen.getByText("asset.ossPolicyAllowList")).toBeInTheDocument();
    expect(screen.getByText("asset.ossPolicyDenyList")).toBeInTheDocument();
  });
});
