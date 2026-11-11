import { useState, useEffect, useLayoutEffect, useRef, useMemo, useCallback } from "react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { useTranslation } from "react-i18next";
import { Loader2, PlugZap, XCircle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Button,
  ConfirmDialog,
  Input,
} from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import {
  AssetFormCarryContext,
  AssetFormErrorsContext,
  type AssetFormErrors,
} from "@/components/asset/useConfigSection";
import { DescriptionBar } from "@/components/asset/DescriptionBar";
import { IconPicker } from "@/components/asset/IconPicker";
import { GroupSelect } from "@/components/asset/GroupSelect";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity } from "../../../wailsjs/go/models";
import { EncryptPassword } from "../../../wailsjs/go/system/System";
import { CancelTest, TestAssetConnection } from "../../../wailsjs/go/system/System";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { AssetTypeSectionSuspense } from "@/components/asset/AssetTypeSectionSuspense";
import { useAssetTypeOptions, getAssetTypeLabel } from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import type {
  AssetFormHandle,
  CommonFields,
  AssetFormContext,
  AssetTestAttempt,
  AssetTestResult,
  SectionValidity,
} from "@/lib/assetTypes/formContract";
import { errorMessage } from "@/lib/errorMessage";

interface AssetFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editAsset?: asset_entity.Asset | null;
  /** 把 editAsset 当作复制来源:标题是「复制」,类型可见但不可改,保存时新建而不是更新。 */
  copy?: boolean;
  defaultGroupId?: number;
}

// 生成测试连接的唯一 ID；用于配合后端 CancelTest 中断本次测试。
function newTestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `test-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

type AssetType =
  | "ssh"
  | "database"
  | "redis"
  | "mongodb"
  | "kafka"
  | "k8s"
  | "serial"
  | "etcd"
  | "local"
  | "vnc"
  | "rdp"
  | (string & {});

const NO_SECTION_VALIDITY: SectionValidity = { canTest: false, canSave: false };

const DEFAULT_ICONS: Record<string, string> = {
  ssh: "server",
  database: "database",
  mysql: "mysql",
  postgresql: "postgresql",
  mssql: "sqlserver",
  sqlite: "sqlite",
  redis: "redis",
  mongodb: "mongodb",
  kafka: "kafka",
  k8s: "kubernetes",
  serial: "usb",
  etcd: "etcd",
  local: "terminal",
  vnc: "screen-share",
  rdp: "monitor-up",
};

/** 表单给该类型资产预填的图标:类型定义声明了 formIcon 时用它(可为空串),否则取内置默认图标。 */
function formIconFor(type: string): string {
  return getAssetType(type)?.formIcon ?? (DEFAULT_ICONS[type] || "server");
}

export function AssetForm({ open, onOpenChange, editAsset, copy = false, defaultGroupId = 0 }: AssetFormProps) {
  const { t } = useTranslation();
  const { createAsset, updateAsset } = useAssetStore();

  const customTypes = useCustomTypeStore((s) => s.types);
  const assetTypeOptions = useAssetTypeOptions(customTypes);

  // Asset type(+ 子类型:通用资产的自定义类型标识)
  const [assetType, setAssetType] = useState<AssetType>("ssh");
  const [typeVariant, setTypeVariant] = useState<string | undefined>(undefined);

  // Basic fields
  const [name, setName] = useState("");
  const [groupId, setGroupId] = useState(0);
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("server");
  // 用户亲手选过图标:之后换类型不再用类型默认图标覆盖,区块(数据库驱动)也不再改它。
  const [iconTouched, setIconTouched] = useState(false);
  // 打开时的壳字段,关闭前据此判断有没有改动;区块自己的改动由区块经 handle.isDirty 报告。
  const [baseline, setBaseline] = useState({
    name: "",
    groupId: 0,
    description: "",
    assetType: "ssh" as AssetType,
    typeVariant: undefined as string | undefined,
  });
  // 换类型时从旧区块取走的通用连接字段,新区块挂载时用它预填。
  const [carried, setCarried] = useState<CommonFields>({});
  // 等用户确认「放弃修改」后才执行的关闭/离开动作。
  const [pendingDiscard, setPendingDiscard] = useState<{ run: () => void } | null>(null);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  // 当前 in-flight 测试；壳只依赖通用 cancel 生命周期，不识别协议类型。
  const activeTestRef = useRef<{ token: symbol; cancel: () => void } | null>(null);

  // 注册化类型走通用 ConfigSection 路径:section 自持 state,经 ref 暴露 build*。
  const sectionRef = useRef<AssetFormHandle>(null);
  // 区块按 类型+子类型 重挂载（通用资产换自定义类型也要换区块）。
  const sectionKey = `${assetType}:${typeVariant ?? ""}`;
  // validity 记着是哪个区块报的：区块按需加载，换类型/子类型后新区块挂上之前，
  // 上一个区块的 canSave/canTest 不能沿用。
  const [sectionValidity, setSectionValidity] = useState<{ key: string; value: SectionValidity }>({
    key: sectionKey,
    value: NO_SECTION_VALIDITY,
  });
  const validity = sectionValidity.key === sectionKey ? sectionValidity.value : NO_SECTION_VALIDITY;
  const reportValidity = useCallback(
    (value: SectionValidity) => setSectionValidity({ key: sectionKey, value }),
    [sectionKey]
  );
  // 点「保存」被拒的次数(校验在点击时发生)。区块按 sectionKey 重挂载,换了区块就从头算,
  // 新区块不会一挂上就满屏报错;名称字段是壳自己的,只要被拒过就一直标着。
  const [rejected, setRejected] = useState({ key: sectionKey, count: 0 });
  const sectionAttempt = rejected.key === sectionKey ? rejected.count : 0;
  // 这次被拒时区块给的原因,以及区块有没有把错误画到字段上;没画就由壳 toast 这个原因。
  const rejectedReasonRef = useRef("");
  const sectionErrorShownRef = useRef(false);
  const formErrors: AssetFormErrors = useMemo(
    () => ({
      attempt: sectionAttempt,
      reportShown: () => {
        sectionErrorShownRef.current = true;
      },
    }),
    [sectionAttempt]
  );
  const ctx: AssetFormContext = useMemo(() => ({ isEdit: !!editAsset, encryptPassword: EncryptPassword }), [editAsset]);

  // 复位测试状态：open 切换时一律清掉上一次表单的 testing 残留（渲染期对比），
  // 活跃测试生命周期的清理留在下方 effect。
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    setTesting(false);
  }

  // open 切换时取消任何还在跑的测试（关闭对话框时直接放弃结果）。
  useLayoutEffect(() => {
    const active = activeTestRef.current;
    activeTestRef.current = null;
    active?.cancel();
  }, [open]);

  useEffect(
    () => () => {
      const active = activeTestRef.current;
      activeTestRef.current = null;
      active?.cancel();
    },
    []
  );

  // 打开(或换编辑对象)时回填表单:渲染期对比上次 open/editAsset/defaultGroupId,替代 effect 里的级联 setState。
  const [prevSync, setPrevSync] = useState<{
    open: boolean;
    editAsset?: asset_entity.Asset | null;
    defaultGroupId?: number;
  }>({ open: false });
  if (open !== prevSync.open || editAsset !== prevSync.editAsset || defaultGroupId !== prevSync.defaultGroupId) {
    setPrevSync({ open, editAsset, defaultGroupId });
    setRejected({ key: sectionKey, count: 0 });
    setIconTouched(false);
    setCarried({});
    setPendingDiscard(null);
    if (open) {
      if (editAsset) {
        const editType = (editAsset.Type || "ssh") as AssetType;
        setAssetType(editType);
        setTypeVariant(getAssetType(editType)?.variantOf?.(editAsset));
        setName(editAsset.Name);
        setGroupId(editAsset.GroupID);
        setIcon(editAsset.Icon || formIconFor(editType));
        setDescription(editAsset.Description);
        setBaseline({
          name: editAsset.Name,
          groupId: editAsset.GroupID,
          description: editAsset.Description,
          assetType: editType,
          typeVariant: getAssetType(editType)?.variantOf?.(editAsset),
        });

        // config 回填一律由 ConfigSection 经 editAsset prop 完成——扩展类型的 section
        // 也是注册出来的（见 extension/assetTypes.ts），壳不再认识任何具体类型。
      } else {
        setAssetType("ssh");
        setTypeVariant(undefined);
        setName("");
        setGroupId(defaultGroupId);
        setIcon("server");
        setDescription("");
        setBaseline({ name: "", groupId: defaultGroupId, description: "", assetType: "ssh", typeVariant: undefined });
        // ConfigSection 经 key={assetType} 重挂载自初始化。
      }
    }
  }

  const handleTypeChange = (newType: AssetType, variant?: string, defaultIcon?: string) => {
    if (newType === assetType && variant === typeVariant) return;
    // 旧区块里用户填过的主机/端口/用户名带去新区块;新旧都没有的字段保留之前带着的值。
    const common = sectionRef.current?.commonFields?.();
    if (common) setCarried((prev) => ({ ...prev, ...common }));
    // 新挂上的区块从头算被拒次数:换回之前被拒过的类型时不能一挂上就满屏报错。
    setRejected({ key: `${newType}:${variant ?? ""}`, count: 0 });
    setAssetType(newType);
    setTypeVariant(variant);
    if (!iconTouched) setIcon(defaultIcon ?? formIconFor(newType));
  };

  const isDirty = () =>
    name !== baseline.name ||
    groupId !== baseline.groupId ||
    description !== baseline.description ||
    assetType !== baseline.assetType ||
    typeVariant !== baseline.typeVariant ||
    iconTouched ||
    !!sectionRef.current?.isDirty?.();

  // 有改动先确认放弃;没有就直接执行。Esc、关闭按钮、取消、离开去管理自定义类型都走这里。
  const guardDiscard = (run: () => void) => {
    if (isDirty()) setPendingDiscard({ run });
    else run();
  };

  const closeForm = () => {
    cancelActiveTest();
    onOpenChange(false);
  };

  // 静默取消正在进行的测试（用于保存/关闭对话框等退出动作）。无 in-flight 测试时是 no-op。
  const cancelActiveTest = () => {
    const active = activeTestRef.current;
    if (!active) return;
    activeTestRef.current = null;
    active.cancel();
    setTesting(false);
  };

  const handleCancelTest = () => {
    if (!activeTestRef.current) return;
    cancelActiveTest();
    toast.info(t("asset.testCancelled"));
  };

  const handleGenericTestConnection = async () => {
    const handle = sectionRef.current;
    if (!handle || (!handle.startTest && !handle.buildTestConfig)) return;

    const token = Symbol("asset-test");
    let cancelled = false;
    let cancelAttempt = () => {
      cancelled = true;
    };
    let attemptErrorMessage: AssetTestAttempt["errorMessage"];
    const active = {
      token,
      cancel: () => {
        cancelled = true;
        cancelAttempt();
      },
    };
    activeTestRef.current = active;
    setTesting(true);

    try {
      let attempt: AssetTestAttempt;
      if (handle.startTest) {
        attempt = handle.startTest(ctx);
      } else {
        const testID = newTestId();
        let testStarted = false;
        const result = (async (): Promise<AssetTestResult> => {
          const tc = await handle.buildTestConfig!(ctx);
          if (cancelled) return {};
          testStarted = true;
          const detail = await TestAssetConnection(testID, tc.assetType, tc.configJSON, tc.password);
          return detail ? { successDetail: detail } : {};
        })();
        attempt = {
          result,
          cancel: () => {
            if (testStarted) void CancelTest(testID);
          },
        };
      }
      cancelAttempt = attempt.cancel;
      attemptErrorMessage = attempt.errorMessage;
      if (cancelled) attempt.cancel();
      const result = await attempt.result;
      if (activeTestRef.current?.token === token) {
        notifySuccess(
          result.successText
            ? result.successText
            : result.successDetail
              ? t("asset.testConnectionSuccessDetail", { detail: result.successDetail })
              : t("asset.testConnectionSuccess")
        );
      }
    } catch (e) {
      if (activeTestRef.current?.token === token) {
        toast.error(attemptErrorMessage?.(e) ?? `${t("asset.testConnectionFailed")}: ${errorMessage(e)}`);
      }
    } finally {
      if (activeTestRef.current?.token === token) {
        activeTestRef.current = null;
        setTesting(false);
      }
    }
  };

  const persistAsset = async (asset: asset_entity.Asset) => {
    setSaving(true);
    try {
      if (editAsset?.ID) {
        asset.ID = editAsset.ID;
        await updateAsset(asset);
      } else {
        await createAsset(asset);
      }
      onOpenChange(false);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const handleSubmit = async () => {
    // 用户决定保存：放弃任何正在进行的测试，避免和保存竞争或弹出过期的 toast。
    cancelActiveTest();

    if (!name.trim() || !validity.canSave) {
      rejectedReasonRef.current = validity.canSave ? "" : (validity.saveDisabledReason ?? "");
      sectionErrorShownRef.current = false;
      setRejected((r) => ({ key: sectionKey, count: r.key === sectionKey ? r.count + 1 : 1 }));
      return;
    }

    // 所有资产类型都经注册表的 ConfigSection 序列化配置——扩展类型的 section 由
    // extension/assetTypes.ts 从它的 configSchema 生成，加密 password 字段也在那里。
    if (!sectionRef.current) return;
    let built;
    try {
      built = await sectionRef.current.buildConfig(ctx);
    } catch (e) {
      toast.error(errorMessage(e));
      return;
    }
    const asset = new asset_entity.Asset({
      ...(editAsset || {}),
      Name: name,
      Type: assetType,
      GroupID: groupId,
      Icon: icon,
      Description: description,
      Config: built.configJSON,
      sshTunnelId: built.sshTunnelId,
    });
    await persistAsset(asset);
  };

  const typeLabel = getAssetTypeLabel(assetType, t, assetTypeOptions, typeVariant);
  const sectionDef = getAssetType(assetType);

  const isTestableAssetType = !!sectionDef?.testable && validity.testable !== false;

  const isTestConnectionDisabled = testing || !validity.canTest;

  // 保存按钮不因校验禁用(点击时校验);只有区块还没上报过校验结果时无从校验,才禁用。
  // 未注册的类型（资产所属扩展已卸载）没有配置区块可挂载，永远不会上报，保存按钮因此保持禁用。
  const sectionReported = sectionValidity.key === sectionKey;
  const saveDisabled = saving || !sectionReported;
  const nameError = rejected.count > 0 && !name.trim() ? t("asset.formMissingName") : undefined;

  // 被拒的这次提交里,区块的原因没能落到任何字段上:用 toast 报出来。子组件的上报 effect 先于这里执行。
  useEffect(() => {
    if (sectionAttempt > 0 && !sectionErrorShownRef.current && rejectedReasonRef.current) {
      toast.error(t(rejectedReasonRef.current));
    }
  }, [sectionAttempt, t]);

  const testConnectionButton = !isTestableAssetType ? null : testing ? (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      onClick={handleCancelTest}
      className="-ml-2 w-fit gap-1.5 px-2 text-primary hover:bg-primary/10 hover:text-primary"
    >
      <Loader2 className="h-3.5 w-3.5 animate-spin" />
      {t("asset.testing")}
      <XCircle className="ml-1 h-3.5 w-3.5" />
      {t("asset.cancelTest")}
    </Button>
  ) : (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      data-testid="asset-test-connection"
      onClick={handleGenericTestConnection}
      disabled={isTestConnectionDisabled}
      className="-ml-2 w-fit gap-1.5 px-2 text-primary hover:bg-primary/10 hover:text-primary disabled:text-muted-foreground"
    >
      {testing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <PlugZap className="h-3.5 w-3.5" />}
      {testing ? t("asset.testing") : t("asset.testConnection")}
    </Button>
  );

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (next) onOpenChange(true);
          else guardDiscard(closeForm);
        }}
      >
        <DialogContent
          data-testid="asset-form-dialog"
          className="sm:max-w-[600px] max-h-[85vh] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden p-0"
          onInteractOutside={(e) => e.preventDefault()}
        >
          <DialogHeader className="border-b px-6 pt-[22px] pb-[18px] text-left">
            <DialogTitle className="text-[17px]">
              {copy
                ? t("asset.copyTitle", { type: typeLabel })
                : `${editAsset ? t("action.edit") : t("action.add")} ${typeLabel}`}
            </DialogTitle>
            <DialogDescription className="text-[12.5px]">{t("asset.formDescription")}</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 overflow-y-auto px-6 py-5">
            <div className="flex flex-col gap-[22px]">
              {/* Asset Type */}
              {(!editAsset || copy) && (
                <Field label={t("asset.type")}>
                  <AssetTypePicker
                    value={assetType}
                    variant={typeVariant}
                    disabled={copy}
                    onChange={(v, variant, defaultIcon) => handleTypeChange(v as AssetType, variant, defaultIcon)}
                    onLeave={(proceed) =>
                      guardDiscard(() => {
                        proceed();
                        closeForm();
                      })
                    }
                  />
                </Field>
              )}

              {/* Icon + Name + Group (single row) */}
              <div className="flex items-end gap-[14px]">
                <Field label={t("asset.icon")} className="w-14 shrink-0">
                  <IconPicker
                    value={icon}
                    onChange={(v) => {
                      setIcon(v);
                      setIconTouched(true);
                    }}
                    type="asset"
                    compact
                  />
                </Field>
                <Field label={t("asset.name")} error={nameError} className="min-w-0 flex-1">
                  <Input
                    data-testid="asset-form-name-input"
                    aria-invalid={!!nameError || undefined}
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder={
                      assetType === "ssh"
                        ? "prod-web-01"
                        : assetType === "database"
                          ? "prod-mysql-01"
                          : assetType === "redis"
                            ? "prod-redis-01"
                            : assetType === "mongodb"
                              ? "prod-mongo-01"
                              : assetType === "kafka"
                                ? "prod-kafka-01"
                                : assetType === "k8s"
                                  ? "prod-k8s-01"
                                  : `prod-${assetType}-01`
                    }
                  />
                </Field>
                <Field label={t("asset.group")} className="w-[168px] shrink-0">
                  <GroupSelect value={groupId} onValueChange={setGroupId} />
                </Field>
              </div>

              {/* 每种类型的配置区块都来自注册表（内置手写、扩展由 configSchema 生成） */}
              {sectionDef?.ConfigSection && (
                <AssetTypeSectionSuspense>
                  <AssetFormErrorsContext value={formErrors}>
                    <AssetFormCarryContext value={carried}>
                      <sectionDef.ConfigSection
                        key={sectionKey}
                        ref={sectionRef}
                        editAsset={editAsset ?? undefined}
                        ctx={ctx}
                        onValidityChange={reportValidity}
                        onIconChange={(v) => {
                          if (!iconTouched) setIcon(v);
                        }}
                        variant={typeVariant}
                      />
                    </AssetFormCarryContext>
                  </AssetFormErrorsContext>
                </AssetTypeSectionSuspense>
              )}

              {/* Description(折叠成一行,贴近 footer) */}
              <DescriptionBar value={description} onChange={setDescription} />
            </div>
          </div>
          <DialogFooter className="border-t bg-background px-6 py-4 sm:items-center sm:justify-between">
            <div className="flex min-w-0 flex-1 flex-col gap-1">{testConnectionButton}</div>
            <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
              <Button variant="ghost" onClick={() => guardDiscard(closeForm)}>
                {t("action.cancel")}
              </Button>
              <Button data-testid="asset-form-submit" onClick={handleSubmit} disabled={saveDisabled}>
                {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                {saving ? t("action.saving") : t("action.save")}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={pendingDiscard !== null}
        onOpenChange={(next) => {
          if (!next) setPendingDiscard(null);
        }}
        title={t("asset.discardTitle")}
        description={t("asset.discardDescription")}
        cancelText={t("asset.discardKeepEditing")}
        confirmText={t("asset.discardConfirm")}
        onConfirm={() => {
          const pending = pendingDiscard;
          setPendingDiscard(null);
          pending?.run();
        }}
      />
    </>
  );
}
