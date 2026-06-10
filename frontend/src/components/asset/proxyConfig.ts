/** 连接方式(直连 / SSH 隧道(跳板机) / SOCKS5 代理)的共享表单状态与序列化。
 *  SSH 与数据库族 section 共用;UI 见 ConnectionMethodFields。 */

export type ConnectionType = "direct" | "jumphost" | "proxy";

export interface ProxyConfigJSON {
  type: string;
  host: string;
  port: number;
  username?: string;
  password?: string;
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
};

/** 编辑态回填:隧道 > 代理 > 直连(与后端拨号优先级一致)。 */
export function parseConnectionFields(
  proxy: ProxyConfigJSON | null | undefined,
  tunnelId: number
): ConnectionFormFields {
  return {
    connectionType: tunnelId ? "jumphost" : proxy ? "proxy" : "direct",
    sshTunnelId: tunnelId,
    proxyType: proxy?.type || "socks5",
    proxyHost: proxy?.host || "",
    proxyPort: proxy?.port || 1080,
    proxyUsername: proxy?.username || "",
    proxyPassword: "",
    encryptedProxyPassword: proxy?.password || "",
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

/** save 路径的 proxy 密码解析:明文优先加密,否则沿用既有密文。 */
export function resolveSaveProxyPassword(
  f: ConnectionFormFields,
  encrypt: (plaintext: string) => Promise<string>
): Promise<string> {
  return f.proxyPassword ? encrypt(f.proxyPassword) : Promise.resolve(f.encryptedProxyPassword);
}
