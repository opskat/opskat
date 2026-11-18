import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Layers } from "lucide-react";
import { Input, Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from "@opskat/ui";
import type { Loadable } from "@/stores/k8sStore";

// Radix Select can't hold an empty value, so "all namespaces" ('' in the store) travels as this sentinel.
const ALL = "__all__";

interface Props {
  value: string;
  namespaces: Loadable<string[]>;
  /** The namespace configured on the asset ('' = none). */
  configured: string;
  onChange: (namespace: string) => void;
}

/**
 * Namespace picker: the cluster's namespaces under "all namespaces". When the account can't list namespaces it
 * offers only the asset's configured namespace (and the current one), plus a box to type any namespace. A failed
 * re-read keeps the namespaces read before.
 */
export function K8sNamespaceSelect({ value, namespaces, configured, onChange }: Props) {
  const { t } = useTranslation();
  const unlisted = namespaces.status === "error" && !namespaces.data;
  const names = unlisted ? [configured].filter(Boolean) : (namespaces.data ?? []);
  const options = value && !names.includes(value) ? [value, ...names] : names;
  return (
    <div title={unlisted ? (namespaces.error ?? undefined) : undefined}>
      <Select
        value={value || ALL}
        onValueChange={(v) => onChange(v === ALL ? "" : v)}
        disabled={namespaces.status === "loading" && !namespaces.data}
      >
        <SelectTrigger size="sm" className="h-7 w-full text-xs" aria-label={t("asset.k8sNamespace")}>
          <Layers className="h-3.5 w-3.5 text-muted-foreground" />
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL} className="text-xs">
            {t("asset.k8sAllNamespaces")}
          </SelectItem>
          {options.length > 0 && <SelectSeparator />}
          {options.map((name) => (
            <SelectItem key={name} value={name} className="font-mono text-xs">
              {name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {unlisted && <NamespaceInput onChange={onChange} />}
    </div>
  );
}

/** Types a namespace the cluster wouldn't list; Enter or leaving the box selects it. */
function NamespaceInput({ onChange }: { onChange: (namespace: string) => void }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  const commit = () => {
    const next = draft.trim();
    if (!next) return;
    onChange(next);
    setDraft("");
  };
  return (
    <div className="mt-1">
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit();
        }}
        placeholder={t("asset.k8sNamespaceInputPlaceholder")}
        aria-label={t("asset.k8sNamespaceInputPlaceholder")}
        className="h-7 font-mono text-xs"
      />
      <p className="mt-1 text-[11px] text-muted-foreground">{t("asset.k8sNamespaceListFailed")}</p>
    </div>
  );
}
