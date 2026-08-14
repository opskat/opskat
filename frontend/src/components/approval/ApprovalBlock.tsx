import { memo, useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";
import {
  ShieldAlert,
  Terminal,
  Database,
  Server,
  Globe,
  FolderOpen,
  FileEdit,
  FilePlus,
  FileUp,
  Usb,
  Trash2,
  Boxes,
} from "lucide-react";
import { Button, Input, Textarea } from "@opskat/ui";
import { S3Icon } from "@/components/asset/brand-icons";
import { RespondAIApproval } from "../../../wailsjs/go/ai/AI";
import { permission } from "../../../wailsjs/go/models";
import type { ContentBlock } from "@/stores/aiStore";
import { hasApprovalCommandEdits } from "@/lib/approval";

interface ApprovalBlockProps {
  block: ContentBlock;
}

// 递归/通配 cp 一次展开出的路径可以到 200 条（D19 上限），原样铺开没法读。超过这条线
// 折叠为一行摘要，展开后仍是全部具体主体——折叠只是呈现，批的还是那 N 条主体（D17）。
// 只对 kind=batch 生效：grant 的每条都要能编辑，折叠会让人够不着编辑框。kind=batch 里还
// 进一步只对带 detail 的批生效（见下方 isBatchCollapsed）——detail 是 cp 每条共享的"两端
// 基点"摘要，batch_exec 的异构批没有这个概念，硬折叠会藏起本该看见的差异。
const BATCH_COLLAPSE_THRESHOLD = 10;

export const ApprovalBlock = memo(function ApprovalBlock({ block }: ApprovalBlockProps) {
  const { t } = useTranslation();
  const isPending = block.status === "pending_confirm";
  const items = block.approvalItems || [];
  const kind = block.approvalKind || "single";
  const isLocalTool = kind === "local_tool";
  const localToolName = block.approvalToolName || items[0]?.type || "";

  // 后端安全投影同时携带显式标志；不能从展示文本中的字面量 <redacted> 反推，
  // 否则合法命令 `printf '<redacted>'` 会被误判并失去持久授权入口。
  const redacted = block.approvalRedacted === true;

  // local_tool 在 rememberMode 用 approvalPatterns（多 sub-command 时多行），
  // 其它 kind 沿用单条 item.command。
  const initialPatterns = isLocalTool ? (block.approvalPatterns || []).join("\n") : "";

  const [editedCommands, setEditedCommands] = useState<Record<number, string>>(() => {
    const map: Record<number, string> = {};
    items.forEach((item, i) => {
      map[i] = isLocalTool && i === 0 ? initialPatterns || item.command : item.command;
    });
    return map;
  });

  const [rememberMode, setRememberMode] = useState(false);

  // 确认/拒绝后不再显示
  if (!isPending) return null;

  // detail 是这次传输唯一携带"两端基点"的地方（checkAccessBatch 给每条都填了同一句
  // "cp src → dst"，哪怕批量只有一条也不为空）；batch_exec 的批量项没有这个概念——
  // tool_handler_batch.go 建 item 时压根不设 Detail，因此永远是空串。detail 是否非空
  // 因此是 payload 里现成的、可靠的判据：折叠是为 cp 这种"每条共享同一句摘要"的批设计的，
  // batch_exec 的条目分属不同资产/工具，没有可摘要的共同点——折叠了只会把 Approve 按钮
  // 架在一句读不出内容的"N 项已折叠"上面，比展示全部异构命令更危险。
  const batchDetail = items[0]?.detail;
  const isBatchCollapsed = kind === "batch" && !!batchDetail && items.length > BATCH_COLLAPSE_THRESHOLD;

  const renderBatchItem = (item: (typeof items)[number], i: number) => (
    <div key={i} className="rounded-lg bg-warning/5 p-2.5 space-y-1.5">
      <div className="flex items-center gap-1.5">
        <TypeBadge type={item.type} compact />
        {item.asset_name && <span className="text-[11px] text-warning">{item.asset_name}</span>}
      </div>
      <div className="rounded bg-warning/5 px-2 py-[5px]">
        <code className="block font-mono text-[10px] text-muted-foreground whitespace-pre-wrap break-all">
          {item.command}
        </code>
      </div>
    </div>
  );

  const respond = (decision: string) => {
    if (!block.confirmId) return;

    const resp = new permission.ApprovalResponse();
    resp.decision = decision;

    const carriesEdited = kind === "grant" || ((kind === "single" || kind === "local_tool") && decision === "allowAll");
    const commands = items.map((item, i) => editedCommands[i] || item.command);
    if (carriesEdited && decision !== "deny" && hasApprovalCommandEdits(items, commands)) {
      resp.edited_items = items.map((item, i) => {
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

    RespondAIApproval(block.confirmId, resp);
  };

  return (
    <div
      data-testid="ai-approval-block"
      data-approval-kind={kind}
      data-confirm-id={block.confirmId}
      className="my-2 rounded-[10px] border border-warning/30 bg-warning/10 p-4 space-y-3 text-xs overflow-hidden"
    >
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <ShieldAlert className="h-4 w-4 shrink-0 text-warning" />
          <span className="font-semibold text-[13px] text-warning">
            {kind === "grant"
              ? t("ai.approvalGrantTitle")
              : kind === "batch"
                ? t("ai.approvalBatchTitle", { count: items.length })
                : kind === "local_tool"
                  ? t("ai.approvalLocalToolTitle", { tool: localToolName })
                  : kind === "delete"
                    ? t("ai.approvalDeleteTitle")
                    : t("ai.approvalSingleTitle")}
          </span>
          {block.agentRole && (
            <span className="text-[10px] text-muted-foreground bg-muted rounded px-1 py-0.5">{block.agentRole}</span>
          )}
        </div>
        <span className="inline-flex items-center rounded-full bg-warning/15 h-5 px-2 text-[10px] font-semibold text-warning">
          {t("ai.approvalPending")}
        </span>
      </div>

      {/* Items */}
      {isBatchCollapsed ? (
        // 既有的折叠交互（同一份 detail 展开态在本文件里已经在用，见下方 <details>）：
        // 摘要行常驻可见，具体主体折叠在里面，点开就是普普通通的列表——不是新的授权范围。
        <details className="rounded-lg bg-warning/5 p-2.5">
          <summary
            data-testid="ai-approval-batch-summary"
            data-count={items.length}
            className="cursor-pointer select-none text-[11px] text-warning"
          >
            {t("ai.approvalBatchCollapsedSummary", { count: items.length })}
            {batchDetail && <span className="text-muted-foreground"> · {batchDetail}</span>}
          </summary>
          <div className="space-y-2 mt-2">{items.map((item, i) => renderBatchItem(item, i))}</div>
        </details>
      ) : (
        <div className="space-y-2">
          {items.map((item, i) =>
            kind === "batch" ? (
              renderBatchItem(item, i)
            ) : (
              <div key={i} className="rounded-lg bg-warning/5 p-3 space-y-2">
                <div className="flex items-center gap-2">
                  {kind === "grant" ? (
                    <ScopeBadge item={item} />
                  ) : (
                    <>
                      <TypeBadge type={item.type} />
                      {scopeName(item) && <span className="text-xs text-warning">{scopeName(item)}</span>}
                    </>
                  )}
                </div>
                {kind === "grant" && !redacted ? (
                  <Textarea
                    value={editedCommands[i] || ""}
                    onChange={(e) => setEditedCommands((prev) => ({ ...prev, [i]: e.target.value }))}
                    className="font-mono text-[11px] min-h-[32px] resize-y bg-background border-border"
                    rows={Math.max(1, (editedCommands[i] || "").split("\n").length)}
                  />
                ) : (
                  <div className="rounded-md bg-warning/5 px-2.5 py-2">
                    <code
                      data-testid="ai-approval-command"
                      className="block font-mono text-[11px] text-muted-foreground whitespace-pre-wrap break-all"
                    >
                      {item.command}
                    </code>
                  </div>
                )}
                {item.detail &&
                  (kind === "delete" ? (
                    // 删除不可逆：警告不能藏在一次点击之后，常驻展示而不是 <details> 折叠。
                    <div className="text-[10px] text-muted-foreground/80">
                      <div className="select-none">{t(detailSummaryKey(item.type))}</div>
                      <DetailPre text={item.detail} />
                    </div>
                  ) : (
                    <details className="text-[10px] text-muted-foreground/80">
                      <summary className="cursor-pointer select-none">{t(detailSummaryKey(item.type))}</summary>
                      <DetailPre text={item.detail} />
                    </details>
                  ))}
              </div>
            )
          )}
        </div>
      )}

      {/* Reason (grant only, before buttons) */}
      {kind === "grant" && block.approvalDescription && (
        <div className="flex gap-1.5">
          <span className="text-[11px] font-medium text-warning shrink-0">{t("ai.approvalReasonLabel")}</span>
          <span className="text-[11px] text-muted-foreground">{block.approvalDescription}</span>
        </div>
      )}

      {/* Remember mode pattern editor */}
      {kind === "single" && rememberMode && (
        <div className="space-y-1.5 pt-0.5">
          <div className="text-[10px] text-muted-foreground">{t("opsctlApproval.patternLabel")}</div>
          {items.map((_item, i) => (
            <Input
              key={i}
              value={editedCommands[i] || ""}
              onChange={(e) => setEditedCommands((prev) => ({ ...prev, [i]: e.target.value }))}
              className="font-mono text-[11px] h-8 bg-background border-border"
              placeholder={t("opsctlApproval.patternPlaceholder")}
            />
          ))}
          <div className="text-[10px] text-muted-foreground/70">{t("opsctlApproval.patternHint")}</div>
        </div>
      )}
      {kind === "local_tool" && rememberMode && (
        <div className="space-y-1.5 pt-0.5">
          <div className="text-[10px] text-muted-foreground">{t("opsctlApproval.patternLabel")}</div>
          <Textarea
            value={editedCommands[0] || ""}
            onChange={(e) => setEditedCommands((prev) => ({ ...prev, 0: e.target.value }))}
            className="font-mono text-[11px] min-h-[60px] resize-y bg-background border-border"
            rows={Math.max(2, (editedCommands[0] || "").split("\n").length)}
            placeholder={t("opsctlApproval.patternPlaceholder")}
          />
          <div className="text-[10px] text-muted-foreground/70">{t("ai.approvalLocalToolPatternHint")}</div>
        </div>
      )}

      {/* Action buttons */}
      <div className="flex justify-end gap-2 pt-1">
        {kind === "batch" ? (
          <>
            <Button
              size="sm"
              variant="outline"
              className="h-8 rounded-md px-4 text-xs border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
              onClick={() => respond("deny")}
            >
              {t("ai.approvalDenyAll")}
            </Button>
            <Button
              size="sm"
              className="h-8 rounded-md px-4 text-xs bg-warning hover:bg-warning/90 text-warning-foreground font-semibold"
              onClick={() => respond("allow")}
            >
              {t("ai.approvalAllowAll")}
            </Button>
          </>
        ) : kind === "grant" ? (
          <>
            <Button
              size="sm"
              variant="outline"
              data-testid="ai-approval-deny"
              className="h-8 rounded-md px-4 text-xs border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
              onClick={() => respond("deny")}
            >
              {t("ai.approvalDeny")}
            </Button>
            {!redacted && (
              <Button
                size="sm"
                className="h-8 rounded-md px-4 text-xs bg-warning hover:bg-warning/90 text-warning-foreground font-semibold"
                onClick={() => respond("allow")}
              >
                {t("ai.approvalApprove")}
              </Button>
            )}
          </>
        ) : (
          // single & local_tool: deny / remember-and-allow / allow（仅本次）
          // delete & extension: deny / allow only —— 两者都没有安全、已定义的 grant pattern，
          // 「记住」开关是通往 allowAll 的唯一入口，因此不给这个入口。
          <>
            <Button
              size="sm"
              variant="outline"
              data-testid="ai-approval-deny"
              className="h-8 rounded-md px-4 text-xs border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
              onClick={() => respond("deny")}
            >
              {t("ai.approvalDeny")}
            </Button>
            {(kind === "single" || kind === "local_tool") &&
              !redacted &&
              (rememberMode ? (
                <Button
                  size="sm"
                  data-testid="ai-approval-allow-all"
                  className="h-8 rounded-md px-4 text-xs bg-warning/20 text-warning hover:bg-warning/30"
                  onClick={() => respond("allowAll")}
                >
                  {t("ai.approvalRememberAndAllow")}
                </Button>
              ) : (
                <Button
                  size="sm"
                  data-testid="ai-approval-remember"
                  className="h-8 rounded-md px-4 text-xs bg-warning/20 text-warning hover:bg-warning/30"
                  onClick={() => {
                    setRememberMode(true);
                  }}
                >
                  {t("opsctlApproval.remember")}
                </Button>
              ))}
            <Button
              size="sm"
              data-testid="ai-approval-allow"
              className="h-8 rounded-md px-4 text-xs bg-warning hover:bg-warning/90 text-warning-foreground font-semibold"
              onClick={() => respond("allow")}
            >
              {rememberMode ? t("ai.approvalOnlyOnce") : t("ai.approvalAllow")}
            </Button>
          </>
        )}
      </div>
    </div>
  );
});

// detail 的展开标题按审批类型取：本地写入看内容、本地编辑看改动、删除看不可撤销影响、文件传输看方向。
function detailSummaryKey(type: string): string {
  switch (type) {
    case "local_write":
      return "ai.approvalLocalToolContentPreview";
    case "local_edit":
      return "ai.approvalLocalToolEditPreview";
    case "delete":
      return "ai.approvalDeleteDetail";
    default:
      return "ai.approvalTransferDetail";
  }
}

function DetailPre({ text }: { text: string }) {
  return (
    <pre className="mt-1.5 max-h-48 overflow-auto rounded bg-warning/5 px-2 py-1.5 font-mono whitespace-pre-wrap break-all">
      {text}
    </pre>
  );
}

// asset_name 优先，退回 group_name——删除分组等以 group 为目标的操作没有 asset_id，
// 只有 group_id/group_name（handleDeleteGroup 就是这样填的）。ScopeBadge 的 asset/group
// 回退分支复用同一份逻辑，避免第三份判断分叉。
function scopeName(item: { asset_id: number; asset_name: string; group_id?: number; group_name?: string }): string {
  if (item.asset_id > 0) return item.asset_name;
  if (item.group_id && item.group_id > 0) return item.group_name || "";
  return "";
}

// lucide 图标是 ForwardRefExoticComponent，S3Icon（brand-icons.tsx）是普通的 React.FC 包装
// @iconify/react；这张表两种都装，因此按调用点唯一用到的形状（接收 className 的组件）来
// 收窄类型，而不是逼 S3Icon 伪装成 lucide 的 ref-forwarding 形状。
type IconComponent = ComponentType<{ className?: string }>;

function TypeBadge({ type, compact }: { type: string; compact?: boolean }) {
  const icons: Record<string, IconComponent> = {
    exec: Terminal,
    serial: Usb,
    sql: Database,
    redis: Server,
    mongo: Database,
    kafka: Database,
    grant: Globe,
    cp: FileUp,
    local_bash: Terminal,
    local_write: FilePlus,
    local_edit: FileEdit,
    delete: Trash2,
    etcd: Database,
    k8s: Boxes,
    oss: S3Icon,
  };
  const Icon = icons[type] || Terminal;
  if (compact) {
    return (
      <span className="inline-flex items-center gap-[3px] rounded-[3px] border border-warning/30 h-[18px] px-[5px] text-[8px] font-bold text-warning bg-background">
        <Icon className="h-[11px] w-[11px]" />
        {type.toUpperCase()}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded border border-warning/30 h-5 px-1.5 text-[9px] font-bold text-warning bg-background">
      <Icon className="h-3 w-3" />
      {type.toUpperCase()}
    </span>
  );
}

function ScopeBadge({
  item,
}: {
  item: { asset_id: number; asset_name: string; group_id?: number; group_name?: string };
}) {
  const { t } = useTranslation();
  const cls =
    "inline-flex items-center gap-[3px] rounded-[3px] border border-warning/30 h-[18px] px-[5px] text-[8px] font-semibold text-warning bg-background";
  if (item.asset_id > 0) {
    return (
      <span className={cls}>
        <Server className="h-[11px] w-[11px]" />
        {scopeName(item)}
      </span>
    );
  }
  if (item.group_id && item.group_id > 0) {
    return (
      <span className={cls}>
        <FolderOpen className="h-[11px] w-[11px]" />
        {scopeName(item)}
      </span>
    );
  }
  return (
    <span className={cls}>
      <Globe className="h-[11px] w-[11px]" />
      {t("opsctlApproval.scopeAll")}
    </span>
  );
}
