import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Sparkline } from "./Sparkline";

describe("Sparkline", () => {
  it("renders a path for >=2 points", () => {
    const { container } = render(<Sparkline values={[1, 4, 2, 8]} />);
    expect(container.querySelectorAll("path").length).toBeGreaterThanOrEqual(1);
    expect(container.querySelector("circle")).not.toBeNull();
  });

  it("renders an empty svg without crashing for <2 points", () => {
    const { container } = render(<Sparkline values={[5]} />);
    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.querySelector("path")).toBeNull();
  });
});
