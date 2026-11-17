import { useTranslation } from "react-i18next";
import { AlertCircle, RotateCw } from "lucide-react";
import { Button, cn } from "@opskat/ui";
import { AsyncState, type AsyncStateProps } from "@/components/common/AsyncState";

/** The reason a re-read failed, with a retry, shown above what was read before. */
export function K8sStaleError({
  error,
  onRetry,
  className,
}: {
  error: string | null | undefined;
  onRetry?: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="alert"
      data-testid="k8s-stale-error"
      className={cn(
        "mb-2 flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-1.5 text-xs text-destructive",
        className
      )}
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 flex-1">
        {t("asset.k8sRefreshFailed")}
        {error && <span className="ml-1 select-text whitespace-pre-wrap break-words font-mono">{error}</span>}
      </span>
      {onRetry && (
        <Button
          variant="outline"
          size="sm"
          className="h-6 shrink-0 px-2 text-xs"
          onClick={onRetry}
          data-testid="k8s-stale-retry"
        >
          <RotateCw className="size-3" />
          {t("action.retry")}
        </Button>
      )}
    </div>
  );
}

/**
 * AsyncState for a K8s region whose data outlives a re-read: while it reloads the data stays, and when the re-read
 * fails the data stays under the reason and a retry — a failed refresh neither blanks the region nor unmounts what
 * it holds (a log stream). Without data it is plain AsyncState.
 */
export function K8sAsyncState({ hasData, ...props }: AsyncStateProps & { hasData: boolean }) {
  if (!hasData) return <AsyncState {...props} />;
  // One shape whether or not the re-read failed, so the content keeps its identity (and its state) across both.
  return (
    <>
      {props.status === "error" && <K8sStaleError error={props.error} onRetry={props.onRetry} />}
      <AsyncState {...props} status="ready" />
    </>
  );
}
