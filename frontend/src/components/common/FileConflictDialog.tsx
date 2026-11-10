import { useCallback, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Label,
} from "@opskat/ui";
import { formatBytes } from "@/lib/formatBytes";

/** 用户对一个同名冲突的决定。"skip" 也是重命名场景下「取消」的结果。 */
export type FileConflictDecision = "skip" | "keepBoth" | "overwrite";

/**
 * transfer：上传 / 移动 / 复制 / 粘贴，可选跳过、保留两者、覆盖，多项时可应用到其余冲突；
 * rename：重命名到已存在的名字，只能取消或覆盖。
 */
export type FileConflictKind = "transfer" | "rename";

/** 冲突一侧的元数据；取不到的字段留空，界面显示占位符。 */
export interface FileConflictSide {
  isDir?: boolean;
  size?: number;
  /** Unix 毫秒时间戳。 */
  modTime?: number;
}

export interface FileConflictItem {
  /** 已被占用的目标路径，原样展示给用户。 */
  targetPath: string;
  existing: FileConflictSide;
  incoming: FileConflictSide;
  /** 选「保留两者」时新项落地的名字；不提供则该项不能保留两者。 */
  keepBothName?: string;
  /** 目标就是来源本身（原地复制）：不能覆盖，只能跳过或保留两者。 */
  noOverwrite?: boolean;
}

export interface FileConflictRequest {
  kind: FileConflictKind;
  items: FileConflictItem[];
}

interface FileConflictDialogProps {
  request: FileConflictRequest | null;
  /** 与 request.items 一一对应的决定；关闭对话框时未决定的项按跳过处理。 */
  onResolve: (decisions: FileConflictDecision[]) => void;
}

function canKeepBoth(kind: FileConflictKind, item: FileConflictItem): boolean {
  return kind === "transfer" && !!item.keepBothName;
}

function canDecide(kind: FileConflictKind, item: FileConflictItem, decision: FileConflictDecision): boolean {
  if (decision === "keepBoth") return canKeepBoth(kind, item);
  if (decision === "overwrite") return !item.noOverwrite;
  return true;
}

function SideSummary({ label, side }: { label: string; side: FileConflictSide }) {
  const { t } = useTranslation();
  const placeholder = t("fileConflict.unknown");
  const size = side.isDir ? t("fileConflict.folder") : side.size === undefined ? placeholder : formatBytes(side.size);
  const modified = side.modTime ? new Date(side.modTime).toLocaleString() : placeholder;
  return (
    <div className="min-w-0 rounded-md border px-3 py-2">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="mt-1 text-sm">{size}</div>
      <div className="text-xs text-muted-foreground">{modified}</div>
    </div>
  );
}

interface FileConflictBodyProps {
  request: FileConflictRequest;
  onProgress: (decided: FileConflictDecision[]) => void;
  onResolve: FileConflictDialogProps["onResolve"];
}

function FileConflictBody({ request, onProgress, onResolve }: FileConflictBodyProps) {
  const { t } = useTranslation();
  const { kind, items } = request;
  const [index, setIndex] = useState(0);
  const [decisions, setDecisions] = useState<FileConflictDecision[]>([]);
  const [applyToRest, setApplyToRest] = useState(false);
  const item = items[index];
  const remaining = items.length - index - 1;

  const decide = (decision: FileConflictDecision) => {
    const next = [...decisions, decision];
    let cursor = index + 1;
    if (applyToRest) {
      // 「保留两者」只能套用到给出了新名字的项、「覆盖」不能套用到原地复制的项，遇到不能套用的停下来单独问。
      while (cursor < items.length && canDecide(kind, items[cursor], decision)) {
        next.push(decision);
        cursor++;
      }
    }
    if (cursor >= items.length) {
      onResolve(next);
      return;
    }
    onProgress(next);
    setDecisions(next);
    setIndex(cursor);
  };

  return (
    <DialogContent className="sm:max-w-md" data-testid="file-conflict-dialog">
      <DialogHeader>
        <DialogTitle>
          {kind === "rename" ? t("fileConflict.renameTitle") : t("fileConflict.title")}
          {items.length > 1 && (
            <span className="ml-2 text-sm font-normal text-muted-foreground">
              {t("fileConflict.progress", { current: index + 1, total: items.length })}
            </span>
          )}
        </DialogTitle>
        <DialogDescription className="break-all font-mono text-xs" data-testid="file-conflict-target">
          {item.targetPath}
        </DialogDescription>
      </DialogHeader>
      <div className="grid grid-cols-2 gap-2">
        <SideSummary label={t("fileConflict.existing")} side={item.existing} />
        <SideSummary label={t("fileConflict.incoming")} side={item.incoming} />
      </div>
      {canKeepBoth(kind, item) && (
        <p className="text-xs text-muted-foreground">{t("fileConflict.keepBothHint", { name: item.keepBothName })}</p>
      )}
      {kind === "transfer" && remaining > 0 && (
        <div className="flex items-center gap-2">
          <Checkbox
            id="file-conflict-apply-rest"
            checked={applyToRest}
            onCheckedChange={(checked) => setApplyToRest(checked === true)}
          />
          <Label htmlFor="file-conflict-apply-rest" className="text-sm font-normal">
            {t("fileConflict.applyToRest", { count: remaining })}
          </Label>
        </div>
      )}
      <DialogFooter>
        {kind === "rename" ? (
          <Button variant="outline" autoFocus onClick={() => decide("skip")}>
            {t("action.cancel")}
          </Button>
        ) : (
          <>
            <Button variant="outline" autoFocus onClick={() => decide("skip")}>
              {t("fileConflict.skip")}
            </Button>
            {canKeepBoth(kind, item) && (
              <Button variant="outline" onClick={() => decide("keepBoth")}>
                {t("fileConflict.keepBoth")}
              </Button>
            )}
          </>
        )}
        {canDecide(kind, item, "overwrite") && (
          <Button variant="destructive" onClick={() => decide("overwrite")}>
            {t("fileConflict.overwrite")}
          </Button>
        )}
      </DialogFooter>
    </DialogContent>
  );
}

/**
 * 同名冲突对话框（受控）。逐项询问；关闭（Esc / 点遮罩）时已决定的项保留，其余全部跳过，
 * 不会默认覆盖。通常通过 useFileConflictResolver 使用。
 */
export function FileConflictDialog({ request, onResolve }: FileConflictDialogProps) {
  const decidedRef = useRef<FileConflictDecision[]>([]);
  return (
    <Dialog
      open={!!request}
      onOpenChange={(open) => {
        if (open || !request) return;
        const decided = decidedRef.current;
        decidedRef.current = [];
        onResolve(request.items.map((_, i) => decided[i] ?? "skip"));
      }}
    >
      {request && (
        <FileConflictBody
          request={request}
          onProgress={(decided) => {
            decidedRef.current = decided;
          }}
          onResolve={(decisions) => {
            decidedRef.current = [];
            onResolve(decisions);
          }}
        />
      )}
    </Dialog>
  );
}

/**
 * 把冲突询问变成一个 promise：resolveConflicts(items) 弹出对话框，resolve 为与 items 一一对应的决定。
 * 调用方负责渲染返回的 conflictDialog。没有冲突项时立即返回空数组、不弹窗。
 */
// eslint-disable-next-line react-refresh/only-export-components -- the resolver is the dialog's only entry point, co-located with it
export function useFileConflictResolver(): {
  resolveConflicts: (items: FileConflictItem[], kind?: FileConflictKind) => Promise<FileConflictDecision[]>;
  conflictDialog: ReactNode;
} {
  // 对话框打开期间还可能来新的一批（例如又拖进来几个文件）：排队依次询问，不能顶掉前一批的 promise。
  const [queue, setQueue] = useState<
    {
      id: number;
      request: FileConflictRequest;
      resolve: (decisions: FileConflictDecision[]) => void;
    }[]
  >([]);
  const nextIdRef = useRef(0);

  const resolveConflicts = useCallback((items: FileConflictItem[], kind: FileConflictKind = "transfer") => {
    if (!items.length) return Promise.resolve<FileConflictDecision[]>([]);
    return new Promise<FileConflictDecision[]>((resolve) => {
      const id = nextIdRef.current++;
      setQueue((current) => [...current, { id, request: { kind, items }, resolve }]);
    });
  }, []);

  const head = queue[0];
  const conflictDialog = (
    <FileConflictDialog
      key={head?.id}
      request={head?.request ?? null}
      onResolve={(decisions) => {
        head?.resolve(decisions);
        setQueue((current) => current.slice(1));
      }}
    />
  );

  return { resolveConflicts, conflictDialog };
}
