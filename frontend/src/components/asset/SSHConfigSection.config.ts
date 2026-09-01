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

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
  agent_source_id?: number;
  agent_key_fingerprint?: string;
  password?: string;
  credential_id?: number;
  private_keys?: string[];
  private_key_passphrase?: string;
  jump_host_id?: number;
  proxy?: ProxyConfigJSON | null;
  proxy_chain?: ProxyChainJSON | null;
  keepalive_interval_seconds?: number;
  restore_cwd_on_reconnect?: boolean;
  startup_command?: string;
}

/** ssh 表单子状态(凭据中的 password 走 useAssetCredential,不入此 state)。 */
export interface SSHFormState extends ConnectionFormFields {
  host: string;
  port: number;
  username: string;
  authType: string;
  /** agent-auth 选中的来源 id(0 = 未选;新建不自动选)。 */
  agentSourceId: number;
  /** agent-auth 选中的精确指纹("" = 未选)。 */
  agentKeyFingerprint: string;
  /** 编辑态已存指纹在当前来源身份中缺失时的只读展示值;非空时禁止保存。 */
  agentMissingFingerprint: string;
  keySource: "managed" | "file";
  /** key-auth managed ssh_key 凭据 id(非 password 凭据)。 */
  credentialId: number;
  selectedKeyPaths: string[];
  /** 用户新输入的明文 passphrase;空表示沿用既有密文。 */
  privateKeyPassphrase: string;
  /** 编辑态既有 passphrase 密文;passphrase 不回显。 */
  encryptedPrivateKeyPassphrase: string;
  /** 覆盖该资产的 SSH 空闲保活间隔(秒)。0 = 跟随全局默认。 */
  keepAliveIntervalSeconds: number;
  /** 开启后断线手动重连时自动 cd 回上次目录(连接时自动启用目录同步追踪 cwd)。 */
  restoreCwdOnReconnect: boolean;
  /** SSH 交互式 shell 建立后自动执行的命令，支持换行分隔多条命令。 */
  startupCommand: string;
}

export const SSH_DEFAULTS: SSHFormState = {
  host: "",
  port: 22,
  username: "root",
  authType: "password",
  agentSourceId: 0,
  agentKeyFingerprint: "",
  agentMissingFingerprint: "",
  keySource: "managed",
  credentialId: 0,
  selectedKeyPaths: [],
  privateKeyPassphrase: "",
  encryptedPrivateKeyPassphrase: "",
  keepAliveIntervalSeconds: 0,
  restoreCwdOnReconnect: false,
  startupCommand: "",
  ...CONNECTION_DEFAULTS,
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
  proxyChainSecrets?: Record<string, { password?: string; token?: string }>;
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

  if (state.authType === "agent") {
    // Agent 模式:仅保存来源 + 规范指纹;与密码/密钥互斥,即便 state 残留 password/key 字段也不序列化。
    if (state.agentSourceId > 0 && state.agentKeyFingerprint) {
      cfg.agent_source_id = state.agentSourceId;
      cfg.agent_key_fingerprint = state.agentKeyFingerprint;
    }
  }

  if (opts.includeJumpHost && state.connectionType === "jumphost" && state.sshTunnelId > 0) {
    cfg.jump_host_id = state.sshTunnelId;
  }

  const proxy = buildProxyJSON(state, opts.proxyPassword);
  if (proxy) {
    cfg.proxy = proxy;
  }
  const proxyChain = buildProxyChainJSON(state.proxyChainLayers, opts.proxyChainSecrets);
  if (proxyChain) cfg.proxy_chain = proxyChain;

  // 0 = 跟随全局默认，不写入 config(omitempty 语义)。
  if (state.keepAliveIntervalSeconds > 0) {
    cfg.keepalive_interval_seconds = state.keepAliveIntervalSeconds;
  }

  // false = 关闭，不写入 config(omitempty 语义)。
  if (state.restoreCwdOnReconnect) {
    cfg.restore_cwd_on_reconnect = true;
  }

  if (state.startupCommand.trim()) {
    cfg.startup_command = state.startupCommand;
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
      agentSourceId: cfg.auth_type === "agent" ? cfg.agent_source_id || 0 : 0,
      agentKeyFingerprint: cfg.auth_type === "agent" ? cfg.agent_key_fingerprint || "" : "",
      agentMissingFingerprint: "", // 运行时检查缺失态,解析阶段未知
      keySource: cfg.private_keys && cfg.private_keys.length > 0 ? "file" : "managed",
      credentialId: cfg.auth_type === "key" ? cfg.credential_id || 0 : 0,
      selectedKeyPaths: cfg.private_keys || [],
      privateKeyPassphrase: "", // passphrase 已加密,不回显
      encryptedPrivateKeyPassphrase: cfg.private_key_passphrase || "",
      keepAliveIntervalSeconds: cfg.keepalive_interval_seconds || 0,
      restoreCwdOnReconnect: cfg.restore_cwd_on_reconnect || false,
      startupCommand: cfg.startup_command || "",
      ...parseConnectionFields(cfg.proxy, tunnelId, cfg.proxy_chain),
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
