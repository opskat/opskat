import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { ConfirmDialog } from "@opskat/ui";

describe("ConfirmDialog", () => {
  it("renders fallback action labels when button text is omitted", () => {
    render(<ConfirmDialog open onOpenChange={vi.fn()} title="Confirm" description="Continue?" onConfirm={vi.fn()} />);

    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Confirm" })).toBeInTheDocument();
  });
});
