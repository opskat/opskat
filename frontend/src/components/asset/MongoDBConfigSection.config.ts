import type { CredentialFragment } from "./credentialConfig";
import {
  CONNECTION_DEFAULTS,
  buildProxyJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyConfigJSON,
} from "./proxyConfig";

export interface MongoDBFormState extends ConnectionFormFields {
  connectionMode: "manual" | "uri";
  connectionURI: string;
  host: string;
  port: number;
  username: string;
  replicaSet: string;
  authSource: string;
  database: string;
  tls: boolean;
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
  ...CONNECTION_DEFAULTS,
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
  proxy?: ProxyConfigJSON;
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支)。cred 由 resolveSave/TestCredential 预解析。
 * 隧道走 asset 顶层列(sshTunnelId);save 不写 ssh_asset_id(锁旧 save 分支)。
 * 测试无 asset 行,buildTestConfig 传 includeSshAssetId=true 把隧道塞进 config(锁旧 handleTestMongoDBConnection)。
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test=明文)预解析;
 * 隧道与代理互斥,按 connectionType 二选一;URI 模式同样写 proxy(后端 DialMongoDB URI+proxy 已支持)。
 */
export function buildMongoDBConfig(
  state: MongoDBFormState,
  cred: CredentialFragment,
  includeSshAssetId = false,
  proxyPassword = ""
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
  if (state.connectionType === "jumphost" && includeSshAssetId && state.sshTunnelId > 0)
    cfg.ssh_asset_id = state.sshTunnelId;
  const proxy = buildProxyJSON(state, proxyPassword);
  if (proxy) cfg.proxy = proxy;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadMongoDBConfig 非凭据字段;connectionType 派生需要 asset 顶层
 *  sshTunnelId 优先(镜像旧 `asset.sshTunnelId || cfg.ssh_asset_id || 0`),故由 section 传入)。 */
export function parseMongoDBConfig(configJSON: string, assetTunnelId = 0): MongoDBFormState {
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
      ...parseConnectionFields(cfg.proxy, assetTunnelId || cfg.ssh_asset_id || 0),
    };
  } catch {
    return { ...MONGODB_DEFAULTS };
  }
}
