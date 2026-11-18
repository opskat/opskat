import { createElement } from "react";
import {
  Boxes,
  Box,
  Circle,
  Clock,
  Copy,
  Cpu,
  Database,
  FileText,
  Gauge,
  Globe,
  HardDrive,
  KeyRound,
  Layers,
  Network,
  Play,
  Puzzle,
  Scaling,
  Server,
  Shield,
  type LucideIcon,
} from "lucide-react";
import type { K8sCategory, K8sResourceType } from "@/stores/k8sStore";
import { typeKeyOf } from "@/stores/k8sStore";

/** Sidebar category order and labels. */
export const K8S_CATEGORIES: { key: K8sCategory; labelKey: string }[] = [
  { key: "cluster", labelKey: "asset.k8sCategoryCluster" },
  { key: "workloads", labelKey: "asset.k8sCategoryWorkloads" },
  { key: "network", labelKey: "asset.k8sCategoryNetwork" },
  { key: "config", labelKey: "asset.k8sCategoryConfig" },
  { key: "storage", labelKey: "asset.k8sCategoryStorage" },
  { key: "rbac", labelKey: "asset.k8sCategoryRbac" },
  { key: "custom", labelKey: "asset.k8sCategoryCustom" },
];

/** Built-in types keyed by `<group>/<resource>`; everything else (CRDs included) uses the generic icon and its Kind. */
const BUILTIN: Record<string, { icon: LucideIcon; labelKey?: string; short?: string }> = {
  "/nodes": { icon: Server, labelKey: "asset.k8sNodes" },
  "/namespaces": { icon: Layers, labelKey: "asset.k8sNamespaces" },
  "/events": { icon: Clock, labelKey: "asset.k8sEvents" },
  "/pods": { icon: Circle },
  "apps/deployments": { icon: Boxes },
  "apps/statefulsets": { icon: Database },
  "apps/daemonsets": { icon: Cpu },
  "apps/replicasets": { icon: Copy },
  "batch/jobs": { icon: Play },
  "batch/cronjobs": { icon: Clock },
  "/services": { icon: Network },
  "networking.k8s.io/ingresses": { icon: Globe },
  "discovery.k8s.io/endpointslices": { icon: Network },
  "networking.k8s.io/networkpolicies": { icon: Shield },
  "/configmaps": { icon: FileText },
  "/secrets": { icon: KeyRound },
  "autoscaling/horizontalpodautoscalers": { icon: Scaling, short: "HPA" },
  "/resourcequotas": { icon: Gauge },
  "/limitranges": { icon: Gauge },
  "/persistentvolumeclaims": { icon: HardDrive, short: "PVC" },
  "/persistentvolumes": { icon: HardDrive, short: "PV" },
  "storage.k8s.io/storageclasses": { icon: HardDrive },
  "/serviceaccounts": { icon: Shield },
  "rbac.authorization.k8s.io/roles": { icon: Shield },
  "rbac.authorization.k8s.io/rolebindings": { icon: Shield },
  "rbac.authorization.k8s.io/clusterroles": { icon: Shield },
  "rbac.authorization.k8s.io/clusterrolebindings": { icon: Shield },
};

export function kindIcon(typeKey: string): LucideIcon {
  return BUILTIN[typeKey]?.icon ?? (typeKey.startsWith("/") ? Box : Puzzle);
}

/** Icon of a resource type (`<group>/<resource>`). */
export function KindIcon({ typeKey, className }: { typeKey: string; className?: string }) {
  return createElement(kindIcon(typeKey), { className });
}

/** Display name of a type: a translated name for the cluster kinds, a short name where kubectl has one, else the Kind. */
export function kindLabel(type: K8sResourceType, t: (key: string) => string): string {
  const meta = BUILTIN[typeKeyOf(type)];
  if (meta?.labelKey) return t(meta.labelKey);
  return meta?.short ?? type.kind;
}

/**
 * Types shown in the sidebar: events.k8s.io Events is the same kind as core Events (shown under 集群),
 * so it is dropped when the core one is there.
 */
export function visibleTypes(types: K8sResourceType[]): K8sResourceType[] {
  const hasCoreEvents = types.some((t) => t.group === "" && t.resource === "events");
  return types.filter((t) => !(hasCoreEvents && t.group === "events.k8s.io" && t.resource === "events"));
}
