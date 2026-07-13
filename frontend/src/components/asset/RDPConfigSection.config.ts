import type { CredentialFragment } from "./credentialConfig";
import {
  buildProxyJSON,
  buildProxyChainJSON,
  CONNECTION_DEFAULTS,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyConfigJSON,
  type ProxyChainJSON,
} from "./proxyConfig";

interface RDPConfig {
  host: string;
  port: number;
  username: string;
  password?: string;
  credential_id?: number;
  domain?: string;
  clipboard: boolean;
  proxy?: ProxyConfigJSON;
  ssh_asset_id?: number;
  proxy_chain?: ProxyChainJSON;
}

export interface RDPFormState extends ConnectionFormFields {
  host: string;
  port: number;
  username: string;
  domain: string;
  clipboard: boolean;
}

export const RDP_DEFAULTS: RDPFormState = {
  host: "",
  port: 3389,
  username: "Administrator",
  domain: "",
  clipboard: true,
  ...CONNECTION_DEFAULTS,
};

/**
 * 保存/测试共用序列化。cred 由 resolveSave/TestCredential 预解析。
 * 隧道走 asset 顶层列(sshTunnelId);save 不写 ssh_asset_id;
 * 测试无 asset 行,buildTest 传 includeSshAssetId=true 把隧道塞进 config(镜像 Redis)。
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test=明文)预解析;
 * 隧道与代理互斥,按 connectionType 二选一。
 */
export function buildRDPConfig(
  state: RDPFormState,
  cred: CredentialFragment,
  includeSshAssetId = false,
  proxyPassword = "",
  proxyChainSecrets: Record<string, { password?: string; token?: string }> = {}
): string {
  const cfg: RDPConfig = {
    host: state.host,
    port: state.port || 3389,
    username: state.username,
    clipboard: state.clipboard,
  };
  if (state.domain.trim()) cfg.domain = state.domain.trim();
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  const proxy = buildProxyJSON(state, proxyPassword);
  if (proxy) cfg.proxy = proxy;
  const proxyChain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (proxyChain) cfg.proxy_chain = proxyChain;
  if (state.connectionType === "jumphost" && includeSshAssetId && state.sshTunnelId > 0)
    cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

export function parseRDPConfig(configJSON: string, assetTunnelId = 0): RDPFormState {
  try {
    const cfg: RDPConfig = JSON.parse(configJSON || "{}");
    return {
      host: cfg.host || "",
      port: cfg.port || 3389,
      username: cfg.username || "Administrator",
      domain: cfg.domain || "",
      clipboard: cfg.clipboard !== false,
      ...parseConnectionFields(cfg.proxy, assetTunnelId || cfg.ssh_asset_id || 0, cfg.proxy_chain),
    };
  } catch {
    return { ...RDP_DEFAULTS };
  }
}

export function parseRDPCredentialConfig(configJSON: string): CredentialFragment {
  try {
    const cfg: RDPConfig = JSON.parse(configJSON || "{}");
    return { credential_id: cfg.credential_id, password: cfg.password };
  } catch {
    return {};
  }
}
