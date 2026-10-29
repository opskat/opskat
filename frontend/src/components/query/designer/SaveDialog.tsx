import { useTranslation } from "react-i18next";
import { CircleAlert, Copy, Loader2 } from "lucide-react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
} from "@opskat/ui";
import { notifyCopied } from "@/lib/notify";
import { statementsScript } from "./format";
import { DropWarning, StatementList, type StatementItem, type StatementStatus } from "./StatementList";
import type { SaveFlowState, SavePlan } from "./useSaveFlow";

interface SaveDialogProps {
  state: SaveFlowState<SavePlan>;
  /** 驱动声明了表结构语句可回滚:整批在一个事务里执行。 */
  transactional: boolean;
  /** 停在中途的结果视图下方的说明:草稿之后怎样了(设计表重新读取了表结构,新建表的草稿留在原处)。 */
  partialNote: string;
  onConfirm: () => void;
  onClose: () => void;
}

/**
 * 保存的确认与结果:确认时列出全部语句(删除列或索引时红色警告、确认按钮为危险样式),执行中不能操作;
 * 之后按结局显示整批回滚、逐条结果或没能执行的错误。
 */
export function SaveDialog({ state, transactional, partialNote, onConfirm, onClose }: SaveDialogProps) {
  const { t } = useTranslation();
  if (state.phase === "idle") return null;
  const { plan } = state;
  const asking = state.phase === "confirm" || state.phase === "running";
  const running = state.phase === "running";
  const destructive = plan.droppedColumns.length > 0 || plan.droppedIndexes.length > 0;

  let title = plan.title;
  let description: string;
  let items: StatementItem[] = plan.statements.map((statement) => ({ statement }));
  if (state.phase === "rolledBack" || state.phase === "partial") {
    const results = state.result.statements;
    const failedAt = results.findIndex((r) => r.status === "failed");
    items = results.map((r) => ({
      statement: r.statement,
      // 回滚后已执行的语句等于没执行:标成已回滚。
      status: (state.phase === "rolledBack" && r.status === "executed" ? "rolledBack" : r.status) as StatementStatus,
      error: r.error,
    }));
    if (state.phase === "rolledBack") {
      title = t("query.designer.save.rolledBackTitle");
      description = t("query.designer.save.rolledBackDesc", { index: failedAt + 1 });
    } else {
      title = t("query.designer.save.partialTitle");
      description =
        failedAt === 0
          ? t("query.designer.save.partialDescNone")
          : t("query.designer.save.partialDesc", { index: failedAt + 1, executed: failedAt });
    }
  } else if (state.phase === "failed") {
    title = t("query.designer.save.failedTitle");
    description = t("query.designer.save.failedDesc");
  } else {
    description = transactional
      ? t("query.designer.save.confirmDescAtomic", { count: plan.statements.length })
      : t("query.designer.save.confirmDesc", { count: plan.statements.length });
  }

  const copy = () =>
    navigator.clipboard.writeText(statementsScript(plan.statements)).then(
      () => notifyCopied(t("query.copied")),
      (err: unknown) => toast.error(String(err))
    );

  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent className="sm:max-w-2xl" onOverlayClick={running ? undefined : onClose}>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {asking && (
          <DropWarning
            testId="save-drop-warning"
            droppedColumns={plan.droppedColumns}
            droppedIndexes={plan.droppedIndexes}
            className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs"
          />
        )}
        {state.phase === "failed" && <ErrorBox error={state.error} />}
        <StatementList
          listTestId="save-statements"
          itemTestId="save-statement"
          items={items}
          className="max-h-[320px] overflow-auto"
        />
        {state.phase === "partial" && (
          <>
            {/* 有失败又没有回滚、批本身还报了错:回滚失败,表结构可能停在中途。 */}
            {state.result.error && <ErrorBox error={state.result.error} />}
            <p className="text-xs text-muted-foreground">{partialNote}</p>
          </>
        )}
        <AlertDialogFooter>
          <Button variant="outline" size="sm" className="mr-auto h-7 text-xs" disabled={running} onClick={copy}>
            <Copy className="h-3.5 w-3.5" aria-hidden />
            {t("action.copy")}
          </Button>
          {asking ? (
            <>
              <Button variant="outline" size="sm" className="h-7 text-xs" disabled={running} onClick={onClose}>
                {t("action.cancel")}
              </Button>
              <Button
                variant={destructive ? "destructive" : "default"}
                size="sm"
                className="h-7 text-xs"
                disabled={running}
                onClick={onConfirm}
              >
                {running && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />}
                {running
                  ? t("query.designer.save.running")
                  : t("query.designer.save.run", { count: plan.statements.length })}
              </Button>
            </>
          ) : (
            <Button size="sm" className="h-7 text-xs" onClick={onClose}>
              {t("query.designer.save.back")}
            </Button>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function ErrorBox({ error }: { error: string }) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive"
    >
      <CircleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden />
      <span className="font-mono break-all whitespace-pre-wrap select-text">{error}</span>
    </div>
  );
}
