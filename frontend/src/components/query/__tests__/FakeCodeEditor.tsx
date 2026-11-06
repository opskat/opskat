import { useEffect } from "react";
import { createFakeSqlEditor, mounted } from "./fakeSqlEditor";

export function FakeCodeEditor({
  value,
  defaultValue = "",
  language,
  readOnly,
  testId,
  onMount,
}: {
  value?: string;
  defaultValue?: string;
  language?: string;
  readOnly?: boolean;
  testId?: string;
  onMount?: (editor: unknown, monaco: unknown) => void;
}) {
  useEffect(() => {
    if (!onMount) return;
    const fake = createFakeSqlEditor(defaultValue);
    mounted.current = fake;
    onMount(fake.editor, fake.monaco);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- mount only, like Monaco's onMount
  }, []);
  return (
    <pre data-testid="code-editor" data-language={language} data-readonly={String(!!readOnly)} data-editor-id={testId}>
      {value ?? defaultValue}
    </pre>
  );
}
