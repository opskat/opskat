import { createElement } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { Copy, Link, Download, Trash2, X, Pencil, MoveRight } from "lucide-react";
import type { oss_svc } from "../../../wailsjs/go/models";
import { formatBytes } from "@/lib/formatBytes";
import { notifyCopied } from "@/lib/notify";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { typeIcon, typeIconColor } from "@/lib/objectContentType";
import { OSSThumbnail } from "./OSSThumbnail";

export interface OSSObjectDetailProps {
  object: oss_svc.ObjectItem;
  thumbnailUrl?: string;
  onEnsureThumbnail: () => void;
  onShare: () => void;
  onDownload: () => void;
  onCopy?: () => void;
  onMove?: () => void;
  onRename?: () => void;
  onDelete: () => void;
  onClose: () => void;
}

export function OSSObjectDetail({
  object,
  thumbnailUrl,
  onEnsureThumbnail,
  onShare,
  onDownload,
  onCopy,
  onMove,
  onRename,
  onDelete,
  onClose,
}: OSSObjectDetailProps) {
  const { t } = useTranslation();
  const rows: [string, string][] = [
    [t("oss.detail.size"), formatBytes(object.size)],
    [t("oss.detail.type"), object.contentType || "—"],
    [t("oss.detail.storageClass"), object.storageClass || "—"],
    [t("oss.detail.etag"), object.etag || "—"],
    [t("oss.detail.lastModified"), object.lastModified ? new Date(object.lastModified * 1000).toLocaleString() : "—"],
  ];
  const copyKey = () =>
    void navigator.clipboard?.writeText(object.key).then(() => notifyCopied(t("oss.detail.copyKey")));
  const iconButtonClass =
    "cursor-pointer rounded-sm p-0.5 outline-none hover:bg-accent focus-visible:ring-1 focus-visible:ring-ring/45";

  return (
    <div className="flex h-full flex-col text-xs" data-testid="oss-object-detail">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        {createElement(typeIcon(object.contentType, object.key), {
          className: `size-3.5 shrink-0 ${typeIconColor(object.contentType, object.key)}`,
        })}
        <span className="min-w-0 flex-1 truncate font-medium" title={object.key}>
          {prefixLeafName(object.key)}
        </span>
        <button
          type="button"
          className={iconButtonClass}
          onClick={onClose}
          title={t("oss.detail.close")}
          aria-label={t("oss.detail.close")}
          data-testid="oss-detail-close"
        >
          <X className="size-3.5 text-muted-foreground" />
        </button>
      </div>

      <div className="aspect-video shrink-0 overflow-hidden border-b bg-muted/20">
        <OSSThumbnail
          objectKey={object.key}
          contentType={object.contentType}
          url={thumbnailUrl}
          onEnsure={onEnsureThumbnail}
          className="size-full"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto px-3 py-2">
        <div className="flex items-center gap-1">
          <span className="min-w-0 flex-1 break-all font-mono text-muted-foreground">{object.key}</span>
          <button
            type="button"
            className={iconButtonClass}
            onClick={copyKey}
            title={t("oss.detail.copyKey")}
            aria-label={t("oss.detail.copyKey")}
            data-testid="oss-detail-copy-key"
          >
            <Copy className="size-3 text-muted-foreground" />
          </button>
        </div>

        <div className="mb-2 mt-3 flex items-center gap-2">
          <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            {t("oss.detail.metadataSection")}
          </span>
          <div className="h-px flex-1 bg-border" />
        </div>

        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          {rows.map(([label, value]) => (
            <div key={label} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="break-all text-right">{value}</dd>
            </div>
          ))}
        </dl>
      </div>

      <div className="flex flex-col gap-1.5 border-t p-3">
        <Button size="sm" onClick={onShare} data-testid="oss-detail-share">
          <Link className="size-3" /> {t("oss.detail.share")}
        </Button>
        <div className="flex gap-1.5">
          <Button size="sm" variant="outline" className="flex-1" onClick={onDownload} data-testid="oss-detail-download">
            <Download className="size-3" /> {t("oss.detail.download")}
          </Button>
        </div>
        {onRename && onCopy && onMove && (
          <div className="grid grid-cols-3 gap-1.5">
            <Button size="sm" variant="outline" onClick={onRename} data-testid="oss-detail-rename">
              <Pencil className="size-3" /> {t("oss.detail.rename")}
            </Button>
            <Button size="sm" variant="outline" onClick={onCopy} data-testid="oss-detail-copy">
              <Copy className="size-3" /> {t("oss.detail.copy")}
            </Button>
            <Button size="sm" variant="outline" onClick={onMove} data-testid="oss-detail-move">
              <MoveRight className="size-3" /> {t("oss.detail.move")}
            </Button>
          </div>
        )}
        <Button size="sm" variant="destructive" onClick={onDelete} data-testid="oss-detail-delete">
          <Trash2 className="size-3" /> {t("oss.detail.delete")}
        </Button>
      </div>
    </div>
  );
}
