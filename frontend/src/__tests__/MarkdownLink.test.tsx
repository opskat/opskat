import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, createEvent } from "@testing-library/react";
import Markdown from "react-markdown";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { markdownComponents } from "../components/MarkdownLink";

describe("markdown external links (#218)", () => {
  beforeEach(() => vi.clearAllMocks());

  it("opens http(s) links in the system browser instead of navigating the webview", () => {
    render(<Markdown components={markdownComponents}>{"[docs](https://opskat.dev)"}</Markdown>);

    const link = screen.getByRole("link", { name: "docs" });
    const clickEvent = createEvent.click(link);
    fireEvent(link, clickEvent);

    // preventDefault stops the Wails webview from replacing the whole app UI.
    expect(clickEvent.defaultPrevented).toBe(true);
    expect(BrowserOpenURL).toHaveBeenCalledWith("https://opskat.dev");
  });

  it("blocks in-webview navigation for non-http links without opening them", () => {
    render(<Markdown components={markdownComponents}>{"[mail](mailto:hi@opskat.dev)"}</Markdown>);

    const link = screen.getByRole("link", { name: "mail" });
    const clickEvent = createEvent.click(link);
    fireEvent(link, clickEvent);

    expect(clickEvent.defaultPrevented).toBe(true);
    expect(BrowserOpenURL).not.toHaveBeenCalled();
  });
});
