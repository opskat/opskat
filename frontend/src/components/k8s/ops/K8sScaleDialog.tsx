import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Minus, Plus } from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
} from "@opskat/ui";
import { errorMessage } from "@/lib/errorMessage";
import { useK8sStore } from "@/stores/k8sStore";
import { GetK8sResource, ScaleK8sResource } from "../../../../wailsjs/go/k8s/K8s";
import type { K8sActionContext } from "../actions";
import { useHpaOf } from "../detail/workloads/useHpaOf";
import { refOf, runK8sOp, typeOf } from "./opTarget";

const REPLICAS = /^\d+$/;

/** Scale dialog: current replicas, a ≥ 0 target by typing or −/+, and a warning (not a block) when an HPA manages it. */
export function K8sScaleDialog({ ctx }: { ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const closeOp = useK8sStore((s) => s.closeOp);
  const kind = typeOf(ctx.assetId, ctx.typeKey)?.kind ?? "";
  const { hpa } = useHpaOf(ctx.assetId, ctx.namespace, kind, ctx.name);
  const [current, setCurrent] = useState<number | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [typed, setTyped] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let live = true;
    GetK8sResource(refOf(ctx) as never)
      .then((raw) => {
        if (!live) return;
        const detail = JSON.parse(raw) as { notFound: boolean; object: { spec?: { replicas?: number } } | null };
        if (detail.notFound || !detail.object) setLoadError(t("asset.k8sScaleNotFound"));
        else setCurrent(detail.object.spec?.replicas ?? 1);
      })
      .catch((e) => live && setLoadError(errorMessage(e)));
    return () => {
      live = false;
    };
  }, [ctx, t]);

  const text = typed ?? (current === null ? "" : String(current));
  const valid = REPLICAS.test(text.trim());
  const target = valid ? Number(text.trim()) : null;
  const step = (delta: number) => setTyped(String(Math.max(0, (target ?? current ?? 0) + delta)));

  const submit = async () => {
    if (target === null) return;
    setSubmitting(true);
    const ok = await runK8sOp(
      () => ScaleK8sResource({ ref: refOf(ctx), replicas: target } as never),
      t("asset.k8sScaled", { name: ctx.name, count: target }),
      () => {
        closeOp(ctx.assetId);
        void useK8sStore.getState().refreshType(ctx.assetId, ctx.typeKey);
      }
    );
    if (!ok) setSubmitting(false);
  };

  return (
    <Dialog open onOpenChange={(open) => !open && closeOp(ctx.assetId)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("asset.k8sScaleTitle")}</DialogTitle>
          <DialogDescription>
            {kind} <span className="font-mono">{ctx.name}</span>
            {current !== null && ` · ${t("asset.k8sScaleCurrent", { count: current })}`}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label className="text-xs">{t("asset.k8sScaleTarget")}</Label>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t("asset.k8sScaleDecrease")}
              disabled={current === null || target === 0}
              onClick={() => step(-1)}
            >
              <Minus />
            </Button>
            <Input
              type="number"
              min={0}
              value={text}
              disabled={current === null}
              onChange={(e) => setTyped(e.target.value)}
              className="h-8 w-20 text-center font-mono text-sm"
            />
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t("asset.k8sScaleIncrease")}
              disabled={current === null}
              onClick={() => step(1)}
            >
              <Plus />
            </Button>
            {current !== null && valid && (
              <span className="text-xs text-muted-foreground">
                {current} → {target}
              </span>
            )}
          </div>
          {current !== null && !valid && (
            <div role="alert" className="text-xs text-destructive">
              {t("asset.k8sScaleInvalid")}
            </div>
          )}
          {loadError && (
            <div role="alert" className="select-text break-words text-xs text-destructive">
              {loadError}
            </div>
          )}
          {hpa && (
            <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
              {t("asset.k8sScaleHpa", { name: hpa.name, min: hpa.min, max: hpa.max })}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => closeOp(ctx.assetId)}>
            {t("action.cancel")}
          </Button>
          <Button size="sm" disabled={target === null || current === null || submitting} onClick={() => void submit()}>
            {t("asset.k8sScaleSubmit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
