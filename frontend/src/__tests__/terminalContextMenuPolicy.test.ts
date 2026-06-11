import { describe, expect, it } from "vitest";
import { getTerminalContextMenuAction } from "@/components/terminal/terminalContextMenuPolicy";

describe("terminalContextMenuPolicy", () => {
  it("keeps the normal context menu in popover/menu mode", () => {
    expect(getTerminalContextMenuAction("popover-menu", "selected")).toBe("menu");
    expect(getTerminalContextMenuAction("popover-menu", "")).toBe("menu");
    expect(getTerminalContextMenuAction("popover-menu", undefined)).toBe("menu");
  });

  it("copies only when smart right-click mode has selected text", () => {
    expect(getTerminalContextMenuAction("smart-right-click", "selected")).toBe("copy");
    expect(getTerminalContextMenuAction("smart-right-click", "")).toBe("paste");
    expect(getTerminalContextMenuAction("smart-right-click", undefined)).toBe("paste");
  });

  it("always pastes on right-click in select-copy/right-paste mode", () => {
    expect(getTerminalContextMenuAction("select-copy-right-paste", "selected")).toBe("paste");
    expect(getTerminalContextMenuAction("select-copy-right-paste", "")).toBe("paste");
    expect(getTerminalContextMenuAction("select-copy-right-paste", undefined)).toBe("paste");
  });
});
