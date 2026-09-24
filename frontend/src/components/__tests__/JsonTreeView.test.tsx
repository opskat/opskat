import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import enCommon from "@/i18n/locales/en/common.json";
import zhCommon from "@/i18n/locales/zh-CN/common.json";
import { JsonTreeView } from "@/components/JsonTreeView";

// react-i18next is globally mocked (src/__tests__/setup.ts) to echo the key back
// from `t()`, so these tests see the literal i18n key rather than translated text —
// that is what proves the label routes through the shared translation hook (and
// therefore follows the host's language at runtime) without pulling in the real
// i18next pipeline.
describe("JsonTreeView", () => {
  it("renders nested keys and primitive values", () => {
    render(<JsonTreeView data={{ key: "runbook-1", meta: { version: 2, published: true } }} />);

    expect(screen.getByText("key:")).toBeInTheDocument();
    expect(screen.getByText('"runbook-1"')).toBeInTheDocument();
    expect(screen.getByText("meta:")).toBeInTheDocument();
    expect(screen.getByText("version:")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("published:")).toBeInTheDocument();
    expect(screen.getByText("true")).toBeInTheDocument();
  });

  it("renders array entries with index labels", () => {
    render(<JsonTreeView data={["a", "b"]} />);

    expect(screen.getByText("[0]:")).toBeInTheDocument();
    expect(screen.getByText("[1]:")).toBeInTheDocument();
  });

  it("renders null distinctly from a missing value", () => {
    render(<JsonTreeView data={{ deleted: null }} />);
    expect(screen.getByText("null")).toBeInTheDocument();
  });

  it("collapses and re-expands a subtree on toggle click, hiding/showing its children", async () => {
    const user = userEvent.setup();
    render(<JsonTreeView data={{ version: 2 }} />);

    expect(screen.getByText("version:")).toBeInTheDocument();
    const toggle = screen.getByRole("button", { name: "extension.hostUi.jsonTreeCollapse" });

    await user.click(toggle);
    expect(screen.queryByText("version:")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "extension.hostUi.jsonTreeExpand" }));
    expect(screen.getByText("version:")).toBeInTheDocument();
  });

  it("shows an empty state routed through the shared translation hook for undefined data", () => {
    render(<JsonTreeView data={undefined} />);
    expect(screen.getByText("extension.hostUi.jsonTreeEmpty")).toBeInTheDocument();
  });

  it("declares its i18n keys in every locale the host ships (en and zh-CN independently)", () => {
    // Cross-file drift check: en and zh-CN are independently maintained sources of
    // truth. If a key were added to one and forgotten in the other, the component
    // would silently show the raw key to users of the other language.
    const hostUiKeys = ["jsonTreeExpand", "jsonTreeCollapse", "jsonTreeEmpty"] as const;
    for (const key of hostUiKeys) {
      expect(enCommon.extension.hostUi[key]).toBeTruthy();
      expect(zhCommon.extension.hostUi[key]).toBeTruthy();
    }
  });
});
