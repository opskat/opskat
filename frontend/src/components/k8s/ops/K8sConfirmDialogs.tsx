import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
} from "@opskat/ui";
import { useK8sStore } from "@/stores/k8sStore";
import { DeleteK8sResource, RestartK8sWorkload } from "../../../../wailsjs/go/k8s/K8s";
import type { K8sActionContext } from "../actions";
import { controllerOf, refOf, runK8sOp, typeOf } from "./opTarget";

const NAMESPACES = "/namespaces";

export function K8sRestartDialog({ ctx }: { ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const closeOp = useK8sStore((s) => s.closeOp);
  const kind = typeOf(ctx.assetId, ctx.typeKey)?.kind ?? "";
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && closeOp(ctx.assetId)}
      variant="default"
      title={t("asset.k8sRestartTitle", { kind, name: ctx.name })}
      description={<p className="text-sm">{t("asset.k8sRestartDesc")}</p>}
      cancelText={t("action.cancel")}
      confirmText={t("asset.k8sRestartConfirm")}
      onConfirm={() => {
        closeOp(ctx.assetId);
        void runK8sOp(
          () => RestartK8sWorkload(refOf(ctx) as never),
          t("asset.k8sRestarted", { name: ctx.name }),
          () => void useK8sStore.getState().refreshType(ctx.assetId, ctx.typeKey)
        );
      }}
    />
  );
}

function deleteResource(ctx: K8sActionContext, message: string) {
  const store = useK8sStore.getState();
  return runK8sOp(
    () => DeleteK8sResource(refOf(ctx) as never),
    message,
    () => {
      store.closeOp(ctx.assetId);
      store.closeResourceTabs(ctx.assetId, ctx.typeKey, ctx.namespace, ctx.name);
      void store.refreshType(ctx.assetId, ctx.typeKey);
      if (ctx.typeKey === NAMESPACES) void store.loadNamespaces(ctx.assetId);
    }
  );
}

/** Delete confirmation; a Namespace needs its name typed because everything inside goes with it. */
export function K8sDeleteDialog({ ctx }: { ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const closeOp = useK8sStore((s) => s.closeOp);
  const kind = typeOf(ctx.assetId, ctx.typeKey)?.kind ?? "";
  const [typed, setTyped] = useState("");
  const [deleting, setDeleting] = useState(false);
  const owner = ctx.typeKey === "/pods" ? controllerOf(ctx) : null;
  const title = t("asset.k8sDeleteTitle", { kind, name: ctx.name });
  const done = t("asset.k8sDeleted", { name: ctx.name });

  if (ctx.typeKey === NAMESPACES) {
    const submit = async () => {
      setDeleting(true);
      if (!(await deleteResource(ctx, done))) setDeleting(false);
    };
    return (
      <Dialog open onOpenChange={(open) => !open && closeOp(ctx.assetId)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{t("asset.k8sDeleteNamespaceConsequence")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <label className="text-xs" htmlFor="k8s-delete-ns-name">
              {t("asset.k8sDeleteNamespaceType", { name: ctx.name })}
            </label>
            <Input
              id="k8s-delete-ns-name"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              className="h-8 font-mono text-sm"
            />
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => closeOp(ctx.assetId)}>
              {t("action.cancel")}
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={typed !== ctx.name || deleting}
              onClick={() => void submit()}
            >
              {t("asset.k8sDeleteConfirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && closeOp(ctx.assetId)}
      title={title}
      description={
        <div className="space-y-2 text-sm">
          <p>{t("asset.k8sDeleteDesc")}</p>
          {owner && <p>{t("asset.k8sDeleteControlled", { owner })}</p>}
        </div>
      }
      cancelText={t("action.cancel")}
      confirmText={t("asset.k8sDeleteConfirm")}
      onConfirm={() => {
        closeOp(ctx.assetId);
        void deleteResource(ctx, done);
      }}
    />
  );
}
