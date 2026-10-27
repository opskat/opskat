import { useTranslation } from "react-i18next";
import { CircleAlert, RefreshCw } from "lucide-react";
import { Button } from "@opskat/ui";

const SKELETON_ROWS = 9;

/** 读取表结构期间的表格区占位:保留行高与列的大致宽度。 */
export function DesignerSkeleton() {
  return (
    <div data-testid="designer-skeleton" className="min-h-0 flex-1 animate-pulse overflow-hidden" aria-busy="true">
      {Array.from({ length: SKELETON_ROWS }, (_, i) => (
        <div key={i} className="flex h-8 items-center gap-4 border-b border-border/60 px-3">
          <span className="h-3 w-6 rounded bg-muted" />
          <span className="h-3 w-32 rounded bg-muted" />
          <span className="h-3 w-28 rounded bg-muted" />
          <span className="h-3 w-8 rounded bg-muted" />
          <span className="h-3 w-40 rounded bg-muted" />
        </div>
      ))}
    </div>
  );
}

interface DesignerLoadErrorProps {
  /** 驱动的原始错误。 */
  error: string;
  onRetry: () => void;
  onClose: () => void;
}

/** 读取表结构失败:显示驱动的原始错误,可重试或关闭标签。 */
export function DesignerLoadError({ error, onRetry, onClose }: DesignerLoadErrorProps) {
  const { t } = useTranslation();
  return (
    <div role="alert" className="flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      <CircleAlert className="h-8 w-8 text-destructive/70" aria-hidden />
      <div className="text-sm">{t("query.designer.loadFailed")}</div>
      <code className="max-w-lg rounded-md bg-muted px-3 py-2 text-left font-mono text-[11px] break-all text-muted-foreground select-text">
        {error}
      </code>
      <div className="flex gap-2">
        <Button size="sm" className="h-7 text-xs" onClick={onRetry}>
          <RefreshCw className="h-3.5 w-3.5" aria-hidden />
          {t("action.retry")}
        </Button>
        <Button variant="outline" size="sm" className="h-7 text-xs" onClick={onClose}>
          {t("query.designer.closeTab")}
        </Button>
      </div>
    </div>
  );
}
