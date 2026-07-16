import { Folder } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { formatBytes } from "@/lib/formatBytes";
import { OSSThumbnail } from "./OSSThumbnail";
import { OSSObjectCollectionFrame } from "./OSSObjectCollectionFrame";

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
  const tile =
    "flex w-[150px] cursor-pointer flex-col gap-1 rounded border p-1.5 text-left outline-none hover:bg-accent/50 focus-visible:ring-1 focus-visible:ring-ring/45";

  return (
    <OSSObjectCollectionFrame
      className="min-h-0 flex-1 overflow-auto p-3"
      empty={prefixes.length === 0 && objects.length === 0}
      loading={loading}
      loadingPage={loadingPage}
      truncated={truncated}
      testIdPrefix="oss-grid"
      collectionTestId="oss-object-grid"
      onScrollNearBottom={onScrollNearBottom}
    >
      <div className="flex flex-wrap gap-3">
        {prefixes.map((p) => (
          <button
            type="button"
            key={p}
            className={`${tile} items-center justify-center`}
            onDoubleClick={() => onNavigatePrefix(p)}
            onKeyDown={(e) => {
              if (e.key === "Enter") onNavigatePrefix(p);
            }}
            data-testid={`oss-grid-folder-${p}`}
          >
            <div className="flex aspect-square w-full items-center justify-center">
              <Folder className="size-8 text-warning" />
            </div>
            <span className="w-full truncate text-center text-xs" title={p}>
              {prefixLeafName(p)}
            </span>
          </button>
        ))}
        {objects.map((o) => (
          <button
            type="button"
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
          </button>
        ))}
      </div>
    </OSSObjectCollectionFrame>
  );
}
