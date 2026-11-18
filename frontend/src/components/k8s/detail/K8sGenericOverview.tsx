import { useTranslation } from "react-i18next";
import { K8sSectionCard } from "../K8sSectionCard";
import { K8sMetadataGrid } from "../K8sMetadataGrid";
import { K8sMetaSections } from "./K8sMetaSections";
import type { K8sDetailProps } from "./registry";

interface Metadata {
  uid?: string;
  resourceVersion?: string;
  creationTimestamp?: string;
}

/** Overview for kinds without a dedicated one: metadata, `status.conditions` if present, labels and annotations. */
export function K8sGenericOverview({ namespace, name, object }: K8sDetailProps) {
  const { t } = useTranslation();
  const meta = (object.metadata ?? {}) as Metadata;

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
      <K8sMetaSections object={object} />
    </div>
  );
}
