import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { K8sCodeBlock } from "./K8sCodeBlock";

describe("K8sCodeBlock", () => {
  it("lets users select code while keeping the collapse control non-selectable", () => {
    render(<K8sCodeBlock code={"kind: Pod"} title="Manifest" />);

    expect(screen.getByText("kind: Pod")).toHaveClass("select-text");
    expect(screen.getByRole("button", { name: "Manifest" })).not.toHaveClass("select-text");
  });
});
