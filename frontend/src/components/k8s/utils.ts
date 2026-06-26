export type StatusVariant = "success" | "warning" | "error" | "info" | "neutral";

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
