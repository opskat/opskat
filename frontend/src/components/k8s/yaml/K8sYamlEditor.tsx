import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import type * as MonacoNS from "monaco-editor";
import { CodeEditor } from "@/components/CodeEditor";
import type { YamlSyntaxError } from "./yamlText";

const MARKER_OWNER = "k8s-yaml";

/** The Monaco YAML editor; a syntax error is drawn as a marker at its position. */
export function K8sYamlEditor({
  value,
  onChange,
  error,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  error?: YamlSyntaxError | null;
  placeholder?: string;
}) {
  const { t } = useTranslation();
  const [ready, setReady] = useState(false);
  const mounted = useRef<{ editor: MonacoNS.editor.IStandaloneCodeEditor; monaco: typeof MonacoNS } | null>(null);

  const applyMarkers = useCallback((err: YamlSyntaxError | null | undefined) => {
    const target = mounted.current;
    const model = target?.editor.getModel();
    if (!target || !model) return;
    target.monaco.editor.setModelMarkers(
      model,
      MARKER_OWNER,
      err
        ? [
            {
              severity: target.monaco.MarkerSeverity.Error,
              message: err.message,
              startLineNumber: err.line,
              startColumn: err.column,
              endLineNumber: err.line,
              endColumn: err.column + 1,
            },
          ]
        : []
    );
  }, []);

  const latestError = useRef(error);
  useEffect(() => {
    latestError.current = error;
    applyMarkers(error);
  }, [error, applyMarkers]);

  const onMount = useCallback(
    (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: typeof MonacoNS) => {
      mounted.current = { editor, monaco };
      applyMarkers(latestError.current);
      setReady(true);
    },
    [applyMarkers]
  );

  return (
    <div className="relative h-full">
      <CodeEditor value={value} onChange={onChange} language="yaml" placeholder={placeholder} onMount={onMount} />
      {!ready && (
        <div
          role="status"
          className="absolute inset-0 flex items-center justify-center gap-2 bg-background text-xs text-muted-foreground"
        >
          <Loader2 className="h-4 w-4 animate-spin" />
          {t("asyncState.loading")}
        </div>
      )}
    </div>
  );
}
