import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CommandPolicyCard } from "@/components/asset/CommandPolicyCard";

function renderCard(props: { groupID?: number }) {
  const onReset = vi.fn();
  render(
    <CommandPolicyCard
      title="t"
      policyType="ssh"
      lists={[]}
      buildPolicyJSON={() => "{}"}
      onReset={onReset}
      {...props}
    />
  );
  return { onReset };
}

describe("CommandPolicyCard reset confirmation", () => {
  it("labels the confirm button with the reset verb for an asset policy and resets on confirm", async () => {
    const { onReset } = renderCard({});
    await userEvent.click(screen.getByRole("button", { name: "asset.policyReset.default" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).queryByRole("button", { name: "action.confirm" })).toBeNull();
    await userEvent.click(within(dialog).getByRole("button", { name: "asset.policyReset.default" }));
    expect(onReset).toHaveBeenCalledTimes(1);
  });

  it("labels the confirm button with the clear verb for a group policy", async () => {
    renderCard({ groupID: 3 });
    await userEvent.click(screen.getByRole("button", { name: "asset.policyReset.clear" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByRole("button", { name: "asset.policyReset.clear" })).toBeInTheDocument();
  });
});
