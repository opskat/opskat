import { useTranslation } from "react-i18next";
import { CircleAlert, Copy, Filter, RotateCw } from "lucide-react";
import { Button } from "@opskat/ui";
import { notifyCopied } from "@/lib/notify";

interface QueryErrorPanelProps {
  title: string;
  /** 驱动返回的错误原文,原样展示与复制。 */
  error: string;
  onRetry: () => void;
  /** 仅在存在筛选条件时传入:打开筛选器让用户改条件。 */
  onEditFilter?: () => void;
}

export function QueryErrorPanel({ title, error, onRetry, onEditFilter }: QueryErrorPanelProps) {
  const { t } = useTranslation();

  const handleCopy = async () => {
    await navigator.clipboard.writeText(error);
    notifyCopied(t("query.copied"));
  };

  return (
    <div className="flex min-h-0 flex-1 items-start justify-center overflow-auto p-6">
      <div
        role="alert"
        className="flex w-full max-w-2xl flex-col gap-3 rounded-md border border-destructive/30 bg-destructive/10 p-4"
      >
        <div className="flex items-center gap-2 text-sm font-semibold text-destructive">
          <CircleAlert className="h-4 w-4 shrink-0" />
          {title}
        </div>
        <pre className="max-h-64 select-text overflow-auto whitespace-pre-wrap break-words rounded border border-destructive/20 bg-background/60 p-2 font-mono text-xs text-destructive">
          {error}
        </pre>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={onRetry}>
            <RotateCw className="h-3.5 w-3.5" />
            {t("query.retry")}
          </Button>
          <Button size="sm" variant="outline" onClick={handleCopy}>
            <Copy className="h-3.5 w-3.5" />
            {t("query.copyError")}
          </Button>
          {onEditFilter && (
            <Button size="sm" variant="outline" onClick={onEditFilter}>
              <Filter className="h-3.5 w-3.5" />
              {t("query.editFilter")}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
