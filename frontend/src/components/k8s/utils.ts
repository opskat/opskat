import { formatBytes } from "@/lib/formatBytes";

export type StatusVariant = "success" | "warning" | "error" | "info" | "neutral";

const K8S_BINARY_MULTIPLIERS: Record<string, number> = {
  Ki: 1024,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  Pi: 1024 ** 5,
  Ei: 1024 ** 6,
};

const K8S_DECIMAL_MULTIPLIERS: Record<string, number> = {
  K: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
  E: 1e18,
};

/** Parse Kubernetes resource quantity (memory/cpu capacity strings) to bytes. */
export function parseK8sQuantityToBytes(quantity: string): number | null {
  const trimmed = quantity.trim();
  const match = /^(\d+(?:\.\d+)?)(Ki|Mi|Gi|Ti|Pi|Ei|K|M|G|T|P|E)?$/.exec(trimmed);
  if (!match) return null;
  const value = Number(match[1]);
  const suffix = match[2];
  if (!suffix) return value;
  const binary = K8S_BINARY_MULTIPLIERS[suffix];
  if (binary) return value * binary;
  const decimal = K8S_DECIMAL_MULTIPLIERS[suffix];
  if (decimal) return value * decimal;
  return null;
}

/** 资产配置的命名空间优先；未配置时用 `default`；都不在集群里则回退到列表第一项。 */
export function resolveK8sPreferredNamespace(configuredNamespace: string, namespaces: { name: string }[]): string {
  const preferred = configuredNamespace.trim() || "default";
  if (namespaces.some((ns) => ns.name === preferred)) return preferred;
  if (preferred !== "default" && namespaces.some((ns) => ns.name === "default")) return "default";
  return namespaces[0]?.name ?? "";
}

/** Display Kubernetes version strings (API often includes a leading `v`). */
export function formatK8sVersion(version: string): string {
  const trimmed = version.trim();
  if (!trimmed) return trimmed;
  return trimmed.startsWith("v") ? trimmed : `v${trimmed}`;
}

/** Human-readable memory from API values like `15815400Ki`. Unparseable input is returned as-is. */
export function formatK8sMemory(quantity: string): string {
  const bytes = parseK8sQuantityToBytes(quantity);
  if (bytes === null) return quantity;
  return formatBytes(bytes);
}

export function getK8sStatusColor(status: string): StatusVariant {
  const s = status.toLowerCase();
  if (s === "running" || s === "true" || s === "ready") return "success";
  if (s === "pending") return "warning";
  if (s === "failed" || s === "false" || s === "unknown") return "error";
  return "neutral";
}

export function getContainerStateColor(state: string): StatusVariant {
  if (state.startsWith("Running")) return "success";
  if (state.startsWith("Waiting")) return "warning";
  return "error";
}

export function statusVariantToClass(variant: StatusVariant): string {
  const map: Record<StatusVariant, string> = {
    success: "bg-success/15 text-success",
    warning: "bg-warning/15 text-warning",
    error: "bg-destructive/15 text-destructive",
    info: "bg-info/15 text-info",
    neutral: "bg-muted text-muted-foreground",
  };
  return map[variant];
}
