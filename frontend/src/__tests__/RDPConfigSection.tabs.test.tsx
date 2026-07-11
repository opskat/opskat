import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RDPConfigSection } from "@/components/asset/RDPConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("RDPConfigSection tabs", () => {
  it("keeps advanced settings separate without exposing ineffective resolution fields", async () => {
    render(<RDPConfigSection ctx={ctx} onValidityChange={vi.fn()} />);

    expect(screen.getByTestId("config-tab-advanced")).toHaveTextContent("asset.tabAdvanced");
    expect(screen.queryByTestId("config-tab-display")).not.toBeInTheDocument();

    await userEvent.click(screen.getByTestId("config-tab-advanced"));
    expect(screen.queryByTestId("rdp-width-input")).not.toBeInTheDocument();
    expect(screen.queryByTestId("rdp-height-input")).not.toBeInTheDocument();
    expect(screen.getByText("asset.rdpClipboardSync")).toBeInTheDocument();
  });

  it("exposes a tunnel tab for SSH tunnel / SOCKS5 proxy", () => {
    render(<RDPConfigSection ctx={ctx} onValidityChange={vi.fn()} />);

    expect(screen.getByTestId("config-tab-tunnel")).toHaveTextContent("asset.tabTunnel");
  });
});
