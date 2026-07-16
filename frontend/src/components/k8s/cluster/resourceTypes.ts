import { Circle, Container, FileText, Grid3X3, Key } from "lucide-react";
import type { ResourceTypeDef } from "./types";

export const RESOURCE_TYPES: ResourceTypeDef[] = [
  { key: "pods", labelKey: "asset.k8sPods", icon: Circle },
  { key: "deployments", labelKey: "asset.k8sDeployments", icon: Grid3X3 },
  { key: "services", labelKey: "asset.k8sServices", icon: Container },
  { key: "config_maps", labelKey: "asset.k8sConfigMaps", icon: FileText },
  { key: "secrets", labelKey: "asset.k8sSecrets", icon: Key },
];
