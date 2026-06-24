import { describe, it, expect } from "vitest";
import { act } from "react";
import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConfigTabs, type ConfigTabsHandle, type ConfigGroup } from "@/components/asset/ConfigTabs";

const twoGroups: ConfigGroup[] = [
  { key: "connection", label: "asset.tabConnection", render: () => <div>conn-pane</div> },
  { key: "advanced", label: "asset.tabAdvanced", optional: true, render: () => <div>adv-pane</div> },
];

describe("ConfigTabs", () => {
  it("single group renders without a tablist", () => {
    render(<ConfigTabs groups={[{ key: "only", label: "asset.tabConnection", render: () => <div>only-pane</div> }]} />);
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.getByText("only-pane")).toBeInTheDocument();
  });

  it("renders tabs and switches panel on click", async () => {
    render(<ConfigTabs groups={twoGroups} />);
    expect(screen.getByRole("tablist")).toBeInTheDocument();
    expect(screen.getByText("conn-pane")).toBeInTheDocument();
    await userEvent.click(screen.getByTestId("config-tab-advanced"));
    expect(screen.getByText("adv-pane")).toBeInTheDocument();
  });

  it("shows a red dot on invalid groups", () => {
    render(<ConfigTabs groups={[twoGroups[0], { ...twoGroups[1], invalid: true }]} />);
    expect(screen.getByTestId("config-tab-dot-advanced")).toBeInTheDocument();
  });

  it("shows a numeric badge", () => {
    render(<ConfigTabs groups={[twoGroups[0], { ...twoGroups[1], badge: 2 }]} />);
    expect(screen.getByText("2")).toBeInTheDocument();
  });

  it("setActive(ref) switches the active tab", () => {
    const ref = createRef<ConfigTabsHandle>();
    render(<ConfigTabs ref={ref} groups={twoGroups} />);
    act(() => ref.current?.setActive("advanced"));
    expect(screen.getByText("adv-pane")).toBeInTheDocument();
  });

  it("does not render a badge when badge is 0", () => {
    render(<ConfigTabs groups={[twoGroups[0], { ...twoGroups[1], badge: 0 }]} />);
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  it("renders the optional label on a group with optional:true", () => {
    render(<ConfigTabs groups={twoGroups} />);
    expect(screen.getByText("asset.optional")).toBeInTheDocument();
  });
});
