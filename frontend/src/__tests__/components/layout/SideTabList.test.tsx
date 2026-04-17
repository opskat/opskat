import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { SideTabList } from "../../../components/layout/SideTabList";
import { useTabStore, type Tab } from "../../../stores/tabStore";
import { useLayoutStore } from "../../../stores/layoutStore";

function renderSide() {
  return render(
    <TooltipProvider>
      <SideTabList />
    </TooltipProvider>
  );
}

function sshTab(id: string, label: string): Tab {
  return {
    id,
    type: "terminal",
    label,
    meta: { type: "terminal", assetId: 1, assetName: label, assetIcon: "", host: "h", port: 22, username: "u" },
  };
}

describe("SideTabList", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useLayoutStore.setState({
      tabBarLayout: "left",
      leftPanelWidth: 220,
      leftPanelVisible: true,
      activeSidePanel: "tabs",
      filterOpen: false,
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("renders 'no tabs' when empty", () => {
    renderSide();
    expect(screen.getByText("sideTabs.noTabs")).toBeInTheDocument();
  });

  it("renders all open tabs", () => {
    useTabStore.setState({
      tabs: [sshTab("a", "prod-web"), sshTab("b", "stage-db")],
      activeTabId: "a",
    });
    renderSide();
    expect(screen.getByText("prod-web")).toBeInTheDocument();
    expect(screen.getByText("stage-db")).toBeInTheDocument();
  });

  it("activates tab on click", () => {
    useTabStore.setState({
      tabs: [sshTab("a", "aaa"), sshTab("b", "bbb")],
      activeTabId: "a",
    });
    renderSide();
    fireEvent.click(screen.getByText("bbb"));
    expect(useTabStore.getState().activeTabId).toBe("b");
  });

  it("filters tabs by substring when query present", () => {
    useTabStore.setState({
      tabs: [sshTab("a", "redis-cache"), sshTab("b", "mysql-db"), sshTab("c", "redis-session")],
      activeTabId: "a",
    });
    renderSide();
    fireEvent.click(screen.getByLabelText("shortcut.panel.filter"));
    const input = screen.getByPlaceholderText("sideTabs.filterPlaceholder");
    fireEvent.change(input, { target: { value: "red" } });
    const marks = screen.getAllByText("red");
    expect(marks.length).toBe(2);
    expect(screen.queryByText("mysql-db")).not.toBeInTheDocument();
  });

  it("clears filter on Esc", () => {
    useTabStore.setState({
      tabs: [sshTab("a", "aaa"), sshTab("b", "bbb")],
      activeTabId: "a",
    });
    renderSide();
    fireEvent.click(screen.getByLabelText("shortcut.panel.filter"));
    const input = screen.getByPlaceholderText("sideTabs.filterPlaceholder");
    fireEvent.change(input, { target: { value: "xyz" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.getByText("aaa")).toBeInTheDocument();
    expect(screen.getByText("bbb")).toBeInTheDocument();
  });

  it("shows count when no filter", () => {
    useTabStore.setState({
      tabs: [sshTab("a", "aaa"), sshTab("b", "bbb"), sshTab("c", "ccc")],
      activeTabId: "a",
    });
    renderSide();
    expect(screen.getByText("sideTabs.count")).toBeInTheDocument();
  });

  it("renders icon-only when collapsed", () => {
    useLayoutStore.setState({ leftPanelWidth: 80 });
    useTabStore.setState({
      tabs: [sshTab("a", "aaa")],
      activeTabId: "a",
    });
    renderSide();
    expect(screen.queryByText("aaa")).not.toBeInTheDocument();
    expect(screen.queryByText("sideTabs.title")).not.toBeInTheDocument();
  });
});
