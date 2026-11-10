import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";
import type { AssetFormContext } from "@/lib/assetTypes/formContract";
import { ListLocalShells } from "../../../../wailsjs/go/local/Local";

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => p };

function renderSection() {
  return render(<LocalConfigSection ref={createRef()} ctx={ctx} onValidityChange={() => {}} />);
}

describe("LocalConfigSection shell preset load", () => {
  it("shows the failure reason with retry when the shell list fails to load, and the free-form shell input stays usable", async () => {
    vi.mocked(ListLocalShells).mockRejectedValueOnce(new Error("no shells db"));
    renderSection();
    expect(await screen.findByText(/no shells db/)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("asset.localShellPlaceholder")).toBeEnabled();

    vi.mocked(ListLocalShells).mockResolvedValueOnce([]);
    await userEvent.click(screen.getByTestId("async-state-retry"));
    await screen.findByText("asset.localShellPreset");
    expect(screen.queryByText(/no shells db/)).not.toBeInTheDocument();
  });
});
