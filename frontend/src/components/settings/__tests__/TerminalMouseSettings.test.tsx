import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { TerminalMouseSettings } from "@/components/settings/TerminalMouseSettings";
import { ShortcutSettings } from "@/components/settings/ShortcutSettings";
import { DEFAULT_SHORTCUTS, useShortcutStore } from "@/stores/shortcutStore";
import { cloneDefaultMouseShortcuts, DEFAULT_TERMINAL_MOUSE_BINDINGS } from "@/stores/terminalMouseShortcuts";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";

describe("TerminalMouseSettings & ShortcutSettings jump link", () => {
  beforeEach(() => {
    localStorage.clear();
    useShortcutStore.setState({
      shortcuts: { ...DEFAULT_SHORTCUTS },
      mouseShortcuts: cloneDefaultMouseShortcuts(),
      isRecording: false,
      disableShortcutsInTerminal: false,
    });
    useTerminalThemeStore.setState({
      selectionAction: "popover",
    });
    useSettingsUiStore.setState({
      activeTab: "terminal",
      focusSection: null,
    });
  });

  it("changes selectionAction via the single dropdown and updates its dynamic hint for all 3 modes", async () => {
    const user = userEvent.setup();
    render(<TerminalMouseSettings />);

    const trigger = screen.getByTestId("terminal-selection-action");
    expect(screen.getByTestId("terminal-selection-action-hint")).toHaveTextContent(
      "terminal.mouse.selectionActionPopoverHint"
    );

    await user.click(trigger);
    await user.click(await screen.findByRole("option", { name: "terminal.mouse.selectionActionAutoCopy" }));
    expect(useTerminalThemeStore.getState().selectionAction).toBe("auto-copy");
    expect(screen.getByTestId("terminal-selection-action-hint")).toHaveTextContent(
      "terminal.mouse.selectionActionAutoCopyHint"
    );

    await user.click(trigger);
    await user.click(await screen.findByRole("option", { name: "terminal.mouse.selectionActionNone" }));
    expect(useTerminalThemeStore.getState().selectionAction).toBe("none");
    expect(screen.getByTestId("terminal-selection-action-hint")).toHaveTextContent(
      "terminal.mouse.selectionActionNoneHint"
    );
  });

  it("renders fixed 4 button rows with action dropdowns, per-row reset, and Reset All", async () => {
    const user = userEvent.setup();
    render(<TerminalMouseSettings />);

    expect(screen.getByTestId("mouse-shortcut-row-right")).toBeInTheDocument();
    expect(screen.getByTestId("mouse-shortcut-row-middle")).toBeInTheDocument();
    expect(screen.getByTestId("mouse-shortcut-row-back")).toBeInTheDocument();
    expect(screen.getByTestId("mouse-shortcut-row-forward")).toBeInTheDocument();

    // No recorder / add-row controls exist in the simplified UI.
    expect(screen.queryByTestId("add-mouse-shortcut-btn")).not.toBeInTheDocument();
    expect(screen.queryByTestId("mouse-shortcut-recorder")).not.toBeInTheDocument();

    const resetAllBtn = screen.getByTestId("reset-mouse-shortcuts-btn");
    expect(resetAllBtn).toBeDisabled();
    expect(screen.queryByTestId("mouse-shortcut-reset-right")).not.toBeInTheDocument();

    // Change Right Click to Smart Copy / Paste and Back to None via dropdowns.
    await user.click(screen.getByTestId("mouse-shortcut-action-right"));
    await user.click(await screen.findByRole("option", { name: "terminal.mouse.action.terminal.copyOrPaste" }));
    await user.click(screen.getByTestId("mouse-shortcut-action-back"));
    await user.click(await screen.findByRole("option", { name: "terminal.mouse.action.none" }));

    expect(useShortcutStore.getState().mouseShortcuts.right).toBe("terminal.copyOrPaste");
    expect(useShortcutStore.getState().mouseShortcuts.back).toBe("none");
    expect(resetAllBtn).not.toBeDisabled();
    expect(screen.getByTestId("mouse-shortcut-reset-right")).toBeInTheDocument();
    expect(screen.getByTestId("mouse-shortcut-reset-back")).toBeInTheDocument();
    expect(screen.queryByTestId("mouse-shortcut-reset-middle")).not.toBeInTheDocument();

    // Per-row reset restores only that slot.
    await user.click(screen.getByTestId("mouse-shortcut-reset-right"));
    expect(useShortcutStore.getState().mouseShortcuts.right).toBe(DEFAULT_TERMINAL_MOUSE_BINDINGS.right);
    expect(useShortcutStore.getState().mouseShortcuts.back).toBe("none");
    expect(screen.queryByTestId("mouse-shortcut-reset-right")).not.toBeInTheDocument();
    expect(resetAllBtn).not.toBeDisabled();

    // Reset All restores all 4 slots and disables the button again.
    await user.click(resetAllBtn);
    expect(useShortcutStore.getState().mouseShortcuts).toEqual(DEFAULT_TERMINAL_MOUSE_BINDINGS);
    expect(resetAllBtn).toBeDisabled();
    expect(screen.queryByTestId("mouse-shortcut-reset-back")).not.toBeInTheDocument();
  });

  it("ShortcutSettings jump link navigates directly to Settings -> Terminal (mouse-selection)", () => {
    useSettingsUiStore.setState({ activeTab: "shortcuts", focusSection: null });
    render(<ShortcutSettings />);

    fireEvent.click(screen.getByTestId("shortcut-jump-terminal-mouse"));

    expect(useSettingsUiStore.getState().activeTab).toBe("terminal");
    expect(useSettingsUiStore.getState().focusSection).toBe("mouse-selection");
  });
});
