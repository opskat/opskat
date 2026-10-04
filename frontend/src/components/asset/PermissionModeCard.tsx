import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "@opskat/ui";
import { AlertTriangle, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { Segmented, type SegmentedOption } from "@/components/asset/fields";
import { resolveInheritedPermissionMode, type EffectivePermissionMode, type PermissionMode } from "@/lib/commandReview";
import { openGroupDetail } from "@/lib/infoTab";
import { useAssetStore } from "@/stores/assetStore";
import { GetCommandReviewSettings } from "../../../wailsjs/go/system/System";

const MODES: PermissionMode[] = ["", "default", "assisted", "autopilot"];
const needsReview = (mode: EffectivePermissionMode) => mode === "assisted" || mode === "autopilot";

interface PermissionModeCardProps {
  /** 资产 / 分组自己的设置；空字符串表示沿用分组。 */
  value: string;
  /** 设置的是资产还是分组，决定沿用时的说法（所在分组 / 上级分组）。 */
  subject: "asset" | "group";
  /** 从这个分组开始往上沿用：资产传所在分组，分组传上级分组；0 表示没有。 */
  parentGroupId: number;
  /** 保存新的设置；卡片负责保存中的状态和成功 / 失败提示。 */
  onChange: (mode: PermissionMode) => Promise<void>;
}

/**
 * 权限模式：规则判断不了、原本要问人的命令怎么处理（默认 / 辅助审批 / Autopilot）。
 * 沿用时写明沿用的是哪个分组，点分组名打开它的详情；
 * 切换后实际生效的模式会变成需要模型审核的模式时，先确认再保存。
 */
export function PermissionModeCard({ value, subject, parentGroupId, onChange }: PermissionModeCardProps) {
  const { t } = useTranslation();
  const groups = useAssetStore((s) => s.groups);
  const { mode: inherited, from } = resolveInheritedPermissionMode(parentGroupId, groups);
  const [apiKeySet, setApiKeySet] = useState<boolean | null>(null);
  const [pending, setPending] = useState<PermissionMode | null>(null);
  const [saving, setSaving] = useState(false);

  const save = async (mode: PermissionMode) => {
    setSaving(true);
    try {
      await onChange(mode);
      notifySuccess(t("commandReview.mode.saved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  useEffect(() => {
    GetCommandReviewSettings()
      .then((s) => setApiKeySet(s.apiKeySet))
      .catch((e: unknown) => toast.error(e instanceof Error ? e.message : String(e)));
  }, []);

  const current = value as PermissionMode;
  const effective: EffectivePermissionMode = current || inherited;
  const modeLabel = (mode: EffectivePermissionMode) => t(`commandReview.mode.${mode}`);
  // 沿用时的说明和可点开的分组：沿用到了指向设置它的分组，没沿用到指向所在（上级）分组，方便去那里设置。
  const parentGroup = groups.find((g) => g.ID === parentGroupId);
  const { text: sourceText, group: sourceGroup } = from
    ? { text: t(`commandReview.mode.inheritFrom.${subject}`, { mode: modeLabel(inherited) }), group: from }
    : parentGroup
      ? { text: t(`commandReview.mode.inheritNone.${subject}`), group: parentGroup }
      : { text: t(`commandReview.mode.inheritNone.${subject === "asset" ? "ungrouped" : "top"}`), group: null };
  const options: SegmentedOption<string>[] = MODES.map((mode) => ({
    value: mode || "inherit",
    label: mode ? modeLabel(mode) : t(`commandReview.mode.inheritOption.${subject}`),
    testid: `permission-mode-${mode || "inherit"}`,
    disabled: saving,
  }));

  const select = (picked: string) => {
    const mode = (picked === "inherit" ? "" : picked) as PermissionMode;
    if (mode === current) return;
    const next: EffectivePermissionMode = mode || inherited;
    if (needsReview(next) && next !== effective) {
      setPending(mode);
      return;
    }
    void save(mode);
  };

  const pendingEffective: EffectivePermissionMode | null = pending === null ? null : pending || inherited;

  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="mb-3 flex items-center gap-2">
        <Sparkles aria-hidden className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t("commandReview.mode.title")}
        </h3>
        {saving && <span className="ml-auto text-[10px] text-muted-foreground">{t("action.saving")}</span>}
      </div>
      <p className="mb-2 text-xs text-muted-foreground">{t("commandReview.mode.desc")}</p>
      <Segmented
        value={current || "inherit"}
        onChange={select}
        options={options}
        aria-label={t("commandReview.mode.title")}
      />
      <div className="mt-2 space-y-1 text-xs">
        {!current && (
          <p data-testid="permission-mode-inherit-source" className="text-muted-foreground">
            {sourceText}
            {sourceGroup && (
              <button
                type="button"
                onClick={() => openGroupDetail(sourceGroup)}
                className="ml-1 font-medium text-foreground underline-offset-2 hover:underline"
              >
                {sourceGroup.Name}
              </button>
            )}
          </p>
        )}
        <p className="text-foreground">{t(`commandReview.mode.${effective}Desc`)}</p>
        {needsReview(effective) && apiKeySet === false && (
          <div
            data-testid="permission-mode-no-api-key"
            className="flex items-start gap-1.5 rounded-md bg-warning/15 px-2.5 py-2 text-foreground"
          >
            <AlertTriangle aria-hidden className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
            <span>{t("commandReview.mode.noApiKey")}</span>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
        variant="default"
        title={t("commandReview.mode.confirmTitle", { mode: pendingEffective ? modeLabel(pendingEffective) : "" })}
        description={
          <div className="space-y-2">
            <p>
              {pendingEffective === "autopilot"
                ? t("commandReview.mode.confirmAutopilot")
                : t("commandReview.mode.confirmAssisted")}
            </p>
            {apiKeySet === false && <p>{t("commandReview.mode.noApiKey")}</p>}
          </div>
        }
        cancelText={t("action.cancel")}
        confirmText={t("commandReview.mode.confirm")}
        onConfirm={() => {
          const mode = pending;
          setPending(null);
          if (mode !== null) void save(mode);
        }}
      />
    </div>
  );
}
