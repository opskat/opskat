import type { CredentialFragment } from "./credentialConfig";
import { normalizeVNCEncryptionPolicy, type VNCEncryptionPolicy } from "@/lib/vncSecurity";
import {
  buildProxyChainJSON,
  parseConnectionFields,
  resolveSaveProxyChainSecrets,
  type ConnectionFormFields,
  type ProxyChainJSON,
} from "./proxyConfig";

export interface VNCFormState extends ConnectionFormFields {
  host: string;
  port: number;
  username: string;
  password: string;
  encryptedPassword: string;
  credentialId: number;
  fileSshAssetId: number;
  encryption: VNCEncryptionPolicy;
}

interface VNCConfigJSON {
  host: string;
  port: number;
  username?: string;
  password?: string;
  credential_id?: number;
  file_ssh_asset_id?: number;
  encryption?: string;
  proxy_chain?: ProxyChainJSON | null;
}

export const VNC_DEFAULTS: VNCFormState = {
  host: "",
  port: 5900,
  username: "",
  password: "",
  encryptedPassword: "",
  credentialId: 0,
  fileSshAssetId: 0,
  encryption: "server",
  ...parseConnectionFields(undefined, 0, undefined),
};

export function parseVNCConfig(configJSON: string): VNCFormState {
  const cfg: VNCConfigJSON = JSON.parse(configJSON || "{}");
  return {
    ...VNC_DEFAULTS,
    host: cfg.host || "",
    port: cfg.port || VNC_DEFAULTS.port,
    username: cfg.username || VNC_DEFAULTS.username,
    password: "",
    encryptedPassword: cfg.password || "",
    credentialId: cfg.credential_id || 0,
    fileSshAssetId: cfg.file_ssh_asset_id || 0,
    encryption: normalizeVNCEncryptionPolicy(cfg.encryption),
    ...parseConnectionFields(undefined, 0, cfg.proxy_chain),
  };
}

export async function buildVNCConfig(
  state: VNCFormState,
  credential: CredentialFragment,
  encryptPassword: (plain: string) => Promise<string>
): Promise<string> {
  const cfg: VNCConfigJSON = {
    host: state.host,
    port: state.port,
  };
  if (state.username) cfg.username = state.username;
  if (credential.credential_id) cfg.credential_id = credential.credential_id;
  else if (credential.password) cfg.password = credential.password;
  else if (state.password) cfg.password = await encryptPassword(state.password);
  else if (state.encryptedPassword) cfg.password = state.encryptedPassword;

  if (state.fileSshAssetId > 0) cfg.file_ssh_asset_id = state.fileSshAssetId;
  if (state.encryption !== "server") cfg.encryption = state.encryption;
  const proxyChainSecrets = await resolveSaveProxyChainSecrets(state.proxyChainLayers, encryptPassword);
  const chain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (chain) cfg.proxy_chain = chain;
  return JSON.stringify(cfg);
}

export function parseVNCPasswordCredentialConfig(configJSON: string): CredentialFragment {
  const cfg: VNCConfigJSON = JSON.parse(configJSON || "{}");
  return { credential_id: cfg.credential_id, password: cfg.password };
}
