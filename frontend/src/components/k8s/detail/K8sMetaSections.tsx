import { useTranslation } from "react-i18next";
import { K8sConditionList } from "../K8sConditionList";
import { K8sTagList } from "../K8sTagList";

interface Condition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

/** `status.conditions` (when present), labels and annotations — the tail every overview ends with. */
export function K8sMetaSections({
  object,
  conditionsCollapsed,
}: {
  object: Record<string, unknown>;
  conditionsCollapsed?: boolean;
}) {
  const { t } = useTranslation();
  const meta = (object.metadata ?? {}) as { labels?: Record<string, string>; annotations?: Record<string, string> };
  const conditions = (object.status as { conditions?: unknown } | undefined)?.conditions;
  return (
    <>
      {Array.isArray(conditions) && conditions.length > 0 && (
        <K8sConditionList
          conditions={conditions as Condition[]}
          title={t("asset.k8sPodConditions")}
          defaultCollapsed={conditionsCollapsed}
        />
      )}
      <K8sTagList tags={meta.labels ?? {}} title={t("asset.k8sPodLabels")} defaultCollapsed />
      <K8sTagList tags={meta.annotations ?? {}} title={t("asset.k8sAnnotations")} defaultCollapsed />
    </>
  );
}
