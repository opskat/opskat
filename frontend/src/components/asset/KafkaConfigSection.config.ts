import type { CredentialFragment } from "./credentialConfig";

/** 序列化后的 kafka config 形状(键序锁旧 save 分支)。 */
export interface KafkaConfig {
  brokers: string[];
  client_id?: string;
  sasl_mechanism?: string;
  username?: string;
  password?: string;
  credential_id?: number;
  tls?: boolean;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
  request_timeout_seconds?: number;
  message_preview_bytes?: number;
  message_fetch_limit?: number;
  ssh_asset_id?: number;
  schema_registry?: KafkaSchemaRegistryConfig;
  connect?: KafkaConnectConfig;
}

export interface KafkaSchemaRegistryConfig {
  enabled?: boolean;
  url?: string;
  auth_type?: string;
  username?: string;
  password?: string;
  credential_id?: number;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
}

export interface KafkaConnectConfig {
  enabled?: boolean;
  clusters?: KafkaConnectClusterConfig[];
}

export interface KafkaConnectClusterConfig {
  name?: string;
  url?: string;
  auth_type?: string;
  username?: string;
  password?: string;
  credential_id?: number;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
}

/** kafka 主连接 + 选项的非凭据/非伴随子状态(伴随由 section 单独持有)。 */
export interface KafkaFormState {
  brokersText: string;
  clientId: string;
  saslMechanism: string;
  /** SASL 用户名(仅 sasl_mechanism !== "none" 时写入)。 */
  username: string;
  tls: boolean;
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
  requestTimeoutSeconds: number;
  messagePreviewBytes: number;
  messageFetchLimit: number;
  sshTunnelId: number;
}

export const KAFKA_DEFAULTS: KafkaFormState = {
  brokersText: "",
  clientId: "opskat",
  saslMechanism: "none",
  username: "",
  tls: false,
  tlsInsecure: false,
  tlsServerName: "",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
  requestTimeoutSeconds: 30,
  messagePreviewBytes: 4096,
  messageFetchLimit: 50,
  sshTunnelId: 0,
};

/** 把 brokers 文本拆为非空 broker 列表(逗号/换行分隔,各自 trim)。 */
export function kafkaBrokers(brokersText: string): string[] {
  return brokersText
    .split(/[\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

/** 主连接 base config(无凭据、无伴随;键序锁旧 buildKafkaConfig)。section 据此追加主凭据/伴随后再 stringify。 */
export function buildKafkaBaseConfig(state: KafkaFormState): KafkaConfig {
  const cfg: KafkaConfig = {
    brokers: kafkaBrokers(state.brokersText),
  };
  if (state.clientId.trim()) cfg.client_id = state.clientId.trim();
  if (state.saslMechanism && state.saslMechanism !== "none") {
    cfg.sasl_mechanism = state.saslMechanism;
    if (state.username) cfg.username = state.username;
  } else {
    cfg.sasl_mechanism = "none";
  }
  if (state.tls) cfg.tls = true;
  if (state.tls && state.tlsInsecure) cfg.tls_insecure = true;
  if (state.tls && state.tlsServerName) cfg.tls_server_name = state.tlsServerName;
  if (state.tls && state.tlsCAFile) cfg.tls_ca_file = state.tlsCAFile;
  if (state.tls && state.tlsCertFile) cfg.tls_cert_file = state.tlsCertFile;
  if (state.tls && state.tlsKeyFile) cfg.tls_key_file = state.tlsKeyFile;
  if (state.requestTimeoutSeconds > 0) cfg.request_timeout_seconds = state.requestTimeoutSeconds;
  if (state.messagePreviewBytes > 0) cfg.message_preview_bytes = state.messagePreviewBytes;
  if (state.messageFetchLimit > 0) cfg.message_fetch_limit = state.messageFetchLimit;
  if (state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  return cfg;
}

/** 把主凭据片段并入 base(键序:base → credential_id|password)。镜像旧 save 凭据追加分支。 */
export function appendKafkaCredential(base: KafkaConfig, cred: CredentialFragment): KafkaConfig {
  if (cred.credential_id) base.credential_id = cred.credential_id;
  else if (cred.password) base.password = cred.password;
  return base;
}

/** bearer 伴随回填:回显空用户名(token 不显示为用户名)。其余 auth_type 用 config.username。 */
export function kafkaCompanionUsernameFromConfig(cfg?: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig): string {
  if (cfg?.auth_type === "bearer") return "";
  return cfg?.username || "";
}

/** bearer 旧迁移兜底:auth_type=bearer 且既无 password/credential_id 时,把旧存于 username 的 token 当明文回填。 */
export function kafkaCompanionPlainSecretFromConfig(
  cfg?: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig
): string {
  if (cfg?.auth_type !== "bearer" || cfg.password || cfg.credential_id) return "";
  return cfg.username || "";
}

/** 编辑态回填非凭据/非伴随字段(镜像旧 loadKafkaConfig;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseKafkaConfig(configJSON: string): KafkaFormState {
  try {
    const cfg: KafkaConfig = JSON.parse(configJSON || "{}");
    return {
      brokersText: (cfg.brokers || []).join("\n"),
      clientId: cfg.client_id || "opskat",
      saslMechanism: cfg.sasl_mechanism || "none",
      username: cfg.username || "",
      tls: cfg.tls || false,
      tlsInsecure: cfg.tls_insecure || false,
      tlsServerName: cfg.tls_server_name || "",
      tlsCAFile: cfg.tls_ca_file || "",
      tlsCertFile: cfg.tls_cert_file || "",
      tlsKeyFile: cfg.tls_key_file || "",
      requestTimeoutSeconds: cfg.request_timeout_seconds || 30,
      messagePreviewBytes: cfg.message_preview_bytes || 4096,
      messageFetchLimit: cfg.message_fetch_limit || 50,
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...KAFKA_DEFAULTS };
  }
}
