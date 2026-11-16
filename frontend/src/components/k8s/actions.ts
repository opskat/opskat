import type { LucideIcon } from "lucide-react";
import type { K8sResourceRow } from "@/stores/k8sStore";

/** What an action runs against; `object` is the full resource when the caller has it (detail view). */
export interface K8sActionContext {
  assetId: number;
  typeKey: string;
  namespace: string;
  name: string;
  object?: Record<string, unknown>;
}

/** A list row, or the full resource object shown by a detail view. */
export type K8sActionTarget = K8sResourceRow | Record<string, unknown>;

export interface K8sAction {
  id: string;
  labelKey: string;
  icon: LucideIcon;
  /** Shown as an icon on the row (list) / header (detail) in addition to the menu. */
  primary?: boolean;
  /** Listed after the other actions and drawn in the error colour. */
  destructive?: boolean;
  appliesTo: (typeKey: string, target: K8sActionTarget) => boolean;
  /** A reason string greys the action out and explains why. */
  disabledReason?: (target: K8sActionTarget) => string | undefined;
  run: (ctx: K8sActionContext) => void | Promise<void>;
}

const actions: K8sAction[] = [];

/** Registers an action for the list and detail views; returns the unregister function. */
export function registerK8sAction(action: K8sAction): () => void {
  actions.push(action);
  return () => {
    const idx = actions.indexOf(action);
    if (idx >= 0) actions.splice(idx, 1);
  };
}

/** Actions that apply to the target, in registration order with destructive ones last. */
export function getK8sActions(typeKey: string, target: K8sActionTarget): K8sAction[] {
  const applicable = actions.filter((a) => a.appliesTo(typeKey, target));
  return [...applicable.filter((a) => !a.destructive), ...applicable.filter((a) => a.destructive)];
}
