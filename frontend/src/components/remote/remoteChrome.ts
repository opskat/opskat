/** 远程桌面会话状态（RDP / VNC 共用）。 */
export type RemoteStatus = "connecting" | "connected" | "error" | "closed";

/** Format elapsed seconds as HH:MM:SS. */
export function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(Math.floor(s / 3600))}:${pad(Math.floor((s % 3600) / 60))}:${pad(s % 60)}`;
}
