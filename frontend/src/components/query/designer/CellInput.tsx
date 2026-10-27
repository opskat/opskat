import { Input, cn } from "@opskat/ui";

interface CellInputProps {
  value: string;
  onChange: (value: string) => void;
  "aria-label": string;
  placeholder?: string;
  /** 问题列表据此找到并聚焦这个格子(见 TableDesignerEditor 的 focusIssue)。 */
  cellId: string;
  invalid?: boolean;
  className?: string;
}

/** 表格里直接编辑的格子:无边框、撑满格子,聚焦时内描边。 */
export function CellInput({ value, onChange, cellId, invalid, className, ...rest }: CellInputProps) {
  return (
    <Input
      value={value}
      onChange={(e) => onChange(e.target.value)}
      data-cell={cellId}
      aria-invalid={invalid || undefined}
      spellCheck={false}
      autoComplete="off"
      className={cn(
        "h-full w-full min-w-0 rounded-none border-0 bg-transparent px-2 font-mono text-xs shadow-none dark:bg-transparent",
        "focus-visible:bg-background focus-visible:ring-2 focus-visible:ring-ring/60 focus-visible:ring-inset",
        className
      )}
      {...rest}
    />
  );
}
