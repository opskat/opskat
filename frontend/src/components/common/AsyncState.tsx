import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, RotateCw } from "lucide-react";
import { Button, cn } from "@opskat/ui";

/** Phase of the request behind a region. */
export type AsyncStatus = "loading" | "error" | "ready";

export interface AsyncStateProps {
  /** `loading` → spinner; `error` → failure reason + retry; `ready` → `empty` (if set) or `children`. */
  status: AsyncStatus;
  /** Failure reason, shown verbatim (monospace, ≥12px) when `status` is `error`. */
  error?: string | null;
  /** Re-runs the failed request. Omit it only when the failure can't be retried — the Retry button is hidden. */
  onRetry?: () => void;
  /**
   * Empty-state content (text or a node). Pass it **only when the successful result is empty**
   * (`empty={rows.length === 0 ? t("x.empty") : undefined}`); it is never shown while loading or
   * after a failure, so a failed request can't masquerade as "nothing here".
   */
  empty?: ReactNode;
  /** Layout classes for the loading / error / empty block (e.g. `flex-1` to fill a pane). */
  className?: string;
  /** The loaded content, rendered when `status` is `ready` and `empty` is not set. */
  children?: ReactNode;
}

/**
 * Shared loading / empty / error+retry block for a region backed by one request (docs/DESIGN.md §10).
 * The empty state only appears after a successful request with an empty result.
 */
export function AsyncState({ status, error, onRetry, empty, className, children }: AsyncStateProps) {
  const { t } = useTranslation();
  const frame = cn("flex flex-col items-center justify-center gap-2 p-4 text-center", className);

  if (status === "loading") {
    return (
      <div role="status" aria-label={t("asyncState.loading")} className={frame} data-testid="async-state-loading">
        <Loader2 className="size-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (status === "error") {
    return (
      <div role="alert" className={frame} data-testid="async-state-error">
        <AlertCircle className="size-5 text-destructive" />
        <p className="text-sm font-medium">{t("asyncState.loadFailed")}</p>
        {error && (
          <p className="max-w-full whitespace-pre-wrap break-words font-mono text-xs text-muted-foreground select-text">
            {error}
          </p>
        )}
        {onRetry && (
          <Button size="sm" variant="outline" onClick={onRetry} data-testid="async-state-retry">
            <RotateCw className="size-3.5" />
            {t("action.retry")}
          </Button>
        )}
      </div>
    );
  }

  if (empty !== undefined && empty !== null && empty !== false) {
    return (
      <div className={cn(frame, "text-sm text-muted-foreground")} data-testid="async-state-empty">
        {empty}
      </div>
    );
  }

  return <>{children}</>;
}
