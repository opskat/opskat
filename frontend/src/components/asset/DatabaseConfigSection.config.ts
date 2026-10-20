import { driverUsesField, type DriverDescriptor } from "@/lib/dbDrivers/descriptors";
import type { CredentialFragment } from "./credentialConfig";
import {
  CONNECTION_DEFAULTS,
  buildProxyChainJSON,
  buildProxyJSON,
  parseConnectionFields,
  type ProxyChainJSON,
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
  queryTimeoutSeconds: number;
  /** 用户在表单上改过的驱动专属字段取值(口令类为明文);切换驱动时清空。 */
  driverOptions: Record<string, unknown>;
  /** 资产已存的 driver_options 及其所属驱动(口令类为密文):回填与沿用未改的口令。 */
  storedDriverOptions: { driver: string; values: Record<string, unknown> };
}

/** 驱动尚未选定的空白表单;新建时由 applyDriverChange 套上驱动描述里的默认值。 */
export const DATABASE_DEFAULTS: DatabaseFormState = {
  driver: "",
  sqliteSource: "local",
  host: "",
  port: 0,
  username: "",
  database: "",
  sslMode: "disable",
  tls: false,
  readOnly: false,
  params: "",
  path: "",
  queryTimeoutSeconds: 30,
  driverOptions: {},
  storedDriverOptions: { driver: "", values: {} },
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
  query_timeout_seconds?: number;
  ssh_asset_id?: number;
  sqlite_source?: "local" | "remote_ssh_vfs";
  path?: string;
  proxy?: ProxyConfigJSON;
  proxy_chain?: ProxyChainJSON;
  driver_options?: Record<string, unknown>;
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支:driver 首键;文件型驱动→[sqlite_source/ssh_asset_id/]path;
 * 主机型驱动→host/port/username/[credential|password]/[ssh_asset_id]/[ssl_mode]/[tls]/[proxy];
 * 末尾共有 database/read_only/params)。写哪些键由驱动描述声明的字段决定,不认驱动名。
 * driverOptions 由 buildDriverOptions 预解析(口令类已加密),放在末尾。
 * cred 由 resolveSave/TestCredential 预解析;proxyPassword 由 resolveSaveProxyPassword(save=密文)
 * 或 state.proxyPassword(test=明文)预解析。隧道与代理互斥,按 connectionType 二选一;
 * 自带 SSH 资产字段的驱动(远端文件)用那个字段,不吃通用隧道 / 代理配置。
 */
export function buildDatabaseConfig(
  state: DatabaseFormState,
  driver: DriverDescriptor,
  cred: CredentialFragment,
  proxyPassword = "",
  proxyChainSecrets?: Record<string, { password?: string; token?: string }>,
  driverOptions?: Record<string, unknown>
): string {
  const uses = (field: string) => driverUsesField(driver, field);
  const cfg: DatabaseConfig = { driver: state.driver };
  if (uses("sqlite_source") && state.sqliteSource === "remote_ssh_vfs") {
    cfg.sqlite_source = "remote_ssh_vfs";
    if (uses("ssh_asset_id") && state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  }
  if (uses("path")) cfg.path = state.path;
  if (uses("host")) cfg.host = state.host;
  if (uses("port")) cfg.port = state.port;
  if (uses("username")) cfg.username = state.username;
  if (uses("password")) {
    if (cred.credential_id) cfg.credential_id = cred.credential_id;
    else if (cred.password) cfg.password = cred.password;
  }
  const sharedTunnel = uses("tunnel") && !uses("ssh_asset_id");
  if (sharedTunnel && state.connectionType === "jumphost" && state.sshTunnelId > 0) {
    cfg.ssh_asset_id = state.sshTunnelId;
  }
  if (uses("ssl_mode") && state.sslMode !== "disable") cfg.ssl_mode = state.sslMode;
  if (uses("tls") && state.tls) cfg.tls = true;
  if (sharedTunnel) {
    const proxy = buildProxyJSON(state, proxyPassword);
    if (proxy) cfg.proxy = proxy;
    const proxyChain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
    if (proxyChain) cfg.proxy_chain = proxyChain;
  }
  if (state.database) cfg.database = state.database;
  if (state.readOnly) cfg.read_only = true;
  if (state.params) cfg.params = state.params;
  if (state.queryTimeoutSeconds > 0) cfg.query_timeout_seconds = state.queryTimeoutSeconds;
  if (driverOptions) cfg.driver_options = driverOptions;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadDatabaseConfig 非凭据字段;connectionType 派生需要 asset 顶层
 *  sshTunnelId 优先(镜像旧 `asset.sshTunnelId || cfg.ssh_asset_id || 0`),故由 section 传入)。 */
export function parseDatabaseConfig(configJSON: string, assetTunnelId = 0): DatabaseFormState {
  try {
    const cfg: DatabaseConfig = JSON.parse(configJSON || "{}");
    return {
      driver: cfg.driver ?? "",
      sqliteSource: cfg.sqlite_source || "local",
      host: cfg.host || "",
      port: cfg.port ?? 0,
      username: cfg.username || "",
      database: cfg.database || "",
      sslMode: cfg.ssl_mode || "disable",
      tls: cfg.tls || false,
      readOnly: cfg.read_only || false,
      params: cfg.params || "",
      path: cfg.path || "",
      queryTimeoutSeconds: cfg.query_timeout_seconds || 30,
      driverOptions: {},
      storedDriverOptions: { driver: cfg.driver ?? "", values: cfg.driver_options ?? {} },
      ...parseConnectionFields(cfg.proxy, assetTunnelId || cfg.ssh_asset_id || 0, cfg.proxy_chain),
    };
  } catch {
    return { ...DATABASE_DEFAULTS };
  }
}

/**
 * driver 切换的 section 自有字段复位(壳 icon 副作用留在组件),按新驱动的描述:
 * 不用主机的驱动(文件型)→ 清 host/username,port=0,path 保留,连接方式复位;
 * 用主机的驱动 → port=描述的默认端口,清 path,驱动不用 ssl_mode 时复位 sslMode。
 * 专属字段是各驱动自己的:改过的取值清空,新驱动的字段从已存值或默认值起步。
 */
export function applyDriverChange(state: DatabaseFormState, driver: DriverDescriptor): DatabaseFormState {
  if (!driverUsesField(driver, "host")) {
    return {
      ...state,
      driver: driver.id,
      driverOptions: {},
      sqliteSource: "local",
      host: "",
      port: 0,
      username: "",
      ...CONNECTION_DEFAULTS,
    };
  }
  return {
    ...state,
    driver: driver.id,
    driverOptions: {},
    sqliteSource: "local",
    port: driver.defaultPort,
    path: "",
    sslMode: driverUsesField(driver, "ssl_mode") ? state.sslMode : "disable",
  };
}
