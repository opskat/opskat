import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { K8sSectionCard } from "../../K8sSectionCard";
import { K8sMetadataGrid } from "../../K8sMetadataGrid";
import { K8sTableSection } from "../../K8sTableSection";
import { K8sMetaSections } from "../K8sMetaSections";
import type { SummaryItem } from "./summaryItems";

interface ContainerSpec {
  name: string;
  image?: string;
  ports?: { containerPort: number; name?: string; protocol?: string }[];
  resources?: { requests?: Record<string, string>; limits?: Record<string, string> };
}

export interface PodTemplate {
  spec?: { containers?: ContainerSpec[]; nodeSelector?: Record<string, string> };
}

const portText = (p: { containerPort: number; name?: string; protocol?: string }) =>
  `${p.containerPort}/${p.name ?? p.protocol ?? "TCP"}`;
const cpuMem = (r: Record<string, string> | undefined) =>
  r?.cpu || r?.memory ? `${r.cpu ?? "-"} / ${r.memory ?? "-"}` : "-";

/** Summary card + the pod template's containers (image / ports / requests / limits) + conditions, labels, annotations. */
export function WorkloadSummary({
  object,
  items,
  template,
  extra,
}: {
  object: Record<string, unknown>;
  items: SummaryItem[];
  template: PodTemplate | undefined;
  extra?: ReactNode;
}) {
  const { t } = useTranslation();
  const created = (object.metadata as { creationTimestamp?: string } | undefined)?.creationTimestamp;
  const nodeSelector = Object.entries(template?.spec?.nodeSelector ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join(",");
  const containers = template?.spec?.containers ?? [];
  return (
    <div data-testid="k8s-overview" className="space-y-4">
      <K8sSectionCard>
        <K8sMetadataGrid
          items={[
            ...items,
            ...(nodeSelector ? [{ label: t("asset.k8sNodeSelector"), value: nodeSelector, mono: true }] : []),
            { label: t("asset.k8sPodCreationTime"), value: created ?? "-" },
          ]}
        />
      </K8sSectionCard>
      <K8sTableSection
        title={t("asset.k8sPodContainers")}
        columns={[
          { key: "name", label: t("asset.k8sPodName") },
          { key: "image", label: "Image" },
          { key: "ports", label: t("asset.k8sPorts") },
          { key: "requests", label: "Requests" },
          { key: "limits", label: "Limits" },
        ]}
        data={containers}
        emptyText={t("asset.k8sNone")}
        renderRow={(c) => (
          <tr key={c.name} className="border-b last:border-0">
            <td className="py-2 pr-4 font-mono text-sm">{c.name}</td>
            <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{c.image}</td>
            <td className="py-2 pr-4 font-mono text-xs">{c.ports?.map(portText).join(", ") || "-"}</td>
            <td className="py-2 pr-4 font-mono text-xs">{cpuMem(c.resources?.requests)}</td>
            <td className="py-2 pr-4 font-mono text-xs">{cpuMem(c.resources?.limits)}</td>
          </tr>
        )}
      />
      {extra}
      <K8sMetaSections object={object} />
    </div>
  );
}
