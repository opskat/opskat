import { useTranslation } from "react-i18next";
import { Copy, Pencil } from "lucide-react";
import { Button } from "@opskat/ui";
import { toast } from "sonner";
import { errorMessage } from "@/lib/errorMessage";
import { notifyCopied } from "@/lib/notify";

/** Read-only YAML (the cluster read already omits managedFields); 「编辑」 only when the type's 编辑 YAML action applies. */
export function K8sYamlTab({ yaml, onEdit }: { yaml: string; onEdit: (() => void) | undefined }) {
  const { t } = useTranslation();
  const copy = () =>
    navigator.clipboard.writeText(yaml).then(
      () => notifyCopied(t("action.copied")),
      (e) => toast.error(errorMessage(e))
    );
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={() => void copy()}>
          <Copy className="h-3 w-3" />
          {t("action.copy")}
        </Button>
        {onEdit && (
          <Button variant="outline" size="sm" onClick={onEdit}>
            <Pencil className="h-3 w-3" />
            {t("asset.k8sEditYaml")}
          </Button>
        )}
      </div>
      <pre
        data-testid="k8s-yaml"
        className="select-text overflow-auto whitespace-pre rounded-lg bg-muted/50 p-3 font-mono text-xs"
      >
        {yaml}
      </pre>
    </div>
  );
}
