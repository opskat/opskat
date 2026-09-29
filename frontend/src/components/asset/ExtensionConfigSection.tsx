import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Input, Label, Switch } from "@opskat/ui";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import {
  CONNECTION_DEFAULTS,
  buildProxyChainJSON,
  parseConnectionFields,
  proxyChainValidationKey,
  resolveSaveProxyChainSecrets,
  type ConnectionFormFields,
} from "@/components/asset/proxyConfig";
import { GetDecryptedExtensionConfig, ValidateExtensionConfig } from "../../../wailsjs/go/extension/Extension";
import type { AssetFormContext, AssetTestConfig, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { defaultValues, passwordFields, type ExtensionConfigSchema } from "@/extension/configSchema";
import type { ExtConnection } from "@/extension/types";
import {
  HOST_CONNECTION_CONFIG_KEY,
  type HostConnectionConfig,
  type HostTLSConfig,
} from "@/extension/connectionConfig";

interface Options {
  extensionName: string;
  assetType: string;
  schema?: ExtensionConfigSchema;
  /** 资产类型在 describe() 声明的宿主连接配置；只渲染、保存声明了的项。 */
  connection?: ExtConnection;
  /** describe() 声明了测试连接处理器；决定"测试连接"按钮是否出现。 */
  testConnection?: boolean;
}

interface TLSFormState {
  enabled: boolean;
  insecure: boolean;
  serverName: string;
  caFile: string;
  certFile: string;
  keyFile: string;
}

const TLS_DEFAULTS: TLSFormState = {
  enabled: false,
  insecure: false,
  serverName: "",
  caFile: "",
  certFile: "",
  keyFile: "",
};

interface ExtensionFormState {
  config: Record<string, unknown>;
  /** 编辑态要先拿到后端解密后的配置：资产上存的是密文，拿它回填/保存会把密文再加密一遍。 */
  status: "ready" | "loading" | "error";
  /**
   * 宿主没回显的已存密码字段（扩展未声明 credentials:read）→ 其已存密文。表单按内置资产
   * 的"已设置，留空则不修改"呈现：留空保存沿用这份密文，填了新值才替换。
   */
  withheldSecrets: Record<string, string>;
  /** 经 SSH 隧道连接；隧道资产存在资产的 sshTunnelId 列上，不进扩展能读到的 config。 */
  tunnel: boolean;
  sshTunnelId: number;
  /** 代理链，宿主保留键（不进扩展能读到的 config），仅 connection.proxyChain 声明时生效。 */
  proxyChain: ConnectionFormFields;
  /** TLS，宿主保留键，仅 connection.tls 声明时生效。 */
  tls: TLSFormState;
}

const STATUS_REASON: Record<ExtensionFormState["status"], string> = {
  ready: "",
  loading: "asset.extConfigLoading",
  error: "asset.extConfigDecryptFailed",
};

/** 从解密后的完整 config 中取出宿主保留键，其余原样返回给扩展表单渲染。 */
function splitHostConnection(config: Record<string, unknown>): {
  guestConfig: Record<string, unknown>;
  proxyChain: ConnectionFormFields;
  tls: TLSFormState;
} {
  const { [HOST_CONNECTION_CONFIG_KEY]: reserved, ...guestConfig } = config;
  const hostConn = reserved as HostConnectionConfig | undefined;
  return {
    guestConfig,
    proxyChain: parseConnectionFields(null, 0, hostConn?.proxyChain),
    tls: parseTLS(hostConn?.tls),
  };
}

function parseTLS(raw?: HostTLSConfig): TLSFormState {
  return {
    enabled: !!raw?.enabled,
    insecure: !!raw?.insecure,
    serverName: raw?.serverName || "",
    caFile: raw?.caFile || "",
    certFile: raw?.certFile || "",
    keyFile: raw?.keyFile || "",
  };
}

function buildTLS(s: TLSFormState): HostTLSConfig | undefined {
  if (!s.enabled) return undefined;
  return {
    enabled: true,
    insecure: s.insecure || undefined,
    serverName: s.serverName.trim() || undefined,
    caFile: s.caFile.trim() || undefined,
    certFile: s.certFile.trim() || undefined,
    keyFile: s.keyFile.trim() || undefined,
  };
}

/**
 * 扩展资产类型的表单区块，接进注册表的 ConfigSection 槽位。
 *
 * 内置类型每种有一个手写 ConfigSection；扩展类型的"手写区块"是它的 configSchema，所以
 * 这里把 ExtensionConfigForm 包成同一个契约（useConfigSection 的 state/校验/imperative
 * handle）。AssetForm 因此不再需要三段只对扩展生效的分支：回填解密配置、保存前加密
 * password 字段、渲染扩展表单，全部收进这里。
 *
 * 代理链与 TLS 是宿主保留键（HOST_CONNECTION_CONFIG_KEY），与 SSH 隧道一样不进扩展能
 * 读到的 config：宿主在 ctx.AssetConfig() / validate_config 之前把它剥掉
 * （pkg/extension.StripHostConnectionConfig）。
 */
export function makeExtensionConfigSection(opts: Options) {
  const secrets = passwordFields(opts.schema);
  const sshTunnel = !!opts.connection?.sshTunnel;
  const proxyChainEnabled = !!opts.connection?.proxyChain;
  const tlsEnabled = !!opts.connection?.tls;

  // 已存配置里有值的密码字段 → 其密文：表单不回显它，留空保存时原样写回。
  function storedSecrets(stored: Record<string, unknown>, fields: string[] = secrets): Record<string, string> {
    const out: Record<string, string> = {};
    for (const f of fields) {
      const value = stored[f];
      if (typeof value === "string" && value) out[f] = value;
    }
    return out;
  }

  async function encryptSecrets(
    config: Record<string, unknown>,
    withheld: Record<string, string>,
    ctx: AssetFormContext
  ) {
    const out = { ...config };
    for (const field of secrets) {
      const value = out[field];
      if (value === undefined || value === null || value === "") {
        if (withheld[field]) out[field] = withheld[field];
        continue;
      }
      out[field] = await ctx.encryptPassword(String(value));
    }
    return out;
  }

  function ExtensionConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
    const { t } = useTranslation();
    // 已存资产解密回填时的密码字段快照，供"测试连接"判断哪些字段用户没碰过——那些字段
    // 从测试请求里整体去掉，让宿主用已存密文补齐，而不是把解密明文再走一遍 IPC。新建
    // 资产没有已存值，起始为空对象：任何非空输入都当作"用户填的"，原样送出。
    const initialSecretsRef = useRef<Record<string, string>>({});
    // 保存校验（扩展的 validate_config）返回的逐字段错误，显示在对应字段下；用户改动该字段即清除。
    const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
    const { state, setState, patch } = useConfigSection<ExtensionFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => {
        const sshTunnelId = sshTunnel ? a?.sshTunnelId || 0 : 0;
        const base = { tunnel: sshTunnelId > 0, sshTunnelId, withheldSecrets: {} };
        if (a?.ID) {
          return { config: {}, status: "loading", ...base, proxyChain: CONNECTION_DEFAULTS, tls: TLS_DEFAULTS };
        }
        // 复制资产（ID 为 0 但带着源资产的存储配置）：密码字段是源资产的密文，按已存密码
        // 对待——不进输入框，留空保存时原样沿用，而不是被当成明文再加密一遍。
        const { guestConfig, proxyChain, tls } = splitHostConnection(parseConfig(a?.Config));
        const withheldSecrets = storedSecrets(guestConfig);
        for (const f of secrets) delete guestConfig[f];
        // 新建资产以声明的默认值起步；复制资产已带源配置，缺失的键同样补默认。
        return {
          config: { ...defaultValues(opts.schema), ...guestConfig },
          status: "ready",
          ...base,
          withheldSecrets,
          proxyChain,
          tls,
        };
      },
      // 必填校验由后端按 configSchema.required 负责；表单侧不复制一份会漂移的规则。
      validate: (s) => {
        const missingTunnel = s.status === "ready" && s.tunnel && s.sshTunnelId === 0;
        const chainError = proxyChainEnabled ? proxyChainValidationKey(s.proxyChain.proxyChainLayers) : "";
        // 宿主拨号时非空代理链优先于隧道列（EffectiveProxyChain），两者都设就会静默跳过
        // 这里选的 SSH 隧道——让用户二选一（SSH 跳板可作为链上一层）。
        const tunnelWithChain =
          proxyChainEnabled && s.tunnel && s.sshTunnelId > 0 && !!buildProxyChainJSON(s.proxyChain.proxyChainLayers);
        const canSave = s.status === "ready" && !missingTunnel && !chainError && !tunnelWithChain;
        return {
          // 表单是否有效可测，与是否有效可存是同一件事；按钮本身是否出现另由
          // sectionDef.testable（describe() 是否声明了处理器）决定。
          canTest: !!opts.testConnection && canSave,
          canSave,
          saveDisabledReason: missingTunnel
            ? "asset.formMissingSSHTunnel"
            : tunnelWithChain
              ? "asset.formTunnelWithProxyChain"
              : chainError || STATUS_REASON[s.status],
        };
      },
      build: async (s, buildCtx) => {
        if (s.status !== "ready") throw new Error(t(STATUS_REASON[s.status]));
        const config = await encryptSecrets(s.config, s.withheldSecrets, buildCtx);

        const hostConnection = hostConnectionOf(
          s,
          proxyChainEnabled
            ? await resolveSaveProxyChainSecrets(s.proxyChain.proxyChainLayers, buildCtx.encryptPassword)
            : {}
        );
        if (Object.keys(hostConnection).length > 0) {
          config[HOST_CONNECTION_CONFIG_KEY] = hostConnection;
        }

        const configJSON = JSON.stringify(config);
        await checkConfig(configJSON);

        return {
          configJSON,
          // 未声明 sshTunnel 的类型不生效：宿主拨号时同样按声明忽略该列。
          sshTunnelId: s.tunnel ? s.sshTunnelId : 0,
        };
      },
      buildTest: opts.testConnection ? buildTestConfig : undefined,
    });

    // 保存前跑扩展的校验器（保存入口还会再跑一遍同一个校验器，这里是它的结构化视图）：
    // 落在本表单字段上的错误显示在字段旁，其余的并进拒绝保存的错误里交给壳 toast。
    async function checkConfig(configJSON: string) {
      const errors = await ValidateExtensionConfig(opts.extensionName, opts.assetType, configJSON);
      if (errors.length === 0) {
        setFieldErrors({});
        return;
      }
      const declared = opts.schema?.properties ?? {};
      const placed: Record<string, string> = {};
      const unplaced: string[] = [];
      for (const e of errors) {
        if (e.field in declared) placed[e.field] = e.message;
        else unplaced.push(e.field ? `${e.field}: ${e.message}` : e.message);
      }
      setFieldErrors(placed);
      throw new Error(unplaced.length > 0 ? unplaced.join("; ") : t("asset.extConfigInvalid"));
    }

    // 宿主保留键里的连接设置（代理链 / TLS），保存与测试连接共用；两者只差代理链各层
    // 密钥的形态（保存时加密，测试时明文）。SSH 隧道存在资产列上，不在这里。
    function hostConnectionOf(
      s: ExtensionFormState,
      secretsByLayer: Parameters<typeof buildProxyChainJSON>[1]
    ): HostConnectionConfig {
      const hostConnection: HostConnectionConfig = {};
      if (proxyChainEnabled) {
        const chainJSON = buildProxyChainJSON(s.proxyChain.proxyChainLayers, secretsByLayer);
        if (chainJSON) hostConnection.proxyChain = chainJSON;
      }
      if (tlsEnabled) {
        const tls = buildTLS(s.tls);
        if (tls) hostConnection.tls = tls;
      }
      return hostConnection;
    }

    // 测试连接：表单当前值（含连接区）原样送出；未改动的密码字段整体去掉，宿主拿资产 id
    // （借道 AssetTestConfig.password——测试连接没有独立密码语义，这个类型专用的分发闭包
    // 把它当资产 id 解析）用已存密文补齐。新建资产没有资产 id，送空串。
    async function buildTestConfig(s: ExtensionFormState): Promise<AssetTestConfig> {
      if (s.status !== "ready") throw new Error(t(STATUS_REASON[s.status]));
      const config: Record<string, unknown> = { ...s.config };
      for (const field of secrets) {
        const current = String(config[field] ?? "");
        // 复制出来的资产没有资产 id，宿主无从补齐沿用的源资产密文：不带它测试测的就不是
        // 保存下来的那份配置，让用户重新输入。
        if (!editAsset?.ID && current === "" && s.withheldSecrets[field]) {
          throw new Error(t("asset.extTestCopiedSecret"));
        }
        if (current === (initialSecretsRef.current[field] ?? "")) {
          delete config[field];
        }
      }

      const secretsByLayer: Record<string, { password?: string; token?: string }> = {};
      for (const layer of s.proxyChain.proxyChainLayers) {
        secretsByLayer[layer.id] = { password: layer.password || undefined, token: layer.token || undefined };
      }
      const hostConnection = hostConnectionOf(s, secretsByLayer);
      if (sshTunnel && s.tunnel && s.sshTunnelId) {
        hostConnection.sshTunnelId = s.sshTunnelId;
      }
      if (Object.keys(hostConnection).length > 0) {
        config[HOST_CONNECTION_CONFIG_KEY] = hostConnection;
      }

      return {
        assetType: opts.assetType,
        configJSON: JSON.stringify(config),
        password: editAsset?.ID ? String(editAsset.ID) : "",
      };
    }

    // 编辑态：把密文字段换成后端解密后的值，用户才能看到自己填过什么。解密失败不能退回
    // 资产上的原始配置——密码框里会是密文，保存时再加密一次就把真实密钥毁了。
    // 扩展未声明 credentials:read 时后端不回显密码字段：已存密文从资产原始配置里取，
    // 只用于留空保存时原样写回，不进输入框。
    const editID = editAsset?.ID;
    const storedConfig = editAsset?.Config;
    useEffect(() => {
      if (!editID) return;
      let cancelled = false;
      GetDecryptedExtensionConfig(editID, opts.extensionName)
        .then((cfg) => {
          if (cancelled) return;
          const { guestConfig, proxyChain, tls } = splitHostConnection(parseConfig(cfg));
          const withheldSecrets = storedSecrets(
            parseConfig(storedConfig),
            secrets.filter((f) => guestConfig[f] === undefined)
          );
          initialSecretsRef.current = Object.fromEntries(secrets.map((f) => [f, String(guestConfig[f] ?? "")]));
          setState((s) => ({ ...s, config: guestConfig, status: "ready", proxyChain, tls, withheldSecrets }));
        })
        .catch((err) => {
          if (cancelled) return;
          setState((s) => ({ ...s, status: "error" }));
          toast.error(`${t("asset.extConfigDecryptFailed")}: ${String(err)}`);
        });
      return () => {
        cancelled = true;
      };
    }, [editID, storedConfig, setState, t]);

    // 未就绪时不渲染表单：原因已经由壳在保存按钮旁显示（saveDisabledReason），失败另有 toast。
    if (!opts.schema?.properties || state.status !== "ready") return null;
    return (
      <div className="flex flex-col gap-4">
        <ExtensionConfigForm
          configSchema={opts.schema}
          value={state.config}
          withheldSecrets={state.withheldSecrets}
          fieldErrors={fieldErrors}
          onChange={(config) => {
            // 改动过的字段，其校验错误作废。
            setFieldErrors((errs) =>
              Object.fromEntries(Object.entries(errs).filter(([k]) => config[k] === state.config[k]))
            );
            patch({ config, status: "ready" });
          }}
        />
        {(sshTunnel || proxyChainEnabled) && (
          <ConnectionMethodFields
            value={state.proxyChain}
            onChange={(patchValue) => patch({ proxyChain: { ...state.proxyChain, ...patchValue } })}
            excludeIds={editAsset?.ID ? [editAsset.ID] : undefined}
            showChain={proxyChainEnabled}
            sshTunnel={
              sshTunnel
                ? {
                    assetId: state.sshTunnelId,
                    onAssetIdChange: (sshTunnelId) => patch({ sshTunnelId }),
                    active: state.tunnel,
                    onActiveChange: (active) => patch({ tunnel: active }),
                    testId: "extension-ssh-tunnel-select",
                  }
                : undefined
            }
          />
        )}
        {tlsEnabled && (
          <div className="flex flex-col gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between">
              <Label htmlFor="ext-tls-enabled">{t("asset.tls")}</Label>
              <Switch
                id="ext-tls-enabled"
                checked={state.tls.enabled}
                onCheckedChange={(enabled) => patch({ tls: { ...state.tls, enabled } })}
              />
            </div>
            {state.tls.enabled && (
              <>
                <div className="flex items-center justify-between">
                  <Label htmlFor="ext-tls-insecure">{t("asset.tlsInsecure")}</Label>
                  <Switch
                    id="ext-tls-insecure"
                    checked={state.tls.insecure}
                    onCheckedChange={(insecure) => patch({ tls: { ...state.tls, insecure } })}
                  />
                </div>
                <div className="flex flex-col gap-[7px]">
                  <Label htmlFor="ext-tls-server-name">{t("asset.tlsServerName")}</Label>
                  <Input
                    id="ext-tls-server-name"
                    value={state.tls.serverName}
                    onChange={(e) => patch({ tls: { ...state.tls, serverName: e.target.value } })}
                  />
                </div>
                <div className="flex flex-col gap-[7px]">
                  <Label htmlFor="ext-tls-ca-file">{t("asset.tlsCAFile")}</Label>
                  <Input
                    id="ext-tls-ca-file"
                    value={state.tls.caFile}
                    onChange={(e) => patch({ tls: { ...state.tls, caFile: e.target.value } })}
                    placeholder="/path/to/ca.pem"
                  />
                </div>
                <div className="flex flex-col gap-[7px]">
                  <Label htmlFor="ext-tls-cert-file">{t("asset.tlsCertFile")}</Label>
                  <Input
                    id="ext-tls-cert-file"
                    value={state.tls.certFile}
                    onChange={(e) => patch({ tls: { ...state.tls, certFile: e.target.value } })}
                    placeholder="/path/to/client.crt"
                  />
                </div>
                <div className="flex flex-col gap-[7px]">
                  <Label htmlFor="ext-tls-key-file">{t("asset.tlsKeyFile")}</Label>
                  <Input
                    id="ext-tls-key-file"
                    value={state.tls.keyFile}
                    onChange={(e) => patch({ tls: { ...state.tls, keyFile: e.target.value } })}
                    placeholder="/path/to/client.key"
                  />
                </div>
              </>
            )}
          </div>
        )}
      </div>
    );
  }
  ExtensionConfigSection.displayName = `ExtensionConfigSection(${opts.assetType})`;
  return ExtensionConfigSection;
}

function parseConfig(raw?: string): Record<string, unknown> {
  if (!raw) return {};
  try {
    return JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return {};
  }
}
