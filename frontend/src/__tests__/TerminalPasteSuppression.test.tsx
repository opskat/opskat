import { fireEvent, render, screen } from "@testing-library/react";
import { act } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { Terminal } from "@/components/terminal/Terminal";

const hoisted = vi.hoisted(() => {
  const pasteFromClipboardSpy = vi.fn().mockResolvedValue(undefined);
  const resizeSpy = vi.fn().mockResolvedValue(undefined);
  const setOnPasteSpy = vi.fn();
  const focusSpy = vi.fn();
  const term = {
    getSelection: vi.fn(() => ""),
    selectAll: vi.fn(),
    focus: focusSpy,
    options: {},
    onSelectionChange: vi.fn(() => ({ dispose: vi.fn() })),
  };
  const fitAddon = {
    fit: vi.fn(),
    proposeDimensions: vi.fn(() => ({ cols: 120, rows: 40 })),
  };
  const instance = {
    term,
    fitAddon,
    searchAddon: {},
    container: document.createElement("div"),
    bridge: {
      setShortcuts: vi.fn(),
      setOnCopy: vi.fn(),
      setOnPaste: setOnPasteSpy,
      setOnSelectAll: vi.fn(),
      setOnFind: vi.fn(),
    },
    urlHighlighter: {
      setEnabled: vi.fn(),
      setColor: vi.fn(),
    },
  };
  return {
    copyBehavior: "popover-menu",
    pasteFromClipboardSpy,
    resizeSpy,
    setOnPasteSpy,
    term,
    fitAddon,
    instance,
  };
});

vi.mock("@/components/terminal/terminalRegistry", () => ({
  getOrCreateTerminal: vi.fn(() => hoisted.instance),
  getTerminalInstance: vi.fn(() => hoisted.instance),
  pasteFromClipboard: hoisted.pasteFromClipboardSpy,
  terminalUrlHighlightColor: vi.fn(() => "#4f8cff"),
  uploadFilesWithRz: vi.fn().mockResolvedValue(false),
}));

vi.mock("@/stores/terminalStore", () => ({
  TRANSPORTS: {
    ssh: {
      resize: hoisted.resizeSpy,
      canSplit: true,
      hasDirectorySync: true,
    },
  },
  useTerminalStore: (selector: (state: unknown) => unknown) =>
    selector({
      tabData: {
        "tab-1": {
          panes: {
            "sess-1": {
              transport: "ssh",
              connected: true,
            },
          },
        },
      },
      splitPane: vi.fn(),
      reconnect: vi.fn(),
      closePane: vi.fn(),
    }),
}));

vi.mock("@/stores/terminalThemeStore", () => ({
  toXtermTheme: vi.fn(() => ({})),
  useTerminalThemeStore: (selector: (state: unknown) => unknown) =>
    selector({
      fontSize: 14,
      fontFamily: "monospace",
      scrollback: 1000,
      webglEnabled: false,
      highlightLinks: false,
      copyBehavior: hoisted.copyBehavior,
      selectedThemeId: "default",
      customThemes: [],
    }),
}));

vi.mock("@/stores/shortcutStore", () => ({
  formatBinding: vi.fn(() => "shortcut"),
  useShortcutStore: (selector: (state: unknown) => unknown) =>
    selector({
      shortcuts: {
        "terminal.copy": {},
        "terminal.paste": {},
        "terminal.selectAll": {},
        "terminal.find": {},
        "split.horizontal": {},
        "split.vertical": {},
        "tab.close": {},
      },
    }),
}));

vi.mock("@/components/theme-provider", () => ({
  useResolvedTheme: vi.fn(() => "light"),
}));

vi.mock("@/data/terminalThemes", () => ({
  builtinThemes: [],
  defaultLightTheme: {},
  defaultDarkTheme: {},
}));

vi.mock("@/data/terminalFonts", () => ({
  withTerminalFontFallback: vi.fn((font: string) => font),
  withTerminalFontIsolation: vi.fn((_sessionId: string, font: string) => font),
}));

vi.mock("@/stores/sftpStore", () => ({
  useSFTPStore: (selector: (state: unknown) => unknown) => selector({ toggleFileManager: vi.fn() }),
}));

vi.mock("@/stores/tabStore", () => ({
  useTabStore: (selector: (state: unknown) => unknown) => selector({ closeTab: vi.fn() }),
}));

vi.mock("@/components/terminal/TerminalSearchBar", () => ({
  TerminalSearchBar: () => <div data-testid="terminal-search-bar" />,
}));

vi.mock("@/components/terminal/terminalFileDropCoordinator", () => ({
  registerTerminalFileDropTarget: vi.fn(() => vi.fn()),
}));

vi.mock("@/lib/notify", () => ({
  notifyCopied: vi.fn(),
}));

vi.mock("@opskat/ui", () => ({
  Button: ({ children, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement>) => (
    <button type="button" {...props}>
      {children}
    </button>
  ),
  ContextMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  ContextMenuTrigger: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => (
    <div {...props}>{children}</div>
  ),
  ContextMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  ContextMenuItem: ({
    children,
    onClick,
    disabled,
  }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: string }) => (
    <button type="button" onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
  ContextMenuSeparator: () => <hr />,
  ContextMenuShortcut: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));

function renderTerminal() {
  return render(<Terminal sessionId="sess-1" active tabId="tab-1" />);
}

function terminalSurface(container: HTMLElement) {
  const surface = Array.from(container.querySelectorAll("div")).find((el) =>
    el.getAttribute("style")?.includes("padding")
  );
  if (!surface) throw new Error("terminal surface not found");
  return surface;
}

describe("Terminal paste suppression", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    hoisted.copyBehavior = "popover-menu";
    hoisted.term.getSelection.mockReturnValue("");
  });

  it("suppresses the following native paste only for terminal keyboard paste callbacks", () => {
    renderTerminal();

    expect(hoisted.setOnPasteSpy).toHaveBeenCalledTimes(1);
    const onPaste = hoisted.setOnPasteSpy.mock.calls[0][0];
    act(() => onPaste());

    expect(hoisted.pasteFromClipboardSpy).toHaveBeenCalledWith("sess-1", { suppressNativePaste: true });
  });

  it("does not suppress native paste for the context menu paste item", () => {
    renderTerminal();

    fireEvent.click(screen.getByText("ssh.contextMenu.paste"));

    expect(hoisted.pasteFromClipboardSpy).toHaveBeenCalledWith("sess-1");
  });

  it("does not suppress native paste for smart right-click paste", () => {
    hoisted.copyBehavior = "smart-right-click";
    const { container } = renderTerminal();

    fireEvent.contextMenu(terminalSurface(container));

    expect(hoisted.pasteFromClipboardSpy).toHaveBeenCalledWith("sess-1");
  });
});
