import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff } from "lucide-react";
import { Button, Input, Textarea } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { Field } from "@/components/asset/fields";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { buildK8sConfig, parseK8sConfig, K8S_DEFAULTS, type K8sFormState } from "./K8sConfigSection.config";
import { ConfigTabs, type ConfigGroup } from "@/components/asset/ConfigTabs";

export const K8sConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function K8sConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<K8sFormState>(() => {
    if (!editAsset) return { ...K8S_DEFAULTS };
    return parseK8sConfig(editAsset.Config ?? "", editAsset.sshTunnelId || 0);
  });
  const patch = (p: Partial<K8sFormState>) => setState((s) => ({ ...s, ...p }));

  // kubeconfig は新規資産では必須;編集モードでは空でも保存可(旧 saveDisabledReason ロジックを保全)。
  useEffect(() => {
    const canSave = !!editAsset || !!state.kubeconfig.trim();
    onValidityChange({
      canTest: false,
      canSave,
      saveDisabledReason: canSave ? "" : "asset.formMissingKubeconfig",
    });
  }, [state.kubeconfig, editAsset, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildTestConfig: null,
      buildConfig: async (ctx) => {
        let ciphertext = "";
        if (state.kubeconfig) {
          // 用户输入了新的 kubeconfig（明文 YAML），加密后落库。
          // 失败抛出异常，由 handleSubmit 的 catch 处理（等价于旧 toast+return 流程）。
          ciphertext = await ctx.encryptPassword(state.kubeconfig);
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
          configJSON: buildK8sConfig(state, ciphertext),
          sshTunnelId: state.sshTunnelId,
        };
      },
    }),
    [state, editAsset]
  );

  const isEditing = !!editAsset;
  const placeholder = isEditing ? t("asset.k8sKubeconfigEditPlaceholder") : t("asset.k8sKubeconfigPlaceholder");

  const groups: ConfigGroup[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      render: () => (
        <div className="flex flex-col gap-4">
          <Field label={t("asset.k8sKubeconfig")} required={!isEditing}>
            {state.showKubeconfig ? (
              <div className="relative min-w-0 overflow-hidden">
                <Textarea
                  value={state.kubeconfig}
                  onChange={(e) => patch({ kubeconfig: e.target.value })}
                  placeholder={placeholder}
                  rows={4}
                  className="font-mono text-xs pr-9 whitespace-pre-wrap break-all"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="absolute right-1 top-2 h-7 w-7"
                  onClick={() => patch({ showKubeconfig: false })}
                >
                  <EyeOff className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : (
              <Button
                type="button"
                variant="outline"
                className="w-full"
                onClick={() => patch({ showKubeconfig: true })}
              >
                <Eye className="h-3.5 w-3.5 mr-1" />
                {isEditing ? t("asset.k8sRevealKubeconfig") : t("asset.k8sEnterKubeconfig")}
              </Button>
            )}
          </Field>
          <Field label={t("asset.k8sNamespace")}>
            <Input
              value={state.namespace}
              onChange={(e) => patch({ namespace: e.target.value })}
              placeholder="default"
            />
          </Field>
          <Field label={t("asset.k8sContext")}>
            <Input
              value={state.context}
              onChange={(e) => patch({ context: e.target.value })}
              placeholder="current context"
            />
          </Field>
        </div>
      ),
    },
    {
      key: "tunnel",
      label: "asset.tabTunnel",
      render: () => (
        <Field label={t("asset.sshTunnel")}>
          <AssetSelect
            value={state.sshTunnelId}
            onValueChange={(v) => patch({ sshTunnelId: v })}
            filterType="ssh"
            placeholder={t("asset.sshTunnelNone")}
          />
        </Field>
      ),
    },
  ];

  return <ConfigTabs groups={groups} />;
});
