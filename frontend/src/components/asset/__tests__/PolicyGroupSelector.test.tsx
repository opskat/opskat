import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PolicyGroupSelector } from "@/components/asset/PolicyGroupSelector";
import { ListPolicyGroups } from "../../../../wailsjs/go/system/System";

function renderSelector() {
  return render(<PolicyGroupSelector policyType="ssh" selectedIds={[]} onChange={() => {}} />);
}

describe("PolicyGroupSelector load states", () => {
  it("shows the failure with retry instead of 'no more groups' when the list fails, and retry recovers", async () => {
    vi.mocked(ListPolicyGroups).mockRejectedValueOnce(new Error("policy db down"));
    renderSelector();
    expect(await screen.findByText(/policy db down/)).toBeInTheDocument();
    expect(screen.queryByText("asset.policyGroup.noMore")).not.toBeInTheDocument();

    vi.mocked(ListPolicyGroups).mockResolvedValueOnce([
      { id: "custom:1", name: "mine", description: "", builtin: false, policyType: "command" },
    ] as never);
    await userEvent.click(screen.getByTestId("async-state-retry"));
    await userEvent.click(await screen.findByTestId("policy-group-add"));
    expect(await screen.findByTestId("policy-group-option-custom:1")).toBeInTheDocument();
  });

  it("shows loading, not 'no more groups', while the list is pending", async () => {
    vi.mocked(ListPolicyGroups).mockReturnValue(new Promise(() => {}));
    renderSelector();
    await userEvent.click(screen.getByTestId("policy-group-add"));
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("asset.policyGroup.noMore")).not.toBeInTheDocument();
  });

  it("shows 'no more groups' after a successful empty load", async () => {
    vi.mocked(ListPolicyGroups).mockResolvedValue([] as never);
    renderSelector();
    await userEvent.click(screen.getByTestId("policy-group-add"));
    expect(await screen.findByText("asset.policyGroup.noMore")).toBeInTheDocument();
  });
});
