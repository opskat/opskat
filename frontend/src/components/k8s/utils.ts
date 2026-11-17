export type StatusVariant = "success" | "warning" | "error" | "info" | "neutral";

const SUCCESS_WORDS = new Set([
  "running",
  "true",
  "ready",
  "bound",
  "complete",
  "completed",
  "succeeded",
  "active",
  "available",
]);
const WARNING_WORDS = new Set(["pending", "containercreating", "podinitializing", "terminating", "creating"]);
const ERROR_WORDS = new Set([
  "failed",
  "false",
  "unknown",
  "error",
  "notready",
  "crashloopbackoff",
  "imagepullbackoff",
  "errimagepull",
  "evicted",
  "oomkilled",
  "createcontainerconfigerror",
  "invalidimagename",
]);

/** Maps a status word as `kubectl get` prints it (`Running`, `Init:0/1`, `Ready,SchedulingDisabled`) to a colour. */
export function getK8sStatusColor(status: string): StatusVariant {
  const word = status.toLowerCase().split(",")[0].trim();
  // An init container's state is printed as `Init:<reason>`; the reason decides unless it is a progress count.
  const s = word.startsWith("init:") && !/^init:\d+\/\d+$/.test(word) ? word.slice("init:".length) : word;
  if (SUCCESS_WORDS.has(s)) return "success";
  if (ERROR_WORDS.has(s) || s.endsWith("error")) return "error";
  if (WARNING_WORDS.has(s) || s.startsWith("init:")) return "warning";
  return "neutral";
}

const STATUS_COLUMNS = new Set(["status", "phase", "state"]);

/** Whether a server table column holds a status word worth colouring. */
export function isStatusColumn(column: { name: string }): boolean {
  return STATUS_COLUMNS.has(column.name.toLowerCase());
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

/** Decodes a base64 stream chunk (k8s log / exec events) to bytes. */
export function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

/** Conditions whose name describes a problem: False is the healthy value. */
const PROBLEM_CONDITIONS = new Set(["MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"]);

/** Colour of a condition's status: healthy → success, problem → error, Unknown → neutral. */
export function getConditionVariant(type: string, status: string): StatusVariant {
  if (status === "Unknown") return "neutral";
  const healthy = PROBLEM_CONDITIONS.has(type) ? "False" : "True";
  return status === healthy ? "success" : "error";
}
