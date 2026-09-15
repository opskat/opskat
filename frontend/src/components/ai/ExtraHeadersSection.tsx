import { useTranslation } from "react-i18next";
import { Button, Input, Label, cn } from "@opskat/ui";
import { ChevronDown, ChevronRight, Plus, Trash2, TriangleAlert } from "lucide-react";
import type { ExtraHeaderValue } from "./AIProviderForm";
import { RESERVED_HEADER_NAMES, shadowedHeaderIndexes } from "./extraHeaders";

export interface ExtraHeadersSectionProps {
  headers: ExtraHeaderValue[];
  open: boolean;
  onToggle: () => void;
  onChange: (headers: ExtraHeaderValue[]) => void;
}

export function ExtraHeadersSection({ headers, open, onToggle, onChange }: ExtraHeadersSectionProps) {
  const { t } = useTranslation();
  const shadowed = shadowedHeaderIndexes(headers);
  const namedCount = headers.filter((header) => header.name.trim()).length;

  const update = (index: number, patch: Partial<ExtraHeaderValue>) =>
    onChange(headers.map((header, i) => (i === index ? { ...header, ...patch } : header)));

  return (
    <div className="@container rounded-lg border">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={open}
        className="flex w-full items-center gap-2 p-4 text-left"
      >
        {open ? (
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-4 w-4 text-muted-foreground" />
        )}
        <Label className="cursor-pointer">{t("settings.extraHeaders")}</Label>
        {namedCount > 0 && (
          <span
            data-testid="extra-headers-count"
            className="rounded-full bg-secondary px-2 py-0.5 text-xs text-secondary-foreground"
          >
            {namedCount}
          </span>
        )}
        <span className="ml-auto text-xs text-muted-foreground">{t("settings.optional")}</span>
      </button>

      {open && (
        <div className="space-y-2 border-t p-4">
          {headers.length === 0 ? (
            <p className="py-2 text-xs text-muted-foreground">{t("settings.extraHeadersEmpty")}</p>
          ) : (
            <div className="space-y-2">
              {headers.map((header, index) => {
                const isDuplicate = shadowed.has(index);
                const isReserved = RESERVED_HEADER_NAMES.includes(header.name.trim().toLowerCase());
                return (
                  <div key={index} className="space-y-1">
                    {/* 窄容器下折成 [名称][删除] / [值] 两行，而不是把名称框压到看不全。
                        用容器查询而非视口断点：这个表单在设置页和新手向导里宽度不同。
                        DOM 顺序保持 名称 → 值 → 删除，Tab 才跟着阅读顺序走。 */}
                    <div className="grid grid-cols-[1fr_auto] items-center gap-2 @md:grid-cols-[40%_1fr_auto]">
                      <Input
                        aria-label={t("settings.extraHeaderName")}
                        value={header.name}
                        placeholder="x-opencode-session"
                        onChange={(e) => update(index, { name: e.target.value })}
                        className={cn((isDuplicate || isReserved) && "border-destructive")}
                      />
                      <Input
                        aria-label={t("settings.extraHeaderValue")}
                        value={header.value}
                        placeholder="{{session}}"
                        onChange={(e) => update(index, { value: e.target.value })}
                        className="col-span-2 col-start-1 row-start-2 font-mono text-xs @md:col-span-1 @md:col-start-2 @md:row-start-1"
                      />
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("settings.extraHeaderRemove")}
                        className="col-start-2 row-start-1 text-muted-foreground hover:text-destructive @md:col-start-3"
                        onClick={() => onChange(headers.filter((_, i) => i !== index))}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                    {isDuplicate && <p className="text-xs text-destructive">{t("settings.extraHeaderDuplicate")}</p>}
                    {isReserved && !isDuplicate && (
                      <p className="flex items-center gap-1 text-xs text-destructive">
                        <TriangleAlert className="h-3 w-3 shrink-0" />
                        {t("settings.extraHeaderReserved", { name: header.name.trim() })}
                      </p>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          <div className="pt-1">
            <Button variant="outline" size="sm" onClick={() => onChange([...headers, { name: "", value: "" }])}>
              <Plus className="mr-1 h-3.5 w-3.5" />
              {t("settings.extraHeaderAdd")}
            </Button>
          </div>

          <p className="pt-1 text-xs text-muted-foreground">{t("settings.extraHeadersHint")}</p>
        </div>
      )}
    </div>
  );
}
