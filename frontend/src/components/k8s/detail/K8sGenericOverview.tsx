import { useTranslation } from "react-i18next";
import { K8sSectionCard } from "../K8sSectionCard";
import { K8sMetadataGrid } from "../K8sMetadataGrid";
import { K8sConditionList } from "../K8sConditionList";
import { K8sTagList } from "../K8sTagList";
import type { K8sDetailProps } from "./registry";

interface Metadata {
  uid?: string;
  resourceVersion?: string;
  creationTimestamp?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

/** Overview for kinds without a dedicated one: metadata, `status.conditions` if present, labels and annotations. */
export function K8sGenericOverview({ namespace, name, object }: K8sDetailProps) {
  const { t } = useTranslation();
  const meta = (object.metadata ?? {}) as Metadata;
  const conditions = (object.status as { conditions?: unknown } | undefined)?.conditions;

  return (
    <div className="space-y-4">
      <K8sSectionCard title={t("asset.k8sMetadata")}>
        <K8sMetadataGrid
          items={[
            { label: t("asset.k8sPodName"), value: name, mono: true },
            ...(namespace ? [{ label: t("asset.k8sNamespace"), value: namespace, mono: true }] : []),
            { label: "UID", value: meta.uid ?? "-", mono: true },
            { label: t("asset.k8sPodCreationTime"), value: meta.creationTimestamp ?? "-" },
            { label: "Resource version", value: meta.resourceVersion ?? "-", mono: true },
          ]}
        />
      </K8sSectionCard>
      {Array.isArray(conditions) && conditions.length > 0 && (
        <K8sConditionList
          conditions={conditions as { type: string; status: string; reason?: string; message?: string }[]}
          title={t("asset.k8sPodConditions")}
        />
      )}
      <K8sTagList tags={meta.labels ?? {}} title={t("asset.k8sPodLabels")} defaultCollapsed />
      <K8sTagList tags={meta.annotations ?? {}} title={t("asset.k8sAnnotations")} defaultCollapsed />
    </div>
  );
}
