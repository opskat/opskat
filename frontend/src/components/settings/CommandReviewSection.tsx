import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from "@opskat/ui";
import { AlertTriangle, Loader2, Save, Settings2 } from "lucide-react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { failReasonLabel } from "@/lib/commandReview";
import { Segmented } from "@/components/asset/fields";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { GetCommandReviewSettings, SaveCommandReviewTimeout } from "../../../wailsjs/go/system/System";
import { GetCommandReviewState, SetCommandReviewer } from "../../../wailsjs/go/extension/Extension";
import type { extension_svc, system } from "../../../wailsjs/go/models";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

// 与 internal/app/system/command_review.go 的校验一致。
const TIMEOUT_MIN = 1000;
const TIMEOUT_MAX = 60000;

/**
 * 命令审核的设置（设置 › AI）。审核由审核扩展来做：这里显示实际在用的审核扩展，装了多个时选用
 * 哪个，没有可用的时说明原因；扩展自己的设置（API key、模型等）只在扩展详情里，这里给个入口。
 * 宿主侧只有调用超时，另有本次启动以来最近一次还没恢复的审核失败（之后审核成功就不再显示）。测试在扩展设置里（保存并测试）。
 */
export function CommandReviewSection() {
  const { t } = useTranslation();
  const [state, setState] = useState<extension_svc.CommandReviewState | null>(null);
  const [settings, setSettings] = useState<system.CommandReviewSettings | null>(null);
  const [timeoutMs, setTimeoutMs] = useState("");
  const [saving, setSaving] = useState(false);

  const applySettings = useCallback((s: system.CommandReviewSettings) => {
    setSettings(s);
    setTimeoutMs(String(s.timeoutMs));
  }, []);
  const reloadState = useCallback(() => GetCommandReviewState().then(setState), []);

  useEffect(() => {
    GetCommandReviewState()
      .then(setState)
      .catch((e: unknown) => toast.error(errMsg(e)));
    GetCommandReviewSettings()
      .then(applySettings)
      .catch((e: unknown) => toast.error(errMsg(e)));
  }, [applySettings]);

  const selectReviewer = async (name: string) => {
    try {
      await SetCommandReviewer(name);
      await reloadState();
      notifySuccess(t("commandReview.settings.reviewerSaved"));
    } catch (e: unknown) {
      toast.error(errMsg(e));
    }
  };

  const saveTimeout = async () => {
    const ms = Number(timeoutMs);
    if (!Number.isInteger(ms) || ms < TIMEOUT_MIN || ms > TIMEOUT_MAX) {
      toast.error(t("commandReview.settings.timeoutRange", { min: TIMEOUT_MIN, max: TIMEOUT_MAX }));
      return;
    }
    setSaving(true);
    try {
      await SaveCommandReviewTimeout(ms);
      applySettings(await GetCommandReviewSettings());
      notifySuccess(t("commandReview.settings.saved"));
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setSaving(false);
    }
  };

  if (!state || !settings) {
    return (
      <Card>
        <CardContent className="flex justify-center py-6">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </CardContent>
      </Card>
    );
  }

  const active = state.reviewers.find((r) => r.name === state.active);
  const label = (r: extension_svc.CommandReviewerInfo) =>
    r.usable && !r.signed ? `${r.displayName} · ${t("commandReview.settings.unsigned")}` : r.displayName;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("commandReview.settings.title")}</CardTitle>
        <CardDescription>{t("commandReview.settings.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-1.5">
          <Label>{t("commandReview.settings.reviewer")}</Label>
          {state.reviewers.length > 1 ? (
            <Segmented
              value={state.selected}
              onChange={selectReviewer}
              aria-label={t("commandReview.settings.reviewer")}
              options={state.reviewers.map((r) => ({
                value: r.name,
                label: label(r),
                disabled: !r.usable,
                testid: `command-reviewer-${r.name}`,
              }))}
            />
          ) : (
            active && (
              <p data-testid="command-reviewer-active" className="text-sm">
                {label(active)}
              </p>
            )
          )}
          {active && (
            <div>
              <Button
                variant="outline"
                size="sm"
                className="gap-1"
                onClick={() => useSettingsUiStore.getState().openExtensionDetail(active.name)}
              >
                <Settings2 className="h-3.5 w-3.5" />
                {t("commandReview.settings.configureReviewer")}
              </Button>
            </div>
          )}
          {state.reviewers
            .filter((r) => !r.usable)
            .map((r) => (
              <p key={r.name} data-testid="command-reviewer-unusable" className="text-xs text-warning">
                {t("commandReview.settings.reviewerUnusable", { name: r.displayName, reason: r.reason })}
              </p>
            ))}
          {state.problem && (
            <div
              data-testid="command-review-problem"
              className="flex items-start gap-1.5 rounded-md bg-warning/15 px-2.5 py-2 text-xs text-foreground"
            >
              <AlertTriangle aria-hidden className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
              <span>
                {state.reviewers.length === 0
                  ? t("commandReview.settings.noReviewer")
                  : state.reviewers.length > 1 && !state.selected
                    ? t("commandReview.settings.selectReviewer")
                    : failReasonLabel(t, state.problem)}
              </span>
            </div>
          )}
        </div>

        <div className="grid gap-1.5 sm:max-w-xs">
          <Label htmlFor="command-review-timeout">{t("commandReview.settings.timeout")}</Label>
          <div className="flex gap-2">
            <Input
              id="command-review-timeout"
              type="number"
              min={TIMEOUT_MIN}
              max={TIMEOUT_MAX}
              step={500}
              value={timeoutMs}
              onChange={(e) => setTimeoutMs(e.target.value)}
            />
            <Button onClick={saveTimeout} disabled={saving} variant="outline" className="gap-1 shrink-0">
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
              {t("commandReview.settings.save")}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t("commandReview.settings.timeoutHint")}</p>
        </div>

        {settings.lastFailReason && (
          <div
            data-testid="command-review-last-fail"
            className="flex items-start gap-1.5 rounded-md bg-warning/15 px-2.5 py-2 text-xs text-foreground"
          >
            <AlertTriangle aria-hidden className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
            <span>
              {t("commandReview.settings.lastFail", {
                reason: failReasonLabel(t, settings.lastFailReason),
                time: new Date(settings.lastFailAt).toLocaleString(),
              })}
            </span>
          </div>
        )}

        <p className="text-xs text-muted-foreground">{t("commandReview.settings.notice")}</p>
      </CardContent>
    </Card>
  );
}
