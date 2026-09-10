import { useState, useEffect, useLayoutEffect, useRef, useMemo } from "react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, PlugZap, XCircle } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Button,
  Input,
} from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { DescriptionBar } from "@/components/asset/DescriptionBar";
import { IconPicker } from "@/components/asset/IconPicker";
import { GroupSelect } from "@/components/asset/GroupSelect";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity } from "../../../wailsjs/go/models";
import { EncryptPassword } from "../../../wailsjs/go/system/System";
import { CancelTest, TestAssetConnection } from "../../../wailsjs/go/system/System";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { useAssetTypeOptions, getAssetTypeLabel } from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import type {
  AssetFormHandle,
  AssetFormContext,
  AssetTestAttempt,
  AssetTestResult,
  SectionValidity,
} from "@/lib/assetTypes/formContract";

interface AssetFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editAsset?: asset_entity.Asset | null;
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

const DEFAULT_ICONS: Record<string, string> = {
  ssh: "server",
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

export function AssetForm({ open, onOpenChange, editAsset, defaultGroupId = 0 }: AssetFormProps) {
  const { t } = useTranslation();
  const { createAsset, updateAsset } = useAssetStore();

  const assetTypeOptions = useAssetTypeOptions();

  // Asset type
  const [assetType, setAssetType] = useState<AssetType>("ssh");

  // Basic fields
  const [name, setName] = useState("");
  const [groupId, setGroupId] = useState(0);
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("server");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  // 当前 in-flight 测试；壳只依赖通用 cancel 生命周期，不识别协议类型。
  const activeTestRef = useRef<{ token: symbol; cancel: () => void } | null>(null);

  // 注册化类型走通用 ConfigSection 路径:section 自持 state,经 ref 暴露 build*。
  const sectionRef = useRef<AssetFormHandle>(null);
  const [validity, setValidity] = useState<SectionValidity>({ canTest: false, canSave: false });
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
    if (open) {
      if (editAsset) {
        const editType = (editAsset.Type || "ssh") as AssetType;
        setAssetType(editType);
        setName(editAsset.Name);
        setGroupId(editAsset.GroupID);
        setIcon(editAsset.Icon || DEFAULT_ICONS[editType] || "server");
        setDescription(editAsset.Description);

        // config 回填一律由 ConfigSection 经 editAsset prop 完成——扩展类型的 section
        // 也是注册出来的（见 extension/assetTypes.ts），壳不再认识任何具体类型。
      } else {
        setAssetType("ssh");
        setName("");
        setGroupId(defaultGroupId);
        setIcon("server");
        setDescription("");
        // ConfigSection 经 key={assetType} 重挂载自初始化。
      }
    }
  }

  const handleTypeChange = (newType: AssetType) => {
    if (newType === assetType) return;
    setAssetType(newType);
    setIcon(newType === "database" ? "mysql" : DEFAULT_ICONS[newType] || "server");
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
    let errorMessage: AssetTestAttempt["errorMessage"];
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
          await TestAssetConnection(testID, tc.assetType, tc.configJSON, tc.password);
          return {};
        })();
        attempt = {
          result,
          cancel: () => {
            if (testStarted) void CancelTest(testID);
          },
        };
      }
      cancelAttempt = attempt.cancel;
      errorMessage = attempt.errorMessage;
      if (cancelled) attempt.cancel();
      const result = await attempt.result;
      if (activeTestRef.current?.token === token) {
        notifySuccess(
          result.successDetail
            ? t("asset.testConnectionSuccessDetail", { detail: result.successDetail })
            : t("asset.testConnectionSuccess")
        );
      }
    } catch (e) {
      if (activeTestRef.current?.token === token) {
        toast.error(errorMessage?.(e) ?? `${t("asset.testConnectionFailed")}: ${String(e)}`);
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
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleSubmit = async () => {
    // 用户决定保存：放弃任何正在进行的测试，避免和保存竞争或弹出过期的 toast。
    cancelActiveTest();

    // 所有资产类型都经注册表的 ConfigSection 序列化配置——扩展类型的 section 由
    // extension/assetTypes.ts 从它的 configSchema 生成，加密 password 字段也在那里。
    if (!sectionRef.current) return;
    let built;
    try {
      built = await sectionRef.current.buildConfig(ctx);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
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

  const typeLabel = getAssetTypeLabel(assetType, t, assetTypeOptions);
  const sectionDef = getAssetType(assetType);

  const isTestableAssetType = !!sectionDef?.testable;

  const isTestConnectionDisabled = testing || !validity.canTest;

  // 未注册的类型（资产所属扩展已卸载）没有配置区块可挂载，因此 validity 停在初始的
  // canSave:false，保存按钮自然是禁用的——不必再为它写一条分支。
  const saveDisabledReason = !name.trim() ? "asset.formMissingName" : (validity.saveDisabledReason ?? "");
  const saveDisabled = saving || !!saveDisabledReason || !validity.canSave;

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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) cancelActiveTest();
        onOpenChange(next);
      }}
    >
      <DialogContent
        data-testid="asset-form-dialog"
        className="sm:max-w-[600px] max-h-[85vh] grid-rows-[auto_minmax(0,1fr)_auto] gap-0 overflow-hidden p-0"
        onInteractOutside={(e) => e.preventDefault()}
      >
        <DialogHeader className="border-b px-6 pt-[22px] pb-[18px] text-left">
          <DialogTitle className="text-[17px]">
            {editAsset ? t("action.edit") : t("action.add")} {typeLabel}
          </DialogTitle>
          <DialogDescription className="text-[12.5px]">{t("asset.formDescription")}</DialogDescription>
        </DialogHeader>
        <div className="min-h-0 overflow-y-auto px-6 py-5">
          <div className="flex flex-col gap-[22px]">
            {/* Asset Type */}
            {!editAsset && (
              <Field label={t("asset.type")}>
                <AssetTypePicker value={assetType} onChange={(v) => handleTypeChange(v as AssetType)} />
              </Field>
            )}

            {/* Icon + Name + Group (single row) */}
            <div className="flex items-end gap-[14px]">
              <Field label={t("asset.icon")} className="w-14 shrink-0">
                <IconPicker value={icon} onChange={setIcon} type="asset" compact />
              </Field>
              <Field label={t("asset.name")} className="min-w-0 flex-1">
                <Input
                  data-testid="asset-form-name-input"
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
              <sectionDef.ConfigSection
                key={assetType}
                ref={sectionRef}
                editAsset={editAsset ?? undefined}
                ctx={ctx}
                onValidityChange={setValidity}
                onIconChange={setIcon}
              />
            )}

            {/* Description(折叠成一行,贴近 footer) */}
            <DescriptionBar value={description} onChange={setDescription} />
          </div>
        </div>
        <DialogFooter className="border-t bg-background px-6 py-4 sm:items-center sm:justify-between">
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            {testConnectionButton}
            {saveDisabledReason && (
              <p className="flex items-center gap-1 text-xs text-muted-foreground">
                <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                {t(saveDisabledReason)}
              </p>
            )}
          </div>
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button
              variant="ghost"
              onClick={() => {
                cancelActiveTest();
                onOpenChange(false);
              }}
            >
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
  );
}
