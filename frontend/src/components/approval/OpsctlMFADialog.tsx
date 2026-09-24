import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Button } from "@opskat/ui";
import { ShieldCheck } from "lucide-react";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { AuthChallengeForm } from "@/components/terminal/AuthChallengeForm";
import { RespondOpsctlMFA, CancelOpsctlMFA } from "../../../wailsjs/go/opsctl/Opsctl";

interface MFAChallengeEvent {
  challenge_id: string;
  asset_id: number;
  asset_name: string;
  name: string;
  instruction: string;
  prompts: string[];
  echo: boolean[];
}

/**
 * opsctl 在不可交互时把 SSH MFA 挑战经 approval.sock 转给桌面端（opsctl:mfa）。并发的
 * 挑战（如 batch 里多个资产）排队逐个展示；请求方 opsctl 退出时后端发 opsctl:mfa-closed，
 * 对应挑战直接移除。答案只在表单本地 state 里，提交或取消即丢弃。
 */
export function OpsctlMFADialog() {
  const { t } = useTranslation();
  const [queue, setQueue] = useState<MFAChallengeEvent[]>([]);
  const current = queue[0];

  const remove = useCallback((id: string) => {
    setQueue((q) => q.filter((c) => c.challenge_id !== id));
  }, []);

  useWailsEvent(
    "opsctl:mfa",
    useCallback((data: MFAChallengeEvent) => {
      setQueue((q) => [...q, data]);
    }, [])
  );
  useWailsEvent(
    "opsctl:mfa-closed",
    useCallback((data: { challenge_id: string }) => remove(data.challenge_id), [remove])
  );

  const cancel = () => {
    if (!current) return;
    CancelOpsctlMFA(current.challenge_id);
    remove(current.challenge_id);
  };

  const submit = async (answers: string[]) => {
    if (!current) return;
    const id = current.challenge_id;
    remove(id);
    try {
      await RespondOpsctlMFA(id, answers);
    } catch (e) {
      toast.error(String(e));
    }
  };

  return (
    <Dialog open={!!current} onOpenChange={(open) => !open && cancel()}>
      <DialogContent className="sm:max-w-sm">
        {current && (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <ShieldCheck className="h-4 w-4 text-warning" />
                {t("opsctlMfa.title")}
              </DialogTitle>
              <DialogDescription>{t("opsctlMfa.description")}</DialogDescription>
            </DialogHeader>
            <div className="text-sm font-medium">{current.asset_name}</div>
            <AuthChallengeForm
              key={current.challenge_id}
              prompts={current.prompts}
              echo={current.echo}
              name={current.name}
              instruction={current.instruction}
              visible
              onSubmit={submit}
              onCancel={cancel}
            />
            <DialogFooter>
              <Button variant="outline" size="sm" onClick={cancel}>
                {t("action.cancel")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
