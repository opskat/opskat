import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { PanelSidebar } from "../PanelSidebar";

function drag(handle: HTMLElement, from: number, to: number) {
  fireEvent.mouseDown(handle, { clientX: from });
  fireEvent.mouseMove(document, { clientX: to });
  fireEvent.mouseUp(document);
}

describe("PanelSidebar", () => {
  beforeEach(() => localStorage.clear());

  it("persists the dragged width per panel type and restores it on remount", () => {
    const { unmount } = render(
      <PanelSidebar panelType="redis" title="Keys">
        body
      </PanelSidebar>
    );
    drag(screen.getByTestId("redis-sidebar-resize"), 100, 160);
    const saved = Number(localStorage.getItem("redis_sidebar_width"));
    expect(saved).toBeGreaterThan(220);
    unmount();

    render(
      <PanelSidebar panelType="redis" title="Keys">
        body
      </PanelSidebar>
    );
    expect(screen.getByTestId("redis-sidebar")).toHaveStyle({ width: `${saved}px` });
    expect(localStorage.getItem("mongodb_sidebar_width")).toBeNull();
  });

  it("keeps a legacy storage key and clamps drags to the shared range", () => {
    render(
      <PanelSidebar panelType="k8s" storageKey="k8s_sidebar_width" title="Cluster">
        body
      </PanelSidebar>
    );
    drag(screen.getByTestId("k8s-sidebar-resize"), 0, 5000);
    expect(localStorage.getItem("k8s_sidebar_width")).toBe("420");
    drag(screen.getByTestId("k8s-sidebar-resize"), 5000, -5000);
    expect(localStorage.getItem("k8s_sidebar_width")).toBe("160");
    expect(localStorage.getItem("k8s_sidebar_width")).not.toBeNull();
  });

  it("renders without a filter row when none is given", () => {
    render(
      <PanelSidebar panelType="kafka" title="Topics" actions={<button>act</button>}>
        body
      </PanelSidebar>
    );
    expect(screen.getByText("Topics")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "act" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
});
