import type { CredentialFragment } from "./credentialConfig";
import {
  CONNECTION_DEFAULTS,
  buildProxyJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyConfigJSON,
} from "./proxyConfig";

export interface DatabaseFormState extends ConnectionFormFields {
  driver: string;
  sqliteSource: "local" | "remote_ssh_vfs";
  host: string;
  port: number;
  username: string;
  database: string;
  sslMode: string;
  tls: boolean;
  readOnly: boolean;
  params: string;
  path: string;
}

export const DATABASE_DEFAULTS: DatabaseFormState = {
  driver: "mysql",
  sqliteSource: "local",
  host: "",
  port: 3306,
  username: "",
  database: "",
  sslMode: "disable",
  tls: false,
  readOnly: false,
  params: "",
  path: "",
  ...CONNECTION_DEFAULTS,
};

interface DatabaseConfig {
  driver: string;
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  credential_id?: number;
  database?: string;
  ssl_mode?: string;
  tls?: boolean;
  params?: string;
  read_only?: boolean;
  ssh_asset_id?: number;
  sqlite_source?: "local" | "remote_ssh_vfs";
  path?: string;
  proxy?: ProxyConfigJSON;
}

/** driver→默认端口(镜像旧 DEFAULT_PORTS;sqlite 无端口)。 */
const DRIVER_PORTS: Record<string, number> = {
  mysql: 3306,
  postgresql: 5432,
  mssql: 1433,
};

/** driver→壳 icon(镜像旧 DEFAULT_ICONS;mssql 落回 "database")。 */
export function driverIcon(driver: string): string {
  if (driver === "sqlite") return "sqlite";
  if (driver === "mysql") return "mysql";
  if (driver === "postgresql") return "postgresql";
  return "database";
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支:driver 首键;sqlite→path;
 * 非 sqlite→host/port/username/[credential|password]/[ssh_asset_id]/[ssl_mode]/[tls]/[proxy];
 * 末尾共有 database/read_only/params)。cred 由 resolveSave/TestCredential 预解析;
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test=明文)预解析;
 * sqlite local 分支忽略 cred / host / port / ssh / proxy;remote_ssh_vfs 写 sqlite_source + ssh_asset_id。
 * 隧道与代理互斥,按 connectionType 二选一。
 */
export function buildDatabaseConfig(state: DatabaseFormState, cred: CredentialFragment, proxyPassword = ""): string {
  const cfg: DatabaseConfig = { driver: state.driver };
  if (state.driver === "sqlite") {
    if (state.sqliteSource === "remote_ssh_vfs") {
      cfg.sqlite_source = "remote_ssh_vfs";
      if (state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
    }
    cfg.path = state.path;
  } else {
    cfg.host = state.host;
    cfg.port = state.port;
    cfg.username = state.username;
    if (cred.credential_id) cfg.credential_id = cred.credential_id;
    else if (cred.password) cfg.password = cred.password;
    if (state.connectionType === "jumphost" && state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
    if (state.driver === "postgresql" && state.sslMode !== "disable") cfg.ssl_mode = state.sslMode;
    if ((state.driver === "mysql" || state.driver === "mssql") && state.tls) cfg.tls = true;
    const proxy = buildProxyJSON(state, proxyPassword);
    if (proxy) cfg.proxy = proxy;
  }
  if (state.database) cfg.database = state.database;
  if (state.readOnly) cfg.read_only = true;
  if (state.params) cfg.params = state.params;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadDatabaseConfig 非凭据字段;connectionType 派生需要 asset 顶层
 *  sshTunnelId 优先(镜像旧 `asset.sshTunnelId || cfg.ssh_asset_id || 0`),故由 section 传入)。 */
export function parseDatabaseConfig(configJSON: string, assetTunnelId = 0): DatabaseFormState {
  try {
    const cfg: DatabaseConfig = JSON.parse(configJSON || "{}");
    return {
      driver: cfg.driver || "mysql",
      sqliteSource: cfg.sqlite_source || "local",
      host: cfg.host || "",
      port: cfg.port || 3306,
      username: cfg.username || "",
      database: cfg.database || "",
      sslMode: cfg.ssl_mode || "disable",
      tls: cfg.tls || false,
      readOnly: cfg.read_only || false,
      params: cfg.params || "",
      path: cfg.path || "",
      ...parseConnectionFields(cfg.proxy, assetTunnelId || cfg.ssh_asset_id || 0),
    };
  } catch {
    return { ...DATABASE_DEFAULTS };
  }
}

/**
 * driver 切换的 section 自有字段复位(镜像旧 handleDriverChange,壳 icon 副作用留在组件)。
 * sqlite → 清 host/username,port=0,path 保留,本地源清连接方式;
 * 非 sqlite → port=DEFAULT_PORTS[driver]||3306,清 path,非 postgresql 复位 sslMode。
 */
export function applyDriverChange(state: DatabaseFormState, newDriver: string): DatabaseFormState {
  if (newDriver === "sqlite") {
    return {
      ...state,
      driver: newDriver,
      sqliteSource: "local",
      host: "",
      port: 0,
      username: "",
      ...CONNECTION_DEFAULTS,
    };
  }
  return {
    ...state,
    driver: newDriver,
    sqliteSource: "local",
    port: DRIVER_PORTS[newDriver] || 3306,
    path: "",
    sslMode: newDriver === "postgresql" ? state.sslMode : "disable",
  };
}
