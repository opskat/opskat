/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import i18n from "../i18n";
import { useExtensionStore } from "../extension/store";
import { useAssetTypeDef } from "@/lib/assetTypes/_register";
import { ListInstalledExtensions } from "../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { LoadedExtension } from "../extension/types";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));
vi.mock("../extension/inject", () => ({ injectExtensionAPI: vi.fn() }));
vi.mock("../extension/api", () => ({ createExtensionAPI: vi.fn() }));
vi.mock("../extension/loader", () => ({ clearExtensionCache: vi.fn() }));

import { bootstrapExtensions, _resetForTesting } from "../extension/init";

// The backend localizes the manifest (Manifest.Localized) for the language the
// frontend asks for; these stand in for its two answers.
const TEXT = {
  en: { type: "Elasticsearch", address: "Address", auth: "Authentication", none: "None", hint: "Cluster URL" },
  "zh-CN": { type: "Elasticsearch 集群", address: "地址", auth: "认证方式", none: "无", hint: "集群地址" },
} as const;
type Lang = keyof typeof TEXT;

function localizedManifest(lang: Lang) {
  const tx = TEXT[lang];
  return {
    name: "es",
    version: "1.0.0",
    icon: "database",
    i18n: { displayName: tx.type, description: "" },
    assetTypes: [
      {
        type: "es-ext",
        i18n: { name: tx.type },
        configSchema: {
          type: "object",
          propertyOrder: ["address", "auth"],
          properties: {
            address: { type: "string", title: tx.address, description: tx.hint },
            auth: {
              type: "string",
              title: tx.auth,
              enum: ["none", "basic"],
              enumLabels: [tx.none, "Basic"],
              default: "none",
            },
          },
        },
      },
    ],
  };
}

function backendAnswer(lang: string) {
  return [{ name: "es", enabled: true, manifest: localizedManifest(lang as Lang) }] as any;
}

// What the asset form renders for the type: the registered ConfigSection, read
// through the same registry subscription the form uses.
function FormHarness() {
  const def = useAssetTypeDef("es-ext");
  if (!def?.ConfigSection) return null;
  const Section = def.ConfigSection;
  return (
    <div>
      <span data-testid="type-label">{def.label}</span>
      <Section ctx={{ isEdit: false, encryptPassword: async (s: string) => s }} onValidityChange={() => {}} />
    </div>
  );
}

function resetStore() {
  for (const name of Object.keys(useExtensionStore.getState().extensions)) {
    useExtensionStore.getState().unregister(name);
  }
  useExtensionStore.setState({ ready: false, extensions: {}, disabled: {} });
}

function expectFormIn(lang: Lang) {
  const tx = TEXT[lang];
  expect(screen.getByTestId("type-label")).toHaveTextContent(tx.type);
  expect(screen.getByText(tx.address)).toBeInTheDocument();
  expect(screen.getByText(tx.hint)).toBeInTheDocument();
  expect(screen.getByText(tx.auth)).toBeInTheDocument();
  expect(screen.getByText(tx.none)).toBeInTheDocument();
}

describe("extension manifest follows the app language without reload", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    _resetForTesting();
    resetStore();
    await i18n.changeLanguage("en");
    vi.mocked(ListInstalledExtensions).mockImplementation(async (lang: string) => backendAnswer(lang));
  });

  afterEach(async () => {
    _resetForTesting();
    await i18n.changeLanguage("en");
  });

  it("re-localizes the asset form en → zh-CN → en", async () => {
    await bootstrapExtensions();
    render(<FormHarness />);
    await waitFor(() => expectFormIn("en"));

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });
    await waitFor(() => expectFormIn("zh-CN"));
    expect(screen.queryByText(TEXT.en.address)).not.toBeInTheDocument();

    await act(async () => {
      await i18n.changeLanguage("en");
    });
    await waitFor(() => expectFormIn("en"));
    expect(screen.queryByText(TEXT["zh-CN"].address)).not.toBeInTheDocument();
  });

  it("asks the backend for the language it is switching to", async () => {
    await bootstrapExtensions();
    expect(ListInstalledExtensions).toHaveBeenLastCalledWith("en");

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });
    await waitFor(() => expect(ListInstalledExtensions).toHaveBeenLastCalledWith("zh-CN"));
  });

  it("drops an answer for a language that is no longer current", async () => {
    await bootstrapExtensions();
    render(<FormHarness />);
    await waitFor(() => expectFormIn("en"));

    // zh-CN is answered only after the user already switched back to en.
    const pending: Record<string, () => void> = {};
    vi.mocked(ListInstalledExtensions).mockImplementation(
      (lang: string) =>
        new Promise((resolve) => {
          pending[lang] = () => resolve(backendAnswer(lang));
        }) as any
    );
    await act(async () => {
      await i18n.changeLanguage("zh-CN");
      await i18n.changeLanguage("en");
    });
    await act(async () => {
      pending["en"]();
    });
    await act(async () => {
      pending["zh-CN"]();
    });
    // The late zh-CN answer is not applied; it is asked for again in en.
    expect(ListInstalledExtensions).toHaveBeenLastCalledWith("en");
    expectFormIn("en");
    expect(screen.queryByText(TEXT["zh-CN"].address)).not.toBeInTheDocument();
  });

  it("keeps an already-loaded extension bundle across a language switch", async () => {
    await bootstrapExtensions();
    const loaded = { name: "es", manifest: localizedManifest("en"), components: {} } as unknown as LoadedExtension;
    useExtensionStore.getState().setLoaded("es", loaded);

    await act(async () => {
      await i18n.changeLanguage("zh-CN");
    });
    await waitFor(() =>
      expect(useExtensionStore.getState().extensions["es"]?.manifest.i18n.displayName).toBe(TEXT["zh-CN"].type)
    );
    expect(useExtensionStore.getState().extensions["es"]?.loaded).toBe(loaded);
  });

  it("still drops loaded bundles when the extensions are reloaded", async () => {
    const handlers: Record<string, () => void> = {};
    vi.mocked(EventsOn).mockImplementation((name: string, cb: (...args: any[]) => void) => {
      handlers[name] = cb as () => void;
      return () => {};
    });
    await bootstrapExtensions();
    const loaded = { name: "es", manifest: localizedManifest("en"), components: {} } as unknown as LoadedExtension;
    useExtensionStore.getState().setLoaded("es", loaded);

    await act(async () => {
      handlers["ext:reload"]();
    });

    await waitFor(() => expect(ListInstalledExtensions).toHaveBeenCalledTimes(2));
    expect(useExtensionStore.getState().extensions["es"]).toBeDefined();
    expect(useExtensionStore.getState().extensions["es"]?.loaded).toBeUndefined();
  });
});
