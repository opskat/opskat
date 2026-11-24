import { useEffect, useRef, useState } from "react";
import { Input } from "@opskat/ui";
import { splitNameForRename } from "./utils";

interface RenameInputProps {
  initialName: string;
  onCommit: (name: string) => void;
  onCancel: () => void;
}

/** 行内重命名输入框:列表行与目录树行共用。 */
export function RenameInput({ initialName, onCommit, onCancel }: RenameInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState(initialName);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    const range = splitNameForRename(initialName);
    input.focus();
    input.setSelectionRange(0, range.stemLength);
  }, [initialName]);

  return (
    <Input
      ref={inputRef}
      value={value}
      className="h-5 flex-1 border-0 bg-background px-1 text-xs shadow-none focus-visible:ring-1"
      onChange={(e) => setValue(e.target.value)}
      onBlur={onCancel}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter") onCommit(value);
        if (e.key === "Escape") onCancel();
      }}
    />
  );
}
