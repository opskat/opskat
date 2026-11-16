import { selectorString, type LabelSelector } from "./selector";

export type SummaryItem = { label: string; value: string; mono?: boolean };

/** `ready/desired`. */
export const ratio = (have: number | undefined, want: number | undefined) => `${have ?? 0}/${want ?? 0}`;

export const selectorItem = (label: string, selector: LabelSelector | undefined): SummaryItem[] => {
  const value = selectorString(selector);
  return value ? [{ label, value, mono: true }] : [];
};
