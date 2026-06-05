import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff } from "lucide-react";
import { Button, Input, Label, Textarea } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { buildK8sConfig, parseK8sConfig, K8S_DEFAULTS, type K8sFormState } from "./K8sConfigSection.config";

export const K8sConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function K8sConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<K8sFormState>(() => {
    if (!editAsset) return { ...K8S_DEFAULTS };
    const { namespace, context } = parseK8sConfig(editAsset.Config ?? "");
    return {
      kubeconfig: "",
      showKubeconfig: false,
      namespace,
      context,
      sshTunnelId: editAsset.sshTunnelId || 0,
    };
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

  return (
    <div className="grid gap-3 border rounded-lg p-4">
      <div className="grid gap-2">
        <Label>{t("asset.k8sKubeconfig")}</Label>
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
          <Button type="button" variant="outline" className="w-full" onClick={() => patch({ showKubeconfig: true })}>
            <Eye className="h-3.5 w-3.5 mr-1" />
            {isEditing ? t("asset.k8sRevealKubeconfig") : t("asset.k8sEnterKubeconfig")}
          </Button>
        )}
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.k8sNamespace")}</Label>
        <Input value={state.namespace} onChange={(e) => patch({ namespace: e.target.value })} placeholder="default" />
      </div>
      <div className="grid gap-2">
        <Label>{t("asset.k8sContext")}</Label>
        <Input
          value={state.context}
          onChange={(e) => patch({ context: e.target.value })}
          placeholder="current context"
        />
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
    </div>
  );
});
