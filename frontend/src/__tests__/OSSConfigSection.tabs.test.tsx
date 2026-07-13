import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { OSSConfigSection } from "@/components/asset/OSSConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("OSSConfigSection tabs", () => {
  it("renders connection / tunnel / advanced tabs (no tls)", () => {
    render(<OSSConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-tls")).not.toBeInTheDocument();
  });
});
