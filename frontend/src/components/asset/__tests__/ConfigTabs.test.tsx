import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";
import { AssetFormErrorsContext } from "@/components/asset/useConfigSection";

// react-i18next is mocked in src/__tests__/setup.ts so `t(key)` returns the key verbatim.
const groups = (tlsError: boolean): ConfigGroup[] => [
  { key: "connection", label: "asset.tabConnection", render: () => <p>connection body</p> },
  { key: "tls", label: "asset.tabTls", error: tlsError, render: () => <p>tls body</p> },
];

describe("ConfigTabs on a rejected save", () => {
  it("marks failing tabs only after a save attempt and tells the controlling parent to switch to the first one", () => {
    const onActiveChange = vi.fn();
    const reportShown = vi.fn();
    const tree = (attempt: number, active = "connection") => (
      <AssetFormErrorsContext value={{ attempt, reportShown }}>
        <ConfigTabs groups={groups(true)} active={active} onActiveChange={onActiveChange} />
      </AssetFormErrorsContext>
    );
    const { rerender } = render(tree(0));
    expect(screen.queryByTestId("config-tab-error-tls")).toBeNull();
    expect(onActiveChange).not.toHaveBeenCalled();

    rerender(tree(1));
    expect(screen.getByTestId("config-tab-error-tls")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-error-connection")).toBeNull();
    expect(onActiveChange).toHaveBeenCalledWith("tls");
    expect(reportShown).toHaveBeenCalled();
  });

  it("switches an uncontrolled strip to the first failing tab on each attempt", () => {
    const tree = (attempt: number) => (
      <AssetFormErrorsContext value={{ attempt, reportShown: vi.fn() }}>
        <ConfigTabs groups={groups(true)} />
      </AssetFormErrorsContext>
    );
    const { rerender } = render(tree(0));
    expect(screen.getByText("connection body")).toBeInTheDocument();

    rerender(tree(1));
    expect(screen.getByText("tls body")).toBeInTheDocument();

    // 用户切走后再次保存被拒，仍然切回出错的页签。
    fireEvent.click(screen.getByTestId("config-tab-connection"));
    rerender(tree(1));
    expect(screen.getByText("connection body")).toBeInTheDocument();
    rerender(tree(2));
    expect(screen.getByText("tls body")).toBeInTheDocument();
  });

  it("does not mark or jump outside the asset form", () => {
    render(<ConfigTabs groups={groups(true)} />);
    expect(screen.getByText("connection body")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-error-tls")).toBeNull();
  });
});
