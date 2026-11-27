import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Label,
} from "@opskat/ui";
import { GroupSelect } from "@/components/asset/GroupSelect";

interface MoveAssetsDialogProps {
  open: boolean;
  count: number;
  initialGroupId?: number;
  onOpenChange: (open: boolean) => void;
  onConfirm: (targetGroupId: number) => Promise<boolean>;
}

/**
 * 批量移动资产到目标分组的对话框，复用共享组件 GroupSelect（支持任意分组或未分组）。
 */
export function MoveAssetsDialog({ open, count, initialGroupId = 0, onOpenChange, onConfirm }: MoveAssetsDialogProps) {
  const { t } = useTranslation();
  const [targetGroupId, setTargetGroupId] = useState(initialGroupId);
  const [saving, setSaving] = useState(false);

  const [prevOpen, setPrevOpen] = useState(false);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setTargetGroupId(initialGroupId);
  }

  const handleConfirm = async () => {
    setSaving(true);
    try {
      const ok = await onConfirm(targetGroupId);
      if (ok) onOpenChange(false);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        data-testid="move-assets-dialog"
        className="sm:max-w-sm"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{t("asset.moveToGroupTitle")}</DialogTitle>
          <DialogDescription>{t("asset.moveToGroupDesc", { count })}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 py-2">
          <Label>{t("asset.group")}</Label>
          <GroupSelect
            value={targetGroupId}
            onValueChange={setTargetGroupId}
            placeholder={t("asset.ungrouped")}
            testId="move-assets-group-select"
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            {t("action.cancel")}
          </Button>
          <Button data-testid="move-assets-submit" onClick={handleConfirm} disabled={saving}>
            {t("asset.moveToGroupAction")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
