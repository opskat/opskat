import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("DatabaseConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<DatabaseConfigSection ctx={ctx} onValidityChange={vi.fn()} onIconChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
