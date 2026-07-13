/** 连接方式与代理链的共享表单状态与序列化。
 *  SSH 与数据库族 section 共用;UI 见 ConnectionMethodFields。 */

export type ConnectionType = "direct" | "jumphost" | "proxy";

export interface ProxyConfigJSON {
  type: string;
  host: string;
  port: number;
  username?: string;
  password?: string;
}

export type ProxyChainLayerType = "ssh" | "socks5" | "http_tunnel";

export interface ProxyChainLayerJSON {
  id?: string;
  name?: string;
  enabled?: boolean;
  type: ProxyChainLayerType;
  order?: number;
  ssh_asset_id?: number;
  host?: string;
  port?: number;
  username?: string;
  password?: string;
  url?: string;
  token?: string;
  timeout_seconds?: number;
}

export interface ProxyChainJSON {
  layers?: ProxyChainLayerJSON[];
}

export interface ProxyChainLayerForm {
  id: string;
  name: string;
  enabled: boolean;
  type: ProxyChainLayerType;
  sshAssetId: number;
  host: string;
  port: number;
  username: string;
  password: string;
  encryptedPassword: string;
  url: string;
  token: string;
  encryptedToken: string;
  timeoutSeconds: number;
}

export interface ConnectionFormFields {
  connectionType: ConnectionType;
  sshTunnelId: number;
  proxyType: string;
  proxyHost: string;
  proxyPort: number;
  proxyUsername: string;
  /** 用户新输入的明文 proxy 密码;空表示沿用既有密文。 */
  proxyPassword: string;
  /** 编辑态既有 proxy 密码密文;不回显。 */
  encryptedProxyPassword: string;
  proxyChainLayers: ProxyChainLayerForm[];
}

export const CONNECTION_DEFAULTS: ConnectionFormFields = {
  connectionType: "direct",
  sshTunnelId: 0,
  proxyType: "socks5",
  proxyHost: "",
  proxyPort: 1080,
  proxyUsername: "",
  proxyPassword: "",
  encryptedProxyPassword: "",
  proxyChainLayers: [],
};

function id(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function sshProxyLayer(assetId = 0, name = "SSH Layer", layerId = id("ssh")): ProxyChainLayerForm {
  return {
    id: layerId,
    name,
    enabled: true,
    type: "ssh",
    sshAssetId: assetId,
    host: "",
    port: 22,
    username: "",
    password: "",
    encryptedPassword: "",
    url: "",
    token: "",
    encryptedToken: "",
    timeoutSeconds: 10,
  };
}

export function socks5ProxyLayer(proxy?: ProxyConfigJSON): ProxyChainLayerForm {
  return {
    id: proxy ? "legacy-socks5-proxy" : id("socks5"),
    name: "SOCKS5 Proxy",
    enabled: true,
    type: "socks5",
    sshAssetId: 0,
    host: proxy?.host || "",
    port: proxy?.port || 1080,
    username: proxy?.username || "",
    password: "",
    encryptedPassword: proxy?.password || "",
    url: "",
    token: "",
    encryptedToken: "",
    timeoutSeconds: 10,
  };
}

export function httpTunnelProxyLayer(): ProxyChainLayerForm {
  return {
    id: id("http"),
    name: "HTTP Tunnel",
    enabled: true,
    type: "http_tunnel",
    sshAssetId: 0,
    host: "",
    port: 0,
    username: "",
    password: "",
    encryptedPassword: "",
    url: "",
    token: "",
    encryptedToken: "",
    timeoutSeconds: 10,
  };
}

/** 编辑态回填:隧道 > 代理 > 直连(与后端拨号优先级一致)。 */
export function parseConnectionFields(
  proxy: ProxyConfigJSON | null | undefined,
  tunnelId: number,
  proxyChain?: ProxyChainJSON | null
): ConnectionFormFields {
  const chainLayers = parseProxyChain(proxyChain, tunnelId, proxy);
  return {
    connectionType: tunnelId ? "jumphost" : proxy ? "proxy" : chainLayers.length > 0 ? "jumphost" : "direct",
    sshTunnelId: tunnelId,
    proxyType: proxy?.type || "socks5",
    proxyHost: proxy?.host || "",
    proxyPort: proxy?.port || 1080,
    proxyUsername: proxy?.username || "",
    proxyPassword: "",
    encryptedProxyPassword: proxy?.password || "",
    proxyChainLayers: chainLayers,
  };
}

/** proxy 模式且填了 host 才输出;resolvedProxyPassword 由调用方预解析(save=密文 / test=明文)。 */
export function buildProxyJSON(f: ConnectionFormFields, resolvedProxyPassword: string): ProxyConfigJSON | undefined {
  if (f.connectionType !== "proxy" || !f.proxyHost) return undefined;
  return {
    type: f.proxyType,
    host: f.proxyHost,
    port: f.proxyPort,
    username: f.proxyUsername || undefined,
    password: resolvedProxyPassword || undefined,
  };
}

export function parseProxyChain(
  proxyChain: ProxyChainJSON | null | undefined,
  tunnelId = 0,
  proxy?: ProxyConfigJSON | null
): ProxyChainLayerForm[] {
  if (proxyChain?.layers?.length) {
    return [...proxyChain.layers]
      .sort((a, b) => (a.order || 0) - (b.order || 0))
      .map((layer) => ({
        id: layer.id || id(layer.type),
        name: layer.name || defaultLayerName(layer.type),
        enabled: layer.enabled ?? true,
        type: layer.type,
        sshAssetId: layer.ssh_asset_id || 0,
        host: layer.host || "",
        port: layer.port || (layer.type === "socks5" ? 1080 : 22),
        username: layer.username || "",
        password: "",
        encryptedPassword: layer.password || "",
        url: layer.url || "",
        token: "",
        encryptedToken: layer.token || "",
        timeoutSeconds: layer.timeout_seconds || 10,
      }));
  }
  if (tunnelId) return [sshProxyLayer(tunnelId, "SSH Tunnel", `legacy-ssh-${tunnelId}`)];
  if (proxy) return [socks5ProxyLayer(proxy)];
  return [];
}

function defaultLayerName(type: ProxyChainLayerType) {
  if (type === "ssh") return "SSH Layer";
  if (type === "http_tunnel") return "HTTP Tunnel";
  return "SOCKS5 Proxy";
}

export function buildProxyChainJSON(
  layers: ProxyChainLayerForm[],
  secrets: Record<string, { password?: string; token?: string }> = {}
): ProxyChainJSON | undefined {
  const enabled = layers.filter((layer) => layer.enabled);
  if (!enabled.length) return undefined;
  return {
    layers: enabled.map((layer, index) => {
      const secret = secrets[layer.id] || {};
      const base: ProxyChainLayerJSON = {
        id: layer.id,
        name: layer.name,
        enabled: layer.enabled,
        type: layer.type,
        order: index + 1,
      };
      if (layer.type === "ssh") return { ...base, ssh_asset_id: layer.sshAssetId };
      if (layer.type === "socks5") {
        return {
          ...base,
          host: layer.host,
          port: layer.port,
          username: layer.username || undefined,
          password: secret.password || layer.encryptedPassword || undefined,
        };
      }
      return {
        ...base,
        url: layer.url,
        token: secret.token || layer.encryptedToken || undefined,
        timeout_seconds: layer.timeoutSeconds || 10,
      };
    }),
  };
}

export function layerTypeShortLabel(type: ProxyChainLayerType): string {
  if (type === "ssh") return "SSH";
  if (type === "http_tunnel") return "HTTP";
  return "SOCKS5";
}

export interface ProxyChainLayerError {
  id: string;
  field: "sshAssetId" | "host" | "port" | "url" | "token";
  messageKey: string;
}

/** 每个启用层的必填/取值校验;按层序返回,同一字段至多一条。 */
export function proxyChainLayerErrors(layers: ProxyChainLayerForm[]): ProxyChainLayerError[] {
  const errors: ProxyChainLayerError[] = [];
  for (const layer of layers) {
    if (!layer.enabled) continue;
    if (layer.type === "ssh" && layer.sshAssetId <= 0) {
      errors.push({ id: layer.id, field: "sshAssetId", messageKey: "asset.proxyChainSSHRequired" });
    }
    if (layer.type === "socks5" && !layer.host.trim()) {
      errors.push({ id: layer.id, field: "host", messageKey: "asset.proxyChainProxyHostRequired" });
    }
    if (layer.type === "socks5" && (layer.port <= 0 || layer.port > 65535)) {
      errors.push({ id: layer.id, field: "port", messageKey: "asset.proxyChainProxyPortInvalid" });
    }
    if (layer.type === "http_tunnel" && !layer.url.trim()) {
      errors.push({ id: layer.id, field: "url", messageKey: "asset.proxyChainHTTPURLRequired" });
    }
    if (layer.type === "http_tunnel" && !layer.token.trim() && !layer.encryptedToken) {
      errors.push({ id: layer.id, field: "token", messageKey: "asset.proxyChainHTTPTokenRequired" });
    }
  }
  return errors;
}

export function proxyChainValidationKey(layers: ProxyChainLayerForm[]): string {
  return proxyChainLayerErrors(layers)[0]?.messageKey ?? "";
}

/** 把 activeId 移动到 overId 的位置;无变化时返回原数组引用。 */
export function reorderLayers(
  layers: ProxyChainLayerForm[],
  activeId: string,
  overId: string
): ProxyChainLayerForm[] {
  const from = layers.findIndex((l) => l.id === activeId);
  const to = layers.findIndex((l) => l.id === overId);
  if (from < 0 || to < 0 || from === to) return layers;
  const next = [...layers];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}

export async function resolveSaveProxyChainSecrets(
  layers: ProxyChainLayerForm[],
  encrypt: (plaintext: string) => Promise<string>
): Promise<Record<string, { password?: string; token?: string }>> {
  const out: Record<string, { password?: string; token?: string }> = {};
  for (const layer of layers) {
    if (layer.type === "socks5") {
      out[layer.id] = {
        password: layer.password ? await encrypt(layer.password) : layer.encryptedPassword,
      };
    } else if (layer.type === "http_tunnel") {
      out[layer.id] = {
        token: layer.token ? await encrypt(layer.token) : layer.encryptedToken,
      };
    }
  }
  return out;
}

/** save 路径的 proxy 密码解析:明文优先加密,否则沿用既有密文。 */
export function resolveSaveProxyPassword(
  f: ConnectionFormFields,
  encrypt: (plaintext: string) => Promise<string>
): Promise<string> {
  return f.proxyPassword ? encrypt(f.proxyPassword) : Promise.resolve(f.encryptedProxyPassword);
}
