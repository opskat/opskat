import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, Button, Textarea } from "@opskat/ui";
import { OSSPresignGet, OSSPresignPut } from "../../../wailsjs/go/oss/OSS";
import { notifyCopied } from "@/lib/notify";

export interface OSSPresignDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetId: number;
  bucket: string;
  objectKey: string;
}

type Method = "get" | "put";
const EXPIRIES: { secs: number; key: string }[] = [
  { secs: 900, key: "oss.share.expiry15m" },
  { secs: 3600, key: "oss.share.expiry1h" },
  { secs: 86400, key: "oss.share.expiry24h" },
  { secs: 604800, key: "oss.share.expiry7d" },
];

export function OSSPresignDialog({ open, onOpenChange, assetId, bucket, objectKey }: OSSPresignDialogProps) {
  const { t } = useTranslation();
  const [method, setMethod] = useState<Method>("get");
  const [expirySecs, setExpirySecs] = useState(3600);
  const [url, setUrl] = useState("");
  const [loading, setLoading] = useState(false);
  const reqIdRef = useRef(0);

  // 每次打开重置；改方法/有效期作废旧 URL（强制重新生成）。
  useEffect(() => {
    if (open) {
      reqIdRef.current++;
      setMethod("get");
      setExpirySecs(3600);
      setUrl("");
      setLoading(false);
    }
  }, [open]);

  const pickMethod = (m: Method) => {
    reqIdRef.current++;
    setMethod(m);
    setUrl("");
  };
  const pickExpiry = (secs: number) => {
    reqIdRef.current++;
    setExpirySecs(secs);
    setUrl("");
  };

  const generate = async () => {
    const myId = ++reqIdRef.current;
    setLoading(true);
    try {
      const req = { assetId, bucket, key: objectKey, expirySecs };
      const u = method === "get" ? await OSSPresignGet(req) : await OSSPresignPut(req);
      if (reqIdRef.current === myId) setUrl(u);
    } catch (err) {
      if (reqIdRef.current === myId) toast.error(`${t("oss.share.generateFailed")}: ${String(err)}`);
    } finally {
      setLoading(false);
    }
  };

  const copy = () => void navigator.clipboard?.writeText(url).then(() => notifyCopied(t("oss.share.copied")));
  const copyAndClose = () => {
    copy();
    onOpenChange(false);
  };

  const seg = (active: boolean) =>
    `flex-1 rounded px-2 py-1 ${active ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground"}`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("oss.share.title")}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-3 text-xs">
          <div className="truncate font-mono text-muted-foreground" title={objectKey}>
            {objectKey}
          </div>

          <div className="flex gap-1">
            <button
              type="button"
              className={seg(method === "get")}
              onClick={() => pickMethod("get")}
              data-testid="oss-share-method-get"
            >
              {t("oss.share.methodGet")}
            </button>
            <button
              type="button"
              className={seg(method === "put")}
              onClick={() => pickMethod("put")}
              data-testid="oss-share-method-put"
            >
              {t("oss.share.methodPut")}
            </button>
          </div>

          <div className="flex gap-1">
            {EXPIRIES.map((e) => (
              <button
                key={e.secs}
                type="button"
                className={seg(expirySecs === e.secs)}
                onClick={() => pickExpiry(e.secs)}
                data-testid={`oss-share-expiry-${e.secs}`}
              >
                {t(e.key)}
              </button>
            ))}
          </div>

          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground">{t("oss.share.urlLabel")}</span>
            <Textarea readOnly value={url} rows={3} className="font-mono" data-testid="oss-share-url" />
          </div>

          <p className="text-warning">{t("oss.share.warning")}</p>
        </div>

        <DialogFooter className="flex-row justify-between gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() => void generate()}
            disabled={loading}
            data-testid="oss-share-generate"
          >
            {url ? t("oss.share.regenerate") : t("oss.share.generate")}
          </Button>
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("oss.share.close")}
            </Button>
            <Button size="sm" onClick={copyAndClose} disabled={!url} data-testid="oss-share-copy-close">
              {t("oss.share.copyAndClose")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
