import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { toast } from "sonner";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Tabs,
  TabsList,
  TabsTrigger,
} from "@opskat/ui";
import { CodeDiffViewer } from "@/components/CodeDiffViewer";
import { errorMessage } from "@/lib/errorMessage";
import { notifySuccess } from "@/lib/notify";
import { buildTextDiffBlocks } from "@/lib/textDiffBlocks";
import { useK8sStore, type K8sResourceDetail } from "@/stores/k8sStore";
import { GetK8sResource, UpdateK8sResource } from "../../../../wailsjs/go/k8s/K8s";
import type { K8sActionContext } from "../actions";
import { K8sYamlEditor } from "./K8sYamlEditor";
import { dumpYaml, editableYaml, parseYamlObject } from "./yamlText";

/** The resource as read from the cluster, and what saving needs from that read. */
interface ClusterRead {
  /** The cluster's version as the editor shows it (no status / managedFields). */
  yaml: string;
  resourceVersion: string;
  /** Kept out of the editor but sent back on save, so a type without a status subresource keeps its status. */
  status: unknown;
}

interface UpdateResult {
  conflict: boolean;
  message: string;
}

function metadataOf(object: Record<string, unknown>) {
  return (object.metadata ?? {}) as Record<string, unknown>;
}

/** The edit-YAML dialog of one resource: edits the text, compares it with the cluster's version and saves it. */
export function K8sYamlEditDialog({ assetId, ctx }: { assetId: number; ctx: K8sActionContext }) {
  const { t } = useTranslation();
  const apiVersion = useK8sStore((s) => {
    const [group] = ctx.typeKey.split("/");
    return group === ""
      ? "v1"
      : s.clusters[assetId]?.types.data?.types.find((x) => `${x.group}/${x.resource}` === ctx.typeKey)?.version;
  });
  const closeDialog = useK8sStore((s) => s.closeYamlDialog);

  const [read, setRead] = useState<ClusterRead | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [mode, setMode] = useState<"edit" | "diff">("edit");
  const [conflict, setConflict] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [group, resource] = ctx.typeKey.split("/");
  const ref = useMemo(
    () => ({ assetId, group, version: apiVersion ?? "", resource, namespace: ctx.namespace, name: ctx.name }),
    [assetId, group, apiVersion, resource, ctx.namespace, ctx.name]
  );

  /** Reads the cluster's current version; throws on failure. */
  const readCluster = useCallback(async (): Promise<ClusterRead> => {
    if (!ref.version) throw new Error(`unknown resource type ${ctx.typeKey}`);
    const detail = JSON.parse(await GetK8sResource(ref as never)) as K8sResourceDetail;
    if (detail.notFound || !detail.object) throw new Error(t("asset.k8sResourceGone"));
    return {
      yaml: editableYaml(detail.object),
      resourceVersion: String(metadataOf(detail.object).resourceVersion ?? ""),
      status: detail.object.status,
    };
  }, [ref, ctx.typeKey, t]);

  useEffect(() => {
    let cancelled = false;
    readCluster().then(
      (r) => {
        if (cancelled) return;
        setRead(r);
        setText(r.yaml);
      },
      (e) => !cancelled && setReadError(errorMessage(e))
    );
    return () => {
      cancelled = true;
    };
  }, [readCluster]);

  const parsed = useMemo(() => parseYamlObject(text), [text]);
  const syntaxError = parsed.ok ? null : parsed.error;
  const changes = useMemo(() => (read ? buildTextDiffBlocks(read.yaml, text).length : 0), [read, text]);

  const save = async () => {
    if (!read || !parsed.ok) return;
    // The version is the one read, whatever the text says: a hand-edited resourceVersion must not defeat the check.
    const body: Record<string, unknown> = {
      ...parsed.value,
      metadata: { ...metadataOf(parsed.value), resourceVersion: read.resourceVersion },
    };
    if (read.status !== undefined && body.status === undefined) body.status = read.status;
    setSaving(true);
    try {
      const result = JSON.parse(await UpdateK8sResource({ ref, yaml: dumpYaml(body) } as never)) as UpdateResult;
      if (result.conflict) {
        setConflict(result.message);
        return;
      }
      notifySuccess(t("asset.k8sYamlSaved", { name: ctx.name }));
      void useK8sStore.getState().refreshType(assetId, ctx.typeKey);
      closeDialog(assetId);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const reloadAndCompare = async () => {
    try {
      setRead(await readCluster());
      setConflict(null);
      setMode("diff");
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && closeDialog(assetId)}>
      <DialogContent className="flex h-[80vh] flex-col sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-base">{t("asset.k8sEditYamlTitle", { name: ctx.name })}</DialogTitle>
          <DialogDescription>{t("asset.k8sEditYamlDesc")}</DialogDescription>
        </DialogHeader>
        {readError ? (
          <p role="alert" className="select-text break-words text-xs text-destructive">
            {readError}
          </p>
        ) : !read ? (
          <div className="flex-1" />
        ) : (
          <>
            <Tabs value={mode} onValueChange={(v) => setMode(v as "edit" | "diff")}>
              <TabsList className="h-7">
                <TabsTrigger value="edit" className="px-2 text-xs">
                  {t("asset.k8sYamlEdit")}
                </TabsTrigger>
                <TabsTrigger value="diff" className="px-2 text-xs">
                  {t("asset.k8sYamlCompare")}
                  {changes > 0 && (
                    <span className="ml-1 text-[11px] text-primary">
                      {t("asset.k8sYamlChanges", { count: changes })}
                    </span>
                  )}
                </TabsTrigger>
              </TabsList>
            </Tabs>
            {conflict !== null && (
              <div
                role="alert"
                className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
              >
                <AlertTriangle className="h-4 w-4 shrink-0" />
                <span className="min-w-0">
                  {t("asset.k8sYamlConflict")}
                  <span className="ml-1 select-text break-words font-mono">{conflict}</span>
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-auto h-7 shrink-0 text-xs"
                  onClick={() => void reloadAndCompare()}
                >
                  {t("asset.k8sYamlReloadCompare")}
                </Button>
              </div>
            )}
            {syntaxError && (
              <p role="alert" className="select-text break-words text-xs text-destructive">
                {t("asset.k8sYamlSyntaxError")}
                <span className="ml-1 font-mono">
                  {t("asset.k8sYamlErrorAt", { line: syntaxError.line })} {syntaxError.message}
                </span>
              </p>
            )}
            <div className="min-h-0 flex-1 overflow-hidden rounded-md border">
              {mode === "diff" ? (
                <CodeDiffViewer
                  original={read.yaml}
                  modified={text}
                  language="yaml"
                  originalTitle={t("asset.k8sYamlClusterVersion")}
                  modifiedTitle={t("asset.k8sYamlYourEdit")}
                />
              ) : (
                <K8sYamlEditor value={text} onChange={setText} error={syntaxError} />
              )}
            </div>
          </>
        )}
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => closeDialog(assetId)}>
            {t("action.cancel")}
          </Button>
          <Button size="sm" disabled={!read || !parsed.ok || saving} onClick={() => void save()}>
            {t("asset.k8sYamlSave")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
