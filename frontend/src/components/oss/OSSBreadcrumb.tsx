import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { RefreshCw, Upload, List, LayoutGrid, FolderPlus } from "lucide-react";

export interface OssCrumb {
  label: string;
  prefix: string;
  isCurrent: boolean;
}

/** bucket + "a/b/" -> [bucket(""), a("a/"), b("a/b/")]，最后一段为当前。 */
export function crumbSegments(bucket: string, prefix: string): OssCrumb[] {
  const parts = prefix.split("/").filter(Boolean);
  const crumbs: OssCrumb[] = [{ label: bucket, prefix: "", isCurrent: parts.length === 0 }];
  let acc = "";
  parts.forEach((part, i) => {
    acc += `${part}/`;
    crumbs.push({ label: part, prefix: acc, isCurrent: i === parts.length - 1 });
  });
  return crumbs;
}

export interface OSSBreadcrumbProps {
  bucket: string;
  prefix: string;
  onNavigate: (prefix: string) => void;
  onRefresh: () => void;
  onUpload?: () => void;
  onNewFolder?: () => void;
  viewMode?: "list" | "grid";
  onViewModeChange?: (m: "list" | "grid") => void;
}

export function OSSBreadcrumb({
  bucket,
  prefix,
  onNavigate,
  onRefresh,
  onUpload,
  onNewFolder,
  viewMode,
  onViewModeChange,
}: OSSBreadcrumbProps) {
  const { t } = useTranslation();
  const crumbs = crumbSegments(bucket, prefix);
  return (
    <div className="flex items-center gap-1 border-b px-3 py-1.5 text-xs" data-testid="oss-breadcrumb">
      <div className="flex min-w-0 flex-1 items-center gap-0.5 font-mono">
        {crumbs.map((c, i) => (
          <span key={c.prefix} className="flex items-center gap-0.5">
            {i > 0 && <span className="text-muted-foreground/40">/</span>}
            <button
              type="button"
              className={c.isCurrent ? "font-semibold text-foreground" : "text-muted-foreground hover:text-foreground"}
              onClick={() => onNavigate(c.prefix)}
              data-testid={`oss-crumb-${i}`}
            >
              {c.label}
            </button>
          </span>
        ))}
      </div>
      {viewMode && onViewModeChange && (
        <div className="flex shrink-0 overflow-hidden rounded border">
          <button
            type="button"
            className={`px-1.5 py-1 ${viewMode === "list" ? "bg-accent" : "text-muted-foreground"}`}
            onClick={() => onViewModeChange("list")}
            title={t("oss.view.list")}
            data-testid="oss-view-list"
          >
            <List className="size-3" />
          </button>
          <button
            type="button"
            className={`px-1.5 py-1 ${viewMode === "grid" ? "bg-accent" : "text-muted-foreground"}`}
            onClick={() => onViewModeChange("grid")}
            title={t("oss.view.grid")}
            data-testid="oss-view-grid"
          >
            <LayoutGrid className="size-3" />
          </button>
        </div>
      )}
      {onUpload && (
        <Button size="sm" variant="outline" className="shrink-0" onClick={onUpload} data-testid="oss-upload">
          <Upload className="size-3" /> {t("oss.transfer.upload")}
        </Button>
      )}
      {onNewFolder && (
        <Button size="sm" variant="outline" className="shrink-0" onClick={onNewFolder} data-testid="oss-new-folder">
          <FolderPlus className="size-3" /> {t("oss.browser.newFolder")}
        </Button>
      )}
      <Button size="sm" variant="outline" className="shrink-0" onClick={onRefresh} data-testid="oss-refresh">
        <RefreshCw className="size-3" /> {t("oss.browser.refresh")}
      </Button>
    </div>
  );
}
