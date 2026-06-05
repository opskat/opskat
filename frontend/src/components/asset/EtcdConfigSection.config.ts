import type { CredentialFragment } from "./credentialConfig";

export interface EtcdFormState {
  endpoints: string;
  username: string;
  tls: boolean;
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
  dialTimeoutSeconds: number;
  commandTimeoutSeconds: number;
  sshTunnelId: number;
}

export const ETCD_DEFAULTS: EtcdFormState = {
  endpoints: "",
  username: "",
  tls: false,
  tlsInsecure: false,
  tlsServerName: "",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
  dialTimeoutSeconds: 5,
  commandTimeoutSeconds: 10,
  sshTunnelId: 0,
};

interface EtcdConfig {
  endpoints?: string[];
  username?: string;
  credential_id?: number;
  password?: string;
  tls?: boolean;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
  dial_timeout_seconds?: number;
  command_timeout_seconds?: number;
  ssh_asset_id?: number;
}

/** 端点文本→数组(镜像旧 save/test/etcdEndpointsList 三处一致切分)。 */
export function parseEtcdEndpoints(raw: string): string[] {
  return raw
    .split(/[\n,;]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** 保存/测试共用序列化(键序锁旧 save 分支)。cred 由 resolveSave/TestCredential 预解析。 */
export function buildEtcdConfig(state: EtcdFormState, cred: CredentialFragment): string {
  const cfg: EtcdConfig = { endpoints: parseEtcdEndpoints(state.endpoints) };
  if (state.username) cfg.username = state.username;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  if (state.tls) cfg.tls = true;
  if (state.tls && state.tlsInsecure) cfg.tls_insecure = true;
  if (state.tls && state.tlsServerName) cfg.tls_server_name = state.tlsServerName;
  if (state.tls && state.tlsCAFile) cfg.tls_ca_file = state.tlsCAFile;
  if (state.tls && state.tlsCertFile) cfg.tls_cert_file = state.tlsCertFile;
  if (state.tls && state.tlsKeyFile) cfg.tls_key_file = state.tlsKeyFile;
  if (state.dialTimeoutSeconds > 0) cfg.dial_timeout_seconds = state.dialTimeoutSeconds;
  if (state.commandTimeoutSeconds > 0) cfg.command_timeout_seconds = state.commandTimeoutSeconds;
  if (state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadEtcdConfig 非凭据字段;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseEtcdConfig(configJSON: string): EtcdFormState {
  try {
    const cfg: EtcdConfig = JSON.parse(configJSON || "{}");
    return {
      endpoints: (cfg.endpoints || []).join("\n"),
      username: cfg.username || "",
      tls: cfg.tls || false,
      tlsInsecure: cfg.tls_insecure || false,
      tlsServerName: cfg.tls_server_name || "",
      tlsCAFile: cfg.tls_ca_file || "",
      tlsCertFile: cfg.tls_cert_file || "",
      tlsKeyFile: cfg.tls_key_file || "",
      dialTimeoutSeconds: cfg.dial_timeout_seconds || 5,
      commandTimeoutSeconds: cfg.command_timeout_seconds || 10,
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...ETCD_DEFAULTS };
  }
}
