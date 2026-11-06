import { describe, it, expect, beforeEach, vi } from "vitest";
import { useState } from "react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { asset_entity, snippet_entity, snippet_svc } from "../../wailsjs/go/models";
import { useTabStore } from "../stores/tabStore";
import { useAssetStore } from "../stores/assetStore";
import { useRecentAssetStore } from "../stores/recentAssetStore";
import { useSnippetStore } from "../stores/snippetStore";
import { TooltipProvider } from "@opskat/ui";
import { TopBar } from "../components/layout/TopBar";
import { CommandPaletteDialog } from "../components/command/CommandPaletteDialog";
import { ListSnippets, ListSnippetCategories } from "../../wailsjs/go/extension/Extension";
import { runSnippetOnAsset } from "../components/snippet/snippetRun";

vi.mock("../components/snippet/snippetRun", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../components/snippet/snippetRun")>();
  return { ...actual, runSnippetOnAsset: vi.fn() };
});

// Issue #354: after picking a snippet in the Ctrl+P palette, keyboard focus must go back to the
// terminal the user was typing in, not stay on the palette host's trigger / body.

const asset = new asset_entity.Asset({ ID: 7, Name: "myhost", Type: "ssh", GroupID: 0 });

function seedActiveTerminalWithSnippet() {
  useAssetStore.setState({ assets: [asset], groups: [] });
  useTabStore.setState({
    tabs: [
      {
        id: "tab-1",
        type: "terminal",
        label: "myhost",
        icon: "",
        meta: {
          type: "terminal",
          assetId: 7,
          assetName: "myhost",
          assetIcon: "",
          host: "10.0.0.1",
          port: 22,
          username: "root",
        },
      },
      { id: "tab-2", type: "page", label: "settings", icon: "", meta: { type: "page", pageId: "settings" } },
    ],
    activeTabId: "tab-1",
  });
  const snippet = new snippet_entity.Snippet({ ID: 10, Name: "deploy-app", Category: "shell", Content: "run deploy" });
  const category = { id: "shell", assetType: "ssh", label: "SHELL", source: "builtin" } as snippet_svc.Category;
  useSnippetStore.setState({ all: [snippet], categories: [category], runTarget: null });
  vi.mocked(ListSnippets).mockResolvedValue([snippet]);
  vi.mocked(ListSnippetCategories).mockResolvedValue([category]);
}

function TopBarHost() {
  const [open, setOpen] = useState(false);
  return (
    <TooltipProvider>
      <textarea data-testid="terminal-input" />
      <button type="button" data-testid="open-palette" onClick={() => setOpen(true)} />
      <TopBar
        commandOpen={open}
        onCommandOpenChange={setOpen}
        onConnectAsset={vi.fn()}
        assetTreeCollapsed={false}
        onToggleAssetTree={vi.fn()}
        aiPanelCollapsed={false}
        onToggleAIPanel={vi.fn()}
      />
    </TooltipProvider>
  );
}

function DialogHost() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <textarea data-testid="terminal-input" />
      <button type="button" data-testid="open-palette" onClick={() => setOpen(true)} />
      <CommandPaletteDialog open={open} onOpenChange={setOpen} onConnectAsset={vi.fn()} />
    </>
  );
}

// Simulates Ctrl+P while the terminal has focus: the shortcut flips state without moving focus.
async function openPaletteFromTerminal() {
  const terminal = screen.getByTestId("terminal-input");
  terminal.focus();
  fireEvent.click(screen.getByTestId("open-palette"));
  const input = await screen.findByPlaceholderText("commandPalette.placeholder");
  await waitFor(() => expect(document.activeElement).toBe(input));
  return { terminal, input };
}

describe.each([
  ["top bar popover", TopBarHost],
  ["hidden-top-bar dialog", DialogHost],
])("command palette focus return (%s)", (_name, Host) => {
  beforeEach(() => {
    vi.clearAllMocks();
    useRecentAssetStore.setState({ recentIds: [] });
    vi.mocked(runSnippetOnAsset).mockResolvedValue(undefined);
    seedActiveTerminalWithSnippet();
  });

  it("returns focus to the terminal after a snippet is inserted into the active tab", async () => {
    render(<Host />);
    const { terminal, input } = await openPaletteFromTerminal();

    fireEvent.change(input, { target: { value: "deploy" } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(runSnippetOnAsset).toHaveBeenCalledWith(asset, "run deploy");
    await waitFor(() => expect(screen.queryByPlaceholderText("commandPalette.placeholder")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(terminal));
  });

  it("returns focus to the terminal when the palette is dismissed with Escape", async () => {
    render(<Host />);
    const { terminal, input } = await openPaletteFromTerminal();

    fireEvent.keyDown(input, { key: "Escape" });

    await waitFor(() => expect(screen.queryByPlaceholderText("commandPalette.placeholder")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(terminal));
  });

  it("does not pull focus back into the previous tab after switching to another tab", async () => {
    render(<Host />);
    const { terminal, input } = await openPaletteFromTerminal();

    fireEvent.change(input, { target: { value: "settings" } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(useTabStore.getState().activeTabId).toBe("tab-2");
    await waitFor(() => expect(screen.queryByPlaceholderText("commandPalette.placeholder")).toBeNull());
    // Let the host's close-autofocus (setTimeout 0) run before asserting.
    await new Promise((r) => setTimeout(r, 20));
    expect(document.activeElement).not.toBe(terminal);
  });
});
