import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "@opskat/ui";
import { useK8sStore } from "@/stores/k8sStore";
import { SetK8sNodeUnschedulable } from "../../../../wailsjs/go/k8s/K8s";
import type { K8sActionContext } from "../actions";
import { runK8sOp } from "./opTarget";
import { triggerCronJob } from "./cronOps";
import { isNodeUnschedulable, targetOf } from "./schedulingState";

const NODES = "/nodes";

export function K8sTriggerDialog({ ctx }: { ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const closeOp = useK8sStore((s) => s.closeOp);
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && closeOp(ctx.assetId)}
      variant="default"
      title={t("asset.k8sCronTriggerTitle", { name: ctx.name })}
      description={<p className="text-sm">{t("asset.k8sCronTriggerDesc")}</p>}
      cancelText={t("action.cancel")}
      confirmText={t("asset.k8sCronTriggerConfirm")}
      onConfirm={() => {
        closeOp(ctx.assetId);
        void triggerCronJob(ctx);
      }}
    />
  );
}

export function K8sCordonDialog({ ctx }: { ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const closeOp = useK8sStore((s) => s.closeOp);
  const found = targetOf(ctx);
  const uncordon = found ? isNodeUnschedulable(found.target) : false;
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && closeOp(ctx.assetId)}
      variant="default"
      title={t(uncordon ? "asset.k8sUncordonTitle" : "asset.k8sCordonTitle", { name: ctx.name })}
      description={<p className="text-sm">{t(uncordon ? "asset.k8sUncordonDesc" : "asset.k8sCordonDesc")}</p>}
      cancelText={t("action.cancel")}
      confirmText={t(uncordon ? "asset.k8sUncordonConfirm" : "asset.k8sCordonConfirm")}
      onConfirm={() => {
        closeOp(ctx.assetId);
        void runK8sOp(
          () => SetK8sNodeUnschedulable({ assetId: ctx.assetId, name: ctx.name, unschedulable: !uncordon }),
          t(uncordon ? "asset.k8sUncordoned" : "asset.k8sCordoned", { name: ctx.name }),
          () => void useK8sStore.getState().refreshType(ctx.assetId, NODES)
        );
      }}
    />
  );
}
