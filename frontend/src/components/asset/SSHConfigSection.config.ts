import type { CredentialFragment } from "./credentialConfig";

interface ProxyConfig {
  type: string;
  host: string;
  port: number;
  username?: string;
  password?: string;
}

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
  password?: string;
  credential_id?: number;
  private_keys?: string[];
  private_key_passphrase?: string;
  jump_host_id?: number;
  proxy?: ProxyConfig | null;
}

/** ssh 表单子状态(凭据中的 password 走 useAssetCredential,不入此 state)。 */
export interface SSHFormState {
  host: string;
  port: number;
  username: string;
  authType: string;
  connectionType: "direct" | "jumphost" | "proxy";
  keySource: "managed" | "file";
  /** key-auth managed ssh_key 凭据 id(非 password 凭据)。 */
  credentialId: number;
  selectedKeyPaths: string[];
  /** 用户新输入的明文 passphrase;空表示沿用既有密文。 */
  privateKeyPassphrase: string;
  /** 编辑态既有 passphrase 密文;passphrase 不回显。 */
  encryptedPrivateKeyPassphrase: string;
  proxyType: string;
  proxyHost: string;
  proxyPort: number;
  proxyUsername: string;
  /** 用户新输入的明文 proxy 密码;空表示沿用既有密文。 */
  proxyPassword: string;
  /** 编辑态既有 proxy 密码密文。 */
  encryptedProxyPassword: string;
  sshTunnelId: number;
}

export const SSH_DEFAULTS: SSHFormState = {
  host: "",
  port: 22,
  username: "root",
  authType: "password",
  connectionType: "direct",
  keySource: "managed",
  credentialId: 0,
  selectedKeyPaths: [],
  privateKeyPassphrase: "",
  encryptedPrivateKeyPassphrase: "",
  proxyType: "socks5",
  proxyHost: "",
  proxyPort: 1080,
  proxyUsername: "",
  proxyPassword: "",
  encryptedProxyPassword: "",
  sshTunnelId: 0,
};

/** buildSSHConfig 的已解析机密入参。save / test 在调用方分别预解析后传入。 */
export interface SSHBuildOptions {
  /** password-auth 凭据片段(credential_id 或 password,二选一或都无)。 */
  passwordCred: CredentialFragment;
  /** key-auth managed ssh_key 凭据 id(0 表示未选)。 */
  keyCredentialId: number;
  /** key-auth file 的 passphrase(save=密文 / test=明文优先,已由调用方解析)。 */
  passphrase: string;
  /** proxy 密码(save=密文 / test=明文,已由调用方解析)。 */
  proxyPassword: string;
  /** jumphost 隧道是否写入 config.jump_host_id:save 为 false(走 asset 顶层),test 为 true。 */
  includeJumpHost: boolean;
}

/** 保存/测试共用序列化(键序锁旧 save/test 分支)。机密由 opts 预解析。 */
export function buildSSHConfig(state: SSHFormState, opts: SSHBuildOptions): string {
  const cfg: SSHConfig = {
    host: state.host,
    port: state.port,
    username: state.username,
    auth_type: state.authType,
  };

  if (state.authType === "password") {
    if (opts.passwordCred.credential_id) cfg.credential_id = opts.passwordCred.credential_id;
    else if (opts.passwordCred.password) cfg.password = opts.passwordCred.password;
  }

  if (state.authType === "key") {
    if (state.keySource === "managed" && opts.keyCredentialId > 0) cfg.credential_id = opts.keyCredentialId;
    if (state.keySource === "file" && state.selectedKeyPaths.length > 0) {
      cfg.private_keys = state.selectedKeyPaths;
      if (opts.passphrase) cfg.private_key_passphrase = opts.passphrase;
    }
  }

  if (opts.includeJumpHost && state.connectionType === "jumphost" && state.sshTunnelId > 0) {
    cfg.jump_host_id = state.sshTunnelId;
  }

  if (state.connectionType === "proxy" && state.proxyHost) {
    cfg.proxy = {
      type: state.proxyType,
      host: state.proxyHost,
      port: state.proxyPort,
      username: state.proxyUsername || undefined,
      password: opts.proxyPassword || undefined,
    };
  }

  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadSSHConfig 非凭据/passphrase 明文字段)。
 *  password 凭据由 useAssetCredential 持有。sshTunnelId / connectionType 派生需要 asset 顶层字段优先
 *  (镜像旧 `asset.sshTunnelId || cfg.jump_host_id || 0`),故由 section 把 assetTunnelId 传入。 */
export function parseSSHConfig(configJSON: string, assetTunnelId = 0): SSHFormState {
  try {
    const cfg: SSHConfig = JSON.parse(configJSON || "{}");
    const tunnelId = assetTunnelId || cfg.jump_host_id || 0;
    return {
      host: cfg.host || "",
      port: cfg.port || 22,
      username: cfg.username || "root",
      authType: cfg.auth_type || "password",
      connectionType: tunnelId ? "jumphost" : cfg.proxy ? "proxy" : "direct",
      keySource: cfg.private_keys && cfg.private_keys.length > 0 ? "file" : "managed",
      credentialId: cfg.auth_type === "key" ? cfg.credential_id || 0 : 0,
      selectedKeyPaths: cfg.private_keys || [],
      privateKeyPassphrase: "", // passphrase 已加密,不回显
      encryptedPrivateKeyPassphrase: cfg.private_key_passphrase || "",
      proxyType: cfg.proxy?.type || "socks5",
      proxyHost: cfg.proxy?.host || "",
      proxyPort: cfg.proxy?.port || 1080,
      proxyUsername: cfg.proxy?.username || "",
      proxyPassword: "",
      encryptedProxyPassword: cfg.proxy?.password || "",
      sshTunnelId: tunnelId,
    };
  } catch {
    return { ...SSH_DEFAULTS };
  }
}

/** SSH 的 credential_id 语义随 auth_type 变化:password-auth 才能初始化 password 凭据子状态。 */
export function parseSSHPasswordCredentialConfig(configJSON: string): CredentialFragment {
  try {
    const cfg: SSHConfig = JSON.parse(configJSON || "{}");
    if ((cfg.auth_type || "password") !== "password") return {};
    return { credential_id: cfg.credential_id, password: cfg.password };
  } catch {
    return {};
  }
}
