import { useTranslation } from "react-i18next";
import { Button } from "@opskat/ui";
import { RefreshCw, Upload } from "lucide-react";

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
}

export function OSSBreadcrumb({ bucket, prefix, onNavigate, onRefresh, onUpload }: OSSBreadcrumbProps) {
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
      {onUpload && (
        <Button size="sm" variant="outline" className="shrink-0" onClick={onUpload} data-testid="oss-upload">
          <Upload className="size-3" /> {t("oss.transfer.upload")}
        </Button>
      )}
      <Button size="sm" variant="outline" className="shrink-0" onClick={onRefresh} data-testid="oss-refresh">
        <RefreshCw className="size-3" /> {t("oss.browser.refresh")}
      </Button>
    </div>
  );
}
