import { memo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ShieldAlert, Terminal, Database, Server, Globe, FolderOpen, FileEdit, FilePlus, Usb } from "lucide-react";
import { Button, Input, Textarea } from "@opskat/ui";
import { RespondAIApproval } from "../../../wailsjs/go/ai/AI";
import { permission } from "../../../wailsjs/go/models";
import type { ContentBlock } from "@/stores/aiStore";

interface ApprovalBlockProps {
  block: ContentBlock;
}

export const ApprovalBlock = memo(function ApprovalBlock({ block }: ApprovalBlockProps) {
  const { t } = useTranslation();
  const isPending = block.status === "pending_confirm";
  const items = block.approvalItems || [];
  const kind = block.approvalKind || "single";
  const isLocalTool = kind === "local_tool";
  const localToolName = block.approvalToolName || items[0]?.type || "";

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

  const respond = (decision: string) => {
    if (!block.confirmId) return;

    const resp = new permission.ApprovalResponse();
    resp.decision = decision;

    const carriesEdited = kind === "grant" || ((kind === "single" || kind === "local_tool") && decision === "allowAll");
    if (carriesEdited && decision !== "deny") {
      resp.edited_items = items.map((item, i) => {
        const edited = new permission.ApprovalItem();
        edited.type = item.type;
        edited.asset_id = item.asset_id;
        edited.asset_name = item.asset_name;
        edited.group_id = item.group_id || 0;
        edited.group_name = item.group_name || "";
        edited.command = editedCommands[i] || item.command;
        edited.detail = item.detail || "";
        return edited;
      });
    }

    RespondAIApproval(block.confirmId, resp);
  };

  return (
    <div className="my-2 rounded-[10px] border border-warning/30 bg-warning/10 p-4 space-y-3 text-xs overflow-hidden">
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
      <div className="space-y-2">
        {items.map((item, i) =>
          kind === "batch" ? (
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
          ) : (
            <div key={i} className="rounded-lg bg-warning/5 p-3 space-y-2">
              <div className="flex items-center gap-2">
                {kind === "grant" ? (
                  <ScopeBadge item={item} />
                ) : (
                  <>
                    <TypeBadge type={item.type} />
                    {item.asset_name && <span className="text-xs text-warning">{item.asset_name}</span>}
                  </>
                )}
              </div>
              {kind === "grant" ? (
                <Textarea
                  value={editedCommands[i] || ""}
                  onChange={(e) => setEditedCommands((prev) => ({ ...prev, [i]: e.target.value }))}
                  className="font-mono text-[11px] min-h-[32px] resize-y bg-background border-border"
                  rows={Math.max(1, (editedCommands[i] || "").split("\n").length)}
                />
              ) : (
                <div className="rounded-md bg-warning/5 px-2.5 py-2">
                  <code className="block font-mono text-[11px] text-muted-foreground whitespace-pre-wrap break-all">
                    {item.command}
                  </code>
                </div>
              )}
              {isLocalTool && item.detail && (
                <details className="text-[10px] text-muted-foreground/80">
                  <summary className="cursor-pointer select-none">
                    {item.type === "local_write"
                      ? t("ai.approvalLocalToolContentPreview")
                      : t("ai.approvalLocalToolEditPreview")}
                  </summary>
                  <pre className="mt-1.5 max-h-48 overflow-auto rounded bg-warning/5 px-2 py-1.5 font-mono whitespace-pre-wrap break-all">
                    {item.detail}
                  </pre>
                </details>
              )}
            </div>
          )
        )}
      </div>

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
              className="h-8 rounded-md px-4 text-xs border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
              onClick={() => respond("deny")}
            >
              {t("ai.approvalDeny")}
            </Button>
            <Button
              size="sm"
              className="h-8 rounded-md px-4 text-xs bg-warning hover:bg-warning/90 text-warning-foreground font-semibold"
              onClick={() => respond("allow")}
            >
              {t("ai.approvalApprove")}
            </Button>
          </>
        ) : (
          // single & local_tool: deny / remember-and-allow / allow（仅本次）
          <>
            <Button
              size="sm"
              variant="outline"
              className="h-8 rounded-md px-4 text-xs border-warning/30 text-warning hover:bg-warning/10 hover:text-warning"
              onClick={() => respond("deny")}
            >
              {t("ai.approvalDeny")}
            </Button>
            {rememberMode ? (
              <Button
                size="sm"
                className="h-8 rounded-md px-4 text-xs bg-warning/20 text-warning hover:bg-warning/30"
                onClick={() => respond("allowAll")}
              >
                {t("ai.approvalRememberAndAllow")}
              </Button>
            ) : (
              <Button
                size="sm"
                className="h-8 rounded-md px-4 text-xs bg-warning/20 text-warning hover:bg-warning/30"
                onClick={() => {
                  setRememberMode(true);
                }}
              >
                {t("opsctlApproval.remember")}
              </Button>
            )}
            <Button
              size="sm"
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

function TypeBadge({ type, compact }: { type: string; compact?: boolean }) {
  const icons: Record<string, typeof Terminal> = {
    exec: Terminal,
    serial: Usb,
    sql: Database,
    redis: Server,
    mongo: Database,
    kafka: Database,
    grant: Globe,
    local_bash: Terminal,
    local_write: FilePlus,
    local_edit: FileEdit,
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
        {item.asset_name}
      </span>
    );
  }
  if (item.group_id && item.group_id > 0) {
    return (
      <span className={cls}>
        <FolderOpen className="h-[11px] w-[11px]" />
        {item.group_name}
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
