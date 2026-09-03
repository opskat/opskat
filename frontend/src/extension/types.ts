// frontend/src/extension/types.ts
//
// ExtManifest is the backend's merged view of one extension, delivered by
// ListInstalledExtensions — the security contract read from manifest.json plus the
// functional face the WASM module reported through describe(). The frontend never
// parses manifest.json itself: pkg/extension is the only reader of that file, and
// the shape below is what internal/service/extension_svc serialises.

export interface ExtManifest {
  name: string;
  version: string;
  icon: string;
  minAppVersion?: string;
  i18n: { displayName: string; description: string };
  backend?: { runtime: string; binary: string };
  assetTypes?: ExtAssetType[];
  tools?: ExtToolDef[];
  policies?: ExtPolicies;
  frontend?: ExtFrontend;
}

export interface ExtAssetType {
  type: string;
  i18n: { name: string };
  configSchema?: Record<string, unknown>;
}

export interface ExtToolDef {
  name: string;
  i18n: { description: string };
  parameters: Record<string, unknown>;
  /** The policy action this tool requests; declared at the tool's registration in the guest. */
  policyAction: string;
}

export interface ExtPolicies {
  type: string;
  /** Derived by the backend from the tools' policy actions — not declared separately. */
  actions: string[];
  groups: { id: string; i18n: { name: string; description: string }; policy: Record<string, unknown> }[];
  default: string[];
}

export interface ExtFrontend {
  entry: string;
  styles: string;
  pages: ExtPage[];
}

export interface ExtPage {
  id: string;
  slot?: string;
  i18n: { name: string };
  component: string;
}

export interface LoadedExtension {
  name: string;
  manifest: ExtManifest;
  components: Record<string, React.ComponentType<{ assetId?: number }>>;
}

export interface ExtEvent {
  eventType: string;
  data: unknown;
}

export interface ExtAPI {
  callTool(extName: string, tool: string, args: unknown): Promise<unknown>;
  executeAction(extName: string, action: string, args: unknown, onEvent?: (e: ExtEvent) => void): Promise<unknown>;
}
