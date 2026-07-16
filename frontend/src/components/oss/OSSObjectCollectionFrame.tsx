import type { ReactNode, UIEvent } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { shouldLoadNextPage } from "@/lib/ossListScroll";

interface OSSObjectCollectionFrameProps {
  children: ReactNode;
  className: string;
  empty: boolean;
  loading: boolean;
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
  loadingPage,
  truncated,
  testIdPrefix,
  collectionTestId,
  onScrollNearBottom,
}: OSSObjectCollectionFrameProps) {
  const { t } = useTranslation();

  if (loading) {
    return (
      <div
        className="flex items-center gap-1.5 p-3 text-xs text-muted-foreground"
        data-testid={`${testIdPrefix}-loading`}
      >
        <Loader2 className="size-3.5 animate-spin text-primary" data-testid={`${testIdPrefix}-loading-spinner`} />
        {t("oss.browser.loading")}
      </div>
    );
  }
  if (empty) {
    return (
      <div className="p-6 text-center text-xs text-muted-foreground" data-testid={`${testIdPrefix}-empty`}>
        {t("oss.browser.emptyDir")}
      </div>
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
