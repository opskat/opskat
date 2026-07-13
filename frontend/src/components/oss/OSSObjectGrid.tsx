import type React from "react";
import { useTranslation } from "react-i18next";
import { Folder } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { formatBytes } from "@/lib/formatBytes";
import { shouldLoadNextPage } from "@/lib/ossListScroll";
import { OSSThumbnail } from "./OSSThumbnail";

export interface OSSObjectGridProps {
  prefixes: string[];
  objects: oss_svc.ObjectItem[];
  focusedKey: string | null;
  loading: boolean;
  loadingPage: boolean;
  truncated: boolean;
  thumbnails: Record<string, string>;
  onNavigatePrefix: (prefix: string) => void;
  onFocusObject: (key: string) => void;
  onEnsureThumbnail: (key: string) => void;
  onScrollNearBottom: () => void;
}

export function OSSObjectGrid({
  prefixes,
  objects,
  focusedKey,
  loading,
  loadingPage,
  truncated,
  thumbnails,
  onNavigatePrefix,
  onFocusObject,
  onEnsureThumbnail,
  onScrollNearBottom,
}: OSSObjectGridProps) {
  const { t } = useTranslation();

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (shouldLoadNextPage(el.scrollTop, el.clientHeight, el.scrollHeight, truncated, loadingPage)) {
      onScrollNearBottom();
    }
  };

  if (loading) {
    return (
      <div className="p-3 text-xs text-muted-foreground" data-testid="oss-grid-loading">
        {t("oss.browser.loading")}
      </div>
    );
  }
  if (prefixes.length === 0 && objects.length === 0) {
    return (
      <div className="p-6 text-center text-xs text-muted-foreground" data-testid="oss-grid-empty">
        {t("oss.browser.emptyDir")}
      </div>
    );
  }

  const tile = "flex w-[150px] cursor-pointer flex-col gap-1 rounded border p-1.5 hover:bg-accent/50";

  return (
    <div className="min-h-0 flex-1 overflow-auto p-3" onScroll={handleScroll} data-testid="oss-object-grid">
      <div className="flex flex-wrap gap-3">
        {prefixes.map((p) => (
          <div
            key={p}
            className={`${tile} items-center justify-center`}
            onDoubleClick={() => onNavigatePrefix(p)}
            data-testid={`oss-grid-folder-${p}`}
          >
            <div className="flex aspect-square w-full items-center justify-center">
              <Folder className="size-8 text-warning" />
            </div>
            <span className="w-full truncate text-center text-xs" title={p}>
              {prefixLeafName(p)}
            </span>
          </div>
        ))}
        {objects.map((o) => (
          <div
            key={o.key}
            className={`${tile} ${o.key === focusedKey ? "ring-2 ring-primary" : ""}`}
            onClick={() => onFocusObject(o.key)}
            data-testid={`oss-grid-object-${o.key}`}
          >
            <div className="aspect-square w-full overflow-hidden rounded bg-muted/20">
              <OSSThumbnail
                objectKey={o.key}
                contentType={o.contentType}
                url={thumbnails[o.key]}
                onEnsure={() => onEnsureThumbnail(o.key)}
                className="size-full"
              />
            </div>
            <span className="w-full truncate text-xs" title={o.key}>
              {prefixLeafName(o.key)}
            </span>
            <span className="text-[10px] text-muted-foreground">{formatBytes(o.size)}</span>
          </div>
        ))}
      </div>
      {loadingPage && (
        <div className="p-2 text-center text-xs text-muted-foreground" data-testid="oss-grid-page-spinner">
          {t("oss.browser.loadingMore")}
        </div>
      )}
    </div>
  );
}
