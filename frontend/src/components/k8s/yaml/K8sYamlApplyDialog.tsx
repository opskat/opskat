import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  cn,
} from "@opskat/ui";
import { errorMessage } from "@/lib/errorMessage";
import { notifySuccess } from "@/lib/notify";
import { useK8sStore } from "@/stores/k8sStore";
import { ApplyK8sYAML } from "../../../../wailsjs/go/k8s/K8s";
import { K8sYamlEditor } from "./K8sYamlEditor";

interface ApplyResult {
  index: number;
  kind: string;
  name: string;
  namespace: string;
  action: "created" | "updated" | "failed";
  error?: string;
  errorCode?: string;
}

const ACTION_LABEL = {
  created: "asset.k8sApplyCreated",
  updated: "asset.k8sApplyUpdated",
  failed: "asset.k8sApplyFailed",
} as const;

/** The apply-YAML dialog: pasted documents go to the cluster one by one and each result is listed. */
export function K8sYamlApplyDialog({ assetId }: { assetId: number }) {
  const { t } = useTranslation();
  const namespace = useK8sStore((s) => s.clusters[assetId]?.namespace ?? "");
  const closeDialog = useK8sStore((s) => s.closeYamlDialog);
  const [text, setText] = useState("");
  const [results, setResults] = useState<ApplyResult[] | null>(null);
  const [applying, setApplying] = useState(false);

  const apply = async () => {
    setApplying(true);
    try {
      const { results: done } = JSON.parse(
        await ApplyK8sYAML({ assetId, yaml: text, defaultNamespace: namespace } as never)
      ) as { results: ApplyResult[] };
      setResults(done);
      const applied = done.filter((r) => r.action !== "failed").length;
      if (applied > 0) void useK8sStore.getState().refreshAll(assetId);
      if (done.length > 0 && applied === done.length) notifySuccess(t("asset.k8sApplyDone", { count: applied }));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setApplying(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && closeDialog(assetId)}>
      <DialogContent className="flex h-[80vh] flex-col sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="text-base">{t("asset.k8sApplyYaml")}</DialogTitle>
          <DialogDescription>
            {namespace ? t("asset.k8sApplyYamlDescNs", { namespace }) : t("asset.k8sApplyYamlDescAll")}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-auto rounded-md border">
          {results ? (
            <ul className="divide-y">
              {results.map((r) => (
                <li key={r.index} data-testid="k8s-apply-result" className="flex items-start gap-3 px-3 py-2 text-xs">
                  <span
                    className={cn(
                      "shrink-0 rounded-full px-2 py-0.5 font-medium",
                      r.action === "failed" ? "bg-destructive/15 text-destructive" : "bg-success/15 text-success"
                    )}
                  >
                    {t(ACTION_LABEL[r.action])}
                  </span>
                  <div className="min-w-0">
                    <div className="font-mono">
                      {r.kind && `${r.kind}/`}
                      {r.name || t("asset.k8sApplyDocument", { index: r.index + 1 })}
                      {r.namespace && <span className="text-muted-foreground"> · {r.namespace}</span>}
                    </div>
                    {r.action === "failed" && (
                      <div className="select-text whitespace-pre-wrap break-words text-destructive">
                        {r.errorCode === "namespaceRequired" ? t("asset.k8sYamlNamespaceRequired") : r.error}
                      </div>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <K8sYamlEditor value={text} onChange={setText} placeholder={t("asset.k8sApplyYamlPlaceholder")} />
          )}
        </div>
        <DialogFooter>
          {results ? (
            <Button variant="outline" size="sm" onClick={() => setResults(null)}>
              {t("asset.k8sApplyBackToEdit")}
            </Button>
          ) : null}
          <Button variant="outline" size="sm" onClick={() => closeDialog(assetId)}>
            {results ? t("action.close") : t("action.cancel")}
          </Button>
          {!results && (
            <Button size="sm" disabled={!text.trim() || applying} onClick={() => void apply()}>
              {t("asset.k8sYamlApply")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
