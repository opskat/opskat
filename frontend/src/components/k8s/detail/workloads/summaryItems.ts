import { selectorString, type LabelSelector } from "./selector";

export type SummaryItem = { label: string; value: string; mono?: boolean };

/** `ready/desired`. */
export const ratio = (have: number | undefined, want: number | undefined) => `${have ?? 0}/${want ?? 0}`;

export const selectorItem = (label: string, selector: LabelSelector | undefined): SummaryItem[] => {
  const value = selectorString(selector);
  return value ? [{ label, value, mono: true }] : [];
};

export interface ContainerPort {
  containerPort: number;
  name?: string;
  protocol?: string;
}
export interface ContainerResources {
  requests?: Record<string, string>;
  limits?: Record<string, string>;
}

/** A container's ports as `8080/http`. */
export const portsText = (ports: ContainerPort[] | undefined) =>
  ports?.map((p) => `${p.containerPort}/${p.name ?? p.protocol ?? "TCP"}`).join(", ") || "-";

/** A requests / limits map as `cpu / memory`. */
export const cpuMem = (r: Record<string, string> | undefined) =>
  r?.cpu || r?.memory ? `${r.cpu ?? "-"} / ${r.memory ?? "-"}` : "-";
