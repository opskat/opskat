import { useTranslation } from "react-i18next";
import { AlertTriangle, CircleAlert, Info } from "lucide-react";
import { cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { capabilityLimits, type DraftIssue } from "./draft";
import { issueLocation, issueMessage } from "./issueText";

/**
 * 驱动能力有限时表格上方的说明:列出它能改与不能改的属性,文案由驱动声明的可改属性生成,
 * 不按驱动写。驱动声明了全部属性时不显示。
 */
export function CapabilityBar({ descriptor }: { descriptor: DriverDescriptor | undefined }) {
  const { t } = useTranslation();
  const limits = capabilityLimits(descriptor);
  if (!limits) return null;
  const list = (attributes: string[]) =>
    attributes.map((a) => t(`query.designer.attribute.${a}`)).join(t("query.designer.listSeparator"));
  const text =
    limits.can.length > 0
      ? t("query.designer.capability.limited", {
          driver: descriptor?.name,
          can: list(limits.can),
          cannot: list(limits.cannot),
        })
      : t("query.designer.capability.basicOnly", { driver: descriptor?.name, cannot: list(limits.cannot) });
  return (
    <div
      data-testid="designer-capability"
      className="flex shrink-0 items-start gap-2 border-b border-border bg-info/10 px-3 py-1.5 text-[11px] text-info"
    >
      <Info className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
      <span>{text}</span>
    </div>
  );
}

interface IssueListProps {
  issues: DraftIssue[];
  /** 点击一条问题:跳到它指向的格子。 */
  onSelect: (issue: DraftIssue) => void;
}

/** 底部的问题与提醒:错误阻止保存,提醒不影响;点一条跳到对应格子。 */
export function IssueList({ issues, onSelect }: IssueListProps) {
  const { t } = useTranslation();
  if (issues.length === 0) return null;
  const errors = issues.filter((i) => i.severity === "error");
  const warnings = issues.filter((i) => i.severity === "warning");
  const item = (issue: DraftIssue, i: number) => {
    const location = issueLocation(t, issue);
    return (
      <li key={i}>
        <button
          type="button"
          className={cn(
            "block max-w-full truncate text-left hover:underline",
            issue.severity === "error" ? "text-destructive/90" : "text-foreground/80"
          )}
          onClick={() => onSelect(issue)}
        >
          {location ? `${location}${t("query.designer.issues.separator")}` : ""}
          {issueMessage(t, issue)}
        </button>
      </li>
    );
  };
  return (
    <div
      data-testid="designer-issues"
      className={cn(
        "max-h-32 shrink-0 overflow-y-auto border-t px-3 py-1.5 text-[11px]",
        errors.length > 0 ? "border-destructive/30 bg-destructive/5" : "border-warning/30 bg-warning/5"
      )}
    >
      {errors.length > 0 && (
        <>
          <div className="flex items-center gap-1 font-medium text-destructive">
            <CircleAlert className="h-3 w-3" aria-hidden />
            {t("query.designer.issues.errors", { count: errors.length })}
          </div>
          <ul>{errors.map(item)}</ul>
        </>
      )}
      {warnings.length > 0 && (
        <>
          <div className={cn("flex items-center gap-1 font-medium text-warning", errors.length > 0 && "mt-1")}>
            <AlertTriangle className="h-3 w-3" aria-hidden />
            {t("query.designer.issues.warnings", { count: warnings.length })}
          </div>
          <ul>{warnings.map(item)}</ul>
        </>
      )}
    </div>
  );
}
