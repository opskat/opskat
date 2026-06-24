import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("EtcdConfigSection tabs", () => {
  it("renders connection / tunnel / tls tabs (no advanced)", () => {
    render(<EtcdConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-advanced")).not.toBeInTheDocument();
  });
});
