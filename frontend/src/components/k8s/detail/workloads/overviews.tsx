import { useTranslation } from "react-i18next";
import { K8sTableSection } from "../../K8sTableSection";
import { resourceTabId, useK8sStore } from "@/stores/k8sStore";
import type { K8sDetailProps } from "../registry";
import { useListed } from "./useListed";
import { WorkloadSummary, type PodTemplate } from "./WorkloadSummary";
import type { LabelSelector } from "./selector";
import { ratio, selectorItem, type SummaryItem } from "./summaryItems";

type Obj = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any
const specOf = (o: Record<string, unknown>) => (o.spec ?? {}) as Obj;
const statusOf = (o: Record<string, unknown>) => (o.status ?? {}) as Obj;

/** The HPA of this workload (same namespace, `scaleTargetRef` pointing at it), as `name · min–max`; null when none. */
function useRelatedHpa({ assetId, namespace, name, object }: K8sDetailProps): SummaryItem | null {
  const { t } = useTranslation();
  const listed = useListed(assetId, `hpa:${namespace}:${object.kind}:${name}`, {
    group: "autoscaling",
    version: "v2",
    resource: "horizontalpodautoscalers",
    namespace,
    labelSelector: "",
  });
  const label = t("asset.k8sHpa");
  if (listed.status === "error") return { label, value: "-", mono: true };
  const hpa = listed.items.find((h) => {
    const ref = specOf(h.object).scaleTargetRef as { kind?: string; name?: string } | undefined;
    return ref?.kind === object.kind && ref?.name === name;
  });
  if (!hpa) return null;
  const spec = specOf(hpa.object);
  return { label, value: `${hpa.name} · ${spec.minReplicas ?? 1}–${spec.maxReplicas}`, mono: true };
}

const strategyText = (strategy: Obj | undefined) => {
  if (!strategy?.type) return "-";
  const ru = strategy.rollingUpdate as Obj | undefined;
  return ru?.maxSurge !== undefined || ru?.maxUnavailable !== undefined
    ? `${strategy.type} (${ru.maxUnavailable ?? "-"} / ${ru.maxSurge ?? "-"})`
    : strategy.type;
};

const num = (n: unknown) => String(n ?? 0);

export function DeploymentOverview(props: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(props.object);
  const status = statusOf(props.object);
  const hpa = useRelatedHpa(props);
  return (
    <WorkloadSummary
      object={props.object}
      template={spec.template as PodTemplate}
      items={[
        { label: t("asset.k8sReplicas"), value: ratio(status.readyReplicas, spec.replicas), mono: true },
        { label: t("asset.k8sUpdated"), value: num(status.updatedReplicas), mono: true },
        { label: t("asset.k8sAvailable"), value: num(status.availableReplicas), mono: true },
        { label: t("asset.k8sStrategy"), value: strategyText(spec.strategy), mono: true },
        ...selectorItem(t("asset.k8sSelector"), spec.selector as LabelSelector),
        ...(hpa ? [hpa] : []),
      ]}
    />
  );
}

export function StatefulSetOverview(props: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(props.object);
  const status = statusOf(props.object);
  const hpa = useRelatedHpa(props);
  return (
    <WorkloadSummary
      object={props.object}
      template={spec.template as PodTemplate}
      items={[
        { label: t("asset.k8sReplicas"), value: ratio(status.readyReplicas, spec.replicas), mono: true },
        { label: t("asset.k8sUpdated"), value: num(status.updatedReplicas), mono: true },
        { label: t("asset.k8sStrategy"), value: spec.updateStrategy?.type ?? "-", mono: true },
        { label: t("asset.k8sServiceName"), value: spec.serviceName ?? "-", mono: true },
        ...selectorItem(t("asset.k8sSelector"), spec.selector as LabelSelector),
        ...(hpa ? [hpa] : []),
      ]}
    />
  );
}

export function DaemonSetOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const status = statusOf(object);
  return (
    <WorkloadSummary
      object={object}
      template={spec.template as PodTemplate}
      items={[
        { label: t("asset.k8sReplicas"), value: ratio(status.numberReady, status.desiredNumberScheduled), mono: true },
        { label: t("asset.k8sUpdated"), value: num(status.updatedNumberScheduled), mono: true },
        { label: t("asset.k8sAvailable"), value: num(status.numberAvailable), mono: true },
        { label: t("asset.k8sStrategy"), value: strategyText(spec.updateStrategy), mono: true },
        ...selectorItem(t("asset.k8sSelector"), spec.selector as LabelSelector),
      ]}
    />
  );
}

export function ReplicaSetOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const status = statusOf(object);
  return (
    <WorkloadSummary
      object={object}
      template={spec.template as PodTemplate}
      items={[
        { label: t("asset.k8sReplicas"), value: ratio(status.readyReplicas, spec.replicas), mono: true },
        { label: t("asset.k8sAvailable"), value: num(status.availableReplicas), mono: true },
        ...selectorItem(t("asset.k8sSelector"), spec.selector as LabelSelector),
      ]}
    />
  );
}

export function JobOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const status = statusOf(object);
  return (
    <WorkloadSummary
      object={object}
      template={spec.template as PodTemplate}
      items={[
        { label: t("asset.k8sCompletions"), value: ratio(status.succeeded, spec.completions ?? 1), mono: true },
        { label: t("asset.k8sParallelism"), value: num(spec.parallelism ?? 1), mono: true },
        { label: t("asset.k8sBackoffLimit"), value: num(spec.backoffLimit ?? 6), mono: true },
        { label: t("asset.k8sActive"), value: num(status.active), mono: true },
        { label: t("asset.k8sFailed"), value: num(status.failed), mono: true },
        ...selectorItem(t("asset.k8sSelector"), spec.selector as LabelSelector),
      ]}
    />
  );
}

/** The Jobs a CronJob created: Jobs of its namespace whose owner reference names it. */
function CronJobJobs({ assetId, typeKey, namespace, name }: K8sDetailProps) {
  const { t } = useTranslation();
  const openTab = useK8sStore((s) => s.openTab);
  const listed = useListed(assetId, `cronjob-jobs:${namespace}:${name}`, {
    group: "batch",
    version: "v1",
    resource: "jobs",
    namespace,
    labelSelector: "",
  });
  const jobs = listed.items.filter((j) =>
    ((j.object.metadata as Obj).ownerReferences ?? []).some((o: Obj) => o.kind === "CronJob" && o.name === name)
  );
  const jobsType = typeKey.replace(/cronjobs$/, "jobs");
  return (
    <K8sTableSection
      title={t("asset.k8sJobs")}
      columns={[
        { key: "name", label: t("asset.k8sPodName") },
        { key: "done", label: t("asset.k8sCompletions") },
        { key: "time", label: t("asset.k8sPodCreationTime") },
      ]}
      data={jobs}
      emptyText={t("asset.k8sNone")}
      renderRow={(j) => {
        const st = statusOf(j.object);
        const sp = specOf(j.object);
        return (
          <tr key={j.name} className="border-b last:border-0">
            <td className="py-2 pr-4 font-mono text-sm">
              <button
                type="button"
                role="link"
                className="text-primary hover:underline"
                onClick={() => openTab(assetId, { id: resourceTabId(jobsType, namespace, j.name), label: j.name })}
              >
                {j.name}
              </button>
            </td>
            <td className="py-2 pr-4 font-mono text-xs">{ratio(st.succeeded, sp.completions ?? 1)}</td>
            <td className="py-2 text-xs text-muted-foreground">{(j.object.metadata as Obj).creationTimestamp}</td>
          </tr>
        );
      }}
    />
  );
}

export function CronJobOverview(props: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(props.object);
  const status = statusOf(props.object);
  return (
    <WorkloadSummary
      object={props.object}
      template={spec.jobTemplate?.spec?.template as PodTemplate}
      items={[
        { label: t("asset.k8sSchedule"), value: spec.schedule ?? "-", mono: true },
        { label: t("asset.k8sSuspend"), value: spec.suspend ? t("asset.k8sSuspended") : t("asset.k8sNotSuspended") },
        { label: t("asset.k8sConcurrencyPolicy"), value: spec.concurrencyPolicy ?? "Allow", mono: true },
        { label: t("asset.k8sLastSchedule"), value: status.lastScheduleTime ?? "-" },
        { label: t("asset.k8sActive"), value: num(status.active?.length), mono: true },
      ]}
      extra={<CronJobJobs {...props} />}
    />
  );
}
