// frontend/src/extension/loader.ts
import type { ComponentType } from "react";
import type { ExtManifest, LoadedExtension } from "./types";

type PageComponent = ComponentType<{ assetId?: number }>;

// Extension names and the entry/styles paths are checked when the extension loads
// (internal/extreg pages point and manifest parsing), so they are trusted here.
const cache = new Map<string, LoadedExtension>();

// Each extension's current load and the stylesheet <link> it injected. A newer load or a
// cache clear supersedes it: its link is removed, and its module, should it still arrive,
// is not cached — that would be the extension without its stylesheet.
interface Load {
  link?: HTMLLinkElement;
}
const loads = new Map<string, Load>();

function dropLoad(name: string): void {
  loads.get(name)?.link?.remove();
  loads.delete(name);
}

export async function loadExtension(name: string, manifest: ExtManifest): Promise<LoadedExtension> {
  const cached = cache.get(name);
  if (cached) return cached;

  const frontend = manifest.frontend;
  if (!frontend) throw new Error(`Extension "${name}" has no frontend definition`);

  // Inject CSS
  dropLoad(name);
  const load: Load = {};
  loads.set(name, load);
  if (frontend.styles) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = `/extensions/${name}/${frontend.styles}`;
    document.head.appendChild(link);
    load.link = link;
  }

  // Load ESM module; one that fails to import takes its stylesheet with it
  let mod: Record<string, PageComponent | undefined>;
  try {
    mod = await import(/* @vite-ignore */ `/extensions/${name}/${frontend.entry}`);
  } catch (err) {
    if (loads.get(name) === load) dropLoad(name);
    throw err;
  }

  // Extract page components
  const components: Record<string, PageComponent> = {};
  for (const page of frontend.pages ?? []) {
    const component = mod[page.component];
    if (component) {
      components[page.component] = component;
    }
  }

  const loaded: LoadedExtension = { name, manifest, components };
  if (loads.get(name) === load) cache.set(name, loaded);
  return loaded;
}

export function clearExtensionCache(name?: string): void {
  for (const n of name ? [name] : [...loads.keys()]) dropLoad(n);
  if (name) cache.delete(name);
  else cache.clear();
}
