import { render, fireEvent } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { TreeSelect } from "@opskat/ui";

describe("TreeSelect", () => {
  it("portals its dropdown out of overflow-clipping ancestors", () => {
    const { getByRole, getByPlaceholderText } = render(
      <div data-testid="scroll" style={{ overflow: "hidden", height: 80 }}>
        <TreeSelect
          value={0}
          onValueChange={() => {}}
          nodes={[{ id: 1, label: "Node A" }]}
          placeholder="none"
          searchable
          searchPlaceholder="搜索"
        />
      </div>
    );
    fireEvent.click(getByRole("button"));
    const searchInput = getByPlaceholderText("搜索");
    const scroll = document.querySelector('[data-testid="scroll"]')!;
    // The open dropdown must NOT live inside the overflow-hidden container (else it gets clipped).
    expect(scroll.contains(searchInput)).toBe(false);
    expect(document.body.contains(searchInput)).toBe(true);
  });

  it("drives its own wheel scrolling so the list scrolls even under a dialog scroll-lock", () => {
    const nodes = Array.from({ length: 40 }, (_, i) => ({ id: i + 1, label: `Node ${i + 1}` }));
    const { getByRole } = render(<TreeSelect value={0} onValueChange={() => {}} nodes={nodes} placeholder="none" />);
    fireEvent.click(getByRole("button"));
    const list = document.querySelector('[data-slot="tree-select-list"]') as HTMLElement | null;
    expect(list).not.toBeNull();
    // A wheel over the list must be handled (defaultPrevented) so react-remove-scroll's
    // native-scroll block does not leave the portaled dropdown unscrollable.
    const ev = new WheelEvent("wheel", { deltaY: 60, bubbles: true, cancelable: true });
    list!.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("keeps the dropdown open when interacting inside it, and selects an item", () => {
    let picked = -1;
    const { getByRole, getByText } = render(
      <TreeSelect
        value={0}
        onValueChange={(v) => (picked = v)}
        nodes={[{ id: 7, label: "Node A" }]}
        placeholder="none"
      />
    );
    fireEvent.click(getByRole("button"));
    fireEvent.click(getByText("Node A"));
    expect(picked).toBe(7);
  });
});
