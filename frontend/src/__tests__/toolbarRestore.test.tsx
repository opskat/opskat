import { renderHook, render, screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useLayoutStore } from "../stores/layoutStore";
import { useTabStore } from "../stores/tabStore";
import { useAssetStore } from "../stores/assetStore";
import { useRecentAssetStore } from "../stores/recentAssetStore";
import { useSnippetStore } from "../stores/snippetStore";
import { useShortcutStore, DEFAULT_SHORTCUTS, isMac, type ShortcutBinding } from "../stores/shortcutStore";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { TabPanelMenu } from "../components/layout/TabPanelMenu";
import { CommandPalette } from "../components/command/CommandPalette";
import { ListSnippets, ListSnippetCategories } from "../../wailsjs/go/extension/Extension";

function press(binding: ShortcutBinding) {
  document.body.dispatchEvent(
    new KeyboardEvent("keydown", {
      code: binding.code,
      metaKey: isMac ? binding.mod : false,
      ctrlKey: isMac ? binding.ctrl : binding.mod,
      shiftKey: binding.shift,
      altKey: binding.alt,
      bubbles: true,
      cancelable: true,
    })
  );
}

beforeEach(() => {
  localStorage.clear();
  useLayoutStore.setState({ toolbarHidden: false });
  useShortcutStore.setState({ shortcuts: { ...DEFAULT_SHORTCUTS }, isRecording: false });
});

describe("toolbarHidden in layoutStore", () => {
  it("toggleToolbar flips the flag and persists it under sidebar_hidden", () => {
    useLayoutStore.getState().toggleToolbar();
    expect(useLayoutStore.getState().toolbarHidden).toBe(true);
    expect(localStorage.getItem("sidebar_hidden")).toBe("true");
    useLayoutStore.getState().toggleToolbar();
    expect(useLayoutStore.getState().toolbarHidden).toBe(false);
    expect(localStorage.getItem("sidebar_hidden")).toBe("false");
  });
});

describe("toolbar shortcut", () => {
  it("defaults to mod+shift+B and toggles the toolbar", () => {
    renderHook(() =>
      useKeyboardShortcuts({ onToggleAIPanel: vi.fn(), onToggleSidebar: vi.fn(), onToggleCommandPalette: vi.fn() })
    );
    press({ code: "KeyB", mod: true, shift: true, ctrl: false, alt: false });
    expect(useLayoutStore.getState().toolbarHidden).toBe(true);
  });
});

describe("TabPanelMenu", () => {
  it.each(["top", "side"] as const)("offers show-toolbar as the first item when hidden (%s)", async (mode) => {
    useLayoutStore.setState({ toolbarHidden: true });
    render(<TabPanelMenu mode={mode} onOpenFilter={vi.fn()} />);
    await userEvent.click(screen.getByRole("button", { name: "tab panel menu" }));
    const items = await screen.findAllByRole("menuitem");
    expect(items[0]).toHaveTextContent("panel.showSidebar");
    await userEvent.click(items[0]);
    expect(useLayoutStore.getState().toolbarHidden).toBe(false);
  });
});

describe("CommandPalette", () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: [], activeTabId: null });
    useAssetStore.setState({ assets: [], groups: [] });
    useRecentAssetStore.setState({ recentIds: [] });
    useSnippetStore.setState({ all: [], categories: [], runTarget: null });
    vi.mocked(ListSnippets).mockResolvedValue([]);
    vi.mocked(ListSnippetCategories).mockResolvedValue([]);
  });

  it.each([
    ["nav.forward", "forward"],
    ["nav.sshKeys", "sshkeys"],
    ["nav.snippets", "snippets"],
    ["nav.audit", "audit"],
    ["nav.settings", "settings"],
  ])("finds page %s and opens its tab", (key, pageId) => {
    const onClose = vi.fn();
    render(<CommandPalette open onClose={onClose} onConnectAsset={vi.fn()} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: key } });
    fireEvent.click(screen.getByRole("option", { name: new RegExp(key) }));
    expect(useTabStore.getState().activeTabId).toBe(pageId);
    expect(onClose).toHaveBeenCalled();
  });

  it("finds the toolbar toggle and runs it", () => {
    render(<CommandPalette open onClose={vi.fn()} onConnectAsset={vi.fn()} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "panel.hideSidebar" } });
    fireEvent.click(screen.getByRole("option", { name: /panel\.hideSidebar/ }));
    expect(useLayoutStore.getState().toolbarHidden).toBe(true);
  });
});
