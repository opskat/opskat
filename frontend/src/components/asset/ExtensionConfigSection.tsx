import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { GetDecryptedExtensionConfig } from "../../../wailsjs/go/extension/Extension";
import type { AssetFormContext, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { passwordFields, type ExtensionConfigSchema } from "@/extension/configSchema";

interface Options {
  extensionName: string;
  assetType: string;
  schema?: ExtensionConfigSchema;
  hasBackend: boolean;
}

interface ExtensionFormState {
  config: Record<string, unknown>;
  /** 编辑态要先拿到后端解密后的配置：资产上存的是密文，拿它回填/保存会把密文再加密一遍。 */
  status: "ready" | "loading" | "error";
}

const STATUS_REASON: Record<ExtensionFormState["status"], string> = {
  ready: "",
  loading: "asset.extConfigLoading",
  error: "asset.extConfigDecryptFailed",
};

/**
 * 扩展资产类型的表单区块，接进注册表的 ConfigSection 槽位。
 *
 * 内置类型每种有一个手写 ConfigSection；扩展类型的"手写区块"是它的 configSchema，所以
 * 这里把 ExtensionConfigForm 包成同一个契约（useConfigSection 的 state/校验/imperative
 * handle）。AssetForm 因此不再需要三段只对扩展生效的分支：回填解密配置、保存前加密
 * password 字段、渲染扩展表单，全部收进这里。
 */
export function makeExtensionConfigSection(opts: Options) {
  const secrets = passwordFields(opts.schema);

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
    const { state, setState } = useConfigSection<ExtensionFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => (a?.ID ? { config: {}, status: "loading" } : { config: parseConfig(a?.Config), status: "ready" }),
      // 必填校验由后端按 configSchema.required 负责；表单侧不复制一份会漂移的规则。
      validate: (s) => ({ canTest: false, canSave: s.status === "ready", saveDisabledReason: STATUS_REASON[s.status] }),
      build: async (s, buildCtx) => {
        if (s.status !== "ready") throw new Error(t(STATUS_REASON[s.status]));
        return {
          configJSON: JSON.stringify(await encryptSecrets(s.config, buildCtx)),
          sshTunnelId: 0, // 扩展资产的网络路径由扩展自己经宿主接口决定。
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
          if (!cancelled) setState({ config: parseConfig(cfg), status: "ready" });
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
      <ExtensionConfigForm
        extensionName={opts.extensionName}
        configSchema={opts.schema}
        value={state.config}
        onChange={(config) => setState({ config, status: "ready" })}
        hasBackend={opts.hasBackend}
      />
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
