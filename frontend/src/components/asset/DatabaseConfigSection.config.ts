import type { CredentialFragment } from "./credentialConfig";

export interface DatabaseFormState {
  driver: string;
  host: string;
  port: number;
  username: string;
  database: string;
  sslMode: string;
  tls: boolean;
  readOnly: boolean;
  params: string;
  path: string;
  sshTunnelId: number;
}

export const DATABASE_DEFAULTS: DatabaseFormState = {
  driver: "mysql",
  host: "",
  port: 3306,
  username: "",
  database: "",
  sslMode: "disable",
  tls: false,
  readOnly: false,
  params: "",
  path: "",
  sshTunnelId: 0,
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
  path?: string;
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
 * 非 sqlite→host/port/username/[credential|password]/[ssh_asset_id]/[ssl_mode]/[tls];
 * 末尾共有 database/read_only/params)。cred 由 resolveSave/TestCredential 预解析;
 * sqlite 分支忽略 cred / host / port / ssh。
 */
export function buildDatabaseConfig(state: DatabaseFormState, cred: CredentialFragment): string {
  const cfg: DatabaseConfig = { driver: state.driver };
  if (state.driver === "sqlite") {
    cfg.path = state.path;
  } else {
    cfg.host = state.host;
    cfg.port = state.port;
    cfg.username = state.username;
    if (cred.credential_id) cfg.credential_id = cred.credential_id;
    else if (cred.password) cfg.password = cred.password;
    if (state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
    if (state.driver === "postgresql" && state.sslMode !== "disable") cfg.ssl_mode = state.sslMode;
    if ((state.driver === "mysql" || state.driver === "mssql") && state.tls) cfg.tls = true;
  }
  if (state.database) cfg.database = state.database;
  if (state.readOnly) cfg.read_only = true;
  if (state.params) cfg.params = state.params;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadDatabaseConfig 非凭据字段;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseDatabaseConfig(configJSON: string): DatabaseFormState {
  try {
    const cfg: DatabaseConfig = JSON.parse(configJSON || "{}");
    return {
      driver: cfg.driver || "mysql",
      host: cfg.host || "",
      port: cfg.port || 3306,
      username: cfg.username || "",
      database: cfg.database || "",
      sslMode: cfg.ssl_mode || "disable",
      tls: cfg.tls || false,
      readOnly: cfg.read_only || false,
      params: cfg.params || "",
      path: cfg.path || "",
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...DATABASE_DEFAULTS };
  }
}

/**
 * driver 切换的 section 自有字段复位(镜像旧 handleDriverChange,壳 icon 副作用留在组件)。
 * sqlite → 清 host/username/sshTunnel,port=0,path 保留;
 * 非 sqlite → port=DEFAULT_PORTS[driver]||3306,清 path,非 postgresql 复位 sslMode。
 */
export function applyDriverChange(state: DatabaseFormState, newDriver: string): DatabaseFormState {
  if (newDriver === "sqlite") {
    return { ...state, driver: newDriver, host: "", port: 0, username: "", sshTunnelId: 0 };
  }
  return {
    ...state,
    driver: newDriver,
    port: DRIVER_PORTS[newDriver] || 3306,
    path: "",
    sslMode: newDriver === "postgresql" ? state.sslMode : "disable",
  };
}
