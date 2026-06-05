import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import {
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Textarea,
} from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { credential_entity } from "../../../wailsjs/go/models";
import type { AssetFormHandle, AssetFormContext, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  appendKafkaCredential,
  buildKafkaBaseConfig,
  kafkaBrokers,
  kafkaCompanionPlainSecretFromConfig,
  kafkaCompanionUsernameFromConfig,
  KAFKA_DEFAULTS,
  parseKafkaConfig,
  type KafkaConnectClusterConfig,
  type KafkaConnectConfig,
  type KafkaFormState,
  type KafkaSchemaRegistryConfig,
} from "./KafkaConfigSection.config";

export type KafkaPasswordSource = "inline" | "managed";

export interface KafkaCompanionAuthForm {
  authType: string;
  username: string;
  password: string;
  encryptedPassword: string;
  passwordSource: KafkaPasswordSource;
  credentialId: number;
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
}

export interface KafkaSchemaRegistryForm extends KafkaCompanionAuthForm {
  enabled: boolean;
  url: string;
}

export interface KafkaConnectClusterForm extends KafkaCompanionAuthForm {
  id: string;
  name: string;
  url: string;
}

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

function normalizedNumber(value: string, fallback: number) {
  const next = Number(value);
  if (!Number.isFinite(next)) return fallback;
  return Math.max(0, Math.floor(next));
}

/** 伴随密码加密(明文优先;无明文沿用既有密文)。加密失败由 encrypt 的 reject 透传给 buildConfig→handleSubmit 统一 toast。 */
async function encryptKafkaCompanionPassword(
  plainPassword: string,
  existingEncryptedPassword: string,
  encrypt: (plain: string) => Promise<string>
): Promise<string> {
  if (plainPassword) return encrypt(plainPassword);
  if (existingEncryptedPassword) return existingEncryptedPassword;
  return "";
}

/** 伴随 auth 注入(none 跳过;basic 写 username;凭据 managed→credential_id 否则加密 password)。镜像旧 applyKafkaCompanionAuth。 */
async function applyKafkaCompanionAuth(
  cfg: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig,
  form: KafkaCompanionAuthForm,
  encrypt: (plain: string) => Promise<string>
): Promise<void> {
  const authType = form.authType || "none";
  if (authType === "none") return;
  cfg.auth_type = authType;
  if (authType !== "bearer" && form.username.trim()) cfg.username = form.username.trim();
  if (form.passwordSource === "managed" && form.credentialId > 0) {
    cfg.credential_id = form.credentialId;
    return;
  }
  const encrypted = await encryptKafkaCompanionPassword(form.password, form.encryptedPassword, encrypt);
  if (encrypted) cfg.password = encrypted;
}

/** 伴随 TLS 注入(各项 trim 后非空才写)。镜像旧 applyKafkaCompanionTLS。 */
function applyKafkaCompanionTLS(
  cfg: KafkaSchemaRegistryConfig | KafkaConnectClusterConfig,
  form: KafkaCompanionAuthForm
) {
  if (form.tlsInsecure) cfg.tls_insecure = true;
  if (form.tlsServerName.trim()) cfg.tls_server_name = form.tlsServerName.trim();
  if (form.tlsCAFile.trim()) cfg.tls_ca_file = form.tlsCAFile.trim();
  if (form.tlsCertFile.trim()) cfg.tls_cert_file = form.tlsCertFile.trim();
  if (form.tlsKeyFile.trim()) cfg.tls_key_file = form.tlsKeyFile.trim();
}

/** 翻译函数最小签名(throw 文案用)。 */
type Translate = (key: string, opts?: Record<string, unknown>) => string;

/** bearer 伴随必须有 token,否则 throw(handleSubmit 的 catch 统一 toast)。镜像旧 validateKafkaCompanionAuth。 */
function validateKafkaCompanionAuth(form: KafkaCompanionAuthForm, t: Translate) {
  if (form.authType !== "bearer") return;
  const hasToken =
    form.passwordSource === "managed" ? form.credentialId > 0 : !!form.password.trim() || !!form.encryptedPassword;
  if (!hasToken) throw new Error(t("asset.kafkaBearerTokenRequired"));
}

/** 伴随 URL/auth 校验:非法即 throw(handleSubmit 的 catch 统一 toast 该 i18n 文案)。镜像旧 validateKafkaCompanions。 */
function validateKafkaCompanions(
  schemaRegistry: KafkaSchemaRegistryForm,
  connectEnabled: boolean,
  connectClusters: KafkaConnectClusterForm[],
  t: Translate
) {
  if (schemaRegistry.enabled && !schemaRegistry.url.trim()) {
    throw new Error(t("asset.kafkaSchemaRegistryURLRequired"));
  }
  if (schemaRegistry.enabled) validateKafkaCompanionAuth(schemaRegistry, t);
  if (connectEnabled) {
    const clusters = connectClusters.filter((cluster) => cluster.name.trim() || cluster.url.trim());
    if (clusters.length === 0) throw new Error(t("asset.kafkaConnectClusterRequired"));
    if (clusters.some((cluster) => !cluster.name.trim() || !cluster.url.trim())) {
      throw new Error(t("asset.kafkaConnectClusterInvalid"));
    }
    clusters.forEach((cluster) => validateKafkaCompanionAuth(cluster, t));
  }
}

/** schema_registry 伴随 config(enabled 才构建;auth/TLS 注入)。镜像旧 buildKafkaSchemaRegistryConfig。 */
async function buildSchemaRegistryConfig(
  schemaRegistry: KafkaSchemaRegistryForm,
  encrypt: (plain: string) => Promise<string>
): Promise<KafkaSchemaRegistryConfig | undefined> {
  if (!schemaRegistry.enabled) return undefined;
  const cfg: KafkaSchemaRegistryConfig = { enabled: true, url: schemaRegistry.url.trim() };
  await applyKafkaCompanionAuth(cfg, schemaRegistry, encrypt);
  applyKafkaCompanionTLS(cfg, schemaRegistry);
  return cfg;
}

/** connect 伴随 config(enabled 才构建;逐 cluster auth/TLS 注入)。镜像旧 buildKafkaConnectConfig。 */
async function buildConnectConfig(
  connectEnabled: boolean,
  connectClusters: KafkaConnectClusterForm[],
  encrypt: (plain: string) => Promise<string>
): Promise<KafkaConnectConfig | undefined> {
  if (!connectEnabled) return undefined;
  const cfg: KafkaConnectConfig = { enabled: true, clusters: [] };
  const clusters = connectClusters.filter((cluster) => cluster.name.trim() || cluster.url.trim());
  for (const cluster of clusters) {
    const next: KafkaConnectClusterConfig = { name: cluster.name.trim(), url: cluster.url.trim() };
    await applyKafkaCompanionAuth(next, cluster, encrypt);
    applyKafkaCompanionTLS(next, cluster);
    cfg.clusters?.push(next);
  }
  return cfg;
}

export const KafkaConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function KafkaConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<KafkaFormState>(() => {
    if (!editAsset) return { ...KAFKA_DEFAULTS };
    const parsed = parseKafkaConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
  });
  const patch = (p: Partial<KafkaFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // 伴随子状态:section 自持(各自带 encryptedPassword/credentialId/passwordSource,不走 useAssetCredential)。
  const [schemaRegistry, setSchemaRegistryState] = useState<KafkaSchemaRegistryForm>(() => {
    if (!editAsset) return defaultKafkaSchemaRegistry();
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { schema_registry?: KafkaSchemaRegistryConfig };
      return kafkaSchemaRegistryFromConfig(cfg.schema_registry);
    } catch {
      return defaultKafkaSchemaRegistry();
    }
  });
  const setSchemaRegistry = (p: Partial<KafkaSchemaRegistryForm>) =>
    setSchemaRegistryState((current) => ({ ...current, ...p }));

  const [connectEnabled, setConnectEnabled] = useState<boolean>(() => {
    if (!editAsset) return false;
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { connect?: KafkaConnectConfig };
      return !!cfg.connect?.enabled;
    } catch {
      return false;
    }
  });
  const [connectClusters, setConnectClusters] = useState<KafkaConnectClusterForm[]>(() => {
    if (!editAsset) return [];
    try {
      const cfg = JSON.parse(editAsset.Config || "{}") as { connect?: KafkaConnectConfig };
      return (cfg.connect?.clusters || []).map((cluster, index) => newKafkaConnectCluster(cluster, index));
    } catch {
      return [];
    }
  });

  const saslEnabled = state.saslMechanism !== "none";

  // brokers 为保存/测试共同必填;上报反应式校验(伴随级校验只在 buildConfig/submit 触发,不反应式 gate)。
  useEffect(() => {
    const ok = kafkaBrokers(state.brokersText).length > 0;
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "asset.formMissingKafkaBrokers" });
  }, [state.brokersText, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx: AssetFormContext) => {
        validateKafkaCompanions(schemaRegistry, connectEnabled, connectClusters, t); // 非法 throw → handleSubmit toast
        const cfg = buildKafkaBaseConfig(state);
        if (saslEnabled) {
          appendKafkaCredential(cfg, await resolveSaveCredential(cred.value, ctx.encryptPassword));
        }
        const schemaRegistryConfig = await buildSchemaRegistryConfig(schemaRegistry, ctx.encryptPassword);
        if (schemaRegistry.enabled && schemaRegistryConfig) cfg.schema_registry = schemaRegistryConfig;
        const connectConfig = await buildConnectConfig(connectEnabled, connectClusters, ctx.encryptPassword);
        if (connectEnabled && connectConfig) cfg.connect = connectConfig;
        return { configJSON: JSON.stringify(cfg), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => {
        const cfg = buildKafkaBaseConfig(state);
        if (saslEnabled) appendKafkaCredential(cfg, resolveTestCredential(cred.value));
        return { assetType: "kafka", configJSON: JSON.stringify(cfg), password: cred.value.password };
      },
    }),
    [state, cred.value, saslEnabled, schemaRegistry, connectEnabled, connectClusters, t]
  );

  return (
    <>
      {/* Connection & Auth (single visual block) */}
      <div className="grid gap-3 border rounded-lg p-3">
        <div className="grid gap-2">
          <Label>{t("asset.kafkaBrokers")}</Label>
          <Textarea
            value={state.brokersText}
            onChange={(e) => patch({ brokersText: e.target.value })}
            rows={3}
            className="font-mono text-sm"
            placeholder="192.168.100.50:9092"
          />
        </div>

        <div className="grid gap-2">
          <Label>{t("asset.kafkaClientId")}</Label>
          <Input value={state.clientId} onChange={(e) => patch({ clientId: e.target.value })} placeholder="opskat" />
        </div>

        <div className="grid gap-2">
          <Label>{t("asset.kafkaSaslMechanism")}</Label>
          <Select value={state.saslMechanism} onValueChange={(v) => patch({ saslMechanism: v })}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">{t("asset.kafkaSaslNone")}</SelectItem>
              <SelectItem value="plain">PLAIN</SelectItem>
              <SelectItem value="scram-sha-256">SCRAM-SHA-256</SelectItem>
              <SelectItem value="scram-sha-512">SCRAM-SHA-512</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {saslEnabled && (
          <>
            <div className="grid gap-2">
              <Label>{t("asset.username")}</Label>
              <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
            </div>
            <PasswordSourceField
              source={cred.value.passwordSource}
              onSourceChange={cred.setPasswordSource}
              password={cred.value.password}
              onPasswordChange={cred.setPassword}
              credentialId={cred.value.passwordCredentialId}
              onCredentialIdChange={cred.setPasswordCredentialId}
              managedPasswords={cred.managedPasswords}
              hasExistingPassword={!!cred.value.encryptedPassword}
              editAssetId={editAsset?.ID}
              onUsernameChange={(v) => patch({ username: v })}
            />
          </>
        )}
      </div>

      <div className="flex items-center justify-between">
        <Label>{t("asset.tls")}</Label>
        <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
      </div>

      {state.tls && (
        <>
          <div className="flex items-center justify-between">
            <Label>{t("asset.kafkaTlsInsecure")}</Label>
            <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.kafkaTlsServerName")}</Label>
            <Input
              value={state.tlsServerName}
              onChange={(e) => patch({ tlsServerName: e.target.value })}
              placeholder="kafka.example.com"
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.kafkaTlsCAFile")}</Label>
            <Input
              value={state.tlsCAFile}
              onChange={(e) => patch({ tlsCAFile: e.target.value })}
              placeholder="/path/to/ca.pem"
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.kafkaTlsCertFile")}</Label>
            <Input
              value={state.tlsCertFile}
              onChange={(e) => patch({ tlsCertFile: e.target.value })}
              placeholder="/path/to/client.crt"
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("asset.kafkaTlsKeyFile")}</Label>
            <Input
              value={state.tlsKeyFile}
              onChange={(e) => patch({ tlsKeyFile: e.target.value })}
              placeholder="/path/to/client.key"
            />
          </div>
        </>
      )}

      <div className="grid grid-cols-3 gap-3">
        <div className="grid gap-2">
          <Label>{t("asset.kafkaRequestTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            max={300}
            value={state.requestTimeoutSeconds}
            onChange={(e) => patch({ requestTimeoutSeconds: normalizedNumber(e.target.value, 30) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.kafkaMessagePreviewBytes")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.messagePreviewBytes}
            onChange={(e) => patch({ messagePreviewBytes: normalizedNumber(e.target.value, 4096) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.kafkaMessageFetchLimit")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            max={1000}
            value={state.messageFetchLimit}
            onChange={(e) => patch({ messageFetchLimit: normalizedNumber(e.target.value, 50) })}
          />
        </div>
      </div>

      <div className="grid gap-2">
        <Label>{t("asset.sshTunnel")}</Label>
        <AssetSelect
          value={state.sshTunnelId}
          onValueChange={(v) => patch({ sshTunnelId: v })}
          filterType="ssh"
          placeholder={t("asset.sshTunnelNone")}
        />
      </div>

      <div className="grid gap-3 rounded-md border p-3">
        <div className="flex items-center justify-between gap-3">
          <Label>{t("asset.kafkaSchemaRegistry")}</Label>
          <Switch checked={schemaRegistry.enabled} onCheckedChange={(enabled) => setSchemaRegistry({ enabled })} />
        </div>
        {schemaRegistry.enabled && (
          <>
            <div className="grid gap-2">
              <Label>{t("asset.kafkaSchemaRegistryURL")}</Label>
              <Input
                value={schemaRegistry.url}
                onChange={(e) => setSchemaRegistry({ url: e.target.value })}
                placeholder="http://schema-registry.example.com:8081"
              />
            </div>
            <KafkaCompanionAuthFields
              value={schemaRegistry}
              onChange={setSchemaRegistry}
              managedPasswords={cred.managedPasswords}
            />
          </>
        )}
      </div>

      <div className="grid gap-3 rounded-md border p-3">
        <div className="flex items-center justify-between gap-3">
          <Label>{t("asset.kafkaConnect")}</Label>
          <Switch
            checked={connectEnabled}
            onCheckedChange={(enabled) => {
              setConnectEnabled(enabled);
              if (enabled && connectClusters.length === 0) {
                setConnectClusters([newKafkaConnectCluster()]);
              }
            }}
          />
        </div>
        {connectEnabled && (
          <div className="grid gap-3">
            {connectClusters.map((cluster, index) => (
              <KafkaConnectClusterEditor
                key={cluster.id}
                index={index}
                value={cluster}
                onChange={(patch) =>
                  setConnectClusters(
                    connectClusters.map((item, itemIndex) => (itemIndex === index ? { ...item, ...patch } : item))
                  )
                }
                onRemove={() => setConnectClusters(connectClusters.filter((_, itemIndex) => itemIndex !== index))}
                managedPasswords={cred.managedPasswords}
              />
            ))}
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 w-fit gap-1.5"
              onClick={() => setConnectClusters([...connectClusters, newKafkaConnectCluster()])}
            >
              <Plus className="h-3.5 w-3.5" />
              {t("asset.kafkaConnectAddCluster")}
            </Button>
          </div>
        )}
      </div>
    </>
  );
});

function KafkaCompanionAuthFields({
  value,
  onChange,
  managedPasswords,
}: {
  value: KafkaCompanionAuthForm;
  onChange: (patch: Partial<KafkaCompanionAuthForm>) => void;
  managedPasswords: credential_entity.Credential[];
}) {
  const { t } = useTranslation();
  const authEnabled = value.authType !== "none";

  return (
    <div className="grid gap-3">
      <div className="grid gap-2">
        <Label>{t("asset.kafkaCompanionAuthType")}</Label>
        <Select
          value={value.authType}
          onValueChange={(authType) => onChange(kafkaCompanionAuthTypePatch(value, authType))}
        >
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="none">{t("asset.kafkaSaslNone")}</SelectItem>
            <SelectItem value="basic">Basic</SelectItem>
            <SelectItem value="bearer">Bearer</SelectItem>
          </SelectContent>
        </Select>
      </div>
      {authEnabled && (
        <>
          {value.authType !== "bearer" && (
            <div className="grid gap-2">
              <Label>{t("asset.username")}</Label>
              <Input value={value.username} onChange={(e) => onChange({ username: e.target.value })} />
            </div>
          )}
          <PasswordSourceField
            source={value.passwordSource}
            onSourceChange={(passwordSource) => onChange({ passwordSource })}
            password={value.password}
            onPasswordChange={(password) => onChange({ password })}
            credentialId={value.credentialId}
            onCredentialIdChange={(credentialId) => onChange({ credentialId })}
            managedPasswords={managedPasswords}
            hasExistingPassword={!!value.encryptedPassword}
            secretLabel={value.authType === "bearer" ? t("asset.kafkaBearerToken") : undefined}
            selectSecretLabel={value.authType === "bearer" ? t("asset.kafkaBearerToken") : undefined}
            onUsernameChange={(username) => onChange({ username })}
          />
        </>
      )}
      <div className="flex items-center justify-between">
        <Label>{t("asset.kafkaTlsInsecure")}</Label>
        <Switch checked={value.tlsInsecure} onCheckedChange={(tlsInsecure) => onChange({ tlsInsecure })} />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.kafkaTlsServerName")}</Label>
        <Input value={value.tlsServerName} onChange={(e) => onChange({ tlsServerName: e.target.value })} />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.kafkaTlsCAFile")}</Label>
        <Input value={value.tlsCAFile} onChange={(e) => onChange({ tlsCAFile: e.target.value })} />
      </div>
      <div className="grid gap-2 md:grid-cols-2">
        <div className="grid gap-2">
          <Label>{t("asset.kafkaTlsCertFile")}</Label>
          <Input value={value.tlsCertFile} onChange={(e) => onChange({ tlsCertFile: e.target.value })} />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.kafkaTlsKeyFile")}</Label>
          <Input value={value.tlsKeyFile} onChange={(e) => onChange({ tlsKeyFile: e.target.value })} />
        </div>
      </div>
    </div>
  );
}

function kafkaCompanionAuthTypePatch(value: KafkaCompanionAuthForm, authType: string): Partial<KafkaCompanionAuthForm> {
  const patch: Partial<KafkaCompanionAuthForm> = { authType };
  if (authType === "bearer") {
    if (value.username && !value.password && !value.encryptedPassword && !value.credentialId) {
      patch.password = value.username;
    }
    patch.username = "";
  }
  return patch;
}

function KafkaConnectClusterEditor({
  index,
  value,
  onChange,
  onRemove,
  managedPasswords,
}: {
  index: number;
  value: KafkaConnectClusterForm;
  onChange: (patch: Partial<KafkaConnectClusterForm>) => void;
  onRemove: () => void;
  managedPasswords: credential_entity.Credential[];
}) {
  const { t } = useTranslation();

  return (
    <div className="grid gap-3 rounded-md border bg-muted/20 p-3">
      <div className="flex items-center justify-between gap-2">
        <Label>{t("asset.kafkaConnectClusterNumber", { index: index + 1 })}</Label>
        <Button type="button" variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={onRemove}>
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="grid gap-2 md:grid-cols-2">
        <div className="grid gap-2">
          <Label>{t("asset.kafkaConnectClusterName")}</Label>
          <Input value={value.name} onChange={(e) => onChange({ name: e.target.value })} placeholder="primary" />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.kafkaConnectClusterURL")}</Label>
          <Input
            value={value.url}
            onChange={(e) => onChange({ url: e.target.value })}
            placeholder="http://connect.example.com:8083"
          />
        </div>
      </div>
      <KafkaCompanionAuthFields value={value} onChange={onChange} managedPasswords={managedPasswords} />
    </div>
  );
}
