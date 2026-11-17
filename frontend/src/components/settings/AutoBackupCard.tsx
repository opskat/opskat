import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { CheckCircle2, Clock, Cloud, FolderOpen, Github, History, Loader2, Save, Upload, XCircle } from "lucide-react";
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Checkbox,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  cn,
} from "@opskat/ui";
import { AsyncState, type AsyncStatus } from "@/components/common/AsyncState";
import { SecretInput } from "@/components/SecretInput";
import { notifySuccess } from "@/lib/notify";
import { errorMessage } from "@/lib/errorMessage";
import { formatRelativeTime } from "@/lib/relativeTime";
import {
  GetAutoBackupSettings,
  GetAutoBackupStatus,
  RunAutoBackupNow,
  SaveAutoBackupSettings,
  SelectAutoBackupDirectory,
} from "../../../wailsjs/go/system/System";
import { system } from "../../../wailsjs/go/models";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

const INTERVAL_CHOICES = [5, 30, 60, 1440] as const;
const KEEP_CHOICES = [5, 10, 30] as const;

/** 下方手动备份卡片的锚点，用于「前往下方配置 / 登录」。 */
export const BACKUP_WEBDAV_CARD_ID = "backup-webdav-card";
export const BACKUP_GIST_CARD_ID = "backup-gist-card";

type TargetId = "webdav" | "gist" | "local";
const TARGET_ORDER: TargetId[] = ["webdav", "gist", "local"];

interface Form {
  enabled: boolean;
  password: string;
  intervalMinutes: number;
  keep: number;
  targets: string[];
  localDir: string;
}

interface FieldErrors {
  password?: boolean;
  targets?: boolean;
  localDir?: boolean;
}

function formFromSettings(s: system.AutoBackupSettings): Form {
  return {
    enabled: s.enabled,
    password: "",
    intervalMinutes: s.intervalMinutes,
    keep: s.keep,
    targets: s.targets ?? [],
    localDir: s.localDir,
  };
}

function scrollToCard(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
}

export function AutoBackupCard() {
  const { t, i18n } = useTranslation();
  const [load, setLoad] = useState<{ status: AsyncStatus; error: string | null }>({ status: "loading", error: null });
  const [saved, setSaved] = useState<system.AutoBackupSettings | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [status, setStatus] = useState<system.AutoBackupStatus | null>(null);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [saving, setSaving] = useState(false);
  const [runningNow, setRunningNow] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // setState 全在异步续体里（挂载时直接在 effect 中调用）。
  const loadAll = useCallback(
    () =>
      Promise.all([GetAutoBackupSettings(), GetAutoBackupStatus()])
        .then(([s, st]) => {
          setSaved(s);
          setForm(formFromSettings(s));
          setStatus(st);
          setLoad({ status: "ready", error: null });
        })
        .catch((e: unknown) => setLoad({ status: "error", error: errorMessage(e) })),
    []
  );

  const refreshStatus = useCallback(() => {
    GetAutoBackupStatus()
      .then((st) => {
        setStatus(st);
        setNow(Date.now());
      })
      .catch((e) => console.error("load auto backup status failed:", e));
  }, []);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  useEffect(() => EventsOn("autobackup:status", refreshStatus), [refreshStatus]);

  // 相对时间（「N 分钟前」「N 分钟后重试」）每 30 秒刷新一次。
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(id);
  }, []);

  const dirty = useMemo(() => {
    if (!saved || !form) return false;
    const base = formFromSettings(saved);
    return (
      form.password !== "" ||
      form.enabled !== base.enabled ||
      form.intervalMinutes !== base.intervalMinutes ||
      form.keep !== base.keep ||
      form.localDir !== base.localDir ||
      form.targets.join(",") !== base.targets.join(",")
    );
  }, [saved, form]);

  const available = useMemo(() => {
    const out: Record<string, boolean> = {};
    for (const ts of status?.targets ?? []) out[ts.id] = ts.available;
    // 本地文件夹的可用性跟随表单里尚未保存的路径。
    out.local = !!form?.localDir;
    return out;
  }, [status, form?.localDir]);

  const update = (patch: Partial<Form>) => {
    setForm((f) => (f ? { ...f, ...patch } : f));
    setJustSaved(false);
    setErrors((e) => ({
      ...e,
      ...("password" in patch ? { password: false } : {}),
      ...("targets" in patch || "localDir" in patch ? { targets: false, localDir: false } : {}),
    }));
  };

  const toggleTarget = (id: string, checked: boolean) => {
    if (!form) return;
    const next = checked ? [...form.targets, id] : form.targets.filter((x) => x !== id);
    update({ targets: TARGET_ORDER.filter((x) => next.includes(x)) });
  };

  const pickDirectory = async () => {
    try {
      const dir = await SelectAutoBackupDirectory();
      if (!dir || !form) return;
      update({ localDir: dir, targets: form.targets.includes("local") ? form.targets : [...form.targets, "local"] });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const validate = (f: Form): FieldErrors => {
    const next: FieldErrors = {};
    if (f.targets.includes("local") && !f.localDir) next.localDir = true;
    if (f.enabled) {
      if (!saved?.passwordSet && !f.password) next.password = true;
      if (!f.targets.some((id) => available[id])) next.targets = true;
    }
    return next;
  };

  const handleSave = async () => {
    if (!form) return;
    const fieldErrors = validate(form);
    setErrors(fieldErrors);
    if (Object.values(fieldErrors).some(Boolean)) return;
    setSaving(true);
    try {
      await SaveAutoBackupSettings(
        new system.AutoBackupSaveInput({
          enabled: form.enabled,
          password: form.password,
          intervalMinutes: form.intervalMinutes,
          keep: form.keep,
          targets: form.targets,
          localDir: form.localDir,
        })
      );
      notifySuccess(t("autoBackup.saved"));
      const [s, st] = await Promise.all([GetAutoBackupSettings(), GetAutoBackupStatus()]);
      setSaved(s);
      setForm(formFromSettings(s));
      setStatus(st);
      setNow(Date.now());
      setJustSaved(true);
    } catch (e) {
      toast.error(`${t("autoBackup.saveFailed")}: ${errorMessage(e)}`);
    } finally {
      setSaving(false);
    }
  };

  const handleRunNow = async () => {
    setRunningNow(true);
    setJustSaved(false);
    try {
      await RunAutoBackupNow();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setRunningNow(false);
      refreshStatus();
    }
  };

  const rel = (unix: number) => formatRelativeTime(unix, i18n.language, now);
  const busy = runningNow || !!status?.running;

  return (
    <Card data-testid="auto-backup-card">
      <CardHeader>
        <div className="flex items-start justify-between gap-4">
          <div className="grid gap-1.5">
            <CardTitle className="text-base flex items-center gap-1.5">
              <History className="h-4 w-4" />
              {t("autoBackup.title")}
            </CardTitle>
            <CardDescription>{t("autoBackup.desc")}</CardDescription>
          </div>
          {form && (
            <Switch
              checked={form.enabled}
              onCheckedChange={(v) => update({ enabled: v })}
              aria-label={t("autoBackup.enable")}
            />
          )}
        </div>
      </CardHeader>
      {(load.status !== "ready" || form?.enabled || dirty) && (
        <CardContent className="space-y-5">
          <AsyncState status={load.status} error={load.error} onRetry={() => void loadAll()}>
            {form && saved && (
              <>
                {form.enabled && (
                  <>
                    <div className="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                      {t("autoBackup.contents")}
                    </div>

                    <div className="grid gap-1.5">
                      <Label htmlFor="auto-backup-password">{t("autoBackup.password")}</Label>
                      <SecretInput
                        id="auto-backup-password"
                        value={form.password}
                        onChange={(e) => update({ password: e.target.value })}
                        placeholder={
                          saved.passwordSet
                            ? t("autoBackup.passwordSavedPlaceholder")
                            : t("autoBackup.passwordPlaceholder")
                        }
                        aria-invalid={errors.password || undefined}
                        className={cn(errors.password && "border-destructive")}
                      />
                      {errors.password ? (
                        <p className="text-xs text-destructive">{t("autoBackup.passwordRequired")}</p>
                      ) : (
                        <p className="text-xs text-muted-foreground">{t("autoBackup.passwordHint")}</p>
                      )}
                    </div>

                    <div className="grid gap-3 sm:grid-cols-2">
                      <div className="grid gap-1.5">
                        <Label>{t("autoBackup.interval")}</Label>
                        <Select
                          value={String(form.intervalMinutes)}
                          onValueChange={(v) => update({ intervalMinutes: Number(v) })}
                        >
                          <SelectTrigger aria-label={t("autoBackup.interval")}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {INTERVAL_CHOICES.map((m) => (
                              <SelectItem key={m} value={String(m)}>
                                {t(`autoBackup.intervalOption.${m}`)}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>
                      <div className="grid gap-1.5">
                        <Label>{t("autoBackup.keep")}</Label>
                        <Select value={String(form.keep)} onValueChange={(v) => update({ keep: Number(v) })}>
                          <SelectTrigger aria-label={t("autoBackup.keep")}>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {KEEP_CHOICES.map((n) => (
                              <SelectItem key={n} value={String(n)}>
                                {t("autoBackup.keepOption", { count: n })}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </div>
                    </div>

                    <div className="grid gap-2">
                      <Label>{t("autoBackup.targets")}</Label>
                      <div className="divide-y rounded-md border">
                        {TARGET_ORDER.map((id) => {
                          const ts = status?.targets.find((x) => x.id === id);
                          const isAvailable = !!available[id];
                          const checked = form.targets.includes(id);
                          return (
                            <TargetRow
                              key={id}
                              id={id}
                              checked={checked}
                              disabled={!isAvailable && id !== "local"}
                              onCheckedChange={(v) => toggleTarget(id, v)}
                              status={checked && isAvailable && ts ? <TargetStatusBadge ts={ts} rel={rel} /> : null}
                              error={
                                checked && isAvailable && !ts?.phase && ts?.errorCode
                                  ? { code: ts.errorCode, detail: ts.errorDetail }
                                  : undefined
                              }
                            >
                              <TargetDetail
                                id={id}
                                available={isAvailable}
                                webdavUrl={status?.webdavUrl ?? ""}
                                githubUser={status?.githubUser ?? ""}
                                localDir={form.localDir}
                                localDirError={errors.localDir}
                                onPickDirectory={pickDirectory}
                              />
                            </TargetRow>
                          );
                        })}
                      </div>
                      {errors.targets && <p className="text-xs text-destructive">{t("autoBackup.targetsRequired")}</p>}
                    </div>
                  </>
                )}

                <div className="flex flex-wrap items-center gap-2">
                  <Button variant="outline" className="gap-1" disabled={!dirty || saving} onClick={handleSave}>
                    {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                    {t("autoBackup.save")}
                  </Button>
                  {saved.enabled && (
                    <Button variant="outline" className="gap-1" disabled={dirty || busy} onClick={handleRunNow}>
                      {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Upload className="h-4 w-4" />}
                      {busy ? t("autoBackup.running") : t("autoBackup.runNow")}
                    </Button>
                  )}
                  <span className="flex-1" />
                  {saved.enabled && !dirty && status && (
                    <span
                      className="flex items-center gap-1 text-xs text-muted-foreground"
                      data-testid="auto-backup-summary"
                    >
                      <Clock className="h-3.5 w-3.5" />
                      <CheckSummary status={status} justSaved={justSaved} busy={busy} rel={rel} />
                    </span>
                  )}
                </div>
                {dirty && saved.enabled && (
                  <p className="text-xs text-muted-foreground">{t("autoBackup.unsavedHint")}</p>
                )}
              </>
            )}
          </AsyncState>
        </CardContent>
      )}
    </Card>
  );
}

function CheckSummary({
  status,
  justSaved,
  busy,
  rel,
}: {
  status: system.AutoBackupStatus;
  justSaved: boolean;
  busy: boolean;
  rel: (unix: number) => string;
}) {
  const { t } = useTranslation();
  if (busy) return <>{t("autoBackup.summary.running")}</>;
  if (justSaved) {
    return <>{t("autoBackup.summary.saved", { next: status.nextCheckAt ? rel(status.nextCheckAt) : "-" })}</>;
  }
  if (!status.lastCheckAt) return <>{t("autoBackup.summary.never")}</>;
  const time = rel(status.lastCheckAt);
  switch (status.lastCheckResult) {
    case "unchanged":
      return <>{t("autoBackup.summary.unchanged", { time })}</>;
    case "written":
      return <>{t("autoBackup.summary.written", { time })}</>;
    default:
      return (
        <>
          {status.lastCheckErrorCode
            ? t("autoBackup.summary.failedWithError", {
                time,
                error: [t(`autoBackup.error.${status.lastCheckErrorCode}`), status.lastCheckErrorDetail]
                  .filter(Boolean)
                  .join(": "),
              })
            : t("autoBackup.summary.failed", { time })}
        </>
      );
  }
}

function TargetRow({
  id,
  checked,
  disabled,
  onCheckedChange,
  status,
  error,
  children,
}: {
  id: TargetId;
  checked: boolean;
  disabled: boolean;
  onCheckedChange: (v: boolean) => void;
  status: ReactNode;
  /** 失败码（按界面语言翻译）与原始细节（原样显示）。 */
  error?: { code: string; detail: string };
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const Icon = id === "webdav" ? Cloud : id === "gist" ? Github : FolderOpen;
  const name = t(`autoBackup.target.${id}`);
  return (
    <div className={cn("flex gap-3 px-3 py-2.5", disabled && "bg-muted/20")} data-testid={`auto-backup-target-${id}`}>
      <Checkbox
        checked={checked}
        disabled={disabled}
        onCheckedChange={(v) => onCheckedChange(v === true)}
        className="mt-0.5"
        aria-label={name}
      />
      <div className="min-w-0 flex-1 grid gap-0.5">
        <div className="flex items-center gap-2">
          <Icon className={cn("h-3.5 w-3.5 shrink-0", disabled && "text-muted-foreground")} />
          <span className={cn("text-sm font-medium", disabled && "text-muted-foreground")}>{name}</span>
          <span className="flex-1" />
          {status}
        </div>
        <div className="min-w-0 break-all text-xs text-muted-foreground">{children}</div>
        {error && (
          <div className="mt-1 grid gap-0.5 rounded bg-destructive/10 px-2 py-1 text-xs text-destructive">
            <span>{t(`autoBackup.error.${error.code}`)}</span>
            {error.detail && (
              <span className="whitespace-pre-wrap break-words font-mono select-text">{error.detail}</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function TargetDetail({
  id,
  available,
  webdavUrl,
  githubUser,
  localDir,
  localDirError,
  onPickDirectory,
}: {
  id: TargetId;
  available: boolean;
  webdavUrl: string;
  githubUser: string;
  localDir: string;
  localDirError?: boolean;
  onPickDirectory: () => void;
}) {
  const { t } = useTranslation();
  if (id === "local") {
    return (
      <div className="grid gap-1 pt-1">
        <div className="flex items-center gap-2">
          <Input
            readOnly
            value={localDir}
            placeholder={t("autoBackup.local.placeholder")}
            aria-invalid={localDirError || undefined}
            className={cn("h-8 flex-1 font-mono text-xs md:text-xs", localDirError && "border-destructive")}
          />
          <Button variant="outline" size="sm" className="gap-1" onClick={onPickDirectory}>
            <FolderOpen className="h-3.5 w-3.5" />
            {t("autoBackup.local.pick")}
          </Button>
        </div>
        {localDirError && <p className="text-xs text-destructive">{t("autoBackup.local.required")}</p>}
      </div>
    );
  }
  if (!available) {
    const cardId = id === "webdav" ? BACKUP_WEBDAV_CARD_ID : BACKUP_GIST_CARD_ID;
    return (
      <span>
        {t(`autoBackup.${id}.unavailable`)}
        <button
          type="button"
          className="text-primary underline-offset-2 hover:underline"
          onClick={() => scrollToCard(cardId)}
        >
          {t(`autoBackup.${id}.goConfigure`)}
        </button>
      </span>
    );
  }
  if (id === "webdav") return <span className="font-mono">{webdavUrl}</span>;
  return <span>{t("autoBackup.gist.detail", { user: githubUser })}</span>;
}

function TargetStatusBadge({ ts, rel }: { ts: system.AutoBackupTargetStatus; rel: (unix: number) => string }) {
  const { t } = useTranslation();
  if (ts.phase === "running") {
    return (
      <Badge kind="muted" icon={<Loader2 className="h-3.5 w-3.5 animate-spin" />}>
        {t("autoBackup.status.running")}
      </Badge>
    );
  }
  if (ts.phase === "queued") {
    return (
      <Badge kind="muted" icon={<Clock className="h-3.5 w-3.5" />}>
        {t("autoBackup.status.queued")}
      </Badge>
    );
  }
  if (ts.errorCode) {
    return (
      <Badge kind="error" icon={<XCircle className="h-3.5 w-3.5" />}>
        {ts.retryAt ? t("autoBackup.status.failedRetry", { time: rel(ts.retryAt) }) : t("autoBackup.status.failed")}
      </Badge>
    );
  }
  if (ts.lastSuccessAt) {
    return (
      <Badge kind="ok" icon={<CheckCircle2 className="h-3.5 w-3.5" />}>
        {rel(ts.lastSuccessAt)}
      </Badge>
    );
  }
  return null;
}

function Badge({ kind, icon, children }: { kind: "ok" | "error" | "muted"; icon: ReactNode; children: ReactNode }) {
  return (
    <span
      className={cn(
        "flex shrink-0 items-center gap-1 text-xs",
        kind === "ok" && "text-success",
        kind === "error" && "text-destructive",
        kind === "muted" && "text-muted-foreground"
      )}
    >
      {icon}
      {children}
    </span>
  );
}
