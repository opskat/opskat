import { useTranslation } from "react-i18next";
import { K8sCodeBlock } from "../K8sCodeBlock";
import { K8sMetadataGrid } from "../K8sMetadataGrid";
import { K8sResourceHeader } from "../K8sResourceHeader";
import { K8sSectionCard } from "../K8sSectionCard";
import { K8sTableSection } from "../K8sTableSection";
import type { ConfigMapListItem, SecretListItem, ServiceListItem } from "./types";

function MissingResource({ message }: { message: string }) {
  return (
    <div className="flex items-center justify-center h-full">
      <span className="text-sm text-muted-foreground">{message}</span>
    </div>
  );
}

export function ServiceDetailView({ service }: { service?: ServiceListItem }) {
  const { t } = useTranslation();
  if (!service) return <MissingResource message={t("asset.k8sNoServices")} />;

  return (
    <div className="max-w-5xl mx-auto p-4 space-y-4">
      <K8sSectionCard>
        <K8sResourceHeader
          name={service.name}
          subtitle={service.namespace}
          status={{ text: service.type, variant: "info" }}
        />
        <K8sMetadataGrid
          items={[
            { label: t("asset.k8sServiceType"), value: service.type, mono: true },
            { label: t("asset.k8sServiceClusterIP"), value: service.cluster_ip || "-", mono: true },
            { label: t("asset.k8sPodAge"), value: service.age, mono: true },
          ]}
        />
      </K8sSectionCard>

      <K8sTableSection
        title={t("asset.k8sServicePorts")}
        columns={[
          { key: "name", label: t("asset.k8sPodName") },
          { key: "port", label: t("asset.k8sServicePort") },
          { key: "target_port", label: t("asset.k8sServiceTargetPort") },
          { key: "protocol", label: t("asset.k8sServiceProtocol") },
          { key: "node_port", label: "NodePort" },
        ]}
        data={service.ports}
        emptyText={t("asset.k8sNoEvents")}
        renderRow={(port, index) => (
          <tr key={index} className="border-b last:border-0">
            <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{port.name || "-"}</td>
            <td className="py-2 pr-4 font-mono text-sm">{port.port}</td>
            <td className="py-2 pr-4 font-mono text-xs text-muted-foreground">{port.target_port || "-"}</td>
            <td className="py-2 pr-4 text-xs">{port.protocol}</td>
            <td className="py-2 font-mono text-xs text-muted-foreground">{port.node_port || "-"}</td>
          </tr>
        )}
      />
    </div>
  );
}

export function ConfigMapDetailView({ configMap }: { configMap?: ConfigMapListItem }) {
  const { t } = useTranslation();
  if (!configMap) return <MissingResource message={t("asset.k8sNoConfigMaps")} />;

  const dataEntries = Object.entries(configMap.data || {});
  return (
    <div className="max-w-5xl mx-auto p-4 space-y-4">
      <K8sSectionCard>
        <K8sResourceHeader
          name={configMap.name}
          subtitle={configMap.namespace}
          status={{
            text: `${dataEntries.length} key${dataEntries.length !== 1 ? "s" : ""}`,
            variant: "neutral",
          }}
        />
        <K8sMetadataGrid items={[{ label: t("asset.k8sPodAge"), value: configMap.age, mono: true }]} />
      </K8sSectionCard>

      <K8sSectionCard title="Data">
        {dataEntries.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNoEvents")}</p>
        ) : (
          <div className="space-y-3">
            {dataEntries.map(([key, value]) => (
              <div key={key}>
                <div className="text-xs text-muted-foreground font-medium mb-1">{key}</div>
                <K8sCodeBlock code={value} maxHeight="max-h-64" />
              </div>
            ))}
          </div>
        )}
      </K8sSectionCard>
    </div>
  );
}

function decodeSecretValue(encoded: string) {
  try {
    return atob(encoded);
  } catch {
    return encoded;
  }
}

export function SecretDetailView({ secret }: { secret?: SecretListItem }) {
  const { t } = useTranslation();
  if (!secret) return <MissingResource message={t("asset.k8sNoSecrets")} />;

  const dataEntries = Object.entries(secret.data || {});
  return (
    <div className="max-w-5xl mx-auto p-4 space-y-4">
      <K8sSectionCard>
        <K8sResourceHeader
          name={secret.name}
          subtitle={secret.namespace}
          status={{ text: secret.type, variant: "neutral" }}
        />
        <K8sMetadataGrid
          items={[
            { label: t("asset.k8sSecretType"), value: secret.type, mono: true },
            { label: t("asset.k8sPodAge"), value: secret.age, mono: true },
          ]}
        />
      </K8sSectionCard>

      <K8sSectionCard title={t("asset.k8sSecretData")}>
        {dataEntries.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("asset.k8sNoEvents")}</p>
        ) : (
          <div className="space-y-3">
            {dataEntries.map(([key, value]) => {
              const decoded = decodeSecretValue(value);
              return (
                <div key={key}>
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs text-muted-foreground font-medium">{key}</span>
                    <span className="text-[10px] text-muted-foreground">{decoded.length}B</span>
                  </div>
                  <K8sCodeBlock code={decoded} maxHeight="max-h-32" />
                </div>
              );
            })}
          </div>
        )}
      </K8sSectionCard>
    </div>
  );
}
