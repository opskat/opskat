import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Pencil, Trash2, TerminalSquare, Loader2 } from "lucide-react";
import Markdown from "react-markdown";
import rehypeSanitize from "rehype-sanitize";
import remarkBreaks from "remark-breaks";
import { markdownComponents, markdownUrlTransform } from "@/components/MarkdownLink";
import { Button, Separator, ConfirmDialog, Tooltip, TooltipContent, TooltipTrigger } from "@opskat/ui";
import { toast } from "sonner";
import { useAssetStore } from "@/stores/assetStore";
import { useAssetTypeDef } from "@/lib/assetTypes";
import { AssetIcon } from "@/components/asset/AssetIcon";
import { CommandPolicyCard } from "@/components/asset/CommandPolicyCard";
import { asset_entity } from "../../../wailsjs/go/models";
import { GetDefaultPolicy } from "../../../wailsjs/go/system/System";

interface AssetDetailProps {
  asset: asset_entity.Asset;
  isConnecting?: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onConnect: () => void;
}

export function AssetDetail({ asset, isConnecting, onEdit, onDelete, onConnect }: AssetDetailProps) {
  const { t } = useTranslation();
  const { assets, updateAsset } = useAssetStore();
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [savingPolicy, setSavingPolicy] = useState(false);

  const [policyFields, setPolicyFields] = useState<Record<string, string[]>>({});
  const [policyGroups, setPolicyGroups] = useState<string[]>([]);

  // 订阅注册表：扩展加载完成时它的资产类型才注册进来，这里要跟着重渲染。
  // 定义还没到位期间只是少一张类型卡，不是全屏 loading——通用信息照常可读。
  const def = useAssetTypeDef(asset.Type);

  // 资产切换 / 策略变化时回填本地编辑态：渲染期对比上次值，替代 effect 里的级联 setState。
  const [prevSync, setPrevSync] = useState<{ id?: number; cmdPolicy?: string; type?: string }>({});
  if (asset.ID !== prevSync.id || asset.CmdPolicy !== prevSync.cmdPolicy || asset.Type !== prevSync.type) {
    setPrevSync({ id: asset.ID, cmdPolicy: asset.CmdPolicy, type: asset.Type });
    try {
      const parsed = JSON.parse(asset.CmdPolicy || "{}");
      setPolicyGroups(parsed.groups || []);
      const fields: Record<string, string[]> = {};
      for (const f of def?.policy?.fields ?? []) {
        fields[f.key] = parsed[f.key] || [];
      }
      setPolicyFields(fields);
    } catch {
      setPolicyFields({});
      setPolicyGroups([]);
    }
  }

  const savePolicy = async (policyObj: Record<string, unknown>, groups?: string[]) => {
    // Remove empty arrays (except groups which is managed separately)
    const cleaned: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(policyObj)) {
      if (Array.isArray(v) && v.length > 0) cleaned[k] = v;
    }
    const grps = groups ?? policyGroups;
    if (grps.length > 0) cleaned.groups = grps;
    const cmdPolicy = Object.keys(cleaned).length > 0 ? JSON.stringify(cleaned) : "";
    const updated = new asset_entity.Asset({ ...asset, CmdPolicy: cmdPolicy });
    setSavingPolicy(true);
    try {
      await updateAsset(updated);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSavingPolicy(false);
    }
  };

  const handleSavePolicyFields = async (updatedFields: Record<string, string[]>, groups?: string[]) => {
    await savePolicy(updatedFields, groups);
  };

  const handleGroupsChange = (newGroups: string[]) => {
    setPolicyGroups(newGroups);
    handleSavePolicyFields(policyFields, newGroups);
  };

  const handleResetPolicy = async () => {
    try {
      const defaultJSON = await GetDefaultPolicy(asset.Type);
      const parsed = JSON.parse(defaultJSON);
      const groups = parsed.groups || [];
      setPolicyGroups(groups);
      const fields: Record<string, string[]> = {};
      for (const f of def?.policy?.fields ?? []) {
        fields[f.key] = parsed[f.key] || [];
      }
      setPolicyFields(fields);
      await savePolicy(fields, groups);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const sshTunnelName = (id?: number) => {
    if (!id) return null;
    return assets.find((a) => a.ID === id)?.Name || `ID:${id}`;
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-3 border-b">
        <div className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10">
            <AssetIcon assets={[asset]} assetId={asset.ID} fallbackType={asset.Type} className="h-4 w-4 text-primary" />
          </div>
          <div>
            <h2 className="font-semibold leading-tight">{asset.Name}</h2>
            <span className="text-xs text-muted-foreground uppercase">{asset.Type}</span>
          </div>
        </div>
        <div className="flex gap-1.5">
          {def?.canConnect && (
            <Button size="sm" className="h-8 gap-1.5" onClick={onConnect} disabled={isConnecting}>
              {isConnecting ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <TerminalSquare className="h-3.5 w-3.5" />
              )}
              {t("ssh.connect")}
            </Button>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onEdit} aria-label={t("action.edit")}>
                <Pencil className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{t("action.edit")}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="h-8 w-8 text-destructive hover:text-destructive"
                onClick={() => setShowDeleteConfirm(true)}
                aria-label={t("action.delete")}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{t("action.delete")}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      <ConfirmDialog
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        title={t("asset.deleteAssetTitle")}
        description={t("asset.deleteAssetDesc", { name: asset.Name })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={onDelete}
      />
      <div className="flex-1 p-4 space-y-4 overflow-y-auto">
        {/* 类型详情卡：内置类型手写，扩展类型由它的 configSchema 生成（同一个槽位） */}
        {def && <def.DetailInfoCard asset={asset} sshTunnelName={sshTunnelName} />}

        {/* 策略卡：内置类型与扩展类型走同一段渲染，差别只在定义里 */}
        {(() => {
          const pol = def?.policy;
          if (!pol) return null;
          const tr = (key: string) => (pol.ns ? t(key, { ns: pol.ns, defaultValue: asset.Type }) : t(key));
          return (
            <CommandPolicyCard
              title={tr(pol.titleKey)}
              policyType={pol.policyType}
              lists={pol.fields.map((f) => ({
                key: f.key,
                label: t(f.labelKey),
                items: policyFields[f.key] || [],
                onAdd: (vals: string[]) => {
                  const next = { ...policyFields, [f.key]: [...(policyFields[f.key] || []), ...vals] };
                  setPolicyFields(next);
                  handleSavePolicyFields(next);
                },
                onRemove: (i: number) => {
                  const next = {
                    ...policyFields,
                    [f.key]: (policyFields[f.key] || []).filter((_, idx) => idx !== i),
                  };
                  setPolicyFields(next);
                  handleSavePolicyFields(next);
                },
                // 占位符二选一：内置类型给 i18n key，扩展给 manifest 列出的 action 名。
                placeholder: f.placeholder ?? (f.placeholderKey ? t(f.placeholderKey) : ""),
                variant: f.variant,
              }))}
              buildPolicyJSON={() =>
                JSON.stringify({
                  ...Object.fromEntries(pol.fields.map((f) => [f.key, policyFields[f.key] || []])),
                  ...(policyGroups.length > 0 ? { groups: policyGroups } : {}),
                })
              }
              hint={pol.hintKey ? t(pol.hintKey) : undefined}
              saving={savingPolicy}
              assetID={asset.ID}
              onReset={handleResetPolicy}
              referencedGroups={policyGroups}
              onGroupsChange={handleGroupsChange}
            />
          );
        })()}

        {asset.Description && (
          <>
            <Separator />
            <div className="text-sm">
              <span className="text-muted-foreground">{t("asset.description")}</span>
              <div className="mt-1 prose prose-sm dark:prose-invert prose-p:my-1 prose-pre:my-1 prose-pre:overflow-x-auto max-w-none">
                <Markdown
                  remarkPlugins={[remarkBreaks]}
                  rehypePlugins={[rehypeSanitize]}
                  urlTransform={markdownUrlTransform}
                  components={markdownComponents}
                >
                  {asset.Description}
                </Markdown>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
