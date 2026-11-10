import { useState, useCallback, useEffect, useRef, type ComponentType, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Button,
  Textarea,
} from "@opskat/ui";
import { useWailsEvent } from "@/hooks/useWailsEvent";
import { S3Icon } from "@/components/asset/brand-icons";
import { ApprovalClassification } from "./ApprovalClassification";
import { ExtensionRequestDetail } from "./ExtensionRequestDetail";
import { TruncatedText } from "./TruncatedText";
import { RememberPatternEditor } from "./RememberPatternEditor";
import { hasRememberPatternErrors, rememberPrefill } from "./rememberPattern";
import { RespondOpsctlApproval } from "../../../wailsjs/go/opsctl/Opsctl";
import { permission } from "../../../wailsjs/go/models";
import {
  ShieldAlert,
  Terminal,
  Database,
  Server,
  FolderOpen,
  Globe,
  Usb,
  Trash2,
  Boxes,
  FileUp,
  Puzzle,
} from "lucide-react";
import { hasApprovalCommandEdits } from "@/lib/approval";
import { errorMessage } from "@/lib/errorMessage";
import { approvalTypePresentation, hasPinnedDetail } from "./approvalTypePresentation";
import type { ReviewInfo } from "@/lib/commandReview";
import { ReviewNotice } from "./ReviewNotice";

interface ApprovalItemData {
  type: string;
  asset_id: number;
  asset_name: string;
  group_id?: number;
  group_name?: string;
  command: string;
  detail?: string;
  // 仅扩展类型（后端 ClassifyFunc 注册）填充：check_policy 分类出的 (action, resource)，
  // 以及"记住"实际落库的 <action>:<resource-glob>。
  action?: string;
  resource?: string;
  resources?: string[];
  remember_pattern?: string;
  // 模型审核结果：辅助审批下审核未通过或审核失败时才有，说明为什么要人确认。
  review?: ReviewInfo;
}

interface SingleApprovalEvent {
  confirm_id: string;
  kind: "single" | "once" | "delete";
  type: string;
  asset_id: number;
  asset_name: string;
  command?: string;
  detail?: string;
  action?: string;
  resource?: string;
  resources?: string[];
  remember_pattern?: string;
  review?: ReviewInfo;
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
  kind: "single" | "once" | "batch" | "grant" | "delete";
  items: ApprovalItemData[];
  description?: string;
  sessionID?: string;
  editable: boolean;
}

// 递归/通配 cp 一次展开出的路径可以到 200 条（D19 上限），原样铺开没法读。超过这条线
// 折叠为一行摘要，展开后仍是全部具体主体——折叠只是呈现，批的还是那 N 条主体（D17）。
// 只对 kind=batch 生效：grant 的每条都要能编辑，折叠会让人够不着编辑框。kind=batch 里还
// 进一步只对带 detail 的批生效（见下方 isBatchCollapsed）——与 ApprovalBlock.tsx 同理。
const BATCH_COLLAPSE_THRESHOLD = 10;

// 与 ApprovalBlock.tsx 同理：lucide 图标是 ForwardRefExoticComponent，S3Icon 是普通的
// React.FC，这张表按调用点唯一用到的形状（接收 className 的组件）收窄类型。
type IconComponent = ComponentType<{ className?: string }>;

function TypeBadge({ type, command }: { type: string; command: string }) {
  const { t } = useTranslation();
  // 登记了操作类型的审批类型(通用资产:HTTP 请求 / 命令 / 取值)徽标显示操作而不是类型名。
  const op = approvalTypePresentation(type).op?.(command);
  const icons: Record<string, IconComponent> = {
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
    ext_dev_install: Puzzle,
  };
  const Icon = op?.icon ?? icons[type] ?? Terminal;
  return (
    <span
      data-testid="approval-type-badge"
      className="inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground bg-muted"
    >
      <Icon className="h-3 w-3" />
      {op ? t(op.labelKey) : type.toUpperCase()}
    </span>
  );
}

function ScopeBadge({ item }: { item: ApprovalItemData }) {
  const { t } = useTranslation();
  const cls =
    "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground bg-muted";
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

// 一轮连续审批：queue[0] 是当前条；handled 是这一轮里已经离开队首的条数（作答或被调用方
// 放弃），用来给出「第 n 条 / 共 m 条」。队列清空即开始新的一轮。
interface ApprovalRun {
  queue: QueueItem[];
  handled: number;
}

export function OpsctlApprovalDialog({ suspended = false }: { suspended?: boolean }) {
  const { t } = useTranslation();
  const [run, setRun] = useState<ApprovalRun>({ queue: [], handled: 0 });
  const queue = run.queue;
  const [editState, setEditState] = useState<Record<string, Record<number, string>>>({});
  // 记住模式属于某一条：换入下一条自然回到普通状态。
  const [rememberId, setRememberId] = useState<string | null>(null);
  const [respondingId, setRespondingId] = useState<string | null>(null);
  const denyRef = useRef<HTMLButtonElement>(null);

  const enqueue = useCallback((item: QueueItem) => {
    setRun((prev) =>
      prev.queue.some((q) => q.id === item.id)
        ? prev
        : { queue: [...prev.queue, item], handled: prev.queue.length === 0 ? 0 : prev.handled }
    );
  }, []);

  const remove = useCallback((id: string) => {
    setRun((prev) => {
      const index = prev.queue.findIndex((q) => q.id === id);
      if (index < 0) return prev;
      const next = prev.queue.filter((q) => q.id !== id);
      return { queue: next, handled: next.length === 0 ? 0 : prev.handled + (index === 0 ? 1 : 0) };
    });
    setEditState((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }, []);

  // 调用方放弃等待（opsctl 退出、Ctrl-C、超时）：三种审批都按 confirm id 关掉对应条目，
  // grant 的 confirm id 是它的 session id。
  useWailsEvent(
    "opsctl:approval-closed",
    useCallback((data: { confirm_id: string }) => remove(data.confirm_id), [remove])
  );

  useWailsEvent(
    "opsctl:approval",
    useCallback(
      (data: SingleApprovalEvent) => {
        const command = data.command || "";
        const detail = data.detail;
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
              command,
              detail,
              action: data.action,
              resource: data.resource,
              resources: data.resources,
              remember_pattern: data.remember_pattern,
              review: data.review,
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
        const mapped = (data.items || []).map((i) => ({
          type: i.type,
          asset_id: i.asset_id,
          asset_name: i.asset_name,
          command: i.command,
          // detail 是一条 cp 传输唯一携带"两端基点"的地方（"opsctl cp <src> → <dst>"）。
          // internal/app/opsctl/approval.go 的 handleBatchApproval 与 approval.BatchItem
          // 都带了它（batch verb 的混合批只给带 scope / 通用资产目标的条目逐条填），折叠摘要
          // 因此报得出两端基点，不止是条数。
          detail: i.detail,
          // opsctl batch 里扩展资产的条目才有：扩展分类出的动作与这次调用触及的资源，
          // 与单条审批一样展示在命令上方（batch 没有「记住」，不带 remember_pattern）。
          action: i.action,
          resource: i.resource,
          resources: i.resources,
          review: i.review,
        }));
        enqueue({
          id: data.confirm_id,
          kind: "batch",
          items: mapped,
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
  const rememberMode = !!current && rememberId === current.id;
  const responding = !!current && respondingId === current.id;
  const position = { current: run.handled + 1, total: run.handled + queue.length };

  // 下一条在同一组按钮下原地换入：焦点回到「拒绝」，不让一次回车/空格落到没看过的那条上。
  const currentId = current?.id;
  useEffect(() => {
    if (currentId && !suspended) denyRef.current?.focus();
  }, [currentId, suspended]);
  const rememberValues = current
    ? current.items.map((item, i) => editState[current.id]?.[i] ?? rememberPrefill(item))
    : [];
  // detail 是 cp 每条共享的"两端基点"摘要（cp.go 给每条 BatchItem 都填了同一句
  // "cp src → dst"，handleBatchApproval 原样转发）；batch verb 的混合批只给带 scope 或审批
  // 展示补充（通用资产）的条目逐条填 Detail——后者是每一条自己的说明（hasPinnedDetail），
  // 即便各条恰好同一句（命令方式都是"用哪个 shell 执行"）也不是整批的摘要。折叠是为 cp
  // 设计的，只对每条共享同一句摘要的批生效——batch verb 的条目分属不同资产/类型，没有可摘要
  // 的共同点，硬折叠会藏起本该看见的差异。
  const isBatchCollapsed =
    !!current &&
    current.kind === "batch" &&
    !!current.items[0]?.detail &&
    current.items.every((item) => item.detail === current.items[0].detail && !hasPinnedDetail(item)) &&
    current.items.length > BATCH_COLLAPSE_THRESHOLD;

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
            <TypeBadge type={item.type} command={item.command} />
            {item.asset_name && (
              <span className="text-sm text-muted-foreground">
                {item.asset_name}
                {item.asset_id > 0 && ` (ID: ${item.asset_id})`}
              </span>
            )}
          </>
        )}
      </div>
      <ApprovalClassification
        action={item.action}
        resource={item.resource}
        resources={item.resources}
        className="text-xs"
        labelClassName="text-foreground"
      />
      {item.review && <ReviewNotice review={item.review} />}
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
          <code className="text-xs font-mono">
            <TruncatedText testId="approval-command-text" text={item.command} />
          </code>
        </div>
      )}
      {item.detail &&
        (item.action ? (
          // 扩展请求的 detail 是格式化后的工具 + 参数 JSON，可能很长——按既有的
          // <details> 折叠机制展示（与 ApprovalBlock.tsx 同一套），而不是常驻铺开。
          <details className="text-xs text-muted-foreground">
            <summary className="cursor-pointer select-none">{t("ai.approvalRequestDetail")}</summary>
            <div className="mt-1 max-h-48 overflow-auto rounded bg-muted p-2 text-xs">
              <ExtensionRequestDetail detail={item.detail} />
            </div>
          </details>
        ) : (
          <div className="select-text text-xs text-muted-foreground font-mono whitespace-pre-wrap">{item.detail}</div>
        ))}
    </div>
  );

  const respond = useCallback(
    async (decision: string) => {
      if (!current || respondingId) return;
      const id = current.id;

      const resp = new permission.ApprovalResponse();
      resp.decision = decision;

      const shouldSendEdits =
        (current.kind === "grant" && decision !== "deny") || (current.kind === "single" && decision === "allowAll");
      const edits = editState[current.id] || {};
      const proposed = current.items.map((item) => ({ command: rememberPrefill(item) }));
      const commands = proposed.map((p, i) => edits[i] ?? p.command);

      if (shouldSendEdits && hasApprovalCommandEdits(proposed, commands)) {
        resp.edited_items = current.items.map((item, i) => {
          const edited = new permission.ApprovalItem();
          edited.type = item.type;
          edited.asset_id = item.asset_id;
          edited.asset_name = item.asset_name;
          edited.group_id = item.group_id || 0;
          edited.group_name = item.group_name || "";
          edited.command = commands[i];
          edited.detail = item.detail || "";
          return edited;
        });
      }

      setRespondingId(id);
      try {
        await RespondOpsctlApproval(id, resp);
        remove(id);
      } catch (e) {
        // 后端不收这次应答（审批已不存在、记住的模式不合规则）：说明原因。允许类的应答留着
        // 这一条让用户改正或拒绝；拒绝本身失败说明这条已经没人在等，留着只会卡住队列。
        toast.error(t("opsctlApproval.respondFailed"), { description: errorMessage(e) });
        if (decision === "deny") remove(id);
      } finally {
        setRespondingId(null);
      }
    },
    [current, editState, respondingId, remove, t]
  );

  // 作答后下一条在同一组按钮下换入，同一次双击的第二下（detail > 1）会落到用户没看过的
  // 那一条上——包括「记住并允许」：第一下进入记住模式，第二下就成了永久授权。
  const onAnswer = (action: () => void) => (e: MouseEvent) => {
    if (e.detail > 1) return;
    action();
  };

  const enterRememberMode = () => {
    if (!current) return;
    // 初始化 editState：命令审批预填命令，分类过的扩展审批预填 <action>:<resource>
    const edits: Record<number, string> = {};
    current.items.forEach((it, idx) => {
      edits[idx] = rememberPrefill(it);
    });
    setEditState((prev) => ({ ...prev, [current.id]: { ...prev[current.id], ...edits } }));
    setRememberId(current.id);
  };

  const exitRememberMode = () => {
    if (!current) return;
    // single 的 editState 只承载记住的模式：退出即丢弃，再进入时重新预填。
    setEditState((prev) => {
      const next = { ...prev };
      delete next[current.id];
      return next;
    });
    setRememberId(null);
  };

  const handleOpenChange = useCallback(
    (v: boolean) => {
      if (!v) void respond("deny");
    },
    [respond]
  );

  return (
    <Dialog open={open && !suspended} onOpenChange={handleOpenChange}>
      <DialogContent
        data-testid="opsctl-approval-dialog"
        className="sm:max-w-lg max-h-[80vh] flex flex-col"
        showCloseButton={false}
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          denyRef.current?.focus();
        }}
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
                {position.total > 1 && (
                  <span
                    data-testid="opsctl-approval-position"
                    data-current={position.current}
                    data-total={position.total}
                    className="text-sm font-normal text-muted-foreground"
                  >
                    {t("opsctlApproval.queuePosition", position)}
                  </span>
                )}
              </DialogTitle>
              <DialogDescription>
                {current.kind === "grant"
                  ? t("opsctlApproval.grantDescription")
                  : current.kind === "batch"
                    ? t("opsctlApproval.batchDescription", { count: current.items.length })
                    : current.items[0]?.type === "ext_dev_install"
                      ? t("opsctlApproval.extDevInstallDescription")
                      : t("opsctlApproval.description")}
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-2 overflow-y-auto flex-1 min-h-0">
              {current.description && <div className="select-text text-sm font-medium">{current.description}</div>}
              {isBatchCollapsed ? (
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
                <RememberPatternEditor
                  items={current.items}
                  values={rememberValues}
                  onChange={(i, value) =>
                    setEditState((prev) => ({
                      ...prev,
                      [current.id]: { ...prev[current.id], [i]: value },
                    }))
                  }
                  textClassName="text-xs"
                  inputClassName="text-xs"
                  onCancel={exitRememberMode}
                />
              )}
            </div>

            <DialogFooter className="gap-2">
              <Button
                ref={denyRef}
                data-testid="opsctl-approval-deny"
                variant="outline"
                disabled={responding}
                onClick={onAnswer(() => void respond("deny"))}
              >
                {t("opsctlApproval.deny")}
              </Button>
              {current.kind === "single" &&
                current.sessionID &&
                (rememberMode ? (
                  <Button
                    data-testid="opsctl-approval-allow-all"
                    variant="secondary"
                    disabled={responding || hasRememberPatternErrors(current.items, rememberValues)}
                    onClick={onAnswer(() => void respond("allowAll"))}
                  >
                    {t("ai.approvalRememberAndAllow")}
                  </Button>
                ) : (
                  <Button
                    data-testid="opsctl-approval-remember"
                    variant="secondary"
                    disabled={responding}
                    onClick={onAnswer(enterRememberMode)}
                  >
                    {t("ai.approvalRememberAndAllow")}
                  </Button>
                ))}
              <Button
                data-testid="opsctl-approval-allow"
                disabled={responding}
                onClick={onAnswer(() => void respond("allow"))}
              >
                {current.kind === "grant"
                  ? t("opsctlApproval.approve")
                  : current.kind === "single"
                    ? t("ai.approvalAllowOnce")
                    : t("opsctlApproval.allow")}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
