import { useMemo } from "react";
import { CodeEditor } from "@/components/CodeEditor";
import { resultRowsToJson } from "@/lib/resultJson";

interface QueryResultJsonViewProps {
  rows: Record<string, unknown>[];
  columns: string[];
  edits?: Map<string, unknown>;
  error?: string;
}

export function QueryResultJsonView({ rows, columns, edits, error }: QueryResultJsonViewProps) {
  const json = useMemo(() => resultRowsToJson(rows, columns, edits), [columns, edits, rows]);

  if (error) {
    return <div className="px-3 py-4 text-xs text-destructive whitespace-pre-wrap font-mono">{error}</div>;
  }

  return (
    <div className="flex-1 min-h-0 bg-background">
      <CodeEditor value={json} language="json" readOnly />
    </div>
  );
}
