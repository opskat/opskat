import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useTerminalThemeStore, useXtermTheme, toXtermTheme } from "@/stores/terminalThemeStore";
import { builtinThemes, defaultDarkTheme, defaultLightTheme, type TerminalTheme } from "@/data/terminalThemes";

const custom: TerminalTheme = { ...defaultDarkTheme, id: "custom-1", name: "Mine", background: "#123456" };

describe("useXtermTheme", () => {
  afterEach(() => {
    document.documentElement.classList.remove("dark");
    useTerminalThemeStore.setState({ selectedThemeId: "default", customThemes: [] });
  });

  it("follows the app's light / dark mode for the default theme", async () => {
    useTerminalThemeStore.setState({ selectedThemeId: "default" });
    const { result } = renderHook(() => useXtermTheme());
    expect(result.current).toEqual(toXtermTheme(defaultLightTheme));

    await act(async () => {
      document.documentElement.classList.add("dark");
      await Promise.resolve();
    });
    expect(result.current).toEqual(toXtermTheme(defaultDarkTheme));
  });

  it("uses the selected built-in or custom theme", () => {
    const builtin = builtinThemes[1];
    useTerminalThemeStore.setState({ selectedThemeId: builtin.id, customThemes: [custom] });
    const { result, rerender } = renderHook(() => useXtermTheme());
    expect(result.current).toEqual(toXtermTheme(builtin));

    act(() => useTerminalThemeStore.setState({ selectedThemeId: custom.id }));
    rerender();
    expect(result.current).toEqual(toXtermTheme(custom));
  });

  it("leaves xterm's own theme when the selection no longer exists", () => {
    useTerminalThemeStore.setState({ selectedThemeId: "deleted", customThemes: [] });
    const { result } = renderHook(() => useXtermTheme());
    expect(result.current).toBeUndefined();
  });
});
