import { forwardRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Plus, Trash2 } from "lucide-react";
import { Button, Input, Label, Switch } from "@opskat/ui";
import { Field, Segmented } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { credential_entity } from "../../../wailsjs/go/models";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
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

  const schemaRegistryInvalid = schemaRegistry.enabled && !schemaRegistry.url.trim();
  const connectInvalid =
    connectEnabled &&
    (() => {
      const clusters = connectClusters.filter((c) => c.name.trim() || c.url.trim());
      if (clusters.length === 0) return true;
      return clusters.some((c) => !c.name.trim() || !c.url.trim());
    })();

  const { state, patch } = useConfigSection<KafkaFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseKafkaConfig(a.Config, a.sshTunnelId || 0) : { ...KAFKA_DEFAULTS }),
    validate: (s) => {
      const brokersOk = kafkaBrokers(s.brokersText).length > 0;
      let saveDisabledReason = "";
      if (!brokersOk) {
        saveDisabledReason = "asset.formMissingKafkaBrokers";
      } else if (schemaRegistryInvalid) {
        saveDisabledReason = "asset.kafkaSchemaRegistryURLRequired";
      } else if (connectInvalid) {
        saveDisabledReason = "asset.kafkaConnectClusterInvalid";
      }
      return {
        canTest: brokersOk,
        canSave: brokersOk && !schemaRegistryInvalid && !connectInvalid,
        saveDisabledReason,
      };
    },
    validityDeps: [schemaRegistryInvalid, connectInvalid],
    build: async (s, ctx) => {
      validateKafkaCompanions(schemaRegistry, connectEnabled, connectClusters, t); // 非法 throw → handleSubmit toast
      const proxyPassword = await resolveSaveProxyPassword(s, ctx.encryptPassword);
      const cfg = buildKafkaBaseConfig(s, proxyPassword);
      if (s.saslMechanism !== "none") {
        appendKafkaCredential(cfg, await resolveSaveCredential(cred.value, ctx.encryptPassword));
      }
      const schemaRegistryConfig = await buildSchemaRegistryConfig(schemaRegistry, ctx.encryptPassword);
      if (schemaRegistry.enabled && schemaRegistryConfig) cfg.schema_registry = schemaRegistryConfig;
      const connectConfig = await buildConnectConfig(connectEnabled, connectClusters, ctx.encryptPassword);
      if (connectEnabled && connectConfig) cfg.connect = connectConfig;
      return {
        configJSON: JSON.stringify(cfg),
        sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
      };
    },
    buildTest: async (s) => {
      // 测试:proxy 密码仅明文(无加密)
      const cfg = buildKafkaBaseConfig(s, s.proxyPassword);
      if (s.saslMechanism !== "none") appendKafkaCredential(cfg, resolveTestCredential(cred.value));
      return { assetType: "kafka", configJSON: JSON.stringify(cfg), password: cred.value.password };
    },
    deps: [cred.value, schemaRegistry, connectEnabled, connectClusters, t],
  });

  const KAFKA_GROUPS: ConfigGroupSchema<KafkaFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "textarea",
          key: "brokersText",
          label: "asset.kafkaBrokers",
          required: true,
          mono: true,
          rows: 3,
          placeholder: "192.168.100.50:9092",
        },
        {
          kind: "select",
          key: "saslMechanism",
          label: "asset.kafkaSaslMechanism",
          options: [
            { value: "none", label: "asset.kafkaSaslNone" },
            { value: "plain", label: "PLAIN" },
            { value: "scram-sha-256", label: "SCRAM-SHA-256" },
            { value: "scram-sha-512", label: "SCRAM-SHA-512" },
          ],
        },
        { kind: "text", key: "username", label: "asset.username", visibleWhen: (s) => s.saslMechanism !== "none" },
        { kind: "password", visibleWhen: (s) => s.saslMechanism !== "none" },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
    {
      key: "tls",
      label: "asset.tabTls",
      fields: [
        {
          kind: "custom",
          render: (s, p) => (
            <div className="flex items-center justify-between">
              <Label>{t("asset.tls")}</Label>
              <Switch checked={s.tls} onCheckedChange={(v) => p({ tls: v })} />
            </div>
          ),
        },
        {
          kind: "custom",
          visibleWhen: (s) => s.tls,
          render: (s, p) => (
            <div className="flex items-center justify-between">
              <Label>{t("asset.kafkaTlsInsecure")}</Label>
              <Switch checked={s.tlsInsecure} onCheckedChange={(v) => p({ tlsInsecure: v })} />
            </div>
          ),
        },
        {
          kind: "text",
          key: "tlsServerName",
          label: "asset.kafkaTlsServerName",
          placeholder: "kafka.example.com",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCAFile",
          label: "asset.kafkaTlsCAFile",
          placeholder: "/path/to/ca.pem",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsCertFile",
          label: "asset.kafkaTlsCertFile",
          placeholder: "/path/to/client.crt",
          visibleWhen: (s) => s.tls,
        },
        {
          kind: "text",
          key: "tlsKeyFile",
          label: "asset.kafkaTlsKeyFile",
          placeholder: "/path/to/client.key",
          visibleWhen: (s) => s.tls,
        },
      ],
    },
    {
      key: "schema_registry",
      label: "asset.tabSchemaRegistry",
      render: () => (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between gap-3">
            <Label>{t("asset.kafkaSchemaRegistry")}</Label>
            <Switch
              data-testid="kafka-sr-enabled"
              checked={schemaRegistry.enabled}
              onCheckedChange={(enabled) => setSchemaRegistry({ enabled })}
            />
          </div>
          {schemaRegistry.enabled && (
            <>
              <Field label={t("asset.kafkaSchemaRegistryURL")} required>
                <Input
                  value={schemaRegistry.url}
                  onChange={(e) => setSchemaRegistry({ url: e.target.value })}
                  placeholder="http://schema-registry.example.com:8081"
                />
              </Field>
              <KafkaCompanionAuthFields
                value={schemaRegistry}
                onChange={setSchemaRegistry}
                managedPasswords={cred.managedPasswords}
              />
            </>
          )}
        </div>
      ),
    },
    {
      key: "connect",
      label: "asset.tabConnect",
      badge: connectEnabled ? connectClusters.length : undefined,
      render: () => (
        <div className="flex flex-col gap-4">
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
            <div className="flex flex-col gap-4">
              {connectClusters.map((cluster, index) => (
                <KafkaConnectClusterEditor
                  key={cluster.id}
                  index={index}
                  value={cluster}
                  onChange={(p) =>
                    setConnectClusters(
                      connectClusters.map((item, itemIndex) => (itemIndex === index ? { ...item, ...p } : item))
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
      ),
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        { kind: "text", key: "clientId", label: "asset.kafkaClientId", placeholder: "opskat" },
        {
          kind: "custom",
          render: (s, p) => (
            <div className="flex items-end gap-3">
              <Field label={t("asset.kafkaRequestTimeout")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  max={300}
                  value={s.requestTimeoutSeconds}
                  onChange={(e) => p({ requestTimeoutSeconds: normalizedNumber(e.target.value, 30) })}
                />
              </Field>
              <Field label={t("asset.kafkaMessagePreviewBytes")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  value={s.messagePreviewBytes}
                  onChange={(e) => p({ messagePreviewBytes: normalizedNumber(e.target.value, 4096) })}
                />
              </Field>
              <Field label={t("asset.kafkaMessageFetchLimit")} className="flex-1">
                <Input
                  className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                  type="number"
                  min={0}
                  max={1000}
                  value={s.messageFetchLimit}
                  onChange={(e) => p({ messageFetchLimit: normalizedNumber(e.target.value, 50) })}
                />
              </Field>
            </div>
          ),
        },
      ],
    },
  ];

  return <ConfigTabs groups={buildConfigGroups(KAFKA_GROUPS, { state, patch, ctx: { cred, editAsset } })} />;
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
    <div className="flex flex-col gap-4">
      <Field label={t("asset.kafkaCompanionAuthType")}>
        <Segmented
          value={value.authType}
          onChange={(authType) => onChange(kafkaCompanionAuthTypePatch(value, authType))}
          aria-label={t("asset.kafkaCompanionAuthType")}
          options={[
            { value: "none", label: t("asset.kafkaSaslNone") },
            { value: "basic", label: "Basic" },
            { value: "bearer", label: "Bearer" },
          ]}
        />
      </Field>
      {authEnabled && (
        <>
          {value.authType !== "bearer" && (
            <Field label={t("asset.username")}>
              <Input value={value.username} onChange={(e) => onChange({ username: e.target.value })} />
            </Field>
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
      <Field label={t("asset.kafkaTlsServerName")}>
        <Input value={value.tlsServerName} onChange={(e) => onChange({ tlsServerName: e.target.value })} />
      </Field>
      <Field label={t("asset.kafkaTlsCAFile")}>
        <Input value={value.tlsCAFile} onChange={(e) => onChange({ tlsCAFile: e.target.value })} />
      </Field>
      <div className="flex items-end gap-3">
        <Field label={t("asset.kafkaTlsCertFile")} className="flex-1">
          <Input value={value.tlsCertFile} onChange={(e) => onChange({ tlsCertFile: e.target.value })} />
        </Field>
        <Field label={t("asset.kafkaTlsKeyFile")} className="flex-1">
          <Input value={value.tlsKeyFile} onChange={(e) => onChange({ tlsKeyFile: e.target.value })} />
        </Field>
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
    <div className="flex flex-col gap-4 rounded-md border bg-muted/20 p-3">
      <div className="flex items-center justify-between gap-2">
        <Label>{t("asset.kafkaConnectClusterNumber", { index: index + 1 })}</Label>
        <Button type="button" variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={onRemove}>
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>
      <div className="flex items-end gap-3">
        <Field label={t("asset.kafkaConnectClusterName")} required className="flex-1">
          <Input value={value.name} onChange={(e) => onChange({ name: e.target.value })} placeholder="primary" />
        </Field>
        <Field label={t("asset.kafkaConnectClusterURL")} required className="flex-1">
          <Input
            value={value.url}
            onChange={(e) => onChange({ url: e.target.value })}
            placeholder="http://connect.example.com:8083"
          />
        </Field>
      </div>
      <KafkaCompanionAuthFields value={value} onChange={onChange} managedPasswords={managedPasswords} />
    </div>
  );
}
