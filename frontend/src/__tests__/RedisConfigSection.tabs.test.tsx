import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("RedisConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<RedisConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
