import { Fragment, useRef, type ComponentType, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown, ChevronUp, ExternalLink, Copy, MoreHorizontal } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  Button,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
  cn,
} from "@opskat/ui";
import { notifyCopied } from "@/lib/notify";
import type { K8sListSort, K8sResourceRow, K8sTableColumn } from "@/stores/k8sStore";
import { getK8sActions, type K8sAction, type K8sActionContext } from "../actions";
import { getK8sStatusColor, isStatusColumn, statusVariantToClass } from "../utils";
import { cellText } from "./listModel";

const ROW_HEIGHT = 32;
const ACTIONS_WIDTH = 96;

interface Props {
  assetId: number;
  typeKey: string;
  columns: K8sTableColumn[];
  rows: K8sResourceRow[];
  /** Effective sort (the default is the first column ascending). */
  sort: K8sListSort;
  onSort: (column: string) => void;
  onOpen: (row: K8sResourceRow) => void;
}

/**
 * Virtualised table of the cluster's server-side Table: only the rows in view are in the DOM, so a
 * 5000 row list scrolls and filters without lag. Hover reveals the registered primary actions and a ⋯ menu.
 */
export function K8sResourceGrid({ assetId, typeKey, columns, rows, sort, onSort, onOpen }: Props) {
  const { t } = useTranslation();
  const scrollRef = useRef<HTMLDivElement>(null);
  // react-virtual 在渲染期返回可变实例,与 React Compiler 语义不兼容(上游库问题);启用 Compiler 前无代码级修复。
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    initialRect: { width: 0, height: ROW_HEIGHT * 20 },
    overscan: 20,
  });

  const template = `${columns.map((_, i) => (i === 0 ? "minmax(200px,2fr)" : "minmax(100px,1fr)")).join(" ")} ${ACTIONS_WIDTH}px`;
  const minWidth = 200 + (columns.length - 1) * 100 + ACTIONS_WIDTH;

  const contextOf = (row: K8sResourceRow): K8sActionContext => ({
    assetId,
    typeKey,
    namespace: row.namespace,
    name: row.name,
    object: row.object,
  });
  const copyName = (row: K8sResourceRow) => {
    void navigator.clipboard.writeText(row.name).then(() => notifyCopied(t("action.copied")));
  };

  return (
    <TooltipProvider delayDuration={300}>
      <div ref={scrollRef} className="h-full overflow-auto" data-testid="k8s-grid">
        <div style={{ minWidth }}>
          <div
            role="row"
            className="sticky top-0 z-10 grid border-b bg-background text-xs text-muted-foreground"
            style={{ gridTemplateColumns: template }}
          >
            {columns.map((col, i) => (
              <button
                key={col.name}
                type="button"
                onClick={() => onSort(col.name)}
                className={cn(
                  "flex items-center gap-1 whitespace-nowrap py-2 pr-4 text-left font-medium hover:text-foreground",
                  i === 0 && "pl-3"
                )}
              >
                {col.name}
                {sort.column === col.name &&
                  (sort.dir === "asc" ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />)}
              </button>
            ))}
            <div />
          </div>
          <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const row = rows[item.index];
              const actions = getK8sActions(typeKey, row);
              const run = (action: K8sAction) => void action.run(contextOf(row));
              return (
                <ContextMenu key={`${row.namespace}/${row.name}`}>
                  <ContextMenuTrigger asChild>
                    <div
                      role="row"
                      data-testid="k8s-row"
                      className="group absolute left-0 grid w-full cursor-pointer items-center border-b text-xs hover:bg-muted/40"
                      style={{ top: item.start, height: ROW_HEIGHT, gridTemplateColumns: template }}
                      onClick={() => onOpen(row)}
                    >
                      {columns.map((col, i) => (
                        <div
                          key={col.name}
                          className={cn("truncate pr-4", i === 0 && "pl-3 font-mono font-medium")}
                          title={i === 0 ? row.name : undefined}
                        >
                          {isStatusColumn(col) ? <StatusCell text={cellText(row.cells[i])} /> : cellText(row.cells[i])}
                        </div>
                      ))}
                      <div
                        className="flex items-center justify-end gap-0.5 pr-2 opacity-0 group-hover:opacity-100 focus-within:opacity-100 has-[[data-state=open]]:opacity-100"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {actions
                          .filter((a) => a.primary)
                          .map((a) => {
                            const label = t(a.labelKey);
                            const reason = a.disabledReason?.(row);
                            return (
                              <Tooltip key={a.id}>
                                <TooltipTrigger asChild>
                                  <span>
                                    <Button
                                      variant="ghost"
                                      size="icon"
                                      className="h-6 w-6"
                                      aria-label={label}
                                      disabled={!!reason}
                                      onClick={() => run(a)}
                                    >
                                      <a.icon className="h-3.5 w-3.5" />
                                    </Button>
                                  </span>
                                </TooltipTrigger>
                                <TooltipContent>{reason ?? label}</TooltipContent>
                              </Tooltip>
                            );
                          })}
                        {actions.length > 0 && (
                          <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-6 w-6"
                                aria-label={t("asset.k8sMoreActions")}
                              >
                                <MoreHorizontal className="h-3.5 w-3.5" />
                              </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                              <ActionItems
                                actions={actions}
                                target={row}
                                run={run}
                                Item={DropdownMenuItem}
                                Separator={DropdownMenuSeparator}
                              />
                            </DropdownMenuContent>
                          </DropdownMenu>
                        )}
                      </div>
                    </div>
                  </ContextMenuTrigger>
                  <ContextMenuContent>
                    <ContextMenuItem onClick={() => onOpen(row)}>
                      <ExternalLink className="h-3.5 w-3.5" />
                      {t("asset.k8sOpenDetail")}
                    </ContextMenuItem>
                    <ContextMenuItem onClick={() => copyName(row)}>
                      <Copy className="h-3.5 w-3.5" />
                      {t("asset.k8sCopyName")}
                    </ContextMenuItem>
                    {actions.length > 0 && <ContextMenuSeparator />}
                    <ActionItems
                      actions={actions}
                      target={row}
                      run={run}
                      Item={ContextMenuItem}
                      Separator={ContextMenuSeparator}
                    />
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </div>
        </div>
      </div>
    </TooltipProvider>
  );
}

function StatusCell({ text }: { text: string }): ReactNode {
  if (!text) return null;
  return (
    <span className={cn("rounded-sm px-1.5 py-0.5 text-xs", statusVariantToClass(getK8sStatusColor(text)))}>
      {text}
    </span>
  );
}

type MenuItem = ComponentType<{
  variant?: "default" | "destructive";
  disabled?: boolean;
  title?: string;
  onClick?: () => void;
  children?: ReactNode;
}>;

function ActionItems({
  actions,
  target,
  run,
  Item,
  Separator,
}: {
  actions: K8sAction[];
  target: K8sResourceRow;
  run: (action: K8sAction) => void;
  Item: MenuItem;
  Separator: ComponentType;
}) {
  const { t } = useTranslation();
  return (
    <>
      {actions.map((a) => {
        const reason = a.disabledReason?.(target);
        return (
          <Fragment key={a.id}>
            {a.destructive && <Separator />}
            <Item
              variant={a.destructive ? "destructive" : undefined}
              disabled={!!reason}
              title={reason}
              onClick={() => run(a)}
            >
              <a.icon className="h-3.5 w-3.5" />
              {t(a.labelKey)}
            </Item>
          </Fragment>
        );
      })}
    </>
  );
}
