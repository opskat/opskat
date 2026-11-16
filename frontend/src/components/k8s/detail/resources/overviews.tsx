import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { K8sCodeBlock } from "../../K8sCodeBlock";
import { K8sMetadataGrid } from "../../K8sMetadataGrid";
import { K8sSectionCard } from "../../K8sSectionCard";
import { K8sTableSection } from "../../K8sTableSection";
import { K8sMetaSections } from "../K8sMetaSections";
import { deriveStatus } from "../status";
import type { K8sDetailProps } from "../registry";
import { selectorString, type LabelSelector } from "../workloads/selector";
import { type SummaryItem } from "../workloads/summaryItems";

type Obj = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any
const specOf = (o: Record<string, unknown>) => (o.spec ?? {}) as Obj;
const statusOf = (o: Record<string, unknown>) => (o.status ?? {}) as Obj;
const dash = (v: unknown) => (v === undefined || v === null || v === "" ? "-" : String(v));
const joined = (list: unknown) => (Array.isArray(list) && list.length > 0 ? list.join(", ") : "-");

function Overview({
  object,
  items,
  children,
}: {
  object: Record<string, unknown>;
  items: SummaryItem[];
  children?: ReactNode;
}) {
  const { t } = useTranslation();
  const created = (object.metadata as { creationTimestamp?: string } | undefined)?.creationTimestamp;
  return (
    <div data-testid="k8s-overview" className="space-y-4">
      <K8sSectionCard>
        <K8sMetadataGrid items={[...items, { label: t("asset.k8sPodCreationTime"), value: created ?? "-" }]} />
      </K8sSectionCard>
      {children}
      <K8sMetaSections object={object} />
    </div>
  );
}

const cell = "py-2 pr-4 font-mono text-xs";

/** `status.loadBalancer.ingress` addresses as `ip, hostname, …`. */
const lbAddresses = (object: Record<string, unknown>) =>
  joined(((statusOf(object).loadBalancer?.ingress ?? []) as Obj[]).map((i) => i.ip ?? i.hostname));

export function ServiceOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const ports = (spec.ports ?? []) as Obj[];
  return (
    <Overview
      object={object}
      items={[
        { label: t("asset.k8sServiceType"), value: dash(spec.type), mono: true },
        { label: t("asset.k8sServiceClusterIP"), value: dash(spec.clusterIP), mono: true },
        {
          label: t("asset.k8sSelector"),
          value: selectorString({ matchLabels: spec.selector } as LabelSelector) || "-",
          mono: true,
        },
        { label: t("asset.k8sExternalIPs"), value: joined(spec.externalIPs), mono: true },
        { label: t("asset.k8sLoadBalancer"), value: lbAddresses(object), mono: true },
      ]}
    >
      <K8sTableSection
        title={t("asset.k8sServicePorts")}
        columns={[
          { key: "name", label: t("asset.k8sPodName") },
          { key: "port", label: t("asset.k8sServicePort") },
          { key: "target_port", label: t("asset.k8sServiceTargetPort") },
          { key: "node_port", label: "NodePort" },
          { key: "protocol", label: t("asset.k8sServiceProtocol") },
        ]}
        data={ports}
        emptyText={t("asset.k8sNone")}
        renderRow={(p, i) => (
          <tr key={i} className="border-b last:border-0">
            <td className={`${cell} text-muted-foreground`}>{dash(p.name)}</td>
            <td className={cell}>{dash(p.port)}</td>
            <td className={`${cell} text-muted-foreground`}>{dash(p.targetPort)}</td>
            <td className={`${cell} text-muted-foreground`}>{dash(p.nodePort)}</td>
            <td className="py-2 pr-4 text-xs">{dash(p.protocol)}</td>
          </tr>
        )}
      />
    </Overview>
  );
}

const backendText = (b: Obj | undefined) =>
  b?.service
    ? `${b.service.name}:${b.service.port?.number ?? b.service.port?.name ?? "-"}`
    : (b?.resource?.name ?? "-");

export function IngressOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const annotations = (object.metadata as Obj | undefined)?.annotations as Record<string, string> | undefined;
  const rules = ((spec.rules ?? []) as Obj[]).flatMap((r) =>
    ((r.http?.paths ?? []) as Obj[]).map((p) => ({
      host: r.host || "*",
      path: p.path || "/",
      backend: backendText(p.backend),
    }))
  );
  if (spec.defaultBackend) rules.push({ host: "*", path: "*", backend: backendText(spec.defaultBackend) });
  const tls = (spec.tls ?? []) as Obj[];
  return (
    <Overview
      object={object}
      items={[
        {
          label: t("asset.k8sIngressClass"),
          value: dash(spec.ingressClassName ?? annotations?.["kubernetes.io/ingress.class"]),
          mono: true,
        },
        { label: t("asset.k8sLoadBalancer"), value: lbAddresses(object), mono: true },
      ]}
    >
      <K8sTableSection
        title={t("asset.k8sIngressRules")}
        columns={[
          { key: "host", label: "Host" },
          { key: "path", label: "Path" },
          { key: "backend", label: t("asset.k8sIngressBackend") },
        ]}
        data={rules}
        emptyText={t("asset.k8sNone")}
        renderRow={(r, i) => (
          <tr key={i} className="border-b last:border-0">
            <td className={cell}>{r.host}</td>
            <td className={cell}>{r.path}</td>
            <td className={cell}>{r.backend}</td>
          </tr>
        )}
      />
      <K8sTableSection
        title="TLS"
        columns={[
          { key: "hosts", label: "Host" },
          { key: "secret", label: "Secret" },
        ]}
        data={tls}
        emptyText={t("asset.k8sNone")}
        renderRow={(tl, i) => (
          <tr key={i} className="border-b last:border-0">
            <td className={cell}>{joined(tl.hosts)}</td>
            <td className={cell}>{dash(tl.secretName)}</td>
          </tr>
        )}
      />
    </Overview>
  );
}

export function ConfigMapOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const entries = Object.entries((object.data ?? {}) as Record<string, string>);
  return (
    <Overview object={object} items={[{ label: t("asset.k8sDataKeys"), value: String(entries.length), mono: true }]}>
      <K8sSectionCard title={t("asset.k8sSecretData")}>
        {entries.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNone")}</p>
        ) : (
          <div className="space-y-3">
            {entries.map(([key, value]) => (
              <div key={key}>
                <div className="text-xs text-muted-foreground font-medium mb-1">{key}</div>
                <K8sCodeBlock code={value} maxHeight="max-h-64" />
              </div>
            ))}
          </div>
        )}
      </K8sSectionCard>
    </Overview>
  );
}

function decodeSecretValue(encoded: string) {
  try {
    return atob(encoded);
  } catch {
    return encoded;
  }
}

export function SecretOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const entries = Object.entries((object.data ?? {}) as Record<string, string>);
  return (
    <Overview object={object} items={[{ label: t("asset.k8sSecretType"), value: dash(object.type), mono: true }]}>
      <K8sSectionCard title={t("asset.k8sSecretData")}>
        {entries.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNone")}</p>
        ) : (
          <div className="space-y-3">
            {entries.map(([key, value]) => {
              const decoded = decodeSecretValue(value);
              return (
                <div key={key}>
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs text-muted-foreground font-medium">{key}</span>
                    <span className="text-[11px] text-muted-foreground">{decoded.length}B</span>
                  </div>
                  <K8sCodeBlock code={decoded} maxHeight="max-h-32" />
                </div>
              );
            })}
          </div>
        )}
      </K8sSectionCard>
    </Overview>
  );
}

const ROLE_PREFIX = "node-role.kubernetes.io/";

export function NodeOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const status = statusOf(object);
  const info = (status.nodeInfo ?? {}) as Obj;
  const labels = ((object.metadata as Obj | undefined)?.labels ?? {}) as Record<string, string>;
  const roles = Object.keys(labels)
    .filter((k) => k.startsWith(ROLE_PREFIX))
    .map((k) => k.slice(ROLE_PREFIX.length));
  const capacity = (status.capacity ?? {}) as Record<string, string>;
  const allocatable = (status.allocatable ?? {}) as Record<string, string>;
  const resourceRows = [
    { label: "CPU", key: "cpu" },
    { label: t("asset.k8sMemory"), key: "memory" },
    { label: "Pods", key: "pods" },
  ];
  const taints = (spec.taints ?? []) as Obj[];
  const addresses = (status.addresses ?? []) as Obj[];
  return (
    <Overview
      object={object}
      items={[
        { label: t("asset.k8sNodeStatus"), value: deriveStatus(object)?.text ?? "-" },
        { label: t("asset.k8sNodeRoles"), value: joined(roles), mono: true },
        {
          label: t("asset.k8sNodeScheduling"),
          value: spec.unschedulable ? t("asset.k8sNodeUnschedulable") : t("asset.k8sNodeSchedulable"),
        },
        { label: t("asset.k8sKubeletVersion"), value: dash(info.kubeletVersion), mono: true },
        { label: t("asset.k8sNodeOS"), value: dash(info.osImage) },
        {
          label: t("asset.k8sNodePlatform"),
          value:
            info.operatingSystem || info.architecture
              ? `${info.operatingSystem ?? "-"}/${info.architecture ?? "-"}`
              : "-",
          mono: true,
        },
        { label: t("asset.k8sKernelVersion"), value: dash(info.kernelVersion), mono: true },
        { label: t("asset.k8sContainerRuntime"), value: dash(info.containerRuntimeVersion), mono: true },
      ]}
    >
      <K8sTableSection
        title={t("asset.k8sNodeResources")}
        columns={[
          { key: "name", label: t("asset.k8sPodName") },
          { key: "capacity", label: t("asset.k8sCapacity") },
          { key: "allocatable", label: t("asset.k8sAllocatable") },
        ]}
        data={resourceRows}
        renderRow={(r) => (
          <tr key={r.key} className="border-b last:border-0">
            <td className="py-2 pr-4 text-xs">{r.label}</td>
            <td className={cell}>{dash(capacity[r.key])}</td>
            <td className={cell}>{dash(allocatable[r.key])}</td>
          </tr>
        )}
      />
      <K8sSectionCard title={t("asset.k8sAddresses")}>
        {addresses.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNone")}</p>
        ) : (
          <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
            {addresses.map((a, i) => (
              <div key={i} className="contents">
                <span className="text-muted-foreground">{a.type}</span>
                <span className="font-mono">{a.address}</span>
              </div>
            ))}
          </div>
        )}
      </K8sSectionCard>
      <K8sSectionCard title={t("asset.k8sTaints")}>
        {taints.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNone")}</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {taints.map((tn, i) => (
              <span
                key={i}
                className="inline-flex items-center rounded-md border bg-muted/50 px-2 py-0.5 text-xs font-mono"
              >
                {`${tn.key}${tn.value ? `=${tn.value}` : ""}:${tn.effect}`}
              </span>
            ))}
          </div>
        )}
      </K8sSectionCard>
    </Overview>
  );
}

export function PvcOverview({ object }: K8sDetailProps) {
  const { t } = useTranslation();
  const spec = specOf(object);
  const status = statusOf(object);
  return (
    <Overview
      object={object}
      items={[
        { label: t("asset.k8sPvcStatus"), value: dash(status.phase) },
        { label: t("asset.k8sPvcVolume"), value: dash(spec.volumeName), mono: true },
        {
          label: t("asset.k8sCapacity"),
          value: dash(status.capacity?.storage ?? spec.resources?.requests?.storage),
          mono: true,
        },
        { label: t("asset.k8sAccessModes"), value: joined(spec.accessModes), mono: true },
        { label: t("asset.k8sStorageClass"), value: dash(spec.storageClassName), mono: true },
        { label: t("asset.k8sVolumeMode"), value: dash(spec.volumeMode), mono: true },
      ]}
    />
  );
}
