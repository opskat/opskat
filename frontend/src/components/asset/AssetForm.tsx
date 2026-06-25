import { useState, useEffect, useRef, useMemo } from "react";
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
import { GetDecryptedExtensionConfig } from "../../../wailsjs/go/extension/Extension";
import { CancelTest, TestAssetConnection } from "../../../wailsjs/go/system/System";
import { useExtensionStore } from "@/extension";
import { ExtensionConfigForm } from "@/components/asset/ExtensionConfigForm";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { getAssetTypeOptions, getAssetTypeLabel } from "@/lib/assetTypes/options";
import { getAssetType } from "@/lib/assetTypes";
import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";

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
};

export function AssetForm({ open, onOpenChange, editAsset, defaultGroupId = 0 }: AssetFormProps) {
  const { t } = useTranslation();
  const { createAsset, updateAsset } = useAssetStore();

  const extensions = useExtensionStore((s) => s.extensions);
  const assetTypeOptions = useMemo(() => getAssetTypeOptions(extensions), [extensions]);

  // Asset type
  const [assetType, setAssetType] = useState<AssetType>("ssh");

  // Basic fields
  const [name, setName] = useState("");
  const [groupId, setGroupId] = useState(0);
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState("server");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  // 当前 in-flight 测试的 ID；切换/取消时用来 race-discard 晚到的结果。
  const activeTestIdRef = useRef<string | null>(null);

  // 注册化类型走通用 ConfigSection 路径:section 自持 state,经 ref 暴露 build*。
  const sectionRef = useRef<AssetFormHandle>(null);
  const [validity, setValidity] = useState<SectionValidity>({ canTest: false, canSave: false });
  const ctx: AssetFormContext = useMemo(() => ({ isEdit: !!editAsset, encryptPassword: EncryptPassword }), [editAsset]);

  // Extension config
  const [extConfig, setExtConfig] = useState<Record<string, unknown>>({});

  // 复位测试状态：open 切换时一律清掉上一次表单的 testing/testID 残留，
  // 并取消任何还在后台跑的测试（关闭对话框时直接放弃结果）。
  useEffect(() => {
    const lastId = activeTestIdRef.current;
    if (lastId) {
      void CancelTest(lastId);
    }
    activeTestIdRef.current = null;
    setTesting(false);
  }, [open]);

  useEffect(() => {
    if (open) {
      if (editAsset) {
        const editType = (editAsset.Type || "ssh") as AssetType;
        setAssetType(editType);
        setName(editAsset.Name);
        setGroupId(editAsset.GroupID);
        setIcon(editAsset.Icon || DEFAULT_ICONS[editType] || "server");
        setDescription(editAsset.Description);

        if (getAssetType(editType)?.ConfigSection) {
          // 已注册化类型:config 回填由 section 经 editAsset prop 完成,壳跳过
        } else {
          // Extension type: load decrypted config
          const extInfo = useExtensionStore.getState().getExtensionForAssetType(editType);
          if (extInfo && editAsset.ID) {
            GetDecryptedExtensionConfig(editAsset.ID, extInfo.name)
              .then((cfg) => setExtConfig(JSON.parse(cfg || "{}")))
              .catch(() => setExtConfig(JSON.parse(editAsset.Config || "{}")));
          } else {
            setExtConfig(JSON.parse(editAsset.Config || "{}"));
          }
        }
      } else {
        setAssetType("ssh");
        setName("");
        setGroupId(defaultGroupId);
        setIcon("server");
        setDescription("");
        // 注册化类型 section 经 key={assetType} 重挂载自初始化,壳只清扩展 config。
        setExtConfig({});
      }
    }
  }, [open, editAsset, defaultGroupId]);

  const handleTypeChange = (newType: AssetType) => {
    if (newType === assetType) return;
    setAssetType(newType);
    setIcon(newType === "database" ? "mysql" : DEFAULT_ICONS[newType] || "server");
  };

  // 静默取消正在进行的测试（用于保存/关闭对话框等退出动作）。无 in-flight 测试时是 no-op。
  const cancelActiveTest = () => {
    const id = activeTestIdRef.current;
    if (!id) return;
    activeTestIdRef.current = null;
    void CancelTest(id);
    setTesting(false);
  };

  const handleCancelTest = () => {
    if (!activeTestIdRef.current) return;
    cancelActiveTest();
    toast.info(t("asset.testCancelled"));
  };

  const handleGenericTestConnection = async () => {
    const build = sectionRef.current?.buildTestConfig;
    if (!build) return;
    let tc;
    try {
      tc = await build(ctx);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return;
    }
    const testId = newTestId();
    activeTestIdRef.current = testId;
    setTesting(true);
    try {
      await TestAssetConnection(testId, tc.assetType, tc.configJSON, tc.password);
      if (activeTestIdRef.current === testId) notifySuccess(t("asset.testConnectionSuccess"));
    } catch (e) {
      if (activeTestIdRef.current === testId) toast.error(`${t("asset.testConnectionFailed")}: ${String(e)}`);
    } finally {
      if (activeTestIdRef.current === testId) {
        activeTestIdRef.current = null;
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

    const def = getAssetType(assetType);
    if (def?.ConfigSection) {
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
      return;
    }

    // Extension type: encrypt password fields from configSchema before saving
    const extInfo = useExtensionStore.getState().getExtensionForAssetType(assetType);
    const schema = extInfo?.manifest.assetTypes?.find((at) => at.type === assetType)?.configSchema as
      | { properties?: Record<string, { format?: string }> }
      | undefined;
    const configCopy = { ...extConfig };
    if (schema?.properties) {
      for (const [key, prop] of Object.entries(schema.properties)) {
        if (prop.format === "password" && configCopy[key]) {
          const encrypted = await EncryptPassword(String(configCopy[key]));
          if (encrypted === undefined) return;
          configCopy[key] = encrypted;
        }
      }
    }

    const asset = new asset_entity.Asset({
      ...(editAsset || {}),
      Name: name,
      Type: assetType,
      GroupID: groupId,
      Icon: icon,
      Description: description,
      Config: JSON.stringify(configCopy),
      sshTunnelId: 0, // 扩展类型无隧道
    });

    await persistAsset(asset);
  };

  const typeLabel = getAssetTypeLabel(assetType, t, assetTypeOptions);
  const sectionDef = getAssetType(assetType);

  const isTestableAssetType = sectionDef?.ConfigSection ? !!sectionDef.testable : false;

  const isTestConnectionDisabled = testing || !validity.canTest;

  const saveDisabledReason = !name.trim()
    ? "asset.formMissingName"
    : sectionDef?.ConfigSection
      ? (validity.saveDisabledReason ?? "")
      : "";
  const saveDisabled = saving || !!saveDisabledReason || (!!sectionDef?.ConfigSection && !validity.canSave);

  const testConnectionButton = !isTestableAssetType ? null : testing && activeTestIdRef.current ? (
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

            {/* 注册化类型:通用 ConfigSection 路径 */}
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

            {/* Extension type config */}
            {!sectionDef?.ConfigSection &&
              (() => {
                const extInfo = useExtensionStore.getState().getExtensionForAssetType(assetType);
                if (!extInfo) return null;
                const assetTypeDef = extInfo.manifest.assetTypes?.find((at) => at.type === assetType);
                if (!assetTypeDef?.configSchema) return null;
                return (
                  <ExtensionConfigForm
                    extensionName={extInfo.name}
                    configSchema={assetTypeDef.configSchema as Record<string, unknown>}
                    value={extConfig}
                    onChange={setExtConfig}
                    hasBackend={!!extInfo.manifest.backend}
                  />
                );
              })()}

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
