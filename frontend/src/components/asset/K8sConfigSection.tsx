import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff } from "lucide-react";
import { Button, Textarea } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { buildK8sConfig, parseK8sConfig, K8S_DEFAULTS, type K8sFormState } from "./K8sConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const K8sConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function K8sConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const isEditing = !!editAsset;
  const kubeconfigPlaceholder = isEditing
    ? t("asset.k8sKubeconfigEditPlaceholder")
    : t("asset.k8sKubeconfigPlaceholder");

  const { state, patch } = useConfigSection<K8sFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseK8sConfig(a.Config ?? "", a.sshTunnelId || 0) : { ...K8S_DEFAULTS }),
    validate: (s) => {
      // kubeconfig 新建必填;编辑态空也可保存(保全旧 saveDisabledReason 逻辑)。
      const canSave = isEditing || !!s.kubeconfig.trim();
      return { canTest: false, canSave, saveDisabledReason: canSave ? "" : "asset.formMissingKubeconfig" };
    },
    build: async (s, ctx) => {
      let ciphertext = "";
      if (s.kubeconfig) {
        // 用户输入了新的 kubeconfig（明文 YAML），加密后落库；失败抛出由 handleSubmit catch 处理。
        ciphertext = await ctx.encryptPassword(s.kubeconfig);
      } else if (editAsset) {
        // 编辑模式且未输入新值：保留原 ciphertext。
        try {
          const old = JSON.parse(editAsset.Config || "{}") as { kubeconfig?: string };
          if (old.kubeconfig) ciphertext = old.kubeconfig;
        } catch {
          // 旧 config 解析失败：让 ciphertext 缺失冒到后端校验
        }
      }
      return {
        configJSON: buildK8sConfig(s, ciphertext, await resolveSaveProxyPassword(s, ctx.encryptPassword)),
        sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
      };
    },
    deps: [editAsset],
  });

  const K8S_GROUPS: ConfigGroupSchema<K8sFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "custom",
          render: (s, p) => (
            <Field label={t("asset.k8sKubeconfig")} required={!isEditing}>
              {s.showKubeconfig ? (
                <div className="relative min-w-0 overflow-hidden">
                  <Textarea
                    value={s.kubeconfig}
                    onChange={(e) => p({ kubeconfig: e.target.value })}
                    placeholder={kubeconfigPlaceholder}
                    rows={4}
                    className="font-mono text-xs pr-9 whitespace-pre-wrap break-all"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="absolute right-1 top-2 h-7 w-7"
                    onClick={() => p({ showKubeconfig: false })}
                  >
                    <EyeOff className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ) : (
                <Button type="button" variant="outline" className="w-full" onClick={() => p({ showKubeconfig: true })}>
                  <Eye className="h-3.5 w-3.5 mr-1" />
                  {isEditing ? t("asset.k8sRevealKubeconfig") : t("asset.k8sEnterKubeconfig")}
                </Button>
              )}
            </Field>
          ),
        },
        { kind: "text", key: "namespace", label: "asset.k8sNamespace", placeholder: "default" },
        { kind: "text", key: "context", label: "asset.k8sContext", placeholder: "current context" },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  ];

  const groups = buildConfigGroups(K8S_GROUPS, { state, patch, ctx: { editAsset } });
  return <ConfigTabs groups={groups} />;
});
