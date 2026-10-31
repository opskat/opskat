import { useTranslation } from "react-i18next";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
} from "@opskat/ui";

interface CloseGuardDialogProps {
  open: boolean;
  /** 库.表 */
  name: string;
  /** 草稿里的改动处数(字段与表名、表注释)。 */
  changes: number;
  /** 草稿能保存时「保存…」才可点(有问题或语句还没生成时不可点)。 */
  canSave: boolean;
  onStay: () => void;
  onDiscard: () => void;
  onSave: () => void;
  /** 新建表:name 是目标库,文案说的是还没创建的表。 */
  creating?: boolean;
}

/** 关闭有未保存改动的设计器标签时的拦截:取消 / 放弃并关闭 / 保存…(同表数据标签的关闭拦截)。 */
export function CloseGuardDialog({
  open,
  name,
  changes,
  canSave,
  onStay,
  onDiscard,
  onSave,
  creating = false,
}: CloseGuardDialogProps) {
  const { t } = useTranslation();
  return (
    <AlertDialog open={open} onOpenChange={(next) => !next && onStay()}>
      <AlertDialogContent className="sm:max-w-md">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {creating ? t("query.designer.guard.createTitle", { name }) : t("query.designer.guard.title", { name })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {creating ? t("query.designer.guard.createDesc") : t("query.designer.guard.desc", { count: changes })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <Button variant="outline" size="sm" className="h-7 text-xs" onClick={onStay}>
            {t("action.cancel")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-7 text-xs text-destructive hover:text-destructive"
            onClick={onDiscard}
          >
            {t("query.unsavedEditsDiscardClose")}
          </Button>
          <Button size="sm" className="h-7 text-xs" disabled={!canSave} onClick={onSave}>
            {creating ? t("query.designer.guard.create") : t("query.designer.guard.save")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
