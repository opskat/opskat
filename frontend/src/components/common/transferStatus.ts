import { Ban, CheckCircle2, Loader2, XCircle, type LucideIcon } from "lucide-react";

/** SFTP 与 OSS 传输列表共用的行状态。 */
export type TransferStatus = "active" | "done" | "error" | "cancelled";

/** 每种状态的图标、图标颜色与状态文字（文字是状态的主要表达，图标只是辅助，因此 aria-hidden）。 */
export const TRANSFER_STATUS: Record<TransferStatus, { icon: LucideIcon; iconClass: string; labelKey: string }> = {
  active: { icon: Loader2, iconClass: "animate-spin text-primary", labelKey: "sftp.transferring" },
  done: { icon: CheckCircle2, iconClass: "text-success", labelKey: "sftp.completed" },
  error: { icon: XCircle, iconClass: "text-destructive", labelKey: "sftp.failed" },
  cancelled: { icon: Ban, iconClass: "text-muted-foreground", labelKey: "sftp.cancelled" },
};
