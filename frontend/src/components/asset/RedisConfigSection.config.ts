import type { CredentialFragment } from "./credentialConfig";
import {
  CONNECTION_DEFAULTS,
  buildProxyChainJSON,
  buildProxyJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyChainJSON,
  type ProxyConfigJSON,
} from "./proxyConfig";

export type RedisMode = "standalone" | "cluster" | "sentinel";

export interface RedisFormState extends ConnectionFormFields {
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
  /** 部署模式;standalone 时不写入 config(旧资产无此字段按单机处理)。 */
  mode: RedisMode;
  /** 集群种子节点 / 哨兵节点原始多行文本,每行一个 host:port。 */
  nodes: string;
  /** 哨兵监控的主节点名称(master name),仅哨兵模式必填。 */
  masterName: string;
  sentinelUsername: string;
  /** 明文,用户新输入才非空;留空且有既有密文时保存/测试沿用既有密文。 */
  sentinelPassword: string;
  /** 编辑态既有密文。 */
  encryptedSentinelPassword: string;
  /** 「宣告地址 = 实际地址」原始多行文本,仅集群/哨兵模式可用。 */
  nodeAddressMap: string;
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
  mode: "standalone",
  nodes: "",
  masterName: "",
  sentinelUsername: "",
  sentinelPassword: "",
  encryptedSentinelPassword: "",
  nodeAddressMap: "",
  ...CONNECTION_DEFAULTS,
};

interface RedisConfig {
  host?: string;
  port?: number;
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
  proxy?: ProxyConfigJSON;
  proxy_chain?: ProxyChainJSON;
  mode?: string;
  nodes?: string[];
  master_name?: string;
  sentinel_username?: string;
  sentinel_password?: string;
  node_address_map?: Record<string, string>;
}

/** 把换行/逗号/分号分隔的节点文本规范化为非空 host:port 列表(镜像 parseEtcdEndpoints)。 */
export function parseRedisNodes(raw: string): string[] {
  return raw
    .split(/[\n,;]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export interface NodeAddressMapError {
  line: number;
  key: string;
}

export interface NodeAddressMapResult {
  map: Record<string, string>;
  error?: NodeAddressMapError;
}

/** host:port 格式校验(镜像后端 validateRedisHostPort:IPv6 需方括号,端口 1-65535)。 */
function isRedisHostPort(addr: string): boolean {
  const m = /^(?:\[([^\]]+)\]|([^:\s[\]]+)):(\d+)$/.exec(addr);
  if (!m) return false;
  const port = Number(m[3]);
  return port > 0 && port <= 65535;
}

/**
 * 解析「宣告地址 = 实际地址」逐行文本。右侧留空视为尚未填写(不写入 map,非错误),
 * 配合「生成映射」占位行(用户后续手填)。缺少 `=`、任一侧不是 host:port 或宣告地址重复
 * 视为格式错误,返回首个错误所在行(1 基)供保存前拦截并定位是哪一行。
 */
export function parseNodeAddressMap(raw: string): NodeAddressMapResult {
  const map: Record<string, string> = {};
  const seen = new Set<string>();
  const lines = raw.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line.trim()) continue;
    const eq = line.indexOf("=");
    if (eq < 0) return { map, error: { line: i + 1, key: "asset.redisMappingLineInvalid" } };
    const announced = line.slice(0, eq).trim();
    const actual = line.slice(eq + 1).trim();
    if (!isRedisHostPort(announced) || (actual && !isRedisHostPort(actual))) {
      return { map, error: { line: i + 1, key: "asset.redisMappingLineInvalid" } };
    }
    if (seen.has(announced)) return { map, error: { line: i + 1, key: "asset.redisMappingDuplicate" } };
    seen.add(announced);
    if (actual) map[announced] = actual;
  }
  return { map };
}

/** saveDisabledReason 用的扁平 key(镜像 proxyChainValidationKey);无错误时为空串。 */
export function nodeAddressMapValidationKey(raw: string): string {
  return parseNodeAddressMap(raw).error?.key ?? "";
}

/** 已列出的宣告地址集合(不论该行是否已填实际地址、也不论格式是否有效),供「生成映射」判断哪些不可达地址还没出现过。 */
export function listedMappingAnnouncedAddresses(raw: string): Set<string> {
  const set = new Set<string>();
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue;
    const eq = line.indexOf("=");
    const announced = (eq < 0 ? line : line.slice(0, eq)).trim();
    if (announced) set.add(announced);
  }
  return set;
}

/** 模式必填校验(镜像后端 validateMode);canTest/canSave 共用,与 formMissingHost 同类提示。 */
export function redisModeRequiredKey(state: RedisFormState): string {
  if (state.mode === "standalone") return state.host.trim() ? "" : "asset.formMissingHost";
  if (parseRedisNodes(state.nodes).length === 0) return "asset.redisNodesRequired";
  if (state.mode === "sentinel" && !state.masterName.trim()) return "asset.redisMasterNameRequired";
  return "";
}

export interface SentinelPasswordFragment {
  sentinel_password?: string;
}

/** 哨兵密码测试片段:未改动(无新明文)且有既有密文时沿用既有密文;否则不写
 *  (RedisProbe 走 plainSentinelPassword 单独传,镜像数据节点密码 resolveTestCredential 的模式)。 */
export function resolveTestSentinelPassword(state: RedisFormState): SentinelPasswordFragment {
  if (!state.sentinelPassword && state.encryptedSentinelPassword) {
    return { sentinel_password: state.encryptedSentinelPassword };
  }
  return {};
}

/** 哨兵密码保存片段:新明文则加密,否则沿用既有密文,都无则不写。加密失败由 encrypt 的 reject 透传。 */
export async function resolveSaveSentinelPassword(
  state: RedisFormState,
  encrypt: (plain: string) => Promise<string>
): Promise<SentinelPasswordFragment> {
  if (state.sentinelPassword) return { sentinel_password: await encrypt(state.sentinelPassword) };
  if (state.encryptedSentinelPassword) return { sentinel_password: state.encryptedSentinelPassword };
  return {};
}

/**
 * 保存/测试共用序列化(键序锁旧 save 分支;新增字段只在非 standalone 模式写入,
 * standalone 输出与旧版逐字节相同)。cred 由 resolveSave/TestCredential 预解析。
 * 隧道走 asset 顶层列(sshTunnelId);save 不写 ssh_asset_id(锁旧 save 分支)。
 * 测试无 asset 行,buildTestConfig 传 includeSshAssetId=true 把隧道塞进 config(锁旧 handleTestRedisConnection)。
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test=明文)预解析;
 * 隧道与代理互斥,按 connectionType 二选一。sentinelPassword 由 resolveSave/TestSentinelPassword 预解析。
 */
export function buildRedisConfig(
  state: RedisFormState,
  cred: CredentialFragment,
  includeSshAssetId = false,
  proxyPassword = "",
  proxyChainSecrets?: Record<string, { password?: string; token?: string }>,
  sentinelPassword: SentinelPasswordFragment = {}
): string {
  const cfg: RedisConfig = {};
  if (state.mode === "standalone") {
    cfg.host = state.host;
    cfg.port = state.port;
  }
  if (state.username) cfg.username = state.username;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  // 集群固定 db0,不提供数据库选项;即便 state.database 留有切换模式前的旧值也不写。
  if (state.mode !== "cluster" && state.database > 0) cfg.database = state.database;
  if (state.tls) cfg.tls = true;
  if (state.tls && state.tlsInsecure) cfg.tls_insecure = true;
  if (state.tls && state.tlsServerName) cfg.tls_server_name = state.tlsServerName;
  if (state.tls && state.tlsCAFile) cfg.tls_ca_file = state.tlsCAFile;
  if (state.tls && state.tlsCertFile) cfg.tls_cert_file = state.tlsCertFile;
  if (state.tls && state.tlsKeyFile) cfg.tls_key_file = state.tlsKeyFile;
  const proxy = buildProxyJSON(state, proxyPassword);
  if (proxy) cfg.proxy = proxy;
  const proxyChain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (proxyChain) cfg.proxy_chain = proxyChain;
  if (state.commandTimeoutSeconds > 0) cfg.command_timeout_seconds = state.commandTimeoutSeconds;
  if (state.scanPageSize > 0) cfg.scan_page_size = state.scanPageSize;
  if (state.keySeparator && state.keySeparator !== ":") cfg.key_separator = state.keySeparator;
  if (state.mode !== "standalone") {
    cfg.mode = state.mode;
    const nodes = parseRedisNodes(state.nodes);
    if (nodes.length) cfg.nodes = nodes;
    const map = parseNodeAddressMap(state.nodeAddressMap).map;
    if (Object.keys(map).length) cfg.node_address_map = map;
  }
  if (state.mode === "sentinel") {
    if (state.masterName.trim()) cfg.master_name = state.masterName.trim();
    if (state.sentinelUsername.trim()) cfg.sentinel_username = state.sentinelUsername.trim();
    if (sentinelPassword.sentinel_password) cfg.sentinel_password = sentinelPassword.sentinel_password;
  }
  if (state.connectionType === "jumphost" && includeSshAssetId && state.sshTunnelId > 0)
    cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadRedisConfig 非凭据字段;connectionType 派生需要 asset 顶层
 *  sshTunnelId 优先(镜像旧 `asset.sshTunnelId || cfg.ssh_asset_id || 0`),故由 section 传入)。 */
export function parseRedisConfig(configJSON: string, assetTunnelId = 0): RedisFormState {
  try {
    const cfg: RedisConfig = JSON.parse(configJSON || "{}");
    const mode: RedisMode = cfg.mode === "cluster" || cfg.mode === "sentinel" ? cfg.mode : "standalone";
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
      mode,
      nodes: (cfg.nodes || []).join("\n"),
      masterName: cfg.master_name || "",
      sentinelUsername: cfg.sentinel_username || "",
      sentinelPassword: "",
      encryptedSentinelPassword: cfg.sentinel_password || "",
      nodeAddressMap: Object.entries(cfg.node_address_map || {})
        .map(([k, v]) => `${k} = ${v}`)
        .join("\n"),
      ...parseConnectionFields(cfg.proxy, assetTunnelId || cfg.ssh_asset_id || 0, cfg.proxy_chain),
    };
  } catch {
    return { ...REDIS_DEFAULTS };
  }
}
