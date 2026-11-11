import { useTranslation } from "react-i18next";
import { ChevronRight, ChevronDown, Folder, Database } from "lucide-react";
import { AsyncState } from "@/components/common/AsyncState";
import type { oss_svc } from "../../../wailsjs/go/models";
import type { OssPrefixRow } from "@/lib/ossPrefixTree";

export interface OSSBucketTreeProps {
  buckets: oss_svc.BucketItem[] | null;
  currentBucket: string;
  rows: OssPrefixRow[];
  /** Bucket-list failure reason (null = none). */
  error?: string | null;
  onRetryBuckets?: () => void;
  onSelectBucket: (bucket: string) => void;
  onToggleExpand: (prefix: string) => void;
  onNavigatePrefix: (prefix: string) => void;
}

export function OSSBucketTree({
  buckets,
  currentBucket,
  rows,
  error,
  onRetryBuckets,
  onSelectBucket,
  onToggleExpand,
  onNavigatePrefix,
}: OSSBucketTreeProps) {
  const { t } = useTranslation();
  const customButtonClass = "cursor-pointer rounded-sm outline-none focus-visible:ring-1 focus-visible:ring-ring/45";

  const status = error ? "error" : buckets === null ? "loading" : "ready";
  if (status !== "ready" || buckets?.length === 0) {
    return (
      <AsyncState
        status={status}
        error={error}
        onRetry={onRetryBuckets}
        empty={buckets?.length === 0 ? t("oss.browser.noBuckets") : undefined}
        className="flex-1"
      />
    );
  }

  return (
    <div className="flex h-full flex-col overflow-auto text-xs" data-testid="oss-bucket-tree">
      {(buckets ?? []).map((b) => {
        const selected = b.name === currentBucket;
        return (
          <div key={b.name}>
            <button
              type="button"
              className={`${customButtonClass} flex w-full items-center gap-1 px-2 py-1 text-left hover:bg-accent/50 ${
                selected ? "bg-accent font-medium" : ""
              }`}
              onClick={() => onSelectBucket(b.name)}
              aria-current={selected ? "true" : undefined}
              data-testid={`oss-bucket-${b.name}`}
            >
              <Database className="size-3 text-muted-foreground" /> {b.name}
            </button>
            {selected &&
              rows.map((row) => (
                <div
                  key={row.prefix}
                  className="flex items-center gap-1 py-0.5 hover:bg-accent/40"
                  style={{ paddingLeft: 12 + row.depth * 12 }}
                  data-testid={`oss-tree-row-${row.prefix}`}
                >
                  <button
                    type="button"
                    className={`${customButtonClass} shrink-0`}
                    onClick={() => onToggleExpand(row.prefix)}
                    aria-label={row.name}
                    aria-expanded={row.isExpanded}
                    data-testid={`oss-tree-toggle-${row.prefix}`}
                  >
                    {row.isExpanded ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
                  </button>
                  <button
                    type="button"
                    className={`${customButtonClass} flex min-w-0 flex-1 items-center gap-1 text-left`}
                    onClick={() => onNavigatePrefix(row.prefix)}
                    data-testid={`oss-tree-nav-${row.prefix}`}
                  >
                    <Folder className="size-3 text-warning" />
                    <span className="truncate">{row.name}</span>
                  </button>
                </div>
              ))}
          </div>
        );
      })}
    </div>
  );
}
