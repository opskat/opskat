import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { SearchAddon } from "@xterm/addon-search";
import { TerminalSearchBar } from "@/components/terminal/TerminalSearchBar";

function makeSearchAddon() {
  return {
    findNext: vi.fn(),
    findPrevious: vi.fn(),
    clearDecorations: vi.fn(),
  } as unknown as SearchAddon;
}

describe("TerminalSearchBar", () => {
  it("fills and searches the selected text when opened with an initial query", async () => {
    const searchAddon = makeSearchAddon();

    render(
      <TerminalSearchBar
        visible
        onClose={vi.fn()}
        searchAddon={searchAddon}
        initialQuery="selected text"
        initialQueryToken={1}
      />
    );

    await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue("selected text"));
    expect(searchAddon.findNext).toHaveBeenCalledWith(
      "selected text",
      expect.objectContaining({
        caseSensitive: false,
        wholeWord: false,
        regex: false,
      })
    );
  });

  it("re-applies the same initial query when the request token changes", async () => {
    const searchAddon = makeSearchAddon();
    const { rerender } = render(
      <TerminalSearchBar
        visible
        onClose={vi.fn()}
        searchAddon={searchAddon}
        initialQuery="repeat"
        initialQueryToken={1}
      />
    );

    await waitFor(() => expect(searchAddon.findNext).toHaveBeenCalledTimes(1));

    rerender(
      <TerminalSearchBar
        visible
        onClose={vi.fn()}
        searchAddon={searchAddon}
        initialQuery="repeat"
        initialQueryToken={2}
      />
    );

    await waitFor(() => expect(searchAddon.findNext).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("textbox")).toHaveValue("repeat");
  });
});
