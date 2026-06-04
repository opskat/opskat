import { useState, useMemo, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Search, ChevronDown } from "lucide-react";
import { cn, Popover, PopoverContent, PopoverTrigger, Input, Button } from "@opskat/ui";
import { useExtensionStore } from "@/extension";
import {
  getAssetTypeOptions,
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  resolveAssetTypeLabel,
  type AssetTypeOption,
} from "@/lib/assetTypes/options";

interface AssetTypePickerProps {
  value: string;
  onChange: (type: string) => void;
  disabled?: boolean;
}

export function AssetTypePicker({ value, onChange, disabled }: AssetTypePickerProps) {
  const { t } = useTranslation();
  const extensions = useExtensionStore((s) => s.extensions);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");

  const options = useMemo(() => getAssetTypeOptions(extensions), [extensions]);
  const resolveLabel = useCallback((o: AssetTypeOption) => resolveAssetTypeLabel(o, t), [t]);

  const selected = options.find((o) => o.value === value);
  const SelectedIcon = selected?.icon;

  const groups = useMemo(
    () => buildAssetTypeGroups(filterAssetTypeOptions(options, search, resolveLabel)),
    [options, search, resolveLabel]
  );

  const handleSelect = (type: string) => {
    onChange(type);
    setOpen(false);
  };

  return (
    <Popover
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) setSearch("");
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          disabled={disabled}
          className="w-full justify-between font-normal h-9"
        >
          <div className="flex items-center gap-2">
            {SelectedIcon && <SelectedIcon className="h-4 w-4 shrink-0" />}
            <span className="truncate">{selected ? resolveLabel(selected) : value}</span>
          </div>
          <ChevronDown className="ml-auto h-4 w-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-[320px] p-0" align="start">
        <div className="p-2 pb-1">
          <div className="relative">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
            <Input
              placeholder={t("assetType.searchPlaceholder")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8 h-8 text-sm"
            />
          </div>
        </div>
        <div className="max-h-[300px] overflow-y-auto p-2 pt-1 space-y-2" onWheel={(e) => e.stopPropagation()}>
          {groups.length === 0 && (
            <div className="text-center text-sm text-muted-foreground py-6">{t("assetType.noResults")}</div>
          )}
          {groups.map((g) => (
            <div key={g.category}>
              <div className="text-[11px] font-medium text-muted-foreground px-0.5 mb-1">
                {t(`assetType.group.${g.category}`)}
              </div>
              <div className="grid grid-cols-3 gap-1">
                {g.options.map((o) => {
                  const Icon = o.icon;
                  const isSelected = o.value === value;
                  return (
                    <button
                      key={o.value}
                      type="button"
                      onClick={() => handleSelect(o.value)}
                      className={cn(
                        "flex flex-col items-center gap-1.5 rounded-md p-2 transition-colors",
                        isSelected
                          ? "bg-primary text-primary-foreground"
                          : "hover:bg-muted text-muted-foreground hover:text-foreground"
                      )}
                    >
                      <Icon className="h-5 w-5" />
                      <span className="text-xs text-center leading-tight">{resolveLabel(o)}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  );
}
