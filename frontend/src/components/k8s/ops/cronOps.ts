import i18n from "@/i18n";
import { resourceTabId, useK8sStore } from "@/stores/k8sStore";
import { SetK8sCronJobSuspend, TriggerK8sCronJob } from "../../../../wailsjs/go/k8s/K8s";
import type { K8sActionContext } from "../actions";
import { runK8sOp } from "./opTarget";

const JOBS = "batch/jobs";
const CRONJOBS = "batch/cronjobs";

/** Creates a Job from the CronJob's template; the toast links to the new Job. */
export function triggerCronJob(ctx: K8sActionContext) {
  const store = useK8sStore.getState();
  return runK8sOp(
    async () => {
      const raw = await TriggerK8sCronJob({ assetId: ctx.assetId, namespace: ctx.namespace, name: ctx.name });
      return (JSON.parse(raw as string) as { jobName: string }).jobName;
    },
    (jobName) => ({
      message: i18n.t("asset.k8sCronTriggered", { name: jobName }),
      action: {
        label: i18n.t("asset.k8sCronOpenJob"),
        onClick: () => store.openTab(ctx.assetId, { id: resourceTabId(JOBS, ctx.namespace, jobName), label: jobName }),
      },
    }),
    () => {
      void store.refreshType(ctx.assetId, JOBS);
      void store.refreshType(ctx.assetId, CRONJOBS);
    }
  );
}

/** Toggles a CronJob's suspend flag straight away (reversible, so no confirmation). */
export function setCronJobSuspend(ctx: K8sActionContext, suspend: boolean) {
  return runK8sOp(
    () => SetK8sCronJobSuspend({ assetId: ctx.assetId, namespace: ctx.namespace, name: ctx.name, suspend }),
    i18n.t(suspend ? "asset.k8sCronSuspended" : "asset.k8sCronResumed", { name: ctx.name }),
    () => void useK8sStore.getState().refreshType(ctx.assetId, CRONJOBS)
  );
}
