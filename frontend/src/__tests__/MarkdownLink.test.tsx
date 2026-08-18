import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, createEvent } from "@testing-library/react";
import Markdown from "react-markdown";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { markdownComponents, markdownUrlTransform } from "../components/MarkdownLink";
import { openAssetInfoTab } from "@/lib/openAssetInfoTab";

vi.mock("@/lib/openAssetInfoTab", () => ({
  openAssetInfoTab: vi.fn(),
}));

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

  it("opens copied opsctl asset refs as in-app asset mentions", () => {
    render(
      <Markdown urlTransform={markdownUrlTransform} components={markdownComponents}>
        {"[web-01](opsctl://asset/1)"}
      </Markdown>
    );

    const link = screen.getByRole("link", { name: "web-01" });
    expect(link).toHaveAttribute("href", "opsctl://asset/1");
    const clickEvent = createEvent.click(link);
    fireEvent(link, clickEvent);

    expect(clickEvent.defaultPrevented).toBe(true);
    expect(BrowserOpenURL).not.toHaveBeenCalled();
    expect(openAssetInfoTab).toHaveBeenCalledWith(1);
  });

  it("keeps opsctl asset hrefs after the chat sanitizer", () => {
    const schema = {
      ...defaultSchema,
      protocols: {
        ...defaultSchema.protocols,
        href: [...(defaultSchema.protocols?.href ?? ["http", "https", "mailto"]), "opsctl"],
      },
    };
    render(
      <Markdown
        rehypePlugins={[[rehypeSanitize, schema]]}
        urlTransform={markdownUrlTransform}
        components={markdownComponents}
      >
        {"[web-01](opsctl://asset/1)"}
      </Markdown>
    );
    expect(screen.getByRole("link", { name: "web-01" })).toHaveAttribute("href", "opsctl://asset/1");
  });
});
