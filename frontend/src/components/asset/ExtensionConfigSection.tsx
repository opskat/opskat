import { useEffect } from "react";
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
}

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
    const { state, setState } = useConfigSection<ExtensionFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => ({ config: parseConfig(a?.Config) }),
      // 必填校验由后端按 configSchema.required 负责；表单侧不复制一份会漂移的规则。
      validate: () => ({ canTest: false, canSave: true }),
      build: async (s, buildCtx) => ({
        configJSON: JSON.stringify(await encryptSecrets(s.config, buildCtx)),
        sshTunnelId: 0, // 扩展资产的网络路径由扩展自己经宿主接口决定。
      }),
    });

    // 编辑态：把密文字段换成后端解密后的值，用户才能看到自己填过什么。解密失败时退回
    // 资产上的原始配置——那正是解密前的样子，比一张空表单诚实。
    const editID = editAsset?.ID;
    const rawConfig = editAsset?.Config;
    useEffect(() => {
      if (!editID) return;
      let cancelled = false;
      GetDecryptedExtensionConfig(editID, opts.extensionName)
        .then((cfg) => {
          if (!cancelled) setState({ config: parseConfig(cfg) });
        })
        .catch(() => {
          if (!cancelled) setState({ config: parseConfig(rawConfig) });
        });
      return () => {
        cancelled = true;
      };
    }, [editID, rawConfig, setState]);

    if (!opts.schema?.properties) return null;
    return (
      <ExtensionConfigForm
        extensionName={opts.extensionName}
        configSchema={opts.schema}
        value={state.config}
        onChange={(config) => setState({ config })}
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
