import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Segmented } from "@/components/asset/fields";

const options = [
  { value: "a", label: "Alpha", testid: "seg-a" },
  { value: "b", label: "Beta", testid: "seg-b" },
] as const;

describe("Segmented", () => {
  it("marks the active option as checked", () => {
    render(<Segmented value="a" onChange={() => {}} options={[...options]} />);
    expect(screen.getByTestId("seg-a")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("seg-b")).toHaveAttribute("aria-checked", "false");
  });

  it("calls onChange with the clicked option value", async () => {
    const onChange = vi.fn();
    render(<Segmented value="a" onChange={onChange} options={[...options]} />);
    await userEvent.click(screen.getByTestId("seg-b"));
    expect(onChange).toHaveBeenCalledWith("b");
  });
});
