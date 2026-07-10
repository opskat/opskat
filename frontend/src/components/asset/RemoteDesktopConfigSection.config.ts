import type { CredentialFragment } from "./credentialConfig";
import {
  buildProxyChainJSON,
  parseConnectionFields,
  resolveSaveProxyChainSecrets,
  type ConnectionFormFields,
  type ProxyChainJSON,
} from "./proxyConfig";

export interface RemoteDesktopFormState extends ConnectionFormFields {
  host: string;
  port: number;
  username: string;
  password: string;
  encryptedPassword: string;
  credentialId: number;
  domain: string;
  securityType: string;
  screenWidth: number;
  screenHeight: number;
  colorDepth: number;
  ignoreCert: boolean;
  fileSshAssetId: number;
}

interface RemoteDesktopConfigJSON {
  host: string;
  port: number;
  username?: string;
  password?: string;
  credential_id?: number;
  domain?: string;
  security_type?: string;
  screen_width?: number;
  screen_height?: number;
  color_depth?: number;
  ignore_cert?: boolean;
  file_ssh_asset_id?: number;
  proxy_chain?: ProxyChainJSON | null;
}

export const VNC_DEFAULTS: RemoteDesktopFormState = {
  host: "",
  port: 5900,
  username: "",
  password: "",
  encryptedPassword: "",
  credentialId: 0,
  domain: "",
  securityType: "",
  screenWidth: 1280,
  screenHeight: 720,
  colorDepth: 24,
  ignoreCert: false,
  fileSshAssetId: 0,
  ...parseConnectionFields(undefined, 0, undefined),
};

export const RDP_DEFAULTS: RemoteDesktopFormState = {
  ...VNC_DEFAULTS,
  port: 3389,
  username: "Administrator",
  ignoreCert: true,
};

export function parseRemoteDesktopConfig(configJSON: string, type: "vnc" | "rdp"): RemoteDesktopFormState {
  const defaults = type === "rdp" ? RDP_DEFAULTS : VNC_DEFAULTS;
  try {
    const cfg: RemoteDesktopConfigJSON = JSON.parse(configJSON || "{}");
    return {
      ...defaults,
      host: cfg.host || "",
      port: cfg.port || defaults.port,
      username: cfg.username || defaults.username,
      password: "",
      encryptedPassword: cfg.password || "",
      credentialId: cfg.credential_id || 0,
      domain: cfg.domain || "",
      securityType: cfg.security_type || "",
      screenWidth: cfg.screen_width || defaults.screenWidth,
      screenHeight: cfg.screen_height || defaults.screenHeight,
      colorDepth: cfg.color_depth || defaults.colorDepth,
      ignoreCert: cfg.ignore_cert ?? defaults.ignoreCert,
      fileSshAssetId: cfg.file_ssh_asset_id || 0,
      ...parseConnectionFields(undefined, 0, cfg.proxy_chain),
    };
  } catch {
    return { ...defaults };
  }
}

export async function buildRemoteDesktopConfig(
  state: RemoteDesktopFormState,
  type: "vnc" | "rdp",
  credential: CredentialFragment,
  encryptPassword: (plain: string) => Promise<string>
): Promise<string> {
  const cfg: RemoteDesktopConfigJSON = {
    host: state.host,
    port: state.port,
  };
  if (state.username) cfg.username = state.username;
  if (credential.credential_id) cfg.credential_id = credential.credential_id;
  else if (credential.password) cfg.password = credential.password;
  else if (state.password) cfg.password = await encryptPassword(state.password);
  else if (state.encryptedPassword) cfg.password = state.encryptedPassword;

  if (type === "vnc" && state.securityType) cfg.security_type = state.securityType;
  if (type === "rdp") {
    if (state.domain) cfg.domain = state.domain;
    if (state.screenWidth > 0) cfg.screen_width = state.screenWidth;
    if (state.screenHeight > 0) cfg.screen_height = state.screenHeight;
    if (state.colorDepth > 0) cfg.color_depth = state.colorDepth;
    if (state.ignoreCert) cfg.ignore_cert = true;
  }
  if (state.fileSshAssetId > 0) cfg.file_ssh_asset_id = state.fileSshAssetId;
  const proxyChainSecrets = await resolveSaveProxyChainSecrets(state.proxyChainLayers, encryptPassword);
  const chain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (chain) cfg.proxy_chain = chain;
  return JSON.stringify(cfg);
}

export function parseRemoteDesktopPasswordCredentialConfig(configJSON: string): CredentialFragment {
  try {
    const cfg: RemoteDesktopConfigJSON = JSON.parse(configJSON || "{}");
    return { credential_id: cfg.credential_id, password: cfg.password };
  } catch {
    return {};
  }
}
