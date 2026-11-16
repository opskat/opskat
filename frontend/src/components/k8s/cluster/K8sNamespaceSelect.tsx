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
  onChange: (namespace: string) => void;
}

/**
 * Namespace picker: the cluster's namespaces under "all namespaces". When the account can't list
 * namespaces it becomes a text box holding the current namespace, so the user can type one.
 */
export function K8sNamespaceSelect({ value, namespaces, onChange }: Props) {
  const { t } = useTranslation();

  if (namespaces.status === "error") {
    return <NamespaceInput value={value} reason={namespaces.error} onChange={onChange} />;
  }

  const names = namespaces.data ?? [];
  const options = value && !names.includes(value) ? [value, ...names] : names;
  return (
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
  );
}

function NamespaceInput({
  value,
  reason,
  onChange,
}: {
  value: string;
  reason: string | null;
  onChange: (namespace: string) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState(value);
  const commit = () => {
    const next = draft.trim();
    if (next !== value) onChange(next);
  };
  return (
    <div title={reason ?? undefined}>
      <div className="relative">
        <Layers className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === "Enter") commit();
          }}
          placeholder={t("asset.k8sNamespaceInputPlaceholder")}
          aria-label={t("asset.k8sNamespace")}
          className="h-7 pl-7 font-mono text-xs"
        />
      </div>
      <p className="mt-1 text-[11px] text-muted-foreground">{t("asset.k8sNamespaceListFailed")}</p>
    </div>
  );
}
