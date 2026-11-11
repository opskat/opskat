import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { DebugSection } from "../DebugSection";
import { GetDebugMode } from "../../../../wailsjs/go/system/System";

describe("DebugSection", () => {
  it("reports a failed read of the debug mode and does not offer a switch it cannot trust", async () => {
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => "");
    vi.mocked(GetDebugMode).mockRejectedValueOnce(new Error("config unreadable"));
    render(<DebugSection />);

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("config unreadable"));
    expect(screen.getByRole("switch")).toBeDisabled();
    toastError.mockRestore();
  });

  it("shows the stored debug mode once it is read", async () => {
    vi.mocked(GetDebugMode).mockResolvedValueOnce(true);
    render(<DebugSection />);
    await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
    expect(screen.getByRole("switch")).toBeEnabled();
  });
});
