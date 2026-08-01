import { useState, useCallback } from "react";
import { useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Button,
  Input,
  Textarea,
} from "@opskat/ui";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { S3Icon } from "@/components/asset/brand-icons";
import { RespondOpsctlApproval } from "../../../wailsjs/go/opsctl/Opsctl";
import { permission } from "../../../wailsjs/go/models";
import { ShieldAlert, Terminal, Database, Server, FolderOpen, Globe, Usb, Trash2, Boxes, FileUp } from "lucide-react";

interface ApprovalItemData {
  type: string;
  asset_id: number;
  asset_name: string;
  group_id?: number;
  group_name?: string;
  command: string;
  detail?: string;
}

interface SingleApprovalEvent {
  confirm_id: string;
  kind: "single" | "once" | "delete" | "extension";
  type: string;
  asset_id: number;
  asset_name: string;
  command?: string;
  detail?: string;
  session_id: string;
}

interface BatchApprovalEvent {
  confirm_id: string;
  items: ApprovalItemData[];
  session_id: string;
}

interface GrantApprovalEvent {
  items: ApprovalItemData[];
  session_id: string;
  description?: string;
}

interface QueueItem {
  id: string;
  // "delete" 与 "single" 共用同一套渲染骨架（同一个 opsctl:approval 事件），
  // 只在按钮区按 kind 收窄——与 ApprovalBlock.tsx 的 kind 取值保持一致，不新造第三套判断。
  kind: "single" | "once" | "batch" | "grant" | "delete" | "extension";
  items: ApprovalItemData[];
  description?: string;
  sessionID?: string;
  editable: boolean;
}

// 递归/通配 cp 一次展开出的路径可以到 200 条（D19 上限），原样铺开没法读。超过这条线
// 折叠为一行摘要，展开后仍是全部具体主体——折叠只是呈现，批的还是那 N 条主体（D17），
// 因此只对 kind=batch 生效：grant 的每条都要能编辑，折叠会让人够不着编辑框。与
// ApprovalBlock.tsx 的折叠阈值保持一致。
const BATCH_COLLAPSE_THRESHOLD = 10;

function TypeBadge({ type }: { type: string }) {
  const icons: Record<string, typeof Terminal> = {
    exec: Terminal,
    serial: Usb,
    sql: Database,
    redis: Server,
    mongo: Database,
    kafka: Database,
    delete: Trash2,
    etcd: Database,
    k8s: Boxes,
    cp: FileUp,
    oss: S3Icon,
  };
  const Icon = icons[type] || Terminal;
  return (
    <span className="inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground bg-muted">
      <Icon className="h-3 w-3" />
      {type.toUpperCase()}
    </span>
  );
}

function ScopeBadge({ item }: { item: ApprovalItemData }) {
  const { t } = useTranslation();
  const cls =
    "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground bg-muted";
  if (item.asset_id > 0) {
    return (
      <span className={cls}>
        <Server className="h-3 w-3" />
        {item.asset_name}
      </span>
    );
  }
  if (item.group_id && item.group_id > 0) {
    return (
      <span className={cls}>
        <FolderOpen className="h-3 w-3" />
        {item.group_name}
      </span>
    );
  }
  return (
    <span className={cls}>
      <Globe className="h-3 w-3" />
      {t("opsctlApproval.scopeAll")}
    </span>
  );
}

export function OpsctlApprovalDialog() {
  const { t } = useTranslation();
  const [queue, setQueue] = useState<QueueItem[]>([]);
  const [editState, setEditState] = useState<Record<string, Record<number, string>>>({});
  const [rememberMode, setRememberMode] = useState(false);

  const enqueue = useCallback((item: QueueItem) => {
    setQueue((prev) => (prev.some((q) => q.id === item.id) ? prev : [...prev, item]));
  }, []);

  useWailsEvent(
    "opsctl:approval",
    useCallback(
      (data: SingleApprovalEvent) => {
        enqueue({
          id: data.confirm_id,
          // 后端按 permission registry 发送 capability kind；只有真正有 pattern
          // 匹配契约的 single 才能进入 remember/allowAll。
          kind: data.kind,
          items: [
            {
              type: data.type,
              asset_id: data.asset_id,
              asset_name: data.asset_name,
              command: data.command || "",
              detail: data.detail,
            },
          ],
          sessionID: data.session_id,
          editable: false,
        });
      },
      [enqueue]
    )
  );

  useWailsEvent(
    "opsctl:batch-approval",
    useCallback(
      (data: BatchApprovalEvent) => {
        enqueue({
          id: data.confirm_id,
          kind: "batch",
          items: (data.items || []).map((i) => ({
            type: i.type,
            asset_id: i.asset_id,
            asset_name: i.asset_name,
            command: i.command,
            // detail 是一条 cp 传输唯一携带"两端基点"的地方（"opsctl cp <src> → <dst>"）。
            // internal/app/opsctl/approval.go 的 handleBatchApproval 与 approval.BatchItem
            // 都带了它（batch_exec 的 exec/sql/redis/mongo 混合批不产出，留空），折叠摘要
            // 因此报得出两端基点，不止是条数。
            detail: i.detail,
          })),
          sessionID: data.session_id,
          editable: false,
        });
      },
      [enqueue]
    )
  );

  useWailsEvent(
    "opsctl:grant-approval",
    useCallback(
      (data: GrantApprovalEvent) => {
        const items: ApprovalItemData[] = (data.items || []).map((i) => ({
          type: i.type,
          asset_id: i.asset_id,
          asset_name: i.asset_name,
          group_id: i.group_id,
          group_name: i.group_name,
          command: i.command,
          detail: i.detail,
        }));
        enqueue({
          id: data.session_id,
          kind: "grant",
          items,
          description: data.description,
          sessionID: data.session_id,
          editable: true,
        });
        const edits: Record<number, string> = {};
        items.forEach((it, idx) => {
          edits[idx] = it.command;
        });
        setEditState((prev) => ({ ...prev, [data.session_id]: edits }));
      },
      [enqueue]
    )
  );

  const current = queue[0] || null;
  const open = !!current;

  // 折叠态与展开态共用同一份单条渲染，避免同一段 JSX 抄两份。cur 显式传参而不是闭包
  // 捕获外层 current——调用点都在 `current && (...)` narrow 过的分支里，但函数本身
  // 定义在分支外，闭包捕获会把 current 的类型退回 QueueItem | null。
  const renderApprovalItem = (cur: QueueItem, item: ApprovalItemData, i: number) => (
    <div key={i} className="rounded-md border p-2 space-y-1.5">
      <div className="flex items-center gap-2">
        {cur.kind === "grant" ? (
          <ScopeBadge item={item} />
        ) : (
          <>
            <TypeBadge type={item.type} />
            {item.asset_name && (
              <span className="text-sm text-muted-foreground">
                {item.asset_name}
                {item.asset_id > 0 && ` (ID: ${item.asset_id})`}
              </span>
            )}
          </>
        )}
      </div>
      {cur.editable ? (
        <Textarea
          value={editState[cur.id]?.[i] ?? item.command}
          onChange={(e) =>
            setEditState((prev) => ({
              ...prev,
              [cur.id]: { ...prev[cur.id], [i]: e.target.value },
            }))
          }
          className="font-mono text-xs min-h-[40px] resize-y"
          rows={Math.max(2, (editState[cur.id]?.[i] ?? item.command).split("\n").length)}
        />
      ) : (
        <div className="rounded-md bg-muted p-2 max-h-[150px] overflow-auto">
          <code className="text-xs font-mono whitespace-pre-wrap break-all">{item.command}</code>
        </div>
      )}
      {item.detail && <div className="text-xs text-muted-foreground font-mono">{item.detail}</div>}
    </div>
  );

  const respond = useCallback(
    (decision: string) => {
      if (!current) return;

      const resp = new permission.ApprovalResponse();
      resp.decision = decision;

      const shouldSendEdits =
        (current.kind === "grant" && decision !== "deny") || (current.kind === "single" && decision === "allowAll");

      if (shouldSendEdits) {
        const edits = editState[current.id] || {};
        resp.edited_items = current.items.map((item, i) => {
          const edited = new permission.ApprovalItem();
          edited.type = item.type;
          edited.asset_id = item.asset_id;
          edited.asset_name = item.asset_name;
          edited.group_id = item.group_id || 0;
          edited.group_name = item.group_name || "";
          edited.command = edits[i] ?? item.command;
          edited.detail = item.detail || "";
          return edited;
        });
      }

      RespondOpsctlApproval(current.id, resp);
      setQueue((prev) => prev.slice(1));
      setRememberMode(false);
      setEditState((prev) => {
        const next = { ...prev };
        delete next[current.id];
        return next;
      });
    },
    [current, editState]
  );

  const handleOpenChange = useCallback(
    (v: boolean) => {
      if (!v) respond("deny");
    },
    [respond]
  );

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        data-testid="opsctl-approval-dialog"
        className="sm:max-w-lg max-h-[80vh] flex flex-col"
        showCloseButton={false}
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        {current && (
          <>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <ShieldAlert className="h-5 w-5 text-warning" />
                {current.kind === "grant"
                  ? t("opsctlApproval.grantTitle")
                  : current.kind === "batch"
                    ? t("opsctlApproval.batchTitle")
                    : current.kind === "delete"
                      ? t("ai.approvalDeleteTitle")
                      : t("opsctlApproval.title")}
                {queue.length > 1 && (
                  <span className="text-sm font-normal text-muted-foreground">(1/{queue.length})</span>
                )}
              </DialogTitle>
              <DialogDescription>
                {current.kind === "grant"
                  ? t("opsctlApproval.grantDescription")
                  : current.kind === "batch"
                    ? t("opsctlApproval.batchDescription", { count: current.items.length })
                    : t("opsctlApproval.description")}
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-2 overflow-y-auto flex-1 min-h-0">
              {current.description && <div className="text-sm font-medium">{current.description}</div>}
              {current.kind === "batch" && current.items.length > BATCH_COLLAPSE_THRESHOLD ? (
                // 既有的折叠交互（与 ApprovalBlock.tsx 同一套 <details>/<summary>）：摘要行
                // 常驻可见，具体主体折叠在里面，展开后就是普普通通的列表——不是新的授权范围（D17）。
                <details className="rounded-md border p-2">
                  <summary
                    data-testid="opsctl-approval-batch-summary"
                    data-count={current.items.length}
                    className="cursor-pointer select-none text-sm"
                  >
                    {t("opsctlApproval.batchCollapsedSummary", { count: current.items.length })}
                    {current.items[0]?.detail && (
                      <span className="text-muted-foreground"> · {current.items[0].detail}</span>
                    )}
                  </summary>
                  <div className="space-y-2 mt-2">
                    {current.items.map((item, i) => renderApprovalItem(current, item, i))}
                  </div>
                </details>
              ) : (
                current.items.map((item, i) => renderApprovalItem(current, item, i))
              )}
              {/* 记住模式：展开模式编辑器 */}
              {current.kind === "single" && current.sessionID && rememberMode && (
                <div className="space-y-1.5 pt-1">
                  <div className="text-xs text-muted-foreground">{t("opsctlApproval.patternLabel")}</div>
                  {current.items.map((_item, i) => (
                    <Input
                      key={i}
                      value={editState[current.id]?.[i] ?? _item.command}
                      onChange={(e) =>
                        setEditState((prev) => ({
                          ...prev,
                          [current.id]: { ...prev[current.id], [i]: e.target.value },
                        }))
                      }
                      className="font-mono text-xs"
                      placeholder={t("opsctlApproval.patternPlaceholder")}
                    />
                  ))}
                  <div className="text-[10px] text-muted-foreground/70">{t("opsctlApproval.patternHint")}</div>
                </div>
              )}
            </div>

            <DialogFooter className="gap-2">
              <Button data-testid="opsctl-approval-deny" variant="outline" onClick={() => respond("deny")}>
                {t("opsctlApproval.deny")}
              </Button>
              {current.kind === "single" &&
                current.sessionID &&
                (rememberMode ? (
                  <Button variant="secondary" onClick={() => respond("allowAll")}>
                    {t("opsctlApproval.approve")}
                  </Button>
                ) : (
                  <Button
                    variant="secondary"
                    onClick={() => {
                      // 初始化 editState 用当前命令预填充
                      const edits: Record<number, string> = {};
                      current.items.forEach((it, idx) => {
                        edits[idx] = it.command;
                      });
                      setEditState((prev) => ({ ...prev, [current.id]: { ...prev[current.id], ...edits } }));
                      setRememberMode(true);
                    }}
                  >
                    {t("opsctlApproval.remember")}
                  </Button>
                ))}
              <Button data-testid="opsctl-approval-allow" onClick={() => respond("allow")}>
                {current.kind === "grant" ? t("opsctlApproval.approve") : t("opsctlApproval.allow")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
