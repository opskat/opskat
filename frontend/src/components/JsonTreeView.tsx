// frontend/src/components/JsonTreeView.tsx
//
// A small read-only JSON tree viewer: expand/collapse per node, no editing. Built
// for @opskat/host-ui (frontend/src/extension/inject.ts) — extension pages had no
// way to show structured data other than dumping raw JSON text into CodeEditor
// (see QueryResultJsonView, which does exactly that). Uses only semantic Tailwind
// tokens (text-foreground / text-muted-foreground / …) so it follows the host
// theme the same way every other component does, and routes its two interactive
// labels through react-i18next so it follows the host language.
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronRight, ChevronDown } from "lucide-react";

export interface JsonTreeViewProps {
  data: unknown;
  className?: string;
}

const INDENT_PX = 14;

function isExpandable(value: unknown): value is Record<string, unknown> | unknown[] {
  return value !== null && typeof value === "object";
}

function formatPrimitive(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "undefined";
  if (typeof value === "string") return JSON.stringify(value);
  return String(value);
}

function primitiveClassName(value: unknown): string {
  if (value === null || value === undefined) return "text-syntax-null";
  switch (typeof value) {
    case "string":
      return "text-syntax-string";
    case "number":
      return "text-syntax-number";
    case "boolean":
      return "text-syntax-boolean";
    default:
      return "text-foreground";
  }
}

function JsonTreeNode({ label, value, depth }: { label: string | null; value: unknown; depth: number }) {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(true);
  const indent = depth * INDENT_PX;

  if (!isExpandable(value)) {
    return (
      <div className="flex items-start gap-1 py-0.5 font-mono text-xs leading-5" style={{ paddingLeft: indent }}>
        <span className="w-3.5 shrink-0" />
        {label !== null && <span className="shrink-0 text-info">{label}:</span>}
        <span className={primitiveClassName(value)}>{formatPrimitive(value)}</span>
      </div>
    );
  }

  const isArray = Array.isArray(value);
  const entries: [string, unknown][] = isArray
    ? (value as unknown[]).map((v, i) => [`[${i}]`, v])
    : Object.entries(value as Record<string, unknown>);
  const isEmpty = entries.length === 0;
  const [openBracket, closeBracket] = isArray ? ["[", "]"] : ["{", "}"];

  return (
    <div className="font-mono text-xs leading-5">
      <div className="flex items-center gap-1 py-0.5" style={{ paddingLeft: indent }}>
        {isEmpty ? (
          <span className="w-3.5 shrink-0" />
        ) : (
          <button
            type="button"
            aria-label={t(expanded ? "extension.hostUi.jsonTreeCollapse" : "extension.hostUi.jsonTreeExpand")}
            onClick={() => setExpanded((e) => !e)}
            className="flex w-3.5 shrink-0 items-center justify-center text-muted-foreground hover:text-foreground"
          >
            {expanded ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
          </button>
        )}
        {label !== null && <span className="shrink-0 text-info">{label}:</span>}
        <span className="text-muted-foreground">
          {isEmpty ? `${openBracket}${closeBracket}` : expanded ? openBracket : `${openBracket}…${closeBracket}`}
        </span>
      </div>
      {expanded && !isEmpty && (
        <div>
          {entries.map(([key, child]) => (
            <JsonTreeNode key={key} label={key} value={child} depth={depth + 1} />
          ))}
          <div className="text-muted-foreground" style={{ paddingLeft: indent + INDENT_PX }}>
            {closeBracket}
          </div>
        </div>
      )}
    </div>
  );
}

/** Read-only JSON tree with per-node expand/collapse. `undefined` renders a translated empty state. */
export function JsonTreeView({ data, className }: JsonTreeViewProps) {
  const { t } = useTranslation();

  if (data === undefined) {
    return (
      <div className={`px-2 py-4 text-xs text-muted-foreground ${className ?? ""}`}>
        {t("extension.hostUi.jsonTreeEmpty")}
      </div>
    );
  }

  return (
    <div className={`overflow-auto ${className ?? ""}`}>
      <JsonTreeNode label={null} value={data} depth={0} />
    </div>
  );
}
