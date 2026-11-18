import { useTranslation } from "react-i18next";
import { ScrollText } from "lucide-react";
import { Button } from "@opskat/ui";
import { getK8sActions } from "../../actions";
import { K8sSectionCard } from "../../K8sSectionCard";
import { K8sMetadataGrid } from "../../K8sMetadataGrid";
import { K8sTableSection } from "../../K8sTableSection";
import { statusVariantToClass } from "../../utils";
import { K8sMetaSections } from "../K8sMetaSections";
import type { K8sDetailProps } from "../registry";
import { openResourceSubTab } from "../workloads/openSubTab";
import { cpuMem, portsText } from "../workloads/summaryItems";
import { podContainers, podReady } from "./podInfo";

interface PodShape {
  metadata?: { creationTimestamp?: string };
  spec?: { nodeName?: string; serviceAccountName?: string };
  status?: { podIP?: string; hostIP?: string; qosClass?: string };
}

export function PodOverview({ assetId, typeKey, namespace, name, object }: K8sDetailProps) {
  const { t } = useTranslation();
  const { metadata, spec, status } = object as PodShape;
  const ctx = { assetId, typeKey, namespace, name, object };
  const terminal = getK8sActions(typeKey, object).find((a) => a.id === "terminal");
  const terminalDisabled = terminal?.disabledReason?.(object);
  const containers = podContainers(object);

  return (
    <div data-testid="k8s-overview" className="space-y-4">
      <K8sSectionCard>
        <K8sMetadataGrid
          items={[
            { label: t("asset.k8sPodIP"), value: status?.podIP || "-", mono: true },
            { label: t("asset.k8sPodHostIP"), value: status?.hostIP || "-", mono: true },
            { label: t("asset.k8sPodNode"), value: spec?.nodeName || "-", mono: true },
            { label: t("asset.k8sPodReady"), value: podReady(object), mono: true },
            { label: t("asset.k8sPodQosClass"), value: status?.qosClass || "-" },
            { label: "ServiceAccount", value: spec?.serviceAccountName || "-", mono: true },
            { label: t("asset.k8sPodCreationTime"), value: metadata?.creationTimestamp ?? "-" },
          ]}
        />
      </K8sSectionCard>
      <K8sTableSection
        title={t("asset.k8sPodContainers")}
        columns={[
          { key: "name", label: t("asset.k8sPodName") },
          { key: "image", label: "Image" },
          { key: "state", label: t("asset.k8sPodStatus") },
          { key: "ready", label: t("asset.k8sPodReady") },
          { key: "restarts", label: t("asset.k8sPodRestarts") },
          { key: "ports", label: t("asset.k8sPorts") },
          { key: "requests", label: "Requests" },
          { key: "limits", label: "Limits" },
          { key: "actions", label: "" },
        ]}
        data={containers}
        emptyText={t("asset.k8sNone")}
        renderRow={(c) => (
          <tr key={`${c.init ? "init:" : ""}${c.name}`} className="border-b last:border-0">
            <td className="py-2 pr-4 font-mono text-sm">{c.init ? `init: ${c.name}` : c.name}</td>
            <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{c.image}</td>
            <td className="py-2 pr-4">
              <span className={`rounded-full px-1.5 py-0.5 text-xs ${statusVariantToClass(c.stateVariant)}`}>
                {c.state}
              </span>
            </td>
            <td className="py-2 pr-4">
              <span className={c.ready ? "text-success" : "text-destructive"}>{c.ready ? "✓" : "✗"}</span>
            </td>
            <td className="py-2 pr-4 font-mono text-sm">{c.restarts}</td>
            <td className="py-2 pr-4 font-mono text-xs">{portsText(c.ports)}</td>
            <td className="py-2 pr-4 font-mono text-xs">{cpuMem(c.resources?.requests)}</td>
            <td className="py-2 pr-4 font-mono text-xs">{cpuMem(c.resources?.limits)}</td>
            <td className="py-1 text-right">
              {!c.init && (
                <>
                  {terminal && (
                    // The wrapper carries the reason: a disabled button gets no hover.
                    <span title={terminalDisabled}>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-6 gap-1 px-1.5 text-xs"
                        disabled={!!terminalDisabled}
                        onClick={() => void terminal.run({ ...ctx, container: c.name })}
                      >
                        <terminal.icon className="h-3.5 w-3.5" />
                        {t(terminal.labelKey)}
                      </Button>
                    </span>
                  )}
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 gap-1 px-1.5 text-xs"
                    onClick={() => openResourceSubTab(ctx, "logs", c.name)}
                  >
                    <ScrollText className="h-3.5 w-3.5" />
                    {t("asset.k8sLogs")}
                  </Button>
                </>
              )}
            </td>
          </tr>
        )}
      />
      <K8sMetaSections object={object} conditionsCollapsed />
    </div>
  );
}
