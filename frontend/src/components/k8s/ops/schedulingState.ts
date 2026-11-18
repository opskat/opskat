import { useK8sStore, type K8sResourceRow, type K8sTableColumn } from "@/stores/k8sStore";
import type { K8sActionContext, K8sActionTarget } from "../actions";

const isRow = (target: K8sActionTarget): target is K8sResourceRow => "cells" in target;
const specOf = (object: unknown) => (object as { spec?: Record<string, unknown> } | undefined)?.spec;

/** Whether a CronJob is suspended: its `spec.suspend` when the object is at hand, else the list's "Suspend" column. */
export function isCronJobSuspended(target: K8sActionTarget, columns?: K8sTableColumn[]): boolean {
  if (!isRow(target)) return specOf(target)?.suspend === true;
  if (target.object) return specOf(target.object)?.suspend === true;
  const idx = columns?.findIndex((c) => c.name === "Suspend") ?? -1;
  const cell = idx >= 0 ? target.cells[idx] : undefined;
  return cell === true || String(cell).toLowerCase() === "true";
}

/** Whether a node is cordoned: `spec.unschedulable`, or `SchedulingDisabled` in the list's status text. */
export function isNodeUnschedulable(target: K8sActionTarget): boolean {
  if (!isRow(target)) return specOf(target)?.unschedulable === true;
  if (target.object) return specOf(target.object)?.unschedulable === true;
  return target.cells.some((c) => typeof c === "string" && c.includes("SchedulingDisabled"));
}

/** The object or cached list row (with its columns) an action started from, to read its current state. */
export function targetOf(ctx: K8sActionContext): { target: K8sActionTarget; columns?: K8sTableColumn[] } | null {
  if (ctx.object) return { target: ctx.object };
  for (const [key, entry] of Object.entries(useK8sStore.getState().lists[ctx.assetId] ?? {})) {
    if (!key.startsWith(`${ctx.typeKey}|`)) continue;
    const row = entry.rows.find((r) => r.name === ctx.name && r.namespace === ctx.namespace);
    if (row) return { target: row, columns: entry.columns };
  }
  return null;
}
