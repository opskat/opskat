import type { ReactNode, UIEvent } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { shouldLoadNextPage } from "@/lib/ossListScroll";
import { AsyncState } from "@/components/common/AsyncState";

interface OSSObjectCollectionFrameProps {
  children: ReactNode;
  className: string;
  empty: boolean;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  loadingPage: boolean;
  truncated: boolean;
  testIdPrefix: "oss-list" | "oss-grid";
  collectionTestId: "oss-object-list" | "oss-object-grid";
  onScrollNearBottom: () => void;
}

export function OSSObjectCollectionFrame({
  children,
  className,
  empty,
  loading,
  error,
  onRetry,
  loadingPage,
  truncated,
  testIdPrefix,
  collectionTestId,
  onScrollNearBottom,
}: OSSObjectCollectionFrameProps) {
  const { t } = useTranslation();

  if (loading || error || empty) {
    return (
      <AsyncState
        status={loading ? "loading" : error ? "error" : "ready"}
        error={error}
        onRetry={onRetry}
        empty={t("oss.browser.emptyDir")}
        className="min-h-0 flex-1"
      />
    );
  }

  const handleScroll = (event: UIEvent<HTMLDivElement>) => {
    const element = event.currentTarget;
    if (shouldLoadNextPage(element.scrollTop, element.clientHeight, element.scrollHeight, truncated, loadingPage)) {
      onScrollNearBottom();
    }
  };

  return (
    <div className={className} onScroll={handleScroll} data-testid={collectionTestId}>
      {children}
      {loadingPage && (
        <div
          className="flex items-center justify-center gap-1.5 p-2 text-xs text-muted-foreground"
          data-testid={`${testIdPrefix}-page-spinner`}
        >
          <Loader2 className="size-3 animate-spin text-primary" data-testid={`${testIdPrefix}-page-spinner-icon`} />
          {t("oss.browser.loadingMore")}
        </div>
      )}
    </div>
  );
}
