import { useCallback, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Puzzle, ShieldAlert } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@opskat/ui";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { EntityIcon } from "@/components/asset/AssetIcon";
import { formatBytes } from "@/lib/formatBytes";
import { RespondExtensionInstallConfirm } from "../../wailsjs/go/extension/Extension";

const INSTALL_MODE_KEYS = {
  install: {
    title: "extension.installConfirm.title",
    description: "extension.installConfirm.description",
    action: "extension.installConfirm.install",
  },
  update: {
    title: "extension.installConfirm.titleUpdate",
    description: "extension.installConfirm.descriptionUpdate",
    action: "extension.installConfirm.update",
  },
  downgrade: {
    title: "extension.installConfirm.titleDowngrade",
    description: "extension.installConfirm.descriptionDowngrade",
    action: "extension.installConfirm.downgradeAction",
  },
} as const;

/** One grant an extension asks for (extension_svc.CapabilityGrant). */
interface CapabilityGrant {
  kind: string;
  value: string;
}

/** The ext:install-confirm payload (internal/app/extension installConfirmRequest). */
interface InstallConfirmRequest {
  id: string;
  name: string;
  displayName: string;
  icon: string;
  /** Installed version; "" for a fresh install. */
  from: string;
  to: string;
  downgrade: boolean;
  source: "store" | "file" | "dir";
  size: number;
  capabilities: CapabilityGrant[];
  /** Grants the installed version did not have. */
  added: CapabilityGrant[];
}

const SOURCE_LABEL: Record<InstallConfirmRequest["source"], string> = {
  store: "extension.installConfirm.sourceStore",
  file: "extension.installConfirm.sourceFile",
  dir: "extension.installConfirm.sourceDir",
};

const sameGrant = (a: CapabilityGrant, b: CapabilityGrant) => a.kind === b.kind && a.value === b.value;

/**
 * App-wide install confirm. Every install — local ZIP / directory, the store,
 * opsctl — asks here before anything lands. Concurrent confirms queue; the
 * backend sends ext:install-confirm-closed when it stops waiting for one (caller
 * gone, timed out, app shutting down) and that confirm is dropped unanswered.
 */
export function ExtensionInstallConfirmDialog() {
  const { t } = useTranslation();
  const [queue, setQueue] = useState<InstallConfirmRequest[]>([]);
  const current = queue[0];

  const remove = useCallback((id: string) => {
    setQueue((q) => q.filter((c) => c.id !== id));
  }, []);

  useWailsEvent(
    "ext:install-confirm",
    useCallback((data: InstallConfirmRequest) => {
      setQueue((q) => [...q, data]);
    }, [])
  );
  useWailsEvent(
    "ext:install-confirm-closed",
    useCallback((data: { id: string }) => remove(data.id), [remove])
  );

  const respond = (ok: boolean) => {
    if (!current) return;
    remove(current.id);
    RespondExtensionInstallConfirm(current.id, ok).catch((e) => toast.error(String(e)));
  };

  // Answering swaps the next queued confirm in under the same buttons, so the rest
  // of a double-click (detail > 1) would answer one the user never saw.
  const answer = (ok: boolean) => (e: MouseEvent) => {
    if (e.detail > 1) return;
    respond(ok);
  };

  const credentialsRead = current?.capabilities.some((c) => c.kind === "credentials" && c.value === "read");
  const mode = !current?.from ? "install" : current.downgrade ? "downgrade" : "update";
  const confirmLabel = INSTALL_MODE_KEYS[mode].action;

  return (
    <Dialog open={!!current} onOpenChange={(open) => !open && respond(false)}>
      <DialogContent className="sm:max-w-md max-h-[80vh] flex flex-col" data-testid="ext-install-confirm-dialog">
        {current && (
          <>
            <DialogHeader>
              <DialogTitle>{t(INSTALL_MODE_KEYS[mode].title)}</DialogTitle>
              <DialogDescription>{t(INSTALL_MODE_KEYS[mode].description)}</DialogDescription>
            </DialogHeader>

            <div className="space-y-3 overflow-y-auto flex-1 min-h-0 text-sm">
              <div className="flex items-center gap-3">
                <div className="h-10 w-10 rounded-lg bg-muted flex items-center justify-center shrink-0">
                  <EntityIcon icon={current.icon} fallback={Puzzle} className="h-5 w-5" aria-hidden />
                </div>
                <div className="min-w-0">
                  <p className="font-medium truncate">{current.displayName}</p>
                  <p className="text-xs text-muted-foreground font-mono truncate">{current.name}</p>
                </div>
              </div>

              {credentialsRead && (
                <div
                  role="alert"
                  className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/15 p-3 text-warning"
                >
                  <ShieldAlert className="h-4 w-4 mt-0.5 shrink-0" />
                  <span>{t("extension.credentialsReadWarning")}</span>
                </div>
              )}

              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5">
                <dt className="text-muted-foreground">{t("extension.version")}</dt>
                <dd className="flex items-center gap-2">
                  <span data-testid="ext-install-version" className="font-mono">
                    {current.from ? `${current.from} → ${current.to}` : current.to}
                  </span>
                  {current.downgrade && (
                    <span className="text-xs px-1.5 py-0.5 rounded bg-warning/15 text-warning">
                      {t("extension.installConfirm.downgrade")}
                    </span>
                  )}
                </dd>
                <dt className="text-muted-foreground">{t("extension.installConfirm.source")}</dt>
                <dd>{t(SOURCE_LABEL[current.source])}</dd>
                <dt className="text-muted-foreground">{t("extension.installConfirm.size")}</dt>
                <dd>{formatBytes(current.size)}</dd>
              </dl>

              <div>
                <h4 className="font-medium mb-1.5">{t("extension.installConfirm.capabilities")}</h4>
                {current.capabilities.length === 0 ? (
                  <p className="text-xs text-muted-foreground">{t("extension.installConfirm.noCapabilities")}</p>
                ) : (
                  <ul className="space-y-1">
                    {current.capabilities.map((cap) => (
                      <li
                        key={`${cap.kind}:${cap.value}`}
                        data-testid={`ext-install-cap-${cap.kind}`}
                        className="flex items-center gap-2 text-xs p-2 rounded bg-muted/50"
                      >
                        <span className="font-mono font-medium shrink-0">{cap.kind}</span>
                        {cap.value && <span className="font-mono text-muted-foreground break-all">{cap.value}</span>}
                        {current.added.some((a) => sameGrant(a, cap)) && (
                          <span className="ml-auto shrink-0 px-1.5 py-0.5 rounded bg-info/15 text-info">
                            {t("extension.installConfirm.added")}
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>

            <DialogFooter className="gap-2">
              <Button variant="outline" data-testid="ext-install-cancel" onClick={answer(false)}>
                {t("action.cancel")}
              </Button>
              <Button data-testid="ext-install-confirm" onClick={answer(true)}>
                {t(confirmLabel)}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
