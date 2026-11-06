import type * as MonacoNS from "monaco-editor";
import { CodeEditor as InternalCodeEditor } from "../../components/CodeEditor";

/** Monaco editor / API handed to onMount; the extension types the subset it uses. */
export type HostMonacoEditor = MonacoNS.editor.IStandaloneCodeEditor;
export type HostMonaco = typeof MonacoNS;

export interface HostCodeEditorProps {
  value?: string;
  onChange?: (value: string) => void;
  language?: "sql" | "javascript" | "json" | "plaintext" | "shell" | "markdown" | "yaml";
  readOnly?: boolean;
  height?: string | number;
  fontSize?: number;
  placeholder?: string;
  /** Merged over the host's editor options. */
  options?: Record<string, unknown>;
  /** Called once the editor is ready, with the editor instance and the Monaco instance. */
  onMount?: (editor: HostMonacoEditor, monaco: HostMonaco) => void;
  className?: string;
}

export function CodeEditor({
  value,
  onChange,
  language,
  readOnly,
  height,
  fontSize,
  placeholder,
  options,
  onMount,
  className,
}: HostCodeEditorProps) {
  return (
    <InternalCodeEditor
      value={value}
      onChange={onChange}
      language={language}
      readOnly={readOnly}
      height={height}
      fontSize={fontSize}
      placeholder={placeholder}
      options={options as MonacoNS.editor.IStandaloneEditorConstructionOptions | undefined}
      onMount={onMount}
      className={className}
    />
  );
}
