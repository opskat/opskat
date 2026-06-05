import type { CredentialFragment } from "./credentialConfig";

export interface RedisFormState {
  host: string;
  port: number;
  username: string;
  database: number;
  commandTimeoutSeconds: number;
  scanPageSize: number;
  keySeparator: string;
  tls: boolean;
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
  sshTunnelId: number;
}

export const REDIS_DEFAULTS: RedisFormState = {
  host: "",
  port: 6379,
  username: "",
  database: 0,
  commandTimeoutSeconds: 30,
  scanPageSize: 200,
  keySeparator: ":",
  tls: false,
  tlsInsecure: false,
  tlsServerName: "",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
  sshTunnelId: 0,
};

interface RedisConfig {
  host: string;
  port: number;
  username?: string;
  password?: string;
  credential_id?: number;
  database?: number;
  tls?: boolean;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
  command_timeout_seconds?: number;
  scan_page_size?: number;
  key_separator?: string;
  ssh_asset_id?: number;
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支)。cred 由 resolveSave/TestCredential 预解析。
 * 隧道走 asset 顶层列(sshTunnelId);save 不写 ssh_asset_id(锁旧 save 分支)。
 * 测试无 asset 行,buildTestConfig 传 includeSshAssetId=true 把隧道塞进 config(锁旧 handleTestRedisConnection)。
 */
export function buildRedisConfig(state: RedisFormState, cred: CredentialFragment, includeSshAssetId = false): string {
  const cfg: RedisConfig = { host: state.host, port: state.port };
  if (state.username) cfg.username = state.username;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  if (state.database > 0) cfg.database = state.database;
  if (state.tls) cfg.tls = true;
  if (state.tls && state.tlsInsecure) cfg.tls_insecure = true;
  if (state.tls && state.tlsServerName) cfg.tls_server_name = state.tlsServerName;
  if (state.tls && state.tlsCAFile) cfg.tls_ca_file = state.tlsCAFile;
  if (state.tls && state.tlsCertFile) cfg.tls_cert_file = state.tlsCertFile;
  if (state.tls && state.tlsKeyFile) cfg.tls_key_file = state.tlsKeyFile;
  if (state.commandTimeoutSeconds > 0) cfg.command_timeout_seconds = state.commandTimeoutSeconds;
  if (state.scanPageSize > 0) cfg.scan_page_size = state.scanPageSize;
  if (state.keySeparator && state.keySeparator !== ":") cfg.key_separator = state.keySeparator;
  if (includeSshAssetId && state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadRedisConfig 非凭据字段;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseRedisConfig(configJSON: string): RedisFormState {
  try {
    const cfg: RedisConfig = JSON.parse(configJSON || "{}");
    return {
      host: cfg.host || "",
      port: cfg.port || 6379,
      username: cfg.username || "",
      database: Math.max(0, cfg.database || 0),
      commandTimeoutSeconds: cfg.command_timeout_seconds || 30,
      scanPageSize: cfg.scan_page_size || 200,
      keySeparator: cfg.key_separator || ":",
      tls: cfg.tls || false,
      tlsInsecure: cfg.tls_insecure || false,
      tlsServerName: cfg.tls_server_name || "",
      tlsCAFile: cfg.tls_ca_file || "",
      tlsCertFile: cfg.tls_cert_file || "",
      tlsKeyFile: cfg.tls_key_file || "",
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...REDIS_DEFAULTS };
  }
}
