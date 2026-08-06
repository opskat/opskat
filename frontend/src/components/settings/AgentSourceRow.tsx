import { useTranslation } from "react-i18next";
import { Button, Tooltip, TooltipContent, TooltipTrigger, cn } from "@opskat/ui";
import { Cable, ChevronDown, ChevronUp, Copy, KeyRound, Loader2, Pencil, RefreshCw, Trash2 } from "lucide-react";
import type { system, ssh_agent_svc } from "../../../wailsjs/go/models";
import { agentStatusInfo, endpointKindLabel, type AgentSourceRuntimeStatus } from "./agentSource";

export interface AgentSourceRowProps {
  source: system.AgentSourceSummary;
  status: AgentSourceRuntimeStatus;
  /** ok 状态下已知的身份数（探测或检查结果），用于显示"可用 · N 把密钥"。 */
  identityCount?: number;
  expanded: boolean;
  identities: ssh_agent_svc.IdentitySummary[] | null;
  identitiesLoading: boolean;
  identitiesError?: string | null;
  onToggle: () => void;
  onRefresh: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onCopyPublicKey: (fingerprint: string) => void;
}

/**
 * 凭据列表中的 Agent 来源行：名称 / 来源类型 / 运行时状态，展开时延迟加载身份。
 * 运行时状态五态明确区分：加载中 / 可用且包含身份 / 可用但为空 / 不可用 / 当前平台不支持。
 * 不可用或不支持的来源仍可编辑、删除（引用约束在删除确认时校验）。
 */
export function AgentSourceRow({
  source,
  status,
  identityCount,
  expanded,
  identities,
  identitiesLoading,
  identitiesError,
  onToggle,
  onRefresh,
  onEdit,
  onDelete,
  onCopyPublicKey,
}: AgentSourceRowProps) {
  const { t } = useTranslation();
  const expandable = status === "ok" || status === "empty";
  const info = agentStatusInfo(status, t);
  const StatusIcon = info.Icon;

  return (
    <div className="overflow-hidden rounded-lg border bg-card" data-testid={`agent-source-row-${source.id}`}>
      <div className="group flex items-center justify-between p-3">
        <div className="flex min-w-0 items-center gap-3">
          <Cable className="h-4 w-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">{source.name}</div>
            <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
              <span className="shrink-0 whitespace-nowrap">SSH Agent</span>
              <span className="shrink-0 whitespace-nowrap">{endpointKindLabel(source.endpoint_type, t)}</span>
              <span className={cn("inline-flex shrink-0 items-center gap-1 whitespace-nowrap", info.tone)}>
                <StatusIcon className={cn("h-3 w-3", info.spin && "animate-spin")} />
                {status === "ok" && identityCount !== undefined
                  ? t("agentSource.statusOkWithCount", { count: identityCount })
                  : info.label}
              </span>
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <div className="flex opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  onClick={onRefresh}
                  aria-label={t("agentSource.refresh")}
                >
                  <RefreshCw className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("agentSource.refresh")}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" className="h-7 w-7" onClick={onEdit} aria-label={t("action.edit")}>
                  <Pencil className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("action.edit")}</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 text-muted-foreground hover:text-destructive"
                  onClick={onDelete}
                  aria-label={t("action.delete")}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("action.delete")}</TooltipContent>
            </Tooltip>
          </div>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="h-7 w-7"
                onClick={onToggle}
                disabled={!expandable}
                aria-label={expanded ? t("agentSource.collapse") : t("agentSource.expand")}
                aria-expanded={expanded}
              >
                {expanded ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{expanded ? t("agentSource.collapse") : t("agentSource.expand")}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      {(status === "unavailable" || status === "unsupported") && (
        <div className="border-t bg-destructive/5 px-10 py-2 text-xs text-destructive">
          {t("agentSource.unavailableHint")}
        </div>
      )}
      {expanded && (
        <div className="border-t bg-muted/20 px-3 py-1">
          {identitiesLoading ? (
            <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" />
              {t("agentSource.loadingIdentities")}
            </div>
          ) : identitiesError ? (
            <div className="py-2 text-xs text-destructive">{identitiesError}</div>
          ) : !identities || identities.length === 0 ? (
            <div className="py-2 text-xs text-muted-foreground">{t("agentSource.emptyIdentities")}</div>
          ) : (
            identities.map((identity) => (
              <AgentIdentityRow key={identity.fingerprint} identity={identity} onCopy={onCopyPublicKey} />
            ))
          )}
        </div>
      )}
    </div>
  );
}

function AgentIdentityRow({
  identity,
  onCopy,
}: {
  identity: ssh_agent_svc.IdentitySummary;
  onCopy: (fingerprint: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="group/identity flex items-center gap-3 border-b border-border/60 py-2 last:border-0">
      <KeyRound className="ml-7 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-xs">
          <span className="truncate font-medium">{identity.comment || t("agentSource.noComment")}</span>
          <span className="shrink-0 text-muted-foreground">{identity.type}</span>
          {identity.usages > 0 && (
            <span className="shrink-0 text-primary">{t("agentSource.usage", { count: identity.usages })}</span>
          )}
        </div>
        <div className="truncate font-mono text-[10px] text-muted-foreground select-text" title={identity.fingerprint}>
          {identity.fingerprint}
        </div>
      </div>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 opacity-0 transition-opacity group-hover/identity:opacity-100 group-focus-within/identity:opacity-100 focus:opacity-100"
            onClick={() => onCopy(identity.fingerprint)}
            aria-label={t("agentSource.copyPublicKey")}
          >
            <Copy className="h-3.5 w-3.5" />
          </Button>
        </TooltipTrigger>
        <TooltipContent>{t("agentSource.copyPublicKey")}</TooltipContent>
      </Tooltip>
    </div>
  );
}
