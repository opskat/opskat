import { useState, useEffect, useCallback, useRef, useMemo } from "react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, PlugZap, XCircle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Button,
  Input,
  Label,
  Textarea,
} from "@opskat/ui";
import { IconPicker } from "@/components/asset/IconPicker";
import { GroupSelect } from "@/components/asset/GroupSelect";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity, credential_entity } from "../../../wailsjs/go/models";
import { EncryptPassword } from "../../../wailsjs/go/system/System";
import { GetDecryptedExtensionConfig } from "../../../wailsjs/go/extension/Extension";
import { ListCredentialsByType, CancelTest, TestAssetConnection } from "../../../wailsjs/go/system/System";
import { ListLocalSSHKeys } from "../../../wailsjs/go/ssh/SSH";
import { ssh as ssh_models } from "../../../wailsjs/go/models";
import { SSHConfigSection } from "@/components/asset/SSHConfigSection";
import {
  KafkaConfigSection,
  type KafkaCompanionAuthForm,
  type KafkaConnectClusterForm,
  type KafkaSchemaRegistryForm,
} from "@/components/asset/KafkaConfigSection";
import { K8sConfigSection } from "@/components/asset/K8sConfigSection";
import { useExtensionStore } from "@/extension";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { getAssetTypeOptions, getAssetTypeLabel } from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";

interface AssetFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editAsset?: asset_entity.Asset | null;
  defaultGroupId?: number;
}

// 生成测试连接的唯一 ID；用于配合后端 CancelTest 中断本次测试。
function newTestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `test-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

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

interface KafkaConfig {
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

interface KafkaSchemaRegistryConfig {
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

interface KafkaConnectConfig {
  enabled?: boolean;
  clusters?: KafkaConnectClusterConfig[];
}

interface KafkaConnectClusterConfig {
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

type AssetType =
  | "ssh"
  | "database"
  | "redis"
  | "mongodb"
  | "kafka"
  | "k8s"
  | "serial"
  | "etcd"
  | "local"
  | (string & {});

const DEFAULT_PORTS: Record<string, number> = {
  ssh: 22,
  mysql: 3306,
  postgresql: 5432,
  mssql: 1433,
  redis: 6379,
  mongodb: 27017,
  kafka: 9092,
  k8s: 6443,
  etcd: 2379,
};

const DEFAULT_ICONS: Record<string, string> = {
  ssh: "server",
  mysql: "mysql",
  postgresql: "postgresql",
  mssql: "database",
  sqlite: "sqlite",
  redis: "redis",
  mongodb: "mongodb",
  kafka: "kafka",
  k8s: "kubernetes",
  serial: "usb",
  etcd: "etcd",
  local: "terminal",
};

function defaultKafkaCompanionAuth(): KafkaCompanionAuthForm {
  return {
    authType: "none",
    username: "",
    password: "",
    encryptedPassword: "",
    passwordSource: "inline",
    credentialId: 0,
    tlsInsecure: false,
    tlsServerName: "",
    tlsCAFile: "",
    tlsCertFile: "",
    tlsKeyFile: "",
  };
}

function defaultKafkaSchemaRegistry(): KafkaSchemaRegistryForm {
  return {
    enabled: false,
    url: "",
    ...defaultKafkaCompanionAuth(),
  };
}

function kafkaSchemaRegistryFromConfig(cfg?: KafkaSchemaRegistryConfig): KafkaSchemaRegistryForm {
  return {
    enabled: !!cfg?.enabled,
    url: cfg?.url || "",
    authType: cfg?.auth_type || "none",
    username: kafkaCompanionUsernameFromConfig(cfg),
    password: kafkaCompanionPlainSecretFromConfig(cfg),
    encryptedPassword: cfg?.password || "",
    passwordSource: cfg?.credential_id ? "managed" : "inline",
    credentialId: cfg?.credential_id || 0,
    tlsInsecure: !!cfg?.tls_insecure,
    tlsServerName: cfg?.tls_server_name || "",
    tlsCAFile: cfg?.tls_ca_file || "",
    tlsCertFile: cfg?.tls_cert_file || "",
    tlsKeyFile: cfg?.tls_key_file || "",
  };
}

function newKafkaConnectCluster(cfg?: KafkaConnectClusterConfig, index = 0): KafkaConnectClusterForm {
  return {
    id: `connect-${Date.now().toString(36)}-${index}-${Math.random().toString(36).slice(2)}`,
    name: cfg?.name || "",
    url: cfg?.url || "",
    authType: cfg?.auth_type || "none",
    username: kafkaCompanionUsernameFromConfig(cfg),
    password: kafkaCompanionPlainSecretFromConfig(cfg),
    encryptedPassword: cfg?.password || "",
    passwordSource: cfg?.credential_id ? "managed" : "inline",
    credentialId: cfg?.credential_id || 0,
    tlsInsecure: !!cfg?.tls_insecure,
    tlsServerName: cfg?.tls_server_name || "",
    tlsCAFile: cfg?.tls_ca_file || "",
    tlsCertFile: cfg?.tls_cert_file || "",
    tlsKeyFile: cfg?.tls_key_file || "",
  };
}

function kafkaCompanionUsernameFromConfig(cfg?: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig): string {
  if (cfg?.auth_type === "bearer") return "";
  return cfg?.username || "";
}

function kafkaCompanionPlainSecretFromConfig(cfg?: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig): string {
  if (cfg?.auth_type !== "bearer" || cfg.password || cfg.credential_id) return "";
  return cfg.username || "";
}

export function AssetForm({ open, onOpenChange, editAsset, defaultGroupId = 0 }: AssetFormProps) {
  const { t } = useTranslation();
  const { createAsset, updateAsset } = useAssetStore();

  const extensions = useExtensionStore((s) => s.extensions);
  const assetTypeOptions = useMemo(() => getAssetTypeOptions(extensions), [extensions]);

  // Asset type
  const [assetType, setAssetType] = useState<AssetType>("ssh");

  // Basic fields
  const [name, setName] = useState("");
  const [groupId, setGroupId] = useState(0);
  const [description, setDescription] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState(22);
  const [username, setUsername] = useState("root");
  const [authType, setAuthType] = useState("password");
  const [icon, setIcon] = useState("server");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  // 当前 in-flight 测试的 ID；切换/取消时用来 race-discard 晚到的结果。
  const activeTestIdRef = useRef<string | null>(null);

  // 注册化类型走通用 ConfigSection 路径:section 自持 state,经 ref 暴露 build*。
  const sectionRef = useRef<AssetFormHandle>(null);
  const [validity, setValidity] = useState<SectionValidity>({ canTest: false, canSave: false });
  const ctx: AssetFormContext = useMemo(() => ({ isEdit: !!editAsset, encryptPassword: EncryptPassword }), [editAsset]);

  // Connection type (SSH only)
  const [connectionType, setConnectionType] = useState<"direct" | "jumphost" | "proxy">("direct");

  // Auth fields
  const [password, setPassword] = useState("");
  const [encryptedPassword, setEncryptedPassword] = useState("");
  const [passwordSource, setPasswordSource] = useState<"inline" | "managed">("inline");
  const [passwordCredentialId, setPasswordCredentialId] = useState(0);
  const [managedPasswords, setManagedPasswords] = useState<credential_entity.Credential[]>([]);
  const [keySource, setKeySource] = useState<"managed" | "file">("managed");
  const [credentialId, setCredentialId] = useState(0);
  const [managedKeys, setManagedKeys] = useState<credential_entity.Credential[]>([]);

  // SSH fields - local key
  const [localKeys, setLocalKeys] = useState<ssh_models.LocalSSHKeyInfo[]>([]);
  const [selectedKeyPaths, setSelectedKeyPaths] = useState<string[]>([]);
  const [privateKeyPassphrase, setPrivateKeyPassphrase] = useState("");
  const [encryptedPrivateKeyPassphrase, setEncryptedPrivateKeyPassphrase] = useState("");
  const [scanningKeys, setScanningKeys] = useState(false);
  const [sshTunnelId, setSshTunnelId] = useState(0);
  const [proxyType, setProxyType] = useState("socks5");
  const [proxyHost, setProxyHost] = useState("");
  const [proxyPort, setProxyPort] = useState(1080);
  const [proxyUsername, setProxyUsername] = useState("");
  const [proxyPassword, setProxyPassword] = useState("");
  const [encryptedProxyPassword, setEncryptedProxyPassword] = useState("");

  // TLS toggle shared by kafka
  const [tls, setTls] = useState(false);

  // Kafka fields
  const [kafkaBrokersText, setKafkaBrokersText] = useState("");
  const [kafkaClientId, setKafkaClientId] = useState("opskat");
  const [kafkaSaslMechanism, setKafkaSaslMechanism] = useState("none");
  const [kafkaTlsInsecure, setKafkaTlsInsecure] = useState(false);
  const [kafkaTlsServerName, setKafkaTlsServerName] = useState("");
  const [kafkaTlsCAFile, setKafkaTlsCAFile] = useState("");
  const [kafkaTlsCertFile, setKafkaTlsCertFile] = useState("");
  const [kafkaTlsKeyFile, setKafkaTlsKeyFile] = useState("");
  const [kafkaRequestTimeoutSeconds, setKafkaRequestTimeoutSeconds] = useState(30);
  const [kafkaMessagePreviewBytes, setKafkaMessagePreviewBytes] = useState(4096);
  const [kafkaMessageFetchLimit, setKafkaMessageFetchLimit] = useState(50);
  const [kafkaSchemaRegistry, setKafkaSchemaRegistryState] =
    useState<KafkaSchemaRegistryForm>(defaultKafkaSchemaRegistry());
  const [kafkaConnectEnabled, setKafkaConnectEnabled] = useState(false);
  const [kafkaConnectClusters, setKafkaConnectClusters] = useState<KafkaConnectClusterForm[]>([]);

  const setKafkaSchemaRegistry = useCallback((patch: Partial<KafkaSchemaRegistryForm>) => {
    setKafkaSchemaRegistryState((current) => ({ ...current, ...patch }));
  }, []);

  // K8S fields
  const [kubeconfig, setKubeconfig] = useState("");
  const [k8sNamespace, setK8sNamespace] = useState("");
  const [k8sContext, setK8sContext] = useState("");
  const [showKubeconfig, setShowKubeconfig] = useState(false);

  // Extension config
  const [extConfig, setExtConfig] = useState<Record<string, unknown>>({});

  // Exclude self from jump host / SSH tunnel selection
  const jumpHostExcludeIds = editAsset?.ID ? [editAsset.ID] : undefined;

  // 复位测试状态：open 切换时一律清掉上一次表单的 testing/testID 残留，
  // 并取消任何还在后台跑的测试（关闭对话框时直接放弃结果）。
  useEffect(() => {
    const lastId = activeTestIdRef.current;
    if (lastId) {
      void CancelTest(lastId);
    }
    activeTestIdRef.current = null;
    setTesting(false);
  }, [open]);

  // Load managed keys/passwords and scan local keys when dialog opens
  useEffect(() => {
    if (open) {
      ListCredentialsByType("ssh_key")
        .then((keys) => setManagedKeys(keys || []))
        .catch(() => setManagedKeys([]));
      ListCredentialsByType("password")
        .then((passwords) => setManagedPasswords(passwords || []))
        .catch(() => setManagedPasswords([]));
      setScanningKeys(true);
      ListLocalSSHKeys()
        .then((keys) => setLocalKeys(keys || []))
        .catch(() => setLocalKeys([]))
        .finally(() => setScanningKeys(false));
    }
  }, [open]);

  useEffect(() => {
    if (open) {
      if (editAsset) {
        const editType = (editAsset.Type || "ssh") as AssetType;
        setAssetType(editType);
        setName(editAsset.Name);
        setGroupId(editAsset.GroupID);
        setIcon(editAsset.Icon || DEFAULT_ICONS[editType] || "server");
        setDescription(editAsset.Description);

        if (getAssetType(editType)?.ConfigSection) {
          // 已注册化类型:config 回填由 section 经 editAsset prop 完成,壳跳过
        } else if (editType === "ssh") {
          loadSSHConfig(editAsset);
        } else if (editType === "kafka") {
          loadKafkaConfig(editAsset);
        } else if (editType === "k8s") {
          loadK8sConfig(editAsset);
        } else {
          // Extension type: load decrypted config
          const extInfo = useExtensionStore.getState().getExtensionForAssetType(editType);
          if (extInfo && editAsset.ID) {
            GetDecryptedExtensionConfig(editAsset.ID, extInfo.name)
              .then((cfg) => setExtConfig(JSON.parse(cfg || "{}")))
              .catch(() => setExtConfig(JSON.parse(editAsset.Config || "{}")));
          } else {
            setExtConfig(JSON.parse(editAsset.Config || "{}"));
          }
        }
      } else {
        setAssetType("ssh");
        setName("");
        setGroupId(defaultGroupId);
        setIcon("server");
        setDescription("");
        resetSharedFields("ssh");
        resetSSHFields();
        resetKafkaFields();
        resetK8sFields();
        setExtConfig({});
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editAsset, defaultGroupId]);

  const loadSSHConfig = (asset: asset_entity.Asset) => {
    try {
      const cfg: SSHConfig = JSON.parse(asset.Config || "{}");
      setHost(cfg.host || "");
      setPort(cfg.port || 22);
      setUsername(cfg.username || "root");
      setAuthType(cfg.auth_type || "password");

      setEncryptedPassword(cfg.password || "");
      setPassword("");
      if (cfg.auth_type === "password" && cfg.credential_id) {
        setPasswordSource("managed");
        setPasswordCredentialId(cfg.credential_id);
      } else {
        setPasswordSource("inline");
        setPasswordCredentialId(0);
      }
      setKeySource(cfg.private_keys && cfg.private_keys.length > 0 ? "file" : "managed");
      setCredentialId(cfg.auth_type === "key" ? cfg.credential_id || 0 : 0);
      setSelectedKeyPaths(cfg.private_keys || []);
      setPrivateKeyPassphrase(""); // passphrase 已加密，不回显
      setEncryptedPrivateKeyPassphrase(cfg.private_key_passphrase || "");

      // Unified SSH tunnel: prefer asset-level field, fall back to config
      const tunnelId = asset.sshTunnelId || cfg.jump_host_id || 0;
      setSshTunnelId(tunnelId);

      if (tunnelId) {
        setConnectionType("jumphost");
      } else if (cfg.proxy) {
        setConnectionType("proxy");
      } else {
        setConnectionType("direct");
      }

      if (cfg.proxy) {
        setProxyType(cfg.proxy.type || "socks5");
        setProxyHost(cfg.proxy.host || "");
        setProxyPort(cfg.proxy.port || 1080);
        setProxyUsername(cfg.proxy.username || "");
        setEncryptedProxyPassword(cfg.proxy.password || "");
        setProxyPassword("");
      } else {
        resetProxyFields();
      }
    } catch {
      resetSharedFields("ssh");
      resetSSHFields();
    }
  };

  const loadKafkaConfig = (asset: asset_entity.Asset) => {
    try {
      const cfg: KafkaConfig = JSON.parse(asset.Config || "{}");
      setKafkaBrokersText((cfg.brokers || []).join("\n"));
      setKafkaClientId(cfg.client_id || "opskat");
      setKafkaSaslMechanism(cfg.sasl_mechanism || "none");
      setUsername(cfg.username || "");
      setTls(cfg.tls || false);
      setKafkaTlsInsecure(cfg.tls_insecure || false);
      setKafkaTlsServerName(cfg.tls_server_name || "");
      setKafkaTlsCAFile(cfg.tls_ca_file || "");
      setKafkaTlsCertFile(cfg.tls_cert_file || "");
      setKafkaTlsKeyFile(cfg.tls_key_file || "");
      setKafkaRequestTimeoutSeconds(cfg.request_timeout_seconds || 30);
      setKafkaMessagePreviewBytes(cfg.message_preview_bytes || 4096);
      setKafkaMessageFetchLimit(cfg.message_fetch_limit || 50);
      setSshTunnelId(asset.sshTunnelId || cfg.ssh_asset_id || 0);
      setKafkaSchemaRegistryState(kafkaSchemaRegistryFromConfig(cfg.schema_registry));
      setKafkaConnectEnabled(!!cfg.connect?.enabled);
      setKafkaConnectClusters(
        (cfg.connect?.clusters || []).map((cluster, index) => newKafkaConnectCluster(cluster, index))
      );

      if (cfg.credential_id) {
        setPasswordSource("managed");
        setPasswordCredentialId(cfg.credential_id);
        setEncryptedPassword("");
        setPassword("");
      } else {
        setPasswordSource("inline");
        setPasswordCredentialId(0);
        setEncryptedPassword(cfg.password || "");
        setPassword("");
      }
    } catch {
      resetSharedFields("kafka");
      resetKafkaFields();
    }
  };

  const loadK8sConfig = (asset: asset_entity.Asset) => {
    try {
      const cfg = JSON.parse(asset.Config || "{}");
      // kubeconfig 已加密落库，编辑时不预填密文；用户重新输入即视为替换。
      setKubeconfig("");
      setK8sNamespace(cfg.namespace || "");
      setK8sContext(cfg.context || "");
      setShowKubeconfig(false);
      setSshTunnelId(asset.sshTunnelId || 0);
      setHost(""); // K8S uses kubeconfig, not host
      setPort(6443);
      setUsername("");
      setPassword("");
      setEncryptedPassword("");
    } catch {
      resetSharedFields("k8s");
      resetK8sFields();
    }
  };

  // Reset shared connection fields with type-appropriate defaults
  const resetSharedFields = (type: AssetType) => {
    setHost("");
    setPort(DEFAULT_PORTS[type] || 22);
    setUsername(type === "ssh" ? "root" : "");
    setPassword("");
    setEncryptedPassword("");
    setPasswordSource("inline");
    setPasswordCredentialId(0);
  };

  const resetProxyFields = () => {
    setProxyType("socks5");
    setProxyHost("");
    setProxyPort(1080);
    setProxyUsername("");
    setProxyPassword("");
    setEncryptedProxyPassword("");
  };

  // SSH-exclusive fields only
  const resetSSHFields = () => {
    setAuthType("password");
    setKeySource("managed");
    setCredentialId(0);
    setSelectedKeyPaths([]);
    setPrivateKeyPassphrase("");
    setEncryptedPrivateKeyPassphrase("");
    setConnectionType("direct");
    setSshTunnelId(0);
    resetProxyFields();
  };

  const resetKafkaFields = () => {
    setKafkaBrokersText("");
    setKafkaClientId("opskat");
    setKafkaSaslMechanism("none");
    setTls(false);
    setKafkaTlsInsecure(false);
    setKafkaTlsServerName("");
    setKafkaTlsCAFile("");
    setKafkaTlsCertFile("");
    setKafkaTlsKeyFile("");
    setKafkaRequestTimeoutSeconds(30);
    setKafkaMessagePreviewBytes(4096);
    setKafkaMessageFetchLimit(50);
    setSshTunnelId(0);
    setKafkaSchemaRegistryState(defaultKafkaSchemaRegistry());
    setKafkaConnectEnabled(false);
    setKafkaConnectClusters([]);
  };

  // K8S-exclusive fields only
  const resetK8sFields = () => {
    setKubeconfig("");
    setK8sNamespace("");
    setK8sContext("");
    setShowKubeconfig(false);
  };

  const handleTypeChange = (newType: AssetType) => {
    if (newType === assetType) return;
    setAssetType(newType);

    // Reset port/username/password to type-appropriate defaults (keep host)
    setPort(newType === "database" ? 3306 : DEFAULT_PORTS[newType] || 22);
    setUsername(newType === "ssh" ? "root" : "");
    setPassword("");
    setEncryptedPassword("");
    setPasswordSource("inline");
    setPasswordCredentialId(0);
    setIcon(newType === "database" ? "mysql" : DEFAULT_ICONS[newType] || "server");
    if (newType === "k8s") setHost("");
  };

  // 测试连接时把当前表单选中的密码来源（托管 / 内联加密缓存）写入 cfg。
  // 明文 password 仍由调用方作为 TestXxxConnection 的第二参数传入；
  // 这里只处理"无明文输入"时需要从托管凭据 ID 或已存加密值兜底的字段。
  const applyTestPasswordSource = <T extends { credential_id?: number; password?: string }>(cfg: T): T => {
    if (passwordSource === "managed" && passwordCredentialId > 0) {
      cfg.credential_id = passwordCredentialId;
    } else if (!password && encryptedPassword) {
      cfg.password = encryptedPassword;
    }
    return cfg;
  };

  const kafkaBrokers = () =>
    kafkaBrokersText
      .split(/[\n,]+/)
      .map((item) => item.trim())
      .filter(Boolean);

  const buildKafkaConfig = (): KafkaConfig => {
    const cfg: KafkaConfig = {
      brokers: kafkaBrokers(),
    };
    if (kafkaClientId.trim()) cfg.client_id = kafkaClientId.trim();
    if (kafkaSaslMechanism && kafkaSaslMechanism !== "none") {
      cfg.sasl_mechanism = kafkaSaslMechanism;
      if (username) cfg.username = username;
    } else {
      cfg.sasl_mechanism = "none";
    }
    if (tls) cfg.tls = true;
    if (tls && kafkaTlsInsecure) cfg.tls_insecure = true;
    if (tls && kafkaTlsServerName) cfg.tls_server_name = kafkaTlsServerName;
    if (tls && kafkaTlsCAFile) cfg.tls_ca_file = kafkaTlsCAFile;
    if (tls && kafkaTlsCertFile) cfg.tls_cert_file = kafkaTlsCertFile;
    if (tls && kafkaTlsKeyFile) cfg.tls_key_file = kafkaTlsKeyFile;
    if (kafkaRequestTimeoutSeconds > 0) cfg.request_timeout_seconds = kafkaRequestTimeoutSeconds;
    if (kafkaMessagePreviewBytes > 0) cfg.message_preview_bytes = kafkaMessagePreviewBytes;
    if (kafkaMessageFetchLimit > 0) cfg.message_fetch_limit = kafkaMessageFetchLimit;
    if (sshTunnelId > 0) cfg.ssh_asset_id = sshTunnelId;
    return cfg;
  };

  const handleTestConnection = async () => {
    const sshConfig: SSHConfig = {
      host,
      port,
      username,
      auth_type: authType,
    };
    if (authType === "password") {
      applyTestPasswordSource(sshConfig);
    }
    if (authType === "key") {
      if (keySource === "managed" && credentialId > 0) sshConfig.credential_id = credentialId;
      if (keySource === "file" && selectedKeyPaths.length > 0) {
        sshConfig.private_keys = selectedKeyPaths;
        // 测试连接时：优先使用用户输入的明文 passphrase，否则使用存储的加密值
        if (privateKeyPassphrase) {
          sshConfig.private_key_passphrase = privateKeyPassphrase;
        } else if (encryptedPrivateKeyPassphrase) {
          sshConfig.private_key_passphrase = encryptedPrivateKeyPassphrase;
        }
      }
    }
    if (connectionType === "jumphost" && sshTunnelId > 0) sshConfig.jump_host_id = sshTunnelId;
    if (connectionType === "proxy" && proxyHost) {
      sshConfig.proxy = {
        type: proxyType,
        host: proxyHost,
        port: proxyPort,
        username: proxyUsername || undefined,
        password: proxyPassword || undefined,
      };
    }
    const testId = newTestId();
    activeTestIdRef.current = testId;
    setTesting(true);
    try {
      await TestAssetConnection(testId, "ssh", JSON.stringify(sshConfig), password);
      if (activeTestIdRef.current === testId) notifySuccess(t("asset.testConnectionSuccess"));
    } catch (e) {
      if (activeTestIdRef.current === testId) toast.error(`${t("asset.testConnectionFailed")}: ${String(e)}`);
    } finally {
      if (activeTestIdRef.current === testId) {
        activeTestIdRef.current = null;
        setTesting(false);
      }
    }
  };

  const handleTestKafkaConnection = async () => {
    const cfg = buildKafkaConfig();
    if (kafkaSaslMechanism !== "none") {
      applyTestPasswordSource(cfg);
    }
    const testId = newTestId();
    activeTestIdRef.current = testId;
    setTesting(true);
    try {
      await TestAssetConnection(testId, "kafka", JSON.stringify(cfg), password);
      if (activeTestIdRef.current === testId) notifySuccess(t("asset.testConnectionSuccess"));
    } catch (e) {
      if (activeTestIdRef.current === testId) toast.error(`${t("asset.testConnectionFailed")}: ${String(e)}`);
    } finally {
      if (activeTestIdRef.current === testId) {
        activeTestIdRef.current = null;
        setTesting(false);
      }
    }
  };

  // 静默取消正在进行的测试（用于保存/关闭对话框等退出动作）。无 in-flight 测试时是 no-op。
  const cancelActiveTest = () => {
    const id = activeTestIdRef.current;
    if (!id) return;
    activeTestIdRef.current = null;
    void CancelTest(id);
    setTesting(false);
  };

  const handleCancelTest = () => {
    if (!activeTestIdRef.current) return;
    cancelActiveTest();
    toast.info(t("asset.testCancelled"));
  };

  const handleGenericTestConnection = async () => {
    const build = sectionRef.current?.buildTestConfig;
    if (!build) return;
    let tc;
    try {
      tc = await build(ctx);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return;
    }
    const testId = newTestId();
    activeTestIdRef.current = testId;
    setTesting(true);
    try {
      await TestAssetConnection(testId, tc.assetType, tc.configJSON, tc.password);
      if (activeTestIdRef.current === testId) notifySuccess(t("asset.testConnectionSuccess"));
    } catch (e) {
      if (activeTestIdRef.current === testId) toast.error(`${t("asset.testConnectionFailed")}: ${String(e)}`);
    } finally {
      if (activeTestIdRef.current === testId) {
        activeTestIdRef.current = null;
        setTesting(false);
      }
    }
  };

  const encryptPasswordValue = async (): Promise<string | undefined> => {
    if (password) {
      try {
        return await EncryptPassword(password);
      } catch {
        toast.error("Failed to encrypt password");
        return undefined;
      }
    }
    if (encryptedPassword) return encryptedPassword;
    return "";
  };

  const encryptKafkaCompanionPassword = async (
    plainPassword: string,
    existingEncryptedPassword: string
  ): Promise<string | undefined> => {
    if (plainPassword) {
      try {
        return await EncryptPassword(plainPassword);
      } catch {
        toast.error("Failed to encrypt password");
        return undefined;
      }
    }
    if (existingEncryptedPassword) return existingEncryptedPassword;
    return "";
  };

  const applyKafkaCompanionAuth = async (
    cfg: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig,
    form: KafkaCompanionAuthForm
  ): Promise<boolean> => {
    const authType = form.authType || "none";
    if (authType === "none") return true;
    cfg.auth_type = authType;
    if (authType !== "bearer" && form.username.trim()) cfg.username = form.username.trim();
    if (form.passwordSource === "managed" && form.credentialId > 0) {
      cfg.credential_id = form.credentialId;
      return true;
    }
    const encrypted = await encryptKafkaCompanionPassword(form.password, form.encryptedPassword);
    if (encrypted === undefined) return false;
    if (encrypted) cfg.password = encrypted;
    return true;
  };

  const applyKafkaCompanionTLS = (
    cfg: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig,
    form: KafkaCompanionAuthForm
  ) => {
    if (form.tlsInsecure) cfg.tls_insecure = true;
    if (form.tlsServerName.trim()) cfg.tls_server_name = form.tlsServerName.trim();
    if (form.tlsCAFile.trim()) cfg.tls_ca_file = form.tlsCAFile.trim();
    if (form.tlsCertFile.trim()) cfg.tls_cert_file = form.tlsCertFile.trim();
    if (form.tlsKeyFile.trim()) cfg.tls_key_file = form.tlsKeyFile.trim();
  };

  const validateKafkaCompanions = (): boolean => {
    if (kafkaSchemaRegistry.enabled && !kafkaSchemaRegistry.url.trim()) {
      toast.error(t("asset.kafkaSchemaRegistryURLRequired"));
      return false;
    }
    if (kafkaSchemaRegistry.enabled && !validateKafkaCompanionAuth(kafkaSchemaRegistry)) return false;
    if (kafkaConnectEnabled) {
      const clusters = kafkaConnectClusters.filter((cluster) => cluster.name.trim() || cluster.url.trim());
      if (clusters.length === 0) {
        toast.error(t("asset.kafkaConnectClusterRequired"));
        return false;
      }
      if (clusters.some((cluster) => !cluster.name.trim() || !cluster.url.trim())) {
        toast.error(t("asset.kafkaConnectClusterInvalid"));
        return false;
      }
      if (clusters.some((cluster) => !validateKafkaCompanionAuth(cluster))) return false;
    }
    return true;
  };

  const validateKafkaCompanionAuth = (form: KafkaCompanionAuthForm): boolean => {
    if (form.authType !== "bearer") return true;
    const hasToken =
      form.passwordSource === "managed" ? form.credentialId > 0 : !!form.password.trim() || !!form.encryptedPassword;
    if (!hasToken) {
      toast.error(t("asset.kafkaBearerTokenRequired"));
      return false;
    }
    return true;
  };

  const buildKafkaSchemaRegistryConfig = async (): Promise<KafkaSchemaRegistryConfig | undefined> => {
    if (!kafkaSchemaRegistry.enabled) return undefined;
    const cfg: KafkaSchemaRegistryConfig = {
      enabled: true,
      url: kafkaSchemaRegistry.url.trim(),
    };
    if (!(await applyKafkaCompanionAuth(cfg, kafkaSchemaRegistry))) return undefined;
    applyKafkaCompanionTLS(cfg, kafkaSchemaRegistry);
    return cfg;
  };

  const buildKafkaConnectConfig = async (): Promise<KafkaConnectConfig | undefined> => {
    if (!kafkaConnectEnabled) return undefined;
    const cfg: KafkaConnectConfig = { enabled: true, clusters: [] };
    const clusters = kafkaConnectClusters.filter((cluster) => cluster.name.trim() || cluster.url.trim());
    for (const cluster of clusters) {
      const next: KafkaConnectClusterConfig = {
        name: cluster.name.trim(),
        url: cluster.url.trim(),
      };
      if (!(await applyKafkaCompanionAuth(next, cluster))) return undefined;
      applyKafkaCompanionTLS(next, cluster);
      cfg.clusters?.push(next);
    }
    return cfg;
  };

  const encryptProxyPassword = async (): Promise<string | undefined> => {
    if (proxyPassword) {
      try {
        return await EncryptPassword(proxyPassword);
      } catch {
        toast.error("Failed to encrypt proxy password");
        return undefined;
      }
    }
    if (encryptedProxyPassword) return encryptedProxyPassword;
    return undefined;
  };

  const persistAsset = async (asset: asset_entity.Asset) => {
    setSaving(true);
    try {
      if (editAsset?.ID) {
        asset.ID = editAsset.ID;
        await updateAsset(asset);
      } else {
        await createAsset(asset);
      }
      onOpenChange(false);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleSubmit = async () => {
    // 用户决定保存：放弃任何正在进行的测试，避免和保存竞争或弹出过期的 toast。
    cancelActiveTest();

    const def = getAssetType(assetType);
    if (def?.ConfigSection) {
      if (!sectionRef.current) return;
      let built;
      try {
        built = await sectionRef.current.buildConfig(ctx);
      } catch (e) {
        toast.error(e instanceof Error ? e.message : String(e));
        return;
      }
      const asset = new asset_entity.Asset({
        ...(editAsset || {}),
        Name: name,
        Type: assetType,
        GroupID: groupId,
        Icon: icon,
        Description: description,
        Config: built.configJSON,
        sshTunnelId: built.sshTunnelId,
      });
      await persistAsset(asset);
      return;
    }

    let config: string;

    if (assetType === "ssh") {
      const sshConfig: SSHConfig = {
        host,
        port,
        username,
        auth_type: authType,
      };

      if (authType === "password") {
        if (passwordSource === "managed" && passwordCredentialId > 0) {
          sshConfig.credential_id = passwordCredentialId;
        } else {
          const encrypted = await encryptPasswordValue();
          if (encrypted === undefined) return;
          if (encrypted) sshConfig.password = encrypted;
        }
      }

      if (authType === "key") {
        if (keySource === "managed" && credentialId > 0) sshConfig.credential_id = credentialId;
        if (keySource === "file" && selectedKeyPaths.length > 0) {
          sshConfig.private_keys = selectedKeyPaths;
          if (privateKeyPassphrase) {
            // 用户输入了新的 passphrase，加密存储
            const encrypted = await EncryptPassword(privateKeyPassphrase);
            if (encrypted === undefined) return;
            sshConfig.private_key_passphrase = encrypted;
          } else if (encryptedPrivateKeyPassphrase) {
            // 用户没有输入新的 passphrase，保留原有的加密值
            sshConfig.private_key_passphrase = encryptedPrivateKeyPassphrase;
          }
        }
      }

      if (connectionType === "proxy" && proxyHost) {
        const encProxy = await encryptProxyPassword();
        sshConfig.proxy = {
          type: proxyType,
          host: proxyHost,
          port: proxyPort,
          username: proxyUsername || undefined,
          password: encProxy || undefined,
        };
      }
      config = JSON.stringify(sshConfig);
    } else if (assetType === "kafka") {
      if (!validateKafkaCompanions()) return;
      const kafkaConfig = buildKafkaConfig();
      if (kafkaSaslMechanism !== "none") {
        if (passwordSource === "managed" && passwordCredentialId > 0) {
          kafkaConfig.credential_id = passwordCredentialId;
        } else {
          const encrypted = await encryptPasswordValue();
          if (encrypted === undefined) return;
          if (encrypted) kafkaConfig.password = encrypted;
        }
      }
      const schemaRegistryConfig = await buildKafkaSchemaRegistryConfig();
      if (kafkaSchemaRegistry.enabled) {
        if (!schemaRegistryConfig) return;
        kafkaConfig.schema_registry = schemaRegistryConfig;
      }
      const connectConfig = await buildKafkaConnectConfig();
      if (kafkaConnectEnabled) {
        if (!connectConfig) return;
        kafkaConfig.connect = connectConfig;
      }
      config = JSON.stringify(kafkaConfig);
    } else if (assetType === "k8s") {
      const k8sConfig: Record<string, unknown> = {};
      if (kubeconfig) {
        // 用户输入了新的 kubeconfig（明文 YAML），加密后落库。
        try {
          k8sConfig.kubeconfig = await EncryptPassword(kubeconfig);
        } catch {
          toast.error("Failed to encrypt kubeconfig");
          return;
        }
      } else if (editAsset) {
        // 编辑模式且未输入新值：保留原 ciphertext。
        try {
          const oldCfg = JSON.parse(editAsset.Config || "{}") as { kubeconfig?: string };
          if (oldCfg.kubeconfig) k8sConfig.kubeconfig = oldCfg.kubeconfig;
        } catch {
          // 旧 config 解析失败：让 ciphertext 缺失冒到后端校验
        }
      }
      if (k8sNamespace) k8sConfig.namespace = k8sNamespace;
      if (k8sContext) k8sConfig.context = k8sContext;
      config = JSON.stringify(k8sConfig);
    } else {
      // Extension type: encrypt password fields from configSchema before saving
      const extInfo = useExtensionStore.getState().getExtensionForAssetType(assetType);
      const schema = extInfo?.manifest.assetTypes?.find((at) => at.type === assetType)?.configSchema as
        | { properties?: Record<string, { format?: string }> }
        | undefined;
      const configCopy = { ...extConfig };
      if (schema?.properties) {
        for (const [key, prop] of Object.entries(schema.properties)) {
          if (prop.format === "password" && configCopy[key]) {
            const encrypted = await EncryptPassword(String(configCopy[key]));
            if (encrypted === undefined) return;
            configCopy[key] = encrypted;
          }
        }
      }
      config = JSON.stringify(configCopy);
    }

    const asset = new asset_entity.Asset({
      ...(editAsset || {}),
      Name: name,
      Type: assetType,
      GroupID: groupId,
      Icon: icon,
      Description: description,
      Config: config,
      sshTunnelId:
        assetType === "ssh"
          ? connectionType === "jumphost" && sshTunnelId > 0
            ? sshTunnelId
            : 0
          : assetType === "k8s"
            ? sshTunnelId > 0
              ? sshTunnelId
              : 0
            : sshTunnelId > 0
              ? sshTunnelId
              : 0,
    });

    await persistAsset(asset);
  };

  const typeLabel = getAssetTypeLabel(assetType, t, assetTypeOptions);
  const sectionDef = getAssetType(assetType);

  const isTestableAssetType = sectionDef?.ConfigSection
    ? !!sectionDef.testable
    : assetType === "ssh" || assetType === "database" || assetType === "kafka";

  const isTestConnectionDisabled =
    testing ||
    (sectionDef?.ConfigSection ? !validity.canTest : assetType === "kafka" ? kafkaBrokers().length === 0 : !host);

  const saveDisabledReason = !name.trim()
    ? "asset.formMissingName"
    : sectionDef?.ConfigSection
      ? (validity.saveDisabledReason ?? "")
      : assetType === "ssh" && !host.trim()
        ? "asset.formMissingHost"
        : assetType === "kafka" && kafkaBrokers().length === 0
          ? "asset.formMissingKafkaBrokers"
          : assetType === "k8s" && !kubeconfig.trim() && !editAsset
            ? "asset.formMissingKubeconfig"
            : "";
  const saveDisabled = saving || !!saveDisabledReason || (!!sectionDef?.ConfigSection && !validity.canSave);

  const handleRunTestConnection = sectionDef?.ConfigSection
    ? handleGenericTestConnection
    : assetType === "ssh"
      ? handleTestConnection
      : handleTestKafkaConnection;

  const testConnectionButton = !isTestableAssetType ? null : testing && activeTestIdRef.current ? (
    <Button type="button" variant="outline" size="sm" onClick={handleCancelTest} className="gap-1 w-fit">
      <Loader2 className="h-3.5 w-3.5 animate-spin" />
      {t("asset.testing")}
      <XCircle className="h-3.5 w-3.5 ml-1" />
      {t("asset.cancelTest")}
    </Button>
  ) : (
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={handleRunTestConnection}
      disabled={isTestConnectionDisabled}
      className="gap-1 w-fit"
    >
      {testing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PlugZap className="h-3.5 w-3.5" />}
      {testing ? t("asset.testing") : t("asset.testConnection")}
    </Button>
  );

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) cancelActiveTest();
        onOpenChange(next);
      }}
    >
      <DialogContent
        className="sm:max-w-2xl max-h-[85vh] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden p-0"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader className="border-b px-6 pt-6 pb-3">
          <DialogTitle>
            {editAsset ? t("action.edit") : t("action.add")} {typeLabel}
          </DialogTitle>
          <DialogDescription>{t("asset.formDescription")}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 overflow-y-auto px-6 py-4">
          <div className="grid gap-4">
            {/* Asset Type */}
            {!editAsset && (
              <div className="grid gap-2">
                <Label>{t("asset.type")}</Label>
                <AssetTypePicker value={assetType} onChange={(v) => handleTypeChange(v as AssetType)} />
              </div>
            )}

            {/* Icon + Name (same row, icon-first compact picker) */}
            <div className="grid gap-2">
              <Label>{t("asset.name")}</Label>
              <div className="flex gap-2">
                <IconPicker value={icon} onChange={setIcon} type="asset" compact />
                <Input
                  className="flex-1"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={
                    assetType === "ssh"
                      ? "prod-web-01"
                      : assetType === "database"
                        ? "prod-mysql-01"
                        : assetType === "redis"
                          ? "prod-redis-01"
                          : assetType === "mongodb"
                            ? "prod-mongo-01"
                            : assetType === "kafka"
                              ? "prod-kafka-01"
                              : assetType === "k8s"
                                ? "prod-k8s-01"
                                : `prod-${assetType}-01`
                  }
                />
              </div>
            </div>

            {/* Group */}
            <div className="grid gap-2">
              <Label>{t("asset.group")}</Label>
              <GroupSelect value={groupId} onValueChange={setGroupId} />
            </div>

            {/* Type-specific config sections */}
            {assetType === "ssh" && (
              <SSHConfigSection
                host={host}
                setHost={setHost}
                port={port}
                setPort={setPort}
                username={username}
                setUsername={setUsername}
                authType={authType}
                setAuthType={setAuthType}
                connectionType={connectionType}
                setConnectionType={setConnectionType}
                password={password}
                setPassword={setPassword}
                encryptedPassword={encryptedPassword}
                passwordSource={passwordSource}
                setPasswordSource={setPasswordSource}
                passwordCredentialId={passwordCredentialId}
                setPasswordCredentialId={setPasswordCredentialId}
                managedPasswords={managedPasswords}
                keySource={keySource}
                setKeySource={setKeySource}
                credentialId={credentialId}
                setCredentialId={setCredentialId}
                managedKeys={managedKeys}
                localKeys={localKeys}
                setLocalKeys={setLocalKeys}
                selectedKeyPaths={selectedKeyPaths}
                setSelectedKeyPaths={setSelectedKeyPaths}
                privateKeyPassphrase={privateKeyPassphrase}
                setPrivateKeyPassphrase={setPrivateKeyPassphrase}
                scanningKeys={scanningKeys}
                sshTunnelId={sshTunnelId}
                setSshTunnelId={setSshTunnelId}
                jumpHostExcludeIds={jumpHostExcludeIds}
                proxyType={proxyType}
                setProxyType={setProxyType}
                proxyHost={proxyHost}
                setProxyHost={setProxyHost}
                proxyPort={proxyPort}
                setProxyPort={setProxyPort}
                proxyUsername={proxyUsername}
                setProxyUsername={setProxyUsername}
                proxyPassword={proxyPassword}
                setProxyPassword={setProxyPassword}
                encryptedProxyPassword={encryptedProxyPassword}
                editAssetId={editAsset?.ID}
              />
            )}

            {assetType === "kafka" && (
              <KafkaConfigSection
                brokersText={kafkaBrokersText}
                setBrokersText={setKafkaBrokersText}
                clientId={kafkaClientId}
                setClientId={setKafkaClientId}
                saslMechanism={kafkaSaslMechanism}
                setSaslMechanism={setKafkaSaslMechanism}
                username={username}
                setUsername={setUsername}
                tls={tls}
                setTls={setTls}
                tlsInsecure={kafkaTlsInsecure}
                setTlsInsecure={setKafkaTlsInsecure}
                tlsServerName={kafkaTlsServerName}
                setTlsServerName={setKafkaTlsServerName}
                tlsCAFile={kafkaTlsCAFile}
                setTlsCAFile={setKafkaTlsCAFile}
                tlsCertFile={kafkaTlsCertFile}
                setTlsCertFile={setKafkaTlsCertFile}
                tlsKeyFile={kafkaTlsKeyFile}
                setTlsKeyFile={setKafkaTlsKeyFile}
                requestTimeoutSeconds={kafkaRequestTimeoutSeconds}
                setRequestTimeoutSeconds={setKafkaRequestTimeoutSeconds}
                messagePreviewBytes={kafkaMessagePreviewBytes}
                setMessagePreviewBytes={setKafkaMessagePreviewBytes}
                messageFetchLimit={kafkaMessageFetchLimit}
                setMessageFetchLimit={setKafkaMessageFetchLimit}
                sshTunnelId={sshTunnelId}
                setSshTunnelId={setSshTunnelId}
                password={password}
                setPassword={setPassword}
                encryptedPassword={encryptedPassword}
                passwordSource={passwordSource}
                setPasswordSource={setPasswordSource}
                passwordCredentialId={passwordCredentialId}
                setPasswordCredentialId={setPasswordCredentialId}
                managedPasswords={managedPasswords}
                editAssetId={editAsset?.ID}
                schemaRegistry={kafkaSchemaRegistry}
                setSchemaRegistry={setKafkaSchemaRegistry}
                connectEnabled={kafkaConnectEnabled}
                setConnectEnabled={setKafkaConnectEnabled}
                connectClusters={kafkaConnectClusters}
                setConnectClusters={setKafkaConnectClusters}
              />
            )}

            {/* K8S config */}
            {assetType === "k8s" && (
              <K8sConfigSection
                kubeconfig={kubeconfig}
                setKubeconfig={setKubeconfig}
                showKubeconfig={showKubeconfig}
                setShowKubeconfig={setShowKubeconfig}
                namespace={k8sNamespace}
                setNamespace={setK8sNamespace}
                contextName={k8sContext}
                setContextName={setK8sContext}
                sshTunnelId={sshTunnelId}
                setSshTunnelId={setSshTunnelId}
                isEditing={!!editAsset}
              />
            )}

            {/* 注册化类型:通用 ConfigSection 路径(local 等) */}
            {sectionDef?.ConfigSection && (
              <sectionDef.ConfigSection
                key={assetType}
                ref={sectionRef}
                editAsset={editAsset ?? undefined}
                ctx={ctx}
                onValidityChange={setValidity}
                onIconChange={setIcon}
              />
            )}

            {/* Extension type config */}
            {assetType !== "ssh" &&
              assetType !== "database" &&
              assetType !== "kafka" &&
              assetType !== "k8s" &&
              (() => {
                const extInfo = useExtensionStore.getState().getExtensionForAssetType(assetType);
                if (!extInfo) return null;
                const assetTypeDef = extInfo.manifest.assetTypes?.find((at) => at.type === assetType);
                if (!assetTypeDef?.configSchema) return null;
                return (
                  <ExtensionConfigForm
                    extensionName={extInfo.name}
                    configSchema={assetTypeDef.configSchema as Record<string, unknown>}
                    value={extConfig}
                    onChange={setExtConfig}
                    hasBackend={!!extInfo.manifest.backend}
                  />
                );
              })()}

            {/* Description */}
            <div className="grid gap-2">
              <Label>{t("asset.description")}</Label>
              <Textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={2} />
            </div>
          </div>
        </div>
        <DialogFooter className="border-t bg-background px-6 py-3 sm:items-center sm:justify-between">
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            {testConnectionButton}
            {saveDisabledReason && (
              <p className="flex items-center gap-1 text-xs text-muted-foreground">
                <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                {t(saveDisabledReason)}
              </p>
            )}
          </div>
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button
              variant="outline"
              onClick={() => {
                cancelActiveTest();
                onOpenChange(false);
              }}
            >
              {t("action.cancel")}
            </Button>
            <Button onClick={handleSubmit} disabled={saveDisabled}>
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {saving ? t("action.saving") : t("action.save")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
