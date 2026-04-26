import { useCallback, useMemo, useState } from "react";
import { ArrowDownNarrowWide, ArrowUpNarrowWide, Check, Plus, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button, Input, Popover, PopoverContent, PopoverTrigger, ScrollArea } from "@opskat/ui";
import { cellValueToText } from "@/lib/cellValue";
import {
  addFilterCondition,
  addFilterGroup,
  addSortCriterion,
  createFilterCondition,
  pickNextFilterColumn,
  toggleFilterJoin,
  toggleSortDirection,
  updateFilterGroupItems,
  updateFilterItem,
  updateSortCriterion,
  type TableFilterCondition,
  type TableFilterItem,
  type TableSortItem,
} from "@/lib/tableFilter";

interface TableFilterBuilderProps {
  columns: string[];
  rows: Record<string, unknown>[];
  filters: TableFilterItem[];
  sorts: TableSortItem[];
  driver?: string;
  onChange: (items: TableFilterItem[]) => void;
  onSortsChange: (items: TableSortItem[]) => void;
  onApply: () => void;
}

interface DistinctValue {
  key: string;
  value: unknown;
  label: string;
  count: number;
}

function valueKey(value: unknown): string {
  if (value == null) return "__opskat_null__";
  return cellValueToText(value);
}

function distinctValues(rows: Record<string, unknown>[], column: string): DistinctValue[] {
  const map = new Map<string, DistinctValue>();
  for (const row of rows) {
    const value = row[column];
    const key = valueKey(value);
    const hit = map.get(key);
    if (hit) {
      hit.count += 1;
    } else {
      map.set(key, {
        key,
        value: value == null ? null : value,
        label: value == null ? "NULL" : cellValueToText(value),
        count: 1,
      });
    }
  }
  return Array.from(map.values());
}

export function TableFilterBuilder({
  columns,
  rows,
  filters,
  sorts,
  driver,
  onChange,
  onSortsChange,
  onApply,
}: TableFilterBuilderProps) {
  const { t } = useTranslation();

  const addCondition = useCallback(() => {
    onChange(addFilterCondition(filters, columns, driver));
  }, [columns, driver, filters, onChange]);
  const addSort = useCallback(() => {
    onSortsChange(addSortCriterion(sorts, columns, driver));
  }, [columns, driver, onSortsChange, sorts]);

  return (
    <div className="shrink-0 border-b border-border bg-background">
      <div className="px-3 pt-2 pb-1">
        <div className="mb-1.5 flex items-center gap-3">
          <span className="text-sm font-semibold text-foreground">{t("query.filterBuilderTitle")}</span>
        </div>
        <div className="min-h-[132px] space-y-1">
          {filters.length === 0 ? (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Button variant="outline" size="icon-xs" title={t("query.addFilter")} onClick={addCondition}>
                <Plus className="h-3.5 w-3.5" />
              </Button>
              <span>{t("query.filterBuilderEmpty")}</span>
            </div>
          ) : (
            <FilterItems
              columns={columns}
              rows={rows}
              items={filters}
              driver={driver}
              rootItems={filters}
              onChange={onChange}
            />
          )}
        </div>
      </div>
      <div className="flex items-center gap-3 border-t border-border px-3 py-2">
        <span className="text-sm font-semibold text-foreground">{t("query.sortBuilderTitle")}</span>
        {sorts.map((sort) => (
          <SortCriterionChip key={sort.id} columns={columns} item={sort} items={sorts} onChange={onSortsChange} />
        ))}
        <Button variant="outline" size="icon-xs" title={t("query.addSort")} onClick={addSort}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
        {sorts.length === 0 && <span className="text-xs text-muted-foreground">{t("query.sortBuilderEmpty")}</span>}
      </div>
      <div className="px-3 pb-2">
        <Button size="sm" className="h-8 text-xs" onClick={onApply}>
          {t("query.applyFilterSort")}
        </Button>
      </div>
    </div>
  );
}

interface SortCriterionChipProps {
  columns: string[];
  item: TableSortItem;
  items: TableSortItem[];
  onChange: (items: TableSortItem[]) => void;
}

function SortCriterionChip({ columns, item, items, onChange }: SortCriterionChipProps) {
  const { t } = useTranslation();
  const DirectionIcon = item.dir === "asc" ? ArrowUpNarrowWide : ArrowDownNarrowWide;

  return (
    <div className="flex h-9 items-center gap-2 rounded-md border border-primary bg-primary/5 px-2 text-primary">
      <select
        className="h-7 rounded border-0 bg-transparent px-1 text-sm font-medium outline-none"
        value={item.column}
        onChange={(event) => onChange(updateSortCriterion(items, item.id, { column: event.target.value }))}
        aria-label={t("query.sortColumnName")}
      >
        {columns.map((column) => (
          <option key={column} value={column}>
            {column}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-primary"
        title={`${t("query.toggleSortDirection")}:${item.column}`}
        onClick={() => onChange(toggleSortDirection(items, item.id))}
      >
        <DirectionIcon className="h-4 w-4" />
      </button>
    </div>
  );
}

interface FilterItemsProps {
  columns: string[];
  rows: Record<string, unknown>[];
  items: TableFilterItem[];
  rootItems: TableFilterItem[];
  driver?: string;
  groupId?: string;
  onChange: (items: TableFilterItem[]) => void;
}

function FilterItems({ columns, rows, items, rootItems, driver, groupId, onChange }: FilterItemsProps) {
  const { t } = useTranslation();

  const addSibling = useCallback(() => {
    if (!groupId) {
      onChange(addFilterCondition(rootItems, columns, driver));
      return;
    }
    const column = pickNextFilterColumn(rootItems, columns);
    if (!column) return;
    onChange(
      updateFilterGroupItems(rootItems, groupId, (children) => [
        ...children,
        createFilterCondition(`filter-${Date.now()}`, column),
      ])
    );
  }, [columns, driver, groupId, onChange, rootItems]);

  const addSiblingGroup = useCallback(() => {
    if (!groupId) {
      onChange(addFilterGroup(rootItems, columns, driver));
      return;
    }
    onChange(updateFilterGroupItems(rootItems, groupId, (children) => addFilterGroup(children, columns, driver)));
  }, [columns, driver, groupId, onChange, rootItems]);

  if (items.length === 0) {
    return (
      <div className="ml-4 flex items-center gap-2 text-xs text-muted-foreground">
        <Button variant="outline" size="icon-xs" title={t("query.addFilter")} onClick={addSibling}>
          <Plus className="h-3.5 w-3.5" />
        </Button>
        <Button variant="outline" size="icon-xs" title={t("query.addFilterGroup")} onClick={addSiblingGroup}>
          ()+
        </Button>
        <span>{t("query.filterBuilderEmpty")}</span>
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {items.map((item, index) =>
        item.kind === "condition" ? (
          <FilterConditionRow
            key={item.id}
            columns={columns}
            rows={rows}
            item={item}
            isLast={index === items.length - 1}
            rootItems={rootItems}
            onChange={onChange}
            onAddAfter={addSibling}
            onAddGroupAfter={addSiblingGroup}
          />
        ) : (
          <div key={item.id} className="space-y-1">
            <div className="flex items-center gap-2 text-sm text-foreground">
              <span className="font-mono">(</span>
            </div>
            <div className="ml-5">
              <FilterItems
                columns={columns}
                rows={rows}
                items={item.items}
                rootItems={rootItems}
                driver={driver}
                groupId={item.id}
                onChange={onChange}
              />
            </div>
            <div className="flex items-center gap-2 text-sm text-foreground">
              <span className="font-mono">)</span>
              {!(index === items.length - 1) && (
                <button
                  type="button"
                  className="text-sm text-muted-foreground hover:text-primary"
                  onClick={() => onChange(toggleFilterJoin(rootItems, item.id))}
                >
                  {item.join}
                </button>
              )}
            </div>
          </div>
        )
      )}
    </div>
  );
}

interface FilterConditionRowProps {
  columns: string[];
  rows: Record<string, unknown>[];
  item: TableFilterCondition;
  isLast: boolean;
  rootItems: TableFilterItem[];
  onChange: (items: TableFilterItem[]) => void;
  onAddAfter: () => void;
  onAddGroupAfter: () => void;
}

function FilterConditionRow({
  columns,
  rows,
  item,
  isLast,
  rootItems,
  onChange,
  onAddAfter,
  onAddGroupAfter,
}: FilterConditionRowProps) {
  const { t } = useTranslation();

  const suggestions = useMemo(() => distinctValues(rows, item.column), [item.column, rows]);
  const setItem = useCallback(
    (patch: Partial<TableFilterCondition>) => onChange(updateFilterItem(rootItems, item.id, patch)),
    [item.id, onChange, rootItems]
  );

  return (
    <div className="flex items-center gap-2 text-sm">
      <input
        type="checkbox"
        className="h-4 w-4 accent-primary"
        checked={item.enabled}
        onChange={(event) => setItem({ enabled: event.target.checked })}
        aria-label={t("query.filterEnabled")}
      />
      <select
        value={item.column}
        onChange={(event) => setItem({ column: event.target.value, value: undefined })}
        className="h-7 rounded border-0 bg-transparent px-1 text-sm font-medium text-primary outline-none"
        aria-label={t("query.filterColumnName")}
      >
        {columns.map((column) => (
          <option key={column} value={column}>
            {column}
          </option>
        ))}
      </select>
      <span className="text-muted-foreground">=</span>
      <FilterValuePicker value={item.value} suggestions={suggestions} onChange={(value) => setItem({ value })} />
      <Button variant="outline" size="icon-xs" title={t("query.addFilter")} onClick={onAddAfter}>
        <Plus className="h-3.5 w-3.5" />
      </Button>
      <Button variant="outline" size="icon-xs" title={t("query.addFilterGroup")} onClick={onAddGroupAfter}>
        ()+
      </Button>
      {!isLast && (
        <button
          type="button"
          className="rounded px-1 text-sm text-muted-foreground hover:text-primary"
          onClick={() => onChange(toggleFilterJoin(rootItems, item.id))}
        >
          {item.join}
        </button>
      )}
    </div>
  );
}

interface FilterValuePickerProps {
  value: unknown;
  suggestions: DistinctValue[];
  onChange: (value: unknown) => void;
}

function FilterValuePicker({ value, suggestions, onChange }: FilterValuePickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [customValue, setCustomValue] = useState("");
  const [search, setSearch] = useState("");
  const label = value === undefined ? "?" : value == null ? "NULL" : cellValueToText(value);
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return suggestions;
    return suggestions.filter((item) => item.label.toLowerCase().includes(q));
  }, [search, suggestions]);

  const commitCustomValue = useCallback(() => {
    if (!customValue.trim()) return;
    onChange(customValue);
    setOpen(false);
    setCustomValue("");
  }, [customValue, onChange]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          title={t("query.chooseFilterValue")}
          className={`min-w-[28px] rounded px-1 text-left font-medium ${
            value === undefined ? "text-muted-foreground" : "text-primary"
          } hover:bg-accent`}
        >
          {label}
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-[420px] p-3" align="start" sideOffset={4}>
        <div className="space-y-2">
          <Input
            className="h-8 font-mono text-xs"
            value={customValue}
            autoFocus
            onChange={(event) => setCustomValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") commitCustomValue();
            }}
          />
          <div className="text-sm font-medium">{t("query.suggestedValues")}</div>
          <ScrollArea className="h-[180px] border border-border bg-background">
            <div className="divide-y divide-border/30">
              {filtered.map((item, index) => {
                const checked = valueKey(value) === item.key;
                return (
                  <button
                    key={item.key}
                    type="button"
                    className={`flex h-8 w-full items-center gap-2 px-3 text-left text-sm ${
                      index % 2 === 0 ? "bg-background" : "bg-muted/40"
                    } hover:bg-accent`}
                    onClick={() => {
                      onChange(item.value);
                      setOpen(false);
                    }}
                  >
                    <span className="flex h-4 w-4 items-center justify-center rounded border border-input bg-background">
                      {checked && <Check className="h-3 w-3 text-primary" />}
                    </span>
                    <span className="min-w-0 flex-1 truncate font-mono">{item.label}</span>
                    <span className="text-xs text-muted-foreground tabular-nums">{item.count}</span>
                  </button>
                );
              })}
            </div>
          </ScrollArea>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-8 pl-8 text-sm"
              placeholder={t("query.search")}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}
