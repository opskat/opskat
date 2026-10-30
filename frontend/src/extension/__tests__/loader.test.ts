import { afterEach, describe, expect, it, vi } from "vitest";
import { clearExtensionCache, loadExtension } from "../loader";
import type { ExtManifest } from "../types";

function manifest(styles: string): ExtManifest {
  return { frontend: { entry: "main.js", styles, pages: [] } } as unknown as ExtManifest;
}

function links(href: string): HTMLLinkElement[] {
  return Array.from(document.head.querySelectorAll<HTMLLinkElement>("link")).filter(
    (l) => l.getAttribute("href") === href
  );
}

describe("loadExtension stylesheet", () => {
  afterEach(() => {
    clearExtensionCache();
    document.head.innerHTML = "";
  });

  it("keeps a single stylesheet link after the cache is cleared and the extension reloaded", async () => {
    vi.doMock("/extensions/es/main.js", () => ({}));
    await loadExtension("es", manifest("style.css"));
    clearExtensionCache("es");
    await loadExtension("es", manifest("style.css"));
    expect(links("/extensions/es/style.css")).toHaveLength(1);
  });

  it("replaces the old link when the styles path changes and leaves other extensions alone", async () => {
    vi.doMock("/extensions/es/main.js", () => ({}));
    vi.doMock("/extensions/other/main.js", () => ({}));
    await loadExtension("other", manifest("o.css"));
    await loadExtension("es", manifest("a.css"));
    clearExtensionCache("es");
    await loadExtension("es", manifest("b.css"));
    expect(links("/extensions/es/a.css")).toHaveLength(0);
    expect(links("/extensions/es/b.css")).toHaveLength(1);
    expect(links("/extensions/other/o.css")).toHaveLength(1);
  });
});
