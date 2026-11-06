import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown } from "lucide-react";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger, cn } from "@opskat/ui";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { declaredLabel } from "./model";
import { CellInput } from "./CellInput";

interface TypeInputProps {
  value: string;
  onChange: (value: string) => void;
  descriptor: DriverDescriptor | undefined;
  /** 问题列表聚焦用的格子标识。 */
  cellId: string;
  invalid?: boolean;
}

/**
 * 类型格子:输入任何文本都原样采用;驱动声明了常用类型时右侧有下拉,按驱动声明的分组列出,点选填入。
 */
export function TypeInput({ value, onChange, descriptor, cellId, invalid }: TypeInputProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const groups = descriptor?.columnTypes?.groups ?? [];
  const input = (
    <CellInput
      value={value}
      onChange={onChange}
      aria-label={t("query.designer.column.type")}
      placeholder={t("query.designer.column.type")}
      cellId={cellId}
      invalid={invalid}
      className={cn(groups.length > 0 && "pr-6")}
    />
  );
  if (groups.length === 0) return input;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <div className="relative h-full w-full">
          {input}
          <PopoverTrigger asChild>
            <button
              type="button"
              aria-label={t("query.designer.typeSuggestions")}
              className="absolute top-1/2 right-1 flex size-5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground/70 hover:bg-accent hover:text-foreground"
            >
              <ChevronDown className="h-3.5 w-3.5" aria-hidden />
            </button>
          </PopoverTrigger>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        className="w-72 p-0"
        // 下拉打开时焦点留在输入框里,可以继续手打。
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <div
          role="listbox"
          data-testid="designer-type-list"
          aria-label={t("query.designer.typeSuggestions")}
          className="max-h-72 overflow-y-auto py-1"
        >
          {groups.map((group) => (
            <div key={group.label} role="group" aria-label={declaredLabel(t, group.label)}>
              <div className="px-3 pt-1.5 pb-0.5 text-[10px] text-muted-foreground">
                {declaredLabel(t, group.label)}
              </div>
              {group.types.map((type) => (
                <button
                  key={type}
                  type="button"
                  role="option"
                  aria-selected={type === value}
                  className={cn(
                    "mx-1 block w-[calc(100%-0.5rem)] rounded-sm px-2 py-1 text-left font-mono text-xs hover:bg-accent",
                    type === value && "bg-accent"
                  )}
                  onClick={() => {
                    onChange(type);
                    setOpen(false);
                  }}
                >
                  {type}
                </button>
              ))}
            </div>
          ))}
        </div>
        <div className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">
          {t("query.designer.typeSuggestionsHint", { driver: descriptor?.name })}
        </div>
      </PopoverContent>
    </Popover>
  );
}
