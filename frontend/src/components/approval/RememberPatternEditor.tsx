import { useTranslation } from "react-i18next";
import { Button, Input } from "@opskat/ui";
import { losesAction, type RememberableItem } from "./rememberPattern";

interface RememberPatternEditorProps {
  items: RememberableItem[];
  values: string[];
  onChange: (index: number, value: string) => void;
  /** 说明文字的字号随所在审批面而定。 */
  textClassName: string;
  /** 输入框的尺寸与底色随所在审批面而定。 */
  inputClassName: string;
  /** 给了就显示「不记住」：退出记住模式，回到普通的作答按钮。 */
  onCancel?: () => void;
}

// AI 会话审批块与 opsctl 审批弹窗共用的「记住」编辑器。
export function RememberPatternEditor({
  items,
  values,
  onChange,
  textClassName,
  inputClassName,
  onCancel,
}: RememberPatternEditorProps) {
  const { t } = useTranslation();
  const classified = items.some((item) => !!item.action);
  return (
    <div className="space-y-1.5 pt-1">
      <div className="flex items-center justify-between gap-2">
        <div className={`${textClassName} text-muted-foreground`}>
          {classified ? t("opsctlApproval.classificationPatternLabel") : t("opsctlApproval.patternLabel")}
        </div>
        {onCancel && (
          <Button
            data-testid="approval-remember-cancel"
            variant="ghost"
            size="sm"
            className="h-6 shrink-0 px-2 text-xs"
            onClick={onCancel}
          >
            {t("opsctlApproval.rememberCancel")}
          </Button>
        )}
      </div>
      {items.map((item, i) => {
        const invalid = losesAction(item, values[i]);
        return (
          <div key={i} className="space-y-1">
            <Input
              data-testid="approval-remember-pattern"
              value={values[i]}
              onChange={(e) => onChange(i, e.target.value)}
              aria-invalid={invalid}
              className={`font-mono ${inputClassName}`}
              placeholder={item.action ? `${item.action}:*` : t("opsctlApproval.patternPlaceholder")}
            />
            {invalid && (
              <div data-testid="approval-remember-pattern-error" className={`${textClassName} text-destructive`}>
                {t("opsctlApproval.classificationPatternActionRequired", { action: item.action })}
              </div>
            )}
          </div>
        );
      })}
      <div className="text-[10px] text-muted-foreground/70">
        {classified ? t("opsctlApproval.classificationPatternHint") : t("opsctlApproval.patternHint")}
      </div>
    </div>
  );
}
