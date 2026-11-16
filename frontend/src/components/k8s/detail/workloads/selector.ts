interface Expression {
  key: string;
  operator: string;
  values?: string[];
}

export interface LabelSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: Expression[];
}

/** A `spec.selector` in label-selector syntax for a list request; '' when it selects nothing explicitly. */
export function selectorString(selector: LabelSelector | undefined): string {
  if (!selector) return "";
  const parts = Object.entries(selector.matchLabels ?? {}).map(([k, v]) => `${k}=${v}`);
  for (const e of selector.matchExpressions ?? []) {
    const values = (e.values ?? []).join(",");
    if (e.operator === "In") parts.push(`${e.key} in (${values})`);
    else if (e.operator === "NotIn") parts.push(`${e.key} notin (${values})`);
    else if (e.operator === "Exists") parts.push(e.key);
    else if (e.operator === "DoesNotExist") parts.push(`!${e.key}`);
  }
  return parts.join(",");
}
