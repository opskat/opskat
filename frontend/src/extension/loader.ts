// frontend/src/extension/loader.ts
import type { ComponentType } from "react";
import type { ExtManifest, LoadedExtension } from "./types";

// Extension names and the entry/styles paths are checked when the extension loads
// (internal/extreg pages point and manifest parsing), so they are trusted here.
const cache = new Map<string, LoadedExtension>();
// The stylesheet <link> each loaded extension injected, removed together with its cache entry.
const styleLinks = new Map<string, HTMLLinkElement>();

export async function loadExtension(name: string, manifest: ExtManifest): Promise<LoadedExtension> {
  const cached = cache.get(name);
  if (cached) return cached;

  const frontend = manifest.frontend;
  if (!frontend) throw new Error(`Extension "${name}" has no frontend definition`);

  // Inject CSS (drop a link left by an earlier load whose import failed)
  styleLinks.get(name)?.remove();
  if (frontend.styles) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = `/extensions/${name}/${frontend.styles}`;
    document.head.appendChild(link);
    styleLinks.set(name, link);
  }

  // Load ESM module
  const mod = await import(/* @vite-ignore */ `/extensions/${name}/${frontend.entry}`);

  // Extract page components
  const components: Record<string, ComponentType<{ assetId?: number }>> = {};
  for (const page of frontend.pages ?? []) {
    if (mod[page.component]) {
      components[page.component] = mod[page.component];
    }
  }

  const loaded: LoadedExtension = { name, manifest, components };
  cache.set(name, loaded);
  return loaded;
}

export function clearExtensionCache(name?: string): void {
  const names = name ? [name] : [...styleLinks.keys()];
  for (const n of names) {
    styleLinks.get(n)?.remove();
    styleLinks.delete(n);
  }
  if (name) cache.delete(name);
  else cache.clear();
}
