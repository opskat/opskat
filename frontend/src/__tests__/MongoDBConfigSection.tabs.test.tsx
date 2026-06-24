import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MongoDBConfigSection } from "@/components/asset/MongoDBConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("MongoDBConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", () => {
    render(<MongoDBConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
