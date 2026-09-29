import { useTranslation } from "react-i18next";

interface ApprovalClassificationProps {
  action?: string;
  resource?: string;
  /** 这次调用触及的全部资源（扩展返回的原样）；缺省时退回单个 resource。 */
  resources?: string[];
  /** 容器排版（字号、颜色）随所在审批面而定。 */
  className: string;
  /** 「动作 / 资源」标签的强调色随所在审批面而定。 */
  labelClassName: string;
}

// 扩展类型的审批项才带 action：check_policy 给出的分类，展示在命令上方，让"批准"批的是
// 一个可读的动作 + 资源，而不只是一串不透明的 exec 文本。AI 会话审批块与 opsctl 审批弹窗共用。
export function ApprovalClassification({
  action,
  resource,
  resources,
  className,
  labelClassName,
}: ApprovalClassificationProps) {
  const { t } = useTranslation();
  if (!action) return null;
  const list = resources?.length ? resources : resource ? [resource] : [];
  return (
    <div className={`flex flex-wrap items-center gap-x-3 gap-y-0.5 ${className}`}>
      <span className="text-muted-foreground">
        <span className={`font-medium ${labelClassName}`}>{t("ai.approvalActionLabel")}</span>
        <span className="select-text">{action}</span>
      </span>
      {list.length > 0 && (
        <span className="text-muted-foreground">
          <span className={`font-medium ${labelClassName}`}>
            {list.length > 1 ? t("ai.approvalResourcesLabel", { count: list.length }) : t("ai.approvalResourceLabel")}
          </span>
          {list.map((r, i) => (
            <span key={i}>
              {i > 0 && ", "}
              <span data-testid="approval-resource" className="select-text">
                {r}
              </span>
            </span>
          ))}
        </span>
      )}
    </div>
  );
}
