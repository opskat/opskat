import { useState } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "@/lib/formatBytes";

/** 超过这个字符数的值在审批面里只显示开头；批准的仍是全文。 */
export const TRUNCATE_LIMIT = 1024;

interface TruncatedTextProps {
  text: string;
  className?: string;
  /** 测试与定位用前缀：`<testId>`（文本）、`<testId>-size`、`<testId>-toggle`。 */
  testId: string;
}

// 审批面展示超长值：截断显示，标出总大小，可展开看全文。size 按 UTF-8 字节算，是
// 用户实际批准的负载大小，不是字符数。
export function TruncatedText({ text, className = "", testId }: TruncatedTextProps) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);
  const long = text.length > TRUNCATE_LIMIT;
  const shown = long && !expanded ? text.slice(0, TRUNCATE_LIMIT) : text;
  return (
    <>
      <span data-testid={testId} className={`select-text whitespace-pre-wrap break-all ${className}`}>
        {shown}
        {long && !expanded && "…"}
      </span>
      {long && (
        <span className="ml-1 whitespace-nowrap text-muted-foreground">
          <span data-testid={`${testId}-size`}>
            {t("ai.approvalValueSize", { size: formatBytes(new TextEncoder().encode(text).length) })}
          </span>
          <button
            type="button"
            data-testid={`${testId}-toggle`}
            className="ml-1 cursor-pointer underline"
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? t("ai.approvalValueCollapse") : t("ai.approvalValueExpand")}
          </button>
        </span>
      )}
    </>
  );
}
