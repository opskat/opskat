import type { LucideIcon } from "lucide-react";
import type { K8sResourceRow, K8sTableColumn } from "@/stores/k8sStore";

/** What an action runs against; `object` is the full resource when the caller has it (detail view). */
export interface K8sActionContext {
  assetId: number;
  typeKey: string;
  namespace: string;
  name: string;
  object?: Record<string, unknown>;
  /** Set when the action was started from one container of a Pod (e.g. its terminal button). */
  container?: string;
}

/** A list row, or the full resource object shown by a detail view. */
export type K8sActionTarget = K8sResourceRow | Record<string, unknown>;

export interface K8sAction {
  id: string;
  labelKey: string;
  /** Overrides `labelKey` when the label depends on the target's state (pause vs resume). */
  labelKeyFor?: (target: K8sActionTarget, columns?: K8sTableColumn[]) => string;
  icon: LucideIcon;
  /** Shown as an icon on the row (list) / header (detail) in addition to the menu. */
  primary?: boolean;
  /** Listed after the other actions and drawn in the error colour. */
  destructive?: boolean;
  /** `assetId` is given when the caller knows the cluster (list / detail), for actions that depend on its discovery. */
  appliesTo: (typeKey: string, target: K8sActionTarget, assetId?: number) => boolean;
  /**
   * A reason string greys the action out and explains why. A list row comes with the list's columns, which
   * its `cells` line up with (a row carries no full object).
   */
  disabledReason?: (target: K8sActionTarget, columns?: K8sTableColumn[]) => string | undefined;
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
export function getK8sActions(typeKey: string, target: K8sActionTarget, assetId?: number): K8sAction[] {
  const applicable = actions.filter((a) => a.appliesTo(typeKey, target, assetId));
  return [...applicable.filter((a) => !a.destructive), ...applicable.filter((a) => a.destructive)];
}

/** The i18n key of the action's label for this target. */
export function actionLabelKey(action: K8sAction, target: K8sActionTarget, columns?: K8sTableColumn[]): string {
  return action.labelKeyFor?.(target, columns) ?? action.labelKey;
}
