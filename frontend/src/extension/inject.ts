// frontend/src/extension/inject.ts
import React from "react";
import ReactDOM from "react-dom/client";
import i18n from "../i18n";
import * as ui from "@opskat/ui";
import { CodeEditor, DataTable, JsonTreeView, type HostUI } from "./hostui";
import type { ExtAPI } from "./types";

// @opskat/host-ui: the components extension pages get for free instead of bundling
// their own (design decision #6 in docs/specs/2026-09-24-ext-platform-capabilities.md).
// They are wrappers (./hostui) with props types owned by hostUI, rendered by the same
// React and i18next instances as the host, so they follow the host's theme (CSS vars
// flipped by ThemeProvider) and language with no extra wiring on either side.
// The version is bound to hostABI (pkg/extmanifest.HostABIVersion): bump both together
// whenever hostABI changes, and keep this in sync by hand — there is no generated
// binding for a Go string constant into the frontend bundle (pkg/extension's
// TestHostUIVersionMirrorsHostABI fails when the two drift).
export const HOST_UI_VERSION = "1.0";

const hostUI: HostUI = {
  version: HOST_UI_VERSION,
  CodeEditor,
  JsonTreeView,
  DataTable,
};

export type { HostUI };

declare global {
  interface Window {
    __OPSKAT_EXT__?: {
      React: typeof React;
      ReactDOM: typeof ReactDOM;
      i18n: typeof i18n;
      ui: typeof ui;
      hostUI: HostUI;
      api: ExtAPI;
    };
  }
}

export function injectExtensionAPI(api: ExtAPI): void {
  window.__OPSKAT_EXT__ = { React, ReactDOM, i18n, ui, hostUI, api };
}
