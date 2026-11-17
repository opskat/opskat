import i18n from "@/i18n";
import type { K8sTerminalEnd } from "@/stores/k8sStore";

/** Why a Pod terminal session ended, for the end banner and the terminal's last line. */
export function endText(end: K8sTerminalEnd): string {
  if (end.startFailed) return i18n.t("asset.k8sTerminalStartFailed", { error: end.error ?? "" });
  if (end.containerReason) {
    return i18n.t("asset.k8sTerminalEndTerminated", { reason: end.containerReason, code: end.exitCode ?? "?" });
  }
  if (end.podGone) return i18n.t("asset.k8sTerminalEndPodGone");
  if (end.reason === "exited") return i18n.t("asset.k8sTerminalEndExited", { code: end.exitCode ?? 0 });
  if (end.reason === "error") return i18n.t("asset.k8sTerminalEndLost", { error: end.error ?? "" });
  if (end.reason === "failed") return i18n.t("asset.k8sTerminalEndFailed", { error: end.error ?? "" });
  return i18n.t("asset.k8sTerminalDisconnected");
}

/** The terminal's first line of a session: where it is attached and the shell actually used. */
export function startedText(target: { namespace: string; pod: string; container: string }, shell: string): string {
  return i18n.t("asset.k8sTerminalStartedLine", {
    target: `${target.namespace}/${target.pod}`,
    container: target.container,
    shell,
  });
}

/** Written between sessions of the same terminal. */
export function separatorText(container: string): string {
  return i18n.t("asset.k8sTerminalNewSession", { container });
}
