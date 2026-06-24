import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { K8sConfigSection } from "@/components/asset/K8sConfigSection";

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("K8sConfigSection tabs", () => {
  it("renders connection + tunnel tabs", () => {
    render(<K8sConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
  });
});
