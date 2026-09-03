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

  it("renders block content in the description as valid HTML", () => {
    // `description` is a ReactNode and callers pass lists (e.g. the batch drop-table
    // confirmation). Radix's Description is a <p>, so block children used to make React
    // log "<ul> cannot be a descendant of <p>" on every open.
    const logged: string[] = [];
    const spy = vi.spyOn(console, "error").mockImplementation((...args: unknown[]) => {
      logged.push(args.map(String).join(" "));
    });
    try {
      render(
        <ConfirmDialog
          open
          onOpenChange={vi.fn()}
          title="Confirm"
          description={
            <ul>
              <li>appdb.users</li>
            </ul>
          }
          onConfirm={vi.fn()}
        />
      );
    } finally {
      spy.mockRestore();
    }

    expect(screen.getByText("appdb.users")).toBeInTheDocument();
    expect(logged.filter((msg) => msg.includes("cannot be a descendant of"))).toEqual([]);
  });
});
