import { useState, useMemo, useCallback, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Search, ChevronDown, Plus, Settings2 } from "lucide-react";
import { cn, Popover, PopoverContent, PopoverTrigger, Input, Button } from "@opskat/ui";
import { useExtensionStore } from "@/extension";
import { useCustomTypeList, useCustomTypeStore } from "@/stores/customTypeStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { openSettingsTab } from "@/stores/tabStore";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";
import {
  getAssetTypeOptions,
  buildAssetTypeGroups,
  filterAssetTypeOptions,
  findAssetTypeOption,
  optionKey,
  resolveAssetTypeLabel,
  type AssetTypeOption,
} from "@/lib/assetTypes/options";

interface AssetTypePickerProps {
  value: string;
  /** 子类型(通用资产 = 自定义类型标识)。 */
  variant?: string;
  onChange: (type: string, variant?: string) => void;
  /** 跳去别的页面(自定义类型管理)前调用,让外层表单关闭。 */
  onLeave?: () => void;
  disabled?: boolean;
}

export function AssetTypePicker({ value, variant, onChange, onLeave, disabled }: AssetTypePickerProps) {
  const { t } = useTranslation();
  const extensions = useExtensionStore((s) => s.extensions);
  const { types: customTypes } = useCustomTypeList();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  // 打开「新建自定义类型」时已有的类型 id;保存后多出来的那个就是新建的类型。
  const idsBeforeCreate = useRef<Set<number>>(new Set());

  const options = useMemo(() => getAssetTypeOptions(extensions, customTypes), [extensions, customTypes]);
  const resolveLabel = useCallback((o: AssetTypeOption) => resolveAssetTypeLabel(o, t), [t]);

  const selected = findAssetTypeOption(options, value, variant);
  const SelectedIcon = selected?.icon;

  const filtered = useMemo(
    () => filterAssetTypeOptions(options, search, resolveLabel),
    [options, search, resolveLabel]
  );
  const groups = useMemo(() => buildAssetTypeGroups(filtered.filter((o) => o.category !== "custom")), [filtered]);
  const customOptions = filtered.filter((o) => o.category === "custom");
  // 没有搜索时「自定义」分组总是显示(至少有「新建自定义类型」入口)。
  const showCustomGroup = customOptions.length > 0 || !search.trim();

  const select = (o: AssetTypeOption) => (o.variant ? onChange(o.value, o.variant) : onChange(o.value));

  const handleSelect = (o: AssetTypeOption) => {
    select(o);
    setOpen(false);
  };

  const openCreateType = () => {
    idsBeforeCreate.current = new Set(customTypes.map((ct) => ct.id));
    setOpen(false);
    setEditorOpen(true);
  };

  const handleTypeCreated = () => {
    // store.save 成功后已重新加载列表。
    const created = useCustomTypeStore.getState().types.find((ct) => !idsBeforeCreate.current.has(ct.id));
    if (!created) return;
    const opt = getAssetTypeOptions({}, [created]).find((o) => o.variant === created.slug);
    if (opt) select(opt);
  };

  const openManage = () => {
    setOpen(false);
    useSettingsUiStore.getState().setActiveTab("custom-types");
    openSettingsTab(t("nav.settings"));
    onLeave?.();
  };

  const renderOption = (o: AssetTypeOption) => {
    const Icon = o.icon;
    const isSelected = o.value === value && o.variant === variant;
    return (
      <button
        key={optionKey(o)}
        type="button"
        data-testid={`asset-type-option-${optionKey(o)}`}
        onClick={() => handleSelect(o)}
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
  };

  return (
    <>
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
            data-testid="asset-type-picker"
            className="w-full justify-between font-normal h-9"
          >
            <div className="flex items-center gap-2">
              {SelectedIcon && <SelectedIcon className="h-4 w-4 shrink-0" />}
              <span className="truncate">{selected ? resolveLabel(selected) : value}</span>
              {selected?.group === "custom" && (
                <span className="rounded-sm bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                  {t("assetType.customBadge")}
                </span>
              )}
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
            {groups.length === 0 && !showCustomGroup && (
              <div className="text-center text-sm text-muted-foreground py-6">{t("assetType.noResults")}</div>
            )}
            {groups.map((g) => (
              <div key={g.category}>
                <div className="text-[11px] font-medium text-muted-foreground px-0.5 mb-1">
                  {t(`assetType.group.${g.category}`)}
                </div>
                <div className="grid grid-cols-3 gap-1">{g.options.map(renderOption)}</div>
              </div>
            ))}
            {showCustomGroup && (
              <div>
                <div className="mb-1 flex items-center justify-between px-0.5">
                  <span className="text-[11px] font-medium text-muted-foreground">{t("assetType.group.custom")}</span>
                  <button
                    type="button"
                    data-testid="asset-type-manage-custom"
                    onClick={openManage}
                    className="flex items-center gap-1 rounded-sm text-[11px] text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring/45"
                  >
                    <Settings2 className="h-3 w-3" />
                    {t("assetType.manageCustomTypes")}
                  </button>
                </div>
                <div className="grid grid-cols-3 gap-1">
                  {customOptions.map(renderOption)}
                  <button
                    type="button"
                    data-testid="asset-type-new-custom"
                    onClick={openCreateType}
                    className="flex flex-col items-center gap-1.5 rounded-md border border-dashed border-border p-2 text-primary transition-colors hover:bg-primary/10"
                  >
                    <Plus className="h-5 w-5" />
                    <span className="text-xs text-center leading-tight">{t("assetType.newCustomType")}</span>
                  </button>
                </div>
              </div>
            )}
          </div>
        </PopoverContent>
      </Popover>
      <CustomTypeEditorDialog open={editorOpen} onOpenChange={setEditorOpen} onSaved={handleTypeCreated} />
    </>
  );
}
