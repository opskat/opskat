import type { CredentialFragment } from "./credentialConfig";

export interface MongoDBFormState {
  connectionMode: "manual" | "uri";
  connectionURI: string;
  host: string;
  port: number;
  username: string;
  replicaSet: string;
  authSource: string;
  database: string;
  tls: boolean;
  sshTunnelId: number;
}

export const MONGODB_DEFAULTS: MongoDBFormState = {
  connectionMode: "manual",
  connectionURI: "",
  host: "",
  port: 27017,
  username: "",
  replicaSet: "",
  authSource: "",
  database: "",
  tls: false,
  sshTunnelId: 0,
};

interface MongoDBConfig {
  connection_uri?: string;
  host?: string;
  port?: number;
  replica_set?: string;
  username?: string;
  password?: string;
  credential_id?: number;
  database?: string;
  auth_source?: string;
  tls?: boolean;
  ssh_asset_id?: number;
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支)。cred 由 resolveSave/TestCredential 预解析。
 * 隧道走 asset 顶层列(sshTunnelId);save 不写 ssh_asset_id(锁旧 save 分支)。
 * 测试无 asset 行,buildTestConfig 传 includeSshAssetId=true 把隧道塞进 config(锁旧 handleTestMongoDBConnection)。
 */
export function buildMongoDBConfig(
  state: MongoDBFormState,
  cred: CredentialFragment,
  includeSshAssetId = false
): string {
  const cfg: MongoDBConfig = {};
  if (state.connectionMode === "uri" && state.connectionURI) {
    cfg.connection_uri = state.connectionURI;
  } else {
    cfg.host = state.host;
    cfg.port = state.port;
  }
  if (state.username) cfg.username = state.username;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  if (state.replicaSet) cfg.replica_set = state.replicaSet;
  if (state.authSource) cfg.auth_source = state.authSource;
  if (state.database) cfg.database = state.database;
  if (state.tls) cfg.tls = true;
  if (includeSshAssetId && state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadMongoDBConfig 非凭据字段;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseMongoDBConfig(configJSON: string): MongoDBFormState {
  try {
    const cfg: MongoDBConfig = JSON.parse(configJSON || "{}");
    return {
      connectionMode: cfg.connection_uri ? "uri" : "manual",
      connectionURI: cfg.connection_uri || "",
      host: cfg.host || "",
      port: cfg.port || 27017,
      username: cfg.username || "",
      replicaSet: cfg.replica_set || "",
      authSource: cfg.auth_source || "",
      database: cfg.database || "",
      tls: cfg.tls || false,
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...MONGODB_DEFAULTS };
  }
}
