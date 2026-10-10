// frontend/src/extension/connectionConfig.ts
//
// The host reserves one key inside an extension asset's config JSON for
// connection settings it owns — proxy chain and TLS — that a type opts into via
// connection.proxyChain / connection.tls in describe(). The guest never sees
// this key: the host strips it before ctx.AssetConfig() and validate_config
// (pkg/extension.StripHostConnectionConfig / HostConnectionConfigKey, the Go
// side of this same contract). Keep the key name and shape in lockstep with
// internal/app/extension/host.go's hostConnectionConfig.
import type { ProxyChainJSON } from "@/components/asset/proxyConfig";

export const HOST_CONNECTION_CONFIG_KEY = "__opskat_connection";

export interface HostTLSConfig {
  enabled?: boolean;
  insecure?: boolean;
  serverName?: string;
  /** Each certificate is either a path on this machine (*File) or its PEM content. */
  caFile?: string;
  certFile?: string;
  keyFile?: string;
  caCert?: string;
  clientCert?: string;
  /** The one secret here: always ciphertext, in a stored asset and a test-connection call alike. */
  clientKey?: string;
}

export interface HostConnectionConfig {
  proxyChain?: ProxyChainJSON;
  tls?: HostTLSConfig;
  /**
   * SSH tunnel asset id, ad-hoc "test connection" calls only: a saved asset's
   * tunnel lives on its own sshTunnelId column, never under this key. A test
   * call has no such column yet (or unsaved edits to it), so it rides here
   * for the one call instead.
   */
  sshTunnelId?: number;
}
