import type { CredentialFragment } from "./credentialConfig";
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
  securityType: string;
  fileSshAssetId: number;
}

interface VNCConfigJSON {
  host: string;
  port: number;
  username?: string;
  password?: string;
  credential_id?: number;
  security_type?: string;
  file_ssh_asset_id?: number;
  proxy_chain?: ProxyChainJSON | null;
}

export const VNC_DEFAULTS: VNCFormState = {
  host: "",
  port: 5900,
  username: "",
  password: "",
  encryptedPassword: "",
  credentialId: 0,
  securityType: "",
  fileSshAssetId: 0,
  ...parseConnectionFields(undefined, 0, undefined),
};

export function parseVNCConfig(configJSON: string): VNCFormState {
  try {
    const cfg: VNCConfigJSON = JSON.parse(configJSON || "{}");
    return {
      ...VNC_DEFAULTS,
      host: cfg.host || "",
      port: cfg.port || VNC_DEFAULTS.port,
      username: cfg.username || VNC_DEFAULTS.username,
      password: "",
      encryptedPassword: cfg.password || "",
      credentialId: cfg.credential_id || 0,
      securityType: cfg.security_type || "",
      fileSshAssetId: cfg.file_ssh_asset_id || 0,
      ...parseConnectionFields(undefined, 0, cfg.proxy_chain),
    };
  } catch {
    return { ...VNC_DEFAULTS };
  }
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

  if (state.securityType) cfg.security_type = state.securityType;
  if (state.fileSshAssetId > 0) cfg.file_ssh_asset_id = state.fileSshAssetId;
  const proxyChainSecrets = await resolveSaveProxyChainSecrets(state.proxyChainLayers, encryptPassword);
  const chain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (chain) cfg.proxy_chain = chain;
  return JSON.stringify(cfg);
}

export function parseVNCPasswordCredentialConfig(configJSON: string): CredentialFragment {
  try {
    const cfg: VNCConfigJSON = JSON.parse(configJSON || "{}");
    return { credential_id: cfg.credential_id, password: cfg.password };
  } catch {
    return {};
  }
}
