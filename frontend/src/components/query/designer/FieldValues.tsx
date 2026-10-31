import { useTranslation } from "react-i18next";
import { KeyRound } from "lucide-react";
import type { DesignerDefault } from "./model";
import { NUMERIC_LITERAL } from "./format";

/** 默认值按写法显示:无为破折号,NULL 斜体,值按字面量(数字 / 加引号的文本),表达式带 ƒx,原文照登。 */
export function DefaultValue({ value }: { value: DesignerDefault }) {
  switch (value.kind) {
    case "none":
      return <span className="text-muted-foreground/50">—</span>;
    case "null":
      return <span className="text-syntax-null italic">NULL</span>;
    case "value":
      return NUMERIC_LITERAL.test(value.text) ? (
        <span className="text-syntax-number">{value.text}</span>
      ) : (
        <span className="text-syntax-string">&apos;{value.text}&apos;</span>
      );
    case "expression":
      return (
        <span className="inline-flex min-w-0 items-center gap-1">
          <span className="rounded bg-info/15 px-1 font-sans text-[10px] text-info">ƒx</span>
          <span className="truncate">{value.text}</span>
        </span>
      );
    case "text":
      return <span>{value.text}</span>;
  }
}

/** 主键格子:主键列是暖色钥匙,其余淡化。 */
export function PrimaryKeyMark({ primaryKey }: { primaryKey: boolean }) {
  const { t } = useTranslation();
  return primaryKey ? (
    <KeyRound className="h-3.5 w-3.5 text-warning" aria-label={t("query.designer.column.primaryKey")} />
  ) : (
    <KeyRound className="h-3.5 w-3.5 text-muted-foreground/20" aria-hidden />
  );
}

export function AutoIncrementBadge() {
  const { t } = useTranslation();
  return (
    <span className="shrink-0 rounded bg-muted px-1 font-sans text-[10px] text-muted-foreground">
      {t("query.designer.autoIncrement")}
    </span>
  );
}
