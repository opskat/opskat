// frontend/src/extension/inject.ts
import React from "react";
import ReactDOM from "react-dom/client";
import i18n from "../i18n";
import * as ui from "@opskat/ui";
import { CodeEditor } from "../components/CodeEditor";
import { JsonTreeView } from "../components/JsonTreeView";
import { QueryResultTable } from "../components/query/QueryResultTable";
import type { ExtAPI } from "./types";

// @opskat/host-ui: the components extension pages get for free instead of bundling
// their own (design decision #6 in docs/specs/2026-09-24-ext-platform-capabilities.md).
// These are the *same* component instances the host itself renders — not copies — so
// they follow the host's theme (CSS vars flipped by ThemeProvider) and language
// (the shared react-i18next instance below) with no extra wiring on either side.
// The version is bound to hostABI (pkg/extension.HostABIVersion): bump both together
// whenever hostABI changes, and keep this in sync by hand — there is no generated
// binding for a Go string constant into the frontend bundle (pkg/extension's
// TestHostUIVersionMirrorsHostABI fails when the two drift).
export const HOST_UI_VERSION = "2.2";

const hostUI = {
  version: HOST_UI_VERSION,
  CodeEditor,
  JsonTreeView,
  QueryResultTable,
};

export type HostUI = typeof hostUI;

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
