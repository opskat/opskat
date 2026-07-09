import { createElement, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Link, Copy, RefreshCw, Hourglass } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, Button } from "@opskat/ui";
import { OSSPresignGet, OSSPresignPut } from "../../../wailsjs/go/oss/OSS";
import { notifyCopied } from "@/lib/notify";
import { prefixLeafName } from "@/lib/ossPrefixTree";
import { typeIcon, typeIconColor } from "@/lib/objectContentType";

export interface OSSPresignDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  bucket: string;
  objectKey: string;
  contentType?: string;
}

type Method = "get" | "put";
const EXPIRIES: { secs: number; key: string }[] = [
  { secs: 900, key: "oss.share.expiry15m" },
  { secs: 3600, key: "oss.share.expiry1h" },
  { secs: 86400, key: "oss.share.expiry24h" },
  { secs: 604800, key: "oss.share.expiry7d" },
];

export function OSSPresignDialog({
  open,
  onOpenChange,
  assetId,
  bucket,
  objectKey,
  contentType,
}: OSSPresignDialogProps) {
  const { t } = useTranslation();
  const [method, setMethod] = useState<Method>("get");
  const [expirySecs, setExpirySecs] = useState(3600);
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const reqIdRef = useRef(0);

  // 生成/重新生成:每次调用递增 reqId,await 回来只认最后一次(方法/有效期改动作废在途请求)。
  const runGenerate = useCallback(() => {
    const myId = ++reqIdRef.current;
    setLoading(true);
    setUrl("");
    const req = { assetId, bucket, key: objectKey, expirySecs };
    const p = method === "get" ? OSSPresignGet(req) : OSSPresignPut(req);
    void p.then(
      (u) => {
        if (reqIdRef.current === myId) {
          setUrl(u);
          setLoading(false);
        }
      },
      (err) => {
        if (reqIdRef.current === myId) {
          toast.error(`${t("oss.share.generateFailed")}: ${String(err)}`);
          setLoading(false);
        }
      }
    );
  }, [assetId, bucket, objectKey, method, expirySecs, t]);

  // 打开时重置为默认(GET / 1 小时),换对象亦然。
  useEffect(() => {
    if (open) {
      setMethod("get");
      setExpirySecs(3600);
    }
  }, [open, objectKey]);

  // 打开即自动生成;方法/有效期变化通过 runGenerate 身份变化触发重生成(仅在 open 时)。
  useEffect(() => {
    if (open) runGenerate();
  }, [open, runGenerate]);

  const copy = () => void navigator.clipboard?.writeText(url).then(() => notifyCopied(t("oss.share.copied")));

  const currentExpiryKey = EXPIRIES.find((e) => e.secs === expirySecs)?.key ?? EXPIRIES[1].key;
  const dirs = objectKey.split("/").slice(0, -1);
  const pathCrumb = [bucket, ...dirs].join(" / ");

  const seg = (active: boolean) =>
    `min-w-0 flex-1 truncate rounded px-2 py-1 text-[12px] transition-colors ${
      active ? "bg-primary font-semibold text-primary-foreground" : "text-muted-foreground hover:text-foreground"
    }`;
  const segGroup = "flex gap-0.5 rounded-md border bg-muted/40 p-0.5";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Link className="size-4 text-primary" /> {t("oss.share.title")}
          </DialogTitle>
        </DialogHeader>

        <div className="flex min-w-0 flex-col gap-3.5">
          {/* 对象卡片:类型图标 + 文件名 + 路径面包屑 */}
          <div className="flex items-center gap-2.5 rounded-md border bg-muted/30 p-2.5">
            {createElement(typeIcon(contentType ?? "", objectKey), {
              className: `size-4 shrink-0 ${typeIconColor(contentType ?? "", objectKey)}`,
            })}
            <div className="flex min-w-0 flex-col">
              <span className="truncate text-[13px] font-medium" title={objectKey}>
                {prefixLeafName(objectKey)}
              </span>
              <span className="truncate font-mono text-[10px] text-muted-foreground" title={pathCrumb}>
                {pathCrumb}
              </span>
            </div>
          </div>

          {/* 请求方法 */}
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-medium text-muted-foreground">{t("oss.share.methodLabel")}</span>
            <div className={segGroup}>
              <button
                type="button"
                className={seg(method === "get")}
                onClick={() => setMethod("get")}
                data-testid="oss-share-method-get"
              >
                {t("oss.share.methodGet")}
              </button>
              <button
                type="button"
                className={seg(method === "put")}
                onClick={() => setMethod("put")}
                data-testid="oss-share-method-put"
              >
                {t("oss.share.methodPut")}
              </button>
            </div>
          </div>

          {/* 有效期 */}
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-medium text-muted-foreground">{t("oss.share.expiryLabel")}</span>
            <div className={segGroup}>
              {EXPIRIES.map((e) => (
                <button
                  key={e.secs}
                  type="button"
                  className={seg(expirySecs === e.secs)}
                  onClick={() => setExpirySecs(e.secs)}
                  data-testid={`oss-share-expiry-${e.secs}`}
                >
                  {t(e.key)}
                </button>
              ))}
            </div>
          </div>

          {/* 预签名 URL */}
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-medium text-muted-foreground">{t("oss.share.urlLabel")}</span>
            <div className="flex flex-col gap-2 rounded-md border bg-muted/40 p-2.5">
              {loading ? (
                <span className="text-[11px] text-muted-foreground">{t("oss.share.generating")}</span>
              ) : url ? (
                <>
                  <span
                    data-testid="oss-share-url"
                    className="min-w-0 break-all font-mono text-[11px] leading-relaxed text-sky-300"
                  >
                    {url}
                  </span>
                  <div className="flex justify-end">
                    <button
                      type="button"
                      className="flex items-center gap-1 rounded px-2 py-1 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                      onClick={copy}
                      data-testid="oss-share-copy-inline"
                    >
                      <Copy className="size-3" /> {t("oss.share.copyLink")}
                    </button>
                  </div>
                </>
              ) : (
                <span className="text-[11px] text-muted-foreground">{t("oss.share.urlEmpty")}</span>
              )}
            </div>
          </div>

          {/* 过期提示 */}
          <div className="flex items-center gap-1.5 text-[11px] text-warning">
            <Hourglass className="size-3 shrink-0" />
            <span>{t("oss.share.warning", { duration: t(currentExpiryKey) })}</span>
          </div>
        </div>

        <DialogFooter className="flex-row justify-between gap-2">
          <Button size="sm" variant="outline" onClick={runGenerate} disabled={loading} data-testid="oss-share-generate">
            <RefreshCw className="size-3" /> {url ? t("oss.share.regenerate") : t("oss.share.generate")}
          </Button>
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("oss.share.close")}
            </Button>
            <Button size="sm" onClick={copy} disabled={!url} data-testid="oss-share-copy">
              <Copy className="size-3" /> {t("oss.share.copyLink")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
