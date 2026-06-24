import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DescriptionBar } from "@/components/asset/DescriptionBar";

describe("DescriptionBar", () => {
  it("collapsed when empty, expands on click", async () => {
    render(<DescriptionBar value="" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-add")).toBeInTheDocument();
    expect(screen.queryByTestId("description-textarea")).not.toBeInTheDocument();
    await userEvent.click(screen.getByTestId("description-add"));
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
  });

  it("starts expanded when value present", () => {
    render(<DescriptionBar value="hello" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-textarea")).toHaveValue("hello");
  });

  it("forwards edits via onChange", async () => {
    const onChange = vi.fn();
    render(<DescriptionBar value="" onChange={onChange} />);
    await userEvent.click(screen.getByTestId("description-add"));
    await userEvent.type(screen.getByTestId("description-textarea"), "x");
    expect(onChange).toHaveBeenCalledWith("x");
  });

  it("expands when value arrives after an initial empty render", () => {
    // 模拟 AssetForm 编辑态:先以空 value 挂载,父级 effect 之后才填入内容。
    const { rerender } = render(<DescriptionBar value="" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-add")).toBeInTheDocument();
    rerender(<DescriptionBar value="loaded later" onChange={vi.fn()} />);
    expect(screen.getByTestId("description-textarea")).toHaveValue("loaded later");
    expect(screen.queryByTestId("description-add")).not.toBeInTheDocument();
  });

  it("stays expanded when focused user clears the text mid-edit", async () => {
    function Wrapper() {
      const [val, setVal] = useState("initial text");
      return <DescriptionBar value={val} onChange={setVal} />;
    }
    render(<Wrapper />);
    const textarea = screen.getByTestId("description-textarea");
    await userEvent.click(textarea);
    await userEvent.clear(textarea);
    // After clearing, textarea must still be present — not collapsed to the add button.
    expect(screen.getByTestId("description-textarea")).toBeInTheDocument();
    expect(screen.queryByTestId("description-add")).not.toBeInTheDocument();
  });
});
