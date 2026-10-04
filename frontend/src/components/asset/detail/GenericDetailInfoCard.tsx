import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { AlertCircle, AlertTriangle, BookOpen, Copy, Eye, EyeOff, KeyRound, Loader2 } from "lucide-react";
import { Button, Dialog, DialogContent, DialogHeader, DialogTitle, cn } from "@opskat/ui";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";
import { parseGenericConfig, EXEC_MODE_HTTP } from "@/components/asset/GenericConfigSection.config";
import { notifyCopied } from "@/lib/notify";
import { useCustomTypeList, useCustomTypeStore } from "@/stores/customTypeStore";
import type { DetailInfoCardProps, DetailSubtitleProps } from "@/lib/assetTypes/types";
import type { customtype } from "../../../../wailsjs/go/models";
import { GetGenericAssetView, RevealGenericSecret } from "../../../../wailsjs/go/customtype/CustomType";
import { DetailGrid, DetailSection, InfoItem, ProxyChainDetailSection } from "./InfoItem";
import { MASKED_SECRET } from "./utils";

/** POSIX shell 单引号转义:只含安全字符时原样返回。 */
function shellQuote(s: string): string {
  if (/^[A-Za-z0-9._@%+=:,/-]+$/.test(s)) return s;
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

/** 按执行方式给出 opsctl 用法示例(可直接复制)。 */
function genericUsageExamples(assetName: string, view: customtype.GenericAssetView): string[] {
  const name = shellQuote(assetName);
  // 命令方式:有命令模板时 exec 只传参数,没有模板时传一整条 shell 命令。
  const execArgs = view.execMode === EXEC_MODE_HTTP ? "GET /" : view.hasCommandTemplate ? "<args>" : "'<command>'";
  const out = [`opsctl exec ${name} -- ${execArgs}`];
  const secret = view.fields.find((f) => f.secret);
  if (secret) out.push(`opsctl secret get ${name} ${secret.name}`);
  out.push(`opsctl help ${name}`);
  return out;
}

/** 通用资产详情:缺值横幅、字段值(密钥掩码可查看)、实际地址与隧道、用法示例与使用说明入口。 */
export function GenericDetailInfoCard({ asset, sshTunnelName, onEdit }: DetailInfoCardProps) {
  const { t } = useTranslation();
  // 类型结构被编辑(列表重新加载)时同样要重新解析这台资产。
  const customTypes = useCustomTypeStore((s) => s.types);
  const [result, setResult] = useState<{ view?: customtype.GenericAssetView; error?: string }>();
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState<string>("");
  const [usageOpen, setUsageOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    GetGenericAssetView(asset.ID)
      .then((view) => {
        if (!cancelled) {
          setResult({ view });
          setRevealed({});
        }
      })
      .catch((e) => {
        if (!cancelled) setResult({ error: String(e) });
      });
    return () => {
      cancelled = true;
    };
  }, [asset.ID, asset.Config, asset.Updatetime, customTypes]);

  if (result?.error) {
    return (
      <p className="flex items-center gap-1.5 rounded-xl border border-destructive/30 bg-destructive/10 p-4 text-sm text-destructive">
        <AlertCircle className="h-4 w-4 shrink-0" />
        <span className="select-text">{result.error}</span>
      </p>
    );
  }
  const view = result?.view;
  if (!view) {
    return (
      <div className="flex items-center justify-center rounded-xl border bg-card p-6">
        <Loader2 className="size-5 animate-spin text-primary" />
      </div>
    );
  }

  const isHttp = view.execMode === EXEC_MODE_HTTP;
  const labelOf = (f: customtype.GenericFieldView) => f.label || f.name;
  const missingFields = view.fields.filter((f) => f.missing);
  const tunnelName = isHttp ? sshTunnelName(asset.sshTunnelId) : null;
  const proxyChain = isHttp ? parseGenericConfig(asset.Config).proxy_chain : undefined;

  const toggleReveal = async (field: string) => {
    if (revealed[field] !== undefined) {
      setRevealed(({ [field]: _hidden, ...rest }) => rest);
      return;
    }
    setRevealing(field);
    try {
      const plain = await RevealGenericSecret(asset.ID, field);
      setRevealed((m) => ({ ...m, [field]: plain }));
    } catch (e) {
      toast.error(String(e));
    } finally {
      setRevealing("");
    }
  };

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      notifyCopied(t("action.copied"));
    } catch (e) {
      toast.error(String(e));
    }
  };

  const notFilled = (f: customtype.GenericFieldView) => (
    <p className={cn("mt-0.5 text-sm", f.missing ? "text-warning" : "text-muted-foreground")}>
      {t("asset.generic.notFilled")}
    </p>
  );

  const renderValue = (f: customtype.GenericFieldView) => {
    if (!f.set) return notFilled(f);
    if (!f.secret) return <p className="mt-0.5 select-text font-mono text-sm">{f.value}</p>;
    if (f.credentialId) {
      return (
        <p className="mt-0.5 flex items-center gap-1.5 text-sm">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
          <span className="text-muted-foreground">{t("asset.generic.managedCredential")}</span>
          <span className="text-muted-foreground">·</span>
          <span className="select-text font-mono">{f.credentialName}</span>
        </p>
      );
    }
    const plain = revealed[f.name];
    const shown = plain !== undefined;
    return (
      <p className="mt-0.5 flex items-center gap-1.5 text-sm">
        <span data-testid={`generic-secret-value-${f.name}`} className={cn("font-mono", shown && "select-text")}>
          {shown ? plain : MASKED_SECRET}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          data-testid={`generic-reveal-${f.name}`}
          aria-label={shown ? t("action.hideSecret") : t("action.showSecret")}
          disabled={revealing === f.name}
          onClick={() => void toggleReveal(f.name)}
        >
          {revealing === f.name ? <Loader2 className="animate-spin" /> : shown ? <EyeOff /> : <Eye />}
        </Button>
      </p>
    );
  };

  const examples = genericUsageExamples(asset.Name, view);

  return (
    <>
      {missingFields.length > 0 && (
        <div
          data-testid="generic-missing-banner"
          className="flex items-start gap-3 rounded-xl border border-warning/40 bg-warning/10 p-4"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />
          <div className="min-w-0 flex-1 text-sm">
            <p className="font-medium text-warning">{t("asset.generic.missingBannerTitle", { name: view.typeName })}</p>
            <p className="mt-1 text-muted-foreground">
              {t("asset.generic.missingBannerList")}
              {missingFields.map((f) => `${labelOf(f)} (${f.name})`).join(t("asset.generic.listSeparator"))}
            </p>
          </div>
          {onEdit && (
            <Button size="sm" data-testid="generic-missing-fill" onClick={onEdit}>
              {t("asset.generic.fillIn")}
            </Button>
          )}
        </div>
      )}

      <DetailSection title={view.typeName}>
        <DetailGrid>
          {view.fields.map((f) => (
            <div key={f.name} data-testid={`generic-detail-field-${f.name}`}>
              <span className="text-xs text-muted-foreground">{labelOf(f)}</span>
              {renderValue(f)}
            </div>
          ))}
          {isHttp && view.actualAddress && (
            <InfoItem label={t("asset.generic.actualAddress")} value={view.actualAddress} mono />
          )}
          {tunnelName && <InfoItem label={t("asset.sshTunnel")} value={tunnelName} mono />}
        </DetailGrid>
      </DetailSection>
      <ProxyChainDetailSection chain={proxyChain} resolveSshName={sshTunnelName} />

      <DetailSection title={t("asset.generic.usageTitle")}>
        <div className="flex flex-col gap-1.5">
          {examples.map((cmd) => (
            <div key={cmd} className="flex items-center gap-2 rounded-md bg-muted px-3 py-1.5">
              <code
                data-testid="generic-usage-example"
                className="min-w-0 flex-1 select-text truncate font-mono text-xs"
              >
                {cmd}
              </code>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                data-testid="generic-usage-copy"
                aria-label={t("action.copy")}
                onClick={() => void copy(cmd)}
              >
                <Copy />
              </Button>
            </div>
          ))}
        </div>
        {view.usage.trim() && (
          <p className="mt-2 flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
            {t("asset.generic.usageNotesHint", { name: view.typeName })}
            <Button
              type="button"
              variant="link"
              size="xs"
              className="h-auto gap-1 px-1"
              data-testid="generic-usage-notes"
              onClick={() => setUsageOpen(true)}
            >
              <BookOpen className="h-3 w-3" aria-hidden />
              {t("asset.generic.viewUsageNotes")}
            </Button>
          </p>
        )}
      </DetailSection>

      <Dialog open={usageOpen} onOpenChange={setUsageOpen}>
        <DialogContent size="lg">
          <DialogHeader>
            <DialogTitle>{t("asset.generic.usageNotesTitle", { name: view.typeName })}</DialogTitle>
          </DialogHeader>
          <div className="max-h-[60vh] overflow-y-auto whitespace-pre-wrap break-words select-text text-sm">
            {view.usage}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** 详情页头部副标题:「<类型名> · 自定义类型 · 编辑类型」。 */
export function GenericDetailSubtitle({ asset }: DetailSubtitleProps) {
  const { t } = useTranslation();
  const { types } = useCustomTypeList();
  const [editorOpen, setEditorOpen] = useState(false);

  const slug = parseGenericConfig(asset.Config).custom_type ?? "";
  const summary = types.find((ct) => ct.slug === slug);

  return (
    <span data-testid="generic-detail-subtitle" className="flex items-center gap-1 text-xs text-muted-foreground">
      <span>{summary?.name ?? slug}</span>
      <span aria-hidden>·</span>
      <span>{t("asset.generic.customTypeLabel")}</span>
      {summary && (
        <>
          <span aria-hidden>·</span>
          <Button
            type="button"
            variant="link"
            size="xs"
            className="h-auto px-0 text-xs"
            data-testid="generic-detail-edit-type"
            onClick={() => setEditorOpen(true)}
          >
            {t("asset.generic.editType")}
          </Button>
          <CustomTypeEditorDialog open={editorOpen} typeId={summary.id} onOpenChange={setEditorOpen} />
        </>
      )}
    </span>
  );
}
