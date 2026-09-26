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
  capabilities?: ExtCapabilities;
  assetTypes?: ExtAssetType[];
  tools?: ExtToolDef[];
  policies?: ExtPolicies;
  frontend?: ExtFrontend;
}

/** The capability grants from manifest.json (the subset the frontend reads). */
export interface ExtCapabilities {
  /** "read" hands the extension the plaintext of its assets' password fields. */
  credentials: string;
}

export interface ExtAssetType {
  type: string;
  i18n: { name: string };
  configSchema?: Record<string, unknown>;
  /** Host-owned connection settings the type supports; absent means none. */
  connection?: ExtConnection;
  /** The type registers a test-connection handler; the asset form shows "Test connection" only when true. */
  testConnection?: boolean;
}

/**
 * The host-owned connection settings an asset type declares in describe(). The host
 * renders and applies a declared item; the extension never reads it — the SSH tunnel
 * is the asset's own `sshTunnelId`, outside its config.
 */
export interface ExtConnection {
  sshTunnel?: boolean;
  proxyChain?: boolean;
  tls?: boolean;
}

export interface ExtToolDef {
  name: string;
  i18n: { description: string };
  parameters: Record<string, unknown>;
  /**
   * The fixed policy action this tool requests; declared at the tool's registration in
   * the guest. Absent for a tool that classifies each call from its arguments.
   */
  policyAction?: string;
}

export interface ExtPolicies {
  type: string;
  /**
   * Derived by the backend from the tools' policy actions — not declared separately.
   * A rule is `<action>` or `<action>:<resource-glob>`; the action part is one of these.
   */
  actions: string[];
  groups: { id: string; i18n: { name: string; description: string }; policy: Record<string, unknown> }[];
  default: string[];
}

export interface ExtFrontend {
  entry: string;
  styles: string;
  /** `null` for an extension with no pages: Go marshals an empty slice as null. */
  pages: ExtPage[] | null;
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

// `assetId` is how a call names the asset it runs against. Extension tools take no
// asset argument — the backend puts the asset in the call envelope — so a page that
// works on an asset passes the `assetId` prop it was rendered with. Leaving it out
// means "this call has no asset", which is what testing an unsaved configuration is.
//
// `options.signal` cancels a call in flight: the backend interrupts the tool —
// host IO it is blocked in included — and the returned promise rejects with the
// signal's reason (an AbortError unless the caller gave another).
export interface ExtCallOptions {
  signal?: AbortSignal;
}

export interface ExtAPI {
  callTool(extName: string, tool: string, args: unknown, assetId?: number, options?: ExtCallOptions): Promise<unknown>;
  executeAction(
    extName: string,
    action: string,
    args: unknown,
    onEvent?: (e: ExtEvent) => void,
    assetId?: number
  ): Promise<unknown>;
}
