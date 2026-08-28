import { Bot, CircleAlert, Command, Monitor, Radio, Terminal } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  cn,
} from "@opskat/ui";

export type QuitActivityKind = "ai" | "opsctl" | "terminal" | "rdp" | "vnc";

export interface QuitActivity {
  kind: QuitActivityKind;
  category: "running" | "connection";
  title: string;
  detail?: string;
}

interface ActiveTasksQuitDialogProps {
  open: boolean;
  activities: QuitActivity[];
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}

const activityIcons = {
  ai: Bot,
  opsctl: Command,
  terminal: Terminal,
  rdp: Monitor,
  vnc: Radio,
};

function ActivityRow({ activity }: { activity: QuitActivity }) {
  const { t } = useTranslation();
  const Icon = activityIcons[activity.kind];
  const running = activity.category === "running";
  return (
    <div className="grid grid-cols-[2.25rem_minmax(0,1fr)_auto] items-center gap-3 rounded-lg border bg-card p-3">
      <div
        className={cn(
          "flex size-9 items-center justify-center rounded-md bg-info/10 text-info",
          running && "bg-destructive/10 text-destructive"
        )}
      >
        <Icon className="size-4" />
      </div>
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm font-medium text-foreground">{activity.title}</span>
          <span className="shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
            {t(`appQuit.kind.${activity.kind}`)}
          </span>
        </div>
        {activity.detail && <div className="mt-1 truncate text-xs text-muted-foreground">{activity.detail}</div>}
      </div>
      <div className={cn("flex items-center gap-1.5 text-xs font-medium text-info", running && "text-destructive")}>
        <span className="size-1.5 rounded-full bg-current ring-3 ring-current/10" />
        {t(running ? "appQuit.runningStatus" : "appQuit.connectedStatus")}
      </div>
    </div>
  );
}

export function ActiveTasksQuitDialog({ open, activities, onOpenChange, onConfirm }: ActiveTasksQuitDialogProps) {
  const { t } = useTranslation();
  const running = activities.filter((activity) => activity.category === "running");
  const connections = activities.filter((activity) => activity.category === "connection");

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className="max-h-[calc(100vh-4.5rem)] gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <AlertDialogHeader className="grid-cols-[2.5rem_minmax(0,1fr)] gap-x-3 gap-y-0 p-6 pb-4 text-left">
          <div className="row-span-2 flex size-10 items-center justify-center rounded-full bg-destructive/10 text-destructive">
            <CircleAlert className="size-5" />
          </div>
          <AlertDialogTitle>{t("appQuit.activityTitle")}</AlertDialogTitle>
          <div className="mt-1 text-sm text-muted-foreground">
            {t("appQuit.activitySummary", { running: running.length, connections: connections.length })}
          </div>
        </AlertDialogHeader>

        <div className="min-h-0 space-y-4 overflow-y-auto px-6 pb-1">
          {running.length > 0 && (
            <section>
              <h3 className="mb-2 text-xs font-semibold text-muted-foreground">{t("appQuit.runningGroup")}</h3>
              <div className="space-y-2">
                {running.map((activity, index) => (
                  <ActivityRow key={`${activity.kind}-${index}`} activity={activity} />
                ))}
              </div>
            </section>
          )}
          {connections.length > 0 && (
            <section>
              <h3 className="mb-2 text-xs font-semibold text-muted-foreground">{t("appQuit.connectionGroup")}</h3>
              <div className="space-y-2">
                {connections.map((activity, index) => (
                  <ActivityRow key={`${activity.kind}-${index}`} activity={activity} />
                ))}
              </div>
            </section>
          )}
        </div>

        <AlertDialogFooter className="p-6 pt-5">
          <AlertDialogCancel>{t("action.cancel")}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" data-testid="confirm-force-quit" onClick={onConfirm}>
            {t("appQuit.quitActivities", { count: activities.length })}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
