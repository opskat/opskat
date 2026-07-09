import { useTranslation } from "react-i18next";
import { ArrowUpRight, Plus } from "lucide-react";
import { useAIStore } from "@/stores/aiStore";
import { deriveReferences, jumpToAsset, isAssetTabOpen } from "@/lib/aiReferences";

export function ReferencesRow({
  conversationId,
  boundAssetId,
}: {
  conversationId: number | null;
  boundAssetId?: number | null;
}) {
  const { t } = useTranslation();
  const messages = useAIStore((s) => (conversationId != null ? s.conversationMessages[conversationId] : undefined));
  if (conversationId == null) return null;
  const refs = deriveReferences(messages ?? [], { excludeAssetId: boundAssetId });
  if (refs.length === 0) return null;

  return (
    <div
      className="flex flex-wrap items-center gap-1.5 pt-1 text-xs text-muted-foreground"
      data-testid="references-row"
    >
      <span className="shrink-0">{t("ai.sidebar.referencedThisSession")}:</span>
      {refs.map((r) => {
        const open = isAssetTabOpen(r.assetId);
        return (
          <button
            key={r.assetId}
            type="button"
            onClick={() => jumpToAsset(r.assetId)}
            className="inline-flex items-center gap-1 rounded border border-border bg-secondary px-1.5 py-0.5 hover:bg-secondary/70"
          >
            <span className="truncate max-w-[120px]">{r.name}</span>
            {open ? <ArrowUpRight className="h-3 w-3" /> : <Plus className="h-3 w-3" />}
          </button>
        );
      })}
    </div>
  );
}
