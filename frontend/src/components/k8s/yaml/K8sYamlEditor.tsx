import { useCallback, useEffect, useRef } from "react";
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
    },
    [applyMarkers]
  );

  return <CodeEditor value={value} onChange={onChange} language="yaml" placeholder={placeholder} onMount={onMount} />;
}
