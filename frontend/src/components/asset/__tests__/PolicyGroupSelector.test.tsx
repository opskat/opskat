import { describe, it, expect, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
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

  it("ignores an earlier request that answers after a later one", async () => {
    let answerOld!: (v: unknown) => void;
    let answerNew!: (v: unknown) => void;
    vi.mocked(ListPolicyGroups)
      .mockReturnValueOnce(new Promise((r) => (answerOld = r)) as never)
      .mockReturnValueOnce(new Promise((r) => (answerNew = r)) as never);
    const { rerender } = render(
      <PolicyGroupSelector policyType="ssh" selectedIds={[]} onChange={() => {}} refreshKey={0} />
    );
    rerender(<PolicyGroupSelector policyType="ssh" selectedIds={[]} onChange={() => {}} refreshKey={1} />);

    await act(async () =>
      answerNew([{ id: "custom:new", name: "new", description: "", builtin: false, policyType: "command" }])
    );
    await act(async () =>
      answerOld([{ id: "custom:old", name: "old", description: "", builtin: false, policyType: "command" }])
    );
    await userEvent.click(screen.getByTestId("policy-group-add"));

    expect(screen.getByTestId("policy-group-option-custom:new")).toBeInTheDocument();
    expect(screen.queryByTestId("policy-group-option-custom:old")).toBeNull();
    expect(screen.queryByTestId("async-state-loading")).toBeNull();
  });
});
