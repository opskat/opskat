import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Check, ChevronDown, ChevronUp, Info, Lock, Plus, Search, X } from "lucide-react";
import {
  Button,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  cn,
} from "@opskat/ui";
import { Segmented } from "@/components/asset/fields";
import type { DriverDescriptor } from "@/lib/dbDrivers";
import { declaredLabel } from "./model";
import type { DraftIssue, TableDraft } from "./draft";
import {
  indexColumnChoices,
  indexColumnName,
  indexDefinition,
  indexEditable,
  indexLock,
  indexStatus,
  indexTypeChoices,
  type DraftIndex,
  type DraftIndexColumn,
  type DraftIndexPatch,
  type IndexField,
} from "./indexDraft";
import { IndexColumnChips } from "./IndexList";
import { issueMessage } from "./issueText";
import { indexLockText } from "./format";

/** 枚举选项「默认」(不给值)在下拉里的取值:Radix 的下拉项不能用空串。 */
const OPTION_DEFAULT = "__default__";

interface IndexEditorProps {
  draft: TableDraft;
  index: DraftIndex;
  descriptor: DriverDescriptor | undefined;
  /** 指向这个索引的问题。 */
  issues: DraftIssue[];
  onChange: (key: string, patch: DraftIndexPatch) => void;
  onClose: () => void;
}

/**
 * 索引编辑区(选中或新建索引时在列表右侧):名称、类型(驱动声明的类型)、按次序的列(加、移除、上下移,
 * 驱动声明时每列可设降序与前缀长度)、驱动声明的选项(枚举给下拉、开关给开关)、注释(驱动声明时)。
 * 锁定的已有索引只显示原因与定义。
 */
export function IndexEditor({ draft, index, descriptor, issues, onChange, onClose }: IndexEditorProps) {
  const { t } = useTranslation();
  const key = index.key;
  const editing = descriptor?.indexEditing;
  const isNew = !index.original;
  const lock = indexLock(descriptor, index);
  const issueOf = (field: IndexField, severity: DraftIssue["severity"]) =>
    issues.find((i) => i.target.kind === "index" && i.target.field === field && i.severity === severity);
  const nameError = issueOf("name", "error");
  const nameWarning = issueOf("name", "warning");
  const columnsError = issueOf("columns", "error");
  const typeError = issueOf("type", "error");

  const setColumns = (columns: DraftIndexColumn[]) => onChange(key, { columns });
  const updateColumnAt = (at: number, patch: Partial<DraftIndexColumn>) =>
    setColumns(index.columns.map((c, i) => (i === at ? { ...c, ...patch } : c)));
  const move = (at: number, by: -1 | 1) => {
    const next = [...index.columns];
    [next[at], next[at + by]] = [next[at + by], next[at]];
    setColumns(next);
  };
  const setOption = (optionKey: string, value: unknown) => {
    const options = { ...index.options };
    if (value === undefined) delete options[optionKey];
    else options[optionKey] = value;
    onChange(key, { options });
  };

  return (
    <div data-testid="designer-index-editor" className="flex w-80 shrink-0 flex-col border-l border-border bg-muted/10">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b border-border px-3 text-xs">
        <span className="font-medium">{isNew ? t("query.designer.newIndex") : t("query.designer.editIndex")}</span>
        {indexStatus(draft, index) === "modified" && (
          <span className="text-[10px] text-warning">{t("query.designer.indexModified")}</span>
        )}
        <Button
          variant="ghost"
          size="icon-xs"
          className="ml-auto"
          aria-label={t("query.designer.closeIndexEditor")}
          onClick={onClose}
        >
          <X />
        </Button>
      </div>
      {!indexEditable(descriptor, index) ? (
        <div className="space-y-3 p-3 text-xs">
          <div className="font-mono">{index.name}</div>
          <div className="flex flex-wrap items-center gap-1">
            <IndexColumnChips index={indexDefinition(draft, index)} />
          </div>
          <p className="flex items-start gap-1.5 text-[11px] text-muted-foreground">
            <Lock className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
            {lock
              ? indexLockText(t, lock, index.original?.readOnlyReason ?? "")
              : t("query.designer.indexesNotEditable")}
          </p>
        </div>
      ) : (
        <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3 text-xs">
          <div className="space-y-1">
            <div className="text-[11px] text-muted-foreground">{t("query.designer.column.name")}</div>
            <Input
              data-cell={`${key}:name`}
              aria-label={t("query.designer.indexName")}
              aria-invalid={!!nameError || undefined}
              spellCheck={false}
              autoComplete="off"
              className={cn("h-7 font-mono text-xs", nameWarning && "border-warning")}
              value={index.name}
              onChange={(e) => onChange(key, { name: e.target.value })}
            />
            {nameError && <p className="text-[11px] text-destructive">{issueMessage(t, nameError)}</p>}
            {nameWarning && (
              <p className="flex items-start gap-1 text-[11px] text-warning">
                <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
                {issueMessage(t, nameWarning)}
              </p>
            )}
          </div>
          <div className="space-y-1">
            <div className="text-[11px] text-muted-foreground">{t("query.designer.column.type")}</div>
            <div data-cell={`${key}:type`} tabIndex={-1} className="outline-none">
              <Segmented
                aria-label={t("query.designer.column.type")}
                className={cn("h-7 w-auto [&>button]:px-2 [&>button]:text-xs", typeError && "ring-1 ring-destructive")}
                value={index.type}
                onChange={(type) => onChange(key, { type })}
                options={indexTypeChoices(descriptor).map((choice) => ({
                  value: choice.id,
                  label: declaredLabel(t, choice.label),
                  disabled: choice.disabled,
                }))}
              />
            </div>
            {typeError && <p className="text-[11px] text-destructive">{issueMessage(t, typeError)}</p>}
          </div>
          <div className="space-y-1">
            <div className="text-[11px] text-muted-foreground">{t("query.designer.indexColumnsHint")}</div>
            <div className="space-y-1">
              {index.columns.map((column, i) => (
                <div
                  key={`${i}:${column.ref}`}
                  data-testid="index-editor-column"
                  className="flex h-7 items-center gap-1 rounded-md border border-border bg-background pr-1 pl-2"
                >
                  <span className="w-4 text-[10px] text-muted-foreground tabular-nums">{i + 1}</span>
                  <span data-testid="index-editor-column-name" className="min-w-0 flex-1 truncate font-mono">
                    {indexColumnName(draft, column)}
                  </span>
                  {editing?.descending && (
                    <button
                      type="button"
                      aria-label={t("query.designer.descending")}
                      aria-pressed={column.descending}
                      title={t("query.designer.descending")}
                      className="rounded border border-border px-1 text-[10px] text-muted-foreground hover:bg-accent"
                      onClick={() => updateColumnAt(i, { descending: !column.descending })}
                    >
                      {column.descending ? "DESC" : "ASC"}
                    </button>
                  )}
                  {editing?.prefixLength && (
                    <input
                      type="number"
                      min={0}
                      aria-label={t("query.designer.prefixLength")}
                      title={t("query.designer.prefixLength")}
                      placeholder={t("query.designer.prefix")}
                      className="h-5 w-12 rounded border border-border bg-transparent px-1 text-[10px]"
                      value={column.prefixLength > 0 ? column.prefixLength : ""}
                      onChange={(e) => {
                        const n = Number.parseInt(e.target.value, 10);
                        updateColumnAt(i, { prefixLength: Number.isFinite(n) && n > 0 ? n : 0 });
                      }}
                    />
                  )}
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="size-5"
                    aria-label={t("query.designer.moveUp")}
                    disabled={i === 0}
                    onClick={() => move(i, -1)}
                  >
                    <ChevronUp />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="size-5"
                    aria-label={t("query.designer.moveDown")}
                    disabled={i === index.columns.length - 1}
                    onClick={() => move(i, 1)}
                  >
                    <ChevronDown />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    className="size-5"
                    aria-label={t("query.designer.removeIndexColumn")}
                    onClick={() => setColumns(index.columns.filter((_, j) => j !== i))}
                  >
                    <X />
                  </Button>
                </div>
              ))}
              <ColumnPicker
                draft={draft}
                index={index}
                invalid={!!columnsError}
                onPick={(columnKey) =>
                  setColumns([
                    ...index.columns,
                    { ref: columnKey, expression: false, descending: false, prefixLength: 0 },
                  ])
                }
              />
              {columnsError && <p className="text-[11px] text-destructive">{issueMessage(t, columnsError)}</p>}
            </div>
          </div>
          {(editing?.options ?? []).map((option) => {
            const label = declaredLabel(t, option.label);
            const error = issueOf(`option:${option.key}`, "error");
            const value = index.options[option.key];
            return (
              <div key={option.key} className="space-y-1">
                {option.type === "switch" ? (
                  <label className="flex items-center gap-2">
                    <Switch
                      data-cell={`${key}:option:${option.key}`}
                      aria-label={label}
                      checked={value === true}
                      onCheckedChange={(checked) => setOption(option.key, checked ? true : undefined)}
                    />
                    {label}
                  </label>
                ) : (
                  <>
                    <div className="text-[11px] text-muted-foreground">{label}</div>
                    <Select
                      value={typeof value === "string" && value !== "" ? value : OPTION_DEFAULT}
                      onValueChange={(v) => setOption(option.key, v === OPTION_DEFAULT ? undefined : v)}
                    >
                      <SelectTrigger
                        size="sm"
                        data-cell={`${key}:option:${option.key}`}
                        aria-label={label}
                        className="h-7 w-full font-mono text-xs"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value={OPTION_DEFAULT}>{t("query.designer.optionDefault")}</SelectItem>
                        {(option.enum ?? []).map((v) => (
                          <SelectItem key={v} value={v} className="font-mono">
                            {v}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </>
                )}
                {error && <p className="text-[11px] text-destructive">{issueMessage(t, error)}</p>}
              </div>
            );
          })}
          {editing?.comments && (
            <div className="space-y-1">
              <div className="text-[11px] text-muted-foreground">{t("query.designer.column.comment")}</div>
              <Input
                aria-label={t("query.designer.column.comment")}
                placeholder={t("query.designer.optional")}
                className="h-7 text-xs"
                value={index.comment}
                onChange={(e) => onChange(key, { comment: e.target.value })}
              />
            </div>
          )}
          {!isNew && (
            <p className="flex items-start gap-1 rounded-md bg-info/10 px-2 py-1.5 text-[10px] leading-4 text-info">
              <Info className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
              {t("query.designer.indexRebuildNote")}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

/** 「添加列」与它的下拉:可筛选,列出草稿里的列(含新列,标「新」),已在索引里的打勾、不可再选。 */
function ColumnPicker({
  draft,
  index,
  invalid,
  onPick,
}: {
  draft: TableDraft;
  index: DraftIndex;
  invalid: boolean;
  onPick: (columnKey: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const used = new Set(index.columns.filter((c) => !c.expression).map((c) => c.ref));
  const choices = indexColumnChoices(draft).filter((c) => c.name.toLowerCase().includes(filter.trim().toLowerCase()));
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setFilter("");
      }}
    >
      <PopoverTrigger asChild>
        <button
          type="button"
          data-cell={`${index.key}:columns`}
          aria-label={t("query.designer.addIndexColumn")}
          aria-invalid={invalid || undefined}
          className={cn(
            "flex h-7 w-full items-center gap-1.5 rounded-md border border-dashed border-border px-2 text-muted-foreground hover:bg-accent/40",
            invalid && "border-destructive text-destructive"
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          {t("query.designer.addIndexColumn")}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72 p-0">
        <div className="flex items-center gap-1.5 border-b border-border px-2 py-1.5">
          <Search className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
          <input
            aria-label={t("query.designer.filterColumns")}
            placeholder={t("query.designer.filterColumns")}
            className="min-w-0 flex-1 bg-transparent text-xs outline-none"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <div
          role="listbox"
          data-testid="designer-index-column-list"
          aria-label={t("query.designer.addIndexColumn")}
          className="max-h-64 overflow-y-auto py-1"
        >
          {choices.map((c) => {
            const taken = used.has(c.key);
            return (
              <button
                key={c.key}
                type="button"
                role="option"
                aria-selected={taken}
                disabled={taken}
                className={cn(
                  "mx-1 flex w-[calc(100%-0.5rem)] items-center gap-2 rounded-sm px-2 py-1 text-left font-mono text-xs",
                  taken ? "text-muted-foreground/50" : "hover:bg-accent"
                )}
                onClick={() => {
                  onPick(c.key);
                  setOpen(false);
                  setFilter("");
                }}
              >
                <span className="w-3">{taken && <Check className="h-3 w-3" aria-hidden />}</span>
                {c.name}
                <span className="ml-auto text-[10px] text-muted-foreground"> {c.type}</span>
                {c.isNew && (
                  <span className="font-sans text-[10px] text-success"> {t("query.designer.newColumn")}</span>
                )}
              </button>
            );
          })}
        </div>
      </PopoverContent>
    </Popover>
  );
}
