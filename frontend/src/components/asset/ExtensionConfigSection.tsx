import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Input, Label, Switch } from "@opskat/ui";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { Field, Segmented } from "@/components/asset/fields";
import { useConfigSection } from "@/components/asset/useConfigSection";
import {
  CONNECTION_DEFAULTS,
  buildProxyChainJSON,
  parseConnectionFields,
  proxyChainValidationKey,
  resolveSaveProxyChainSecrets,
  type ConnectionFormFields,
} from "@/components/asset/proxyConfig";
import { GetDecryptedExtensionConfig } from "../../../wailsjs/go/extension/Extension";
import type { AssetFormContext, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { passwordFields, type ExtensionConfigSchema } from "@/extension/configSchema";
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
  hasBackend: boolean;
  /** 资产类型在 describe() 声明的宿主连接配置；只渲染、保存声明了的项。 */
  connection?: ExtConnection;
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

  async function encryptSecrets(config: Record<string, unknown>, ctx: AssetFormContext) {
    const out = { ...config };
    for (const field of secrets) {
      const value = out[field];
      if (value === undefined || value === null || value === "") continue;
      out[field] = await ctx.encryptPassword(String(value));
    }
    return out;
  }

  function ExtensionConfigSection({ editAsset, onValidityChange, ref }: ConfigSectionProps) {
    const { t } = useTranslation();
    const { state, setState, patch } = useConfigSection<ExtensionFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => {
        const sshTunnelId = sshTunnel ? a?.sshTunnelId || 0 : 0;
        const tunnel = { tunnel: sshTunnelId > 0, sshTunnelId };
        if (a?.ID) {
          return { config: {}, status: "loading", ...tunnel, proxyChain: CONNECTION_DEFAULTS, tls: TLS_DEFAULTS };
        }
        const { guestConfig, proxyChain, tls } = splitHostConnection(parseConfig(a?.Config));
        return { config: guestConfig, status: "ready", ...tunnel, proxyChain, tls };
      },
      // 必填校验由后端按 configSchema.required 负责；表单侧不复制一份会漂移的规则。
      validate: (s) => {
        const missingTunnel = s.status === "ready" && s.tunnel && s.sshTunnelId === 0;
        const chainError = proxyChainEnabled ? proxyChainValidationKey(s.proxyChain.proxyChainLayers) : "";
        const canSave = s.status === "ready" && !missingTunnel && !chainError;
        return {
          canTest: false,
          canSave,
          saveDisabledReason: missingTunnel ? "asset.formMissingSSHTunnel" : chainError || STATUS_REASON[s.status],
        };
      },
      build: async (s, buildCtx) => {
        if (s.status !== "ready") throw new Error(t(STATUS_REASON[s.status]));
        const config = await encryptSecrets(s.config, buildCtx);

        const hostConnection: HostConnectionConfig = {};
        if (proxyChainEnabled) {
          const secretsByLayer = await resolveSaveProxyChainSecrets(
            s.proxyChain.proxyChainLayers,
            buildCtx.encryptPassword
          );
          const chainJSON = buildProxyChainJSON(s.proxyChain.proxyChainLayers, secretsByLayer);
          if (chainJSON) hostConnection.proxyChain = chainJSON;
        }
        if (tlsEnabled) {
          const tls = buildTLS(s.tls);
          if (tls) hostConnection.tls = tls;
        }
        if (Object.keys(hostConnection).length > 0) {
          config[HOST_CONNECTION_CONFIG_KEY] = hostConnection;
        }

        return {
          configJSON: JSON.stringify(config),
          // 未声明 sshTunnel 的类型不生效：宿主拨号时同样按声明忽略该列。
          sshTunnelId: s.tunnel ? s.sshTunnelId : 0,
        };
      },
    });

    // 编辑态：把密文字段换成后端解密后的值，用户才能看到自己填过什么。解密失败不能退回
    // 资产上的原始配置——密码框里会是密文，保存时再加密一次就把真实密钥毁了。
    const editID = editAsset?.ID;
    useEffect(() => {
      if (!editID) return;
      let cancelled = false;
      GetDecryptedExtensionConfig(editID, opts.extensionName)
        .then((cfg) => {
          if (cancelled) return;
          const { guestConfig, proxyChain, tls } = splitHostConnection(parseConfig(cfg));
          setState((s) => ({ ...s, config: guestConfig, status: "ready", proxyChain, tls }));
        })
        .catch((err) => {
          if (cancelled) return;
          setState((s) => ({ ...s, status: "error" }));
          toast.error(`${t("asset.extConfigDecryptFailed")}: ${String(err)}`);
        });
      return () => {
        cancelled = true;
      };
    }, [editID, setState, t]);

    // 未就绪时不渲染表单：原因已经由壳在保存按钮旁显示（saveDisabledReason），失败另有 toast。
    if (!opts.schema?.properties || state.status !== "ready") return null;
    return (
      <div className="flex flex-col gap-4">
        <ExtensionConfigForm
          extensionName={opts.extensionName}
          configSchema={opts.schema}
          value={state.config}
          onChange={(config) => patch({ config, status: "ready" })}
          hasBackend={opts.hasBackend}
        />
        {sshTunnel && (
          <Field label={t("asset.connectionType")}>
            <Segmented
              value={state.tunnel ? "tunnel" : "direct"}
              onChange={(v) => patch({ tunnel: v === "tunnel", sshTunnelId: 0 })}
              aria-label={t("asset.connectionType")}
              options={[
                { value: "direct", label: t("asset.connectionDirect") },
                { value: "tunnel", label: t("asset.sshTunnel") },
              ]}
            />
            {state.tunnel && (
              <AssetSelect
                value={state.sshTunnelId}
                onValueChange={(sshTunnelId) => patch({ sshTunnelId })}
                filterType="ssh"
                placeholder={t("asset.jumpHostNone")}
                testId="extension-ssh-tunnel-select"
              />
            )}
          </Field>
        )}
        {proxyChainEnabled && (
          <ConnectionMethodFields
            value={state.proxyChain}
            onChange={(patchValue) => patch({ proxyChain: { ...state.proxyChain, ...patchValue } })}
            excludeIds={editAsset?.ID ? [editAsset.ID] : undefined}
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
