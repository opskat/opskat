import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("SSHConfigSection tabs", () => {
  it("splits into connection + tunnel tabs", () => {
    render(<SSHConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
  });

  it("reports connection group invalid until host filled", async () => {
    const onValidity = vi.fn();
    render(<SSHConfigSection ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, saveDisabledReason: "asset.formMissingHost" })
    );
    await userEvent.type(screen.getByTestId("ssh-host-input"), "example.com");
    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: true }));
  });
});
