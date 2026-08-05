import { useTranslation } from "react-i18next";
import { Button, cn } from "@opskat/ui";
import { Loader2, PlugZap, Plus, RefreshCw } from "lucide-react";
import type { ssh_agent_svc } from "../../../wailsjs/go/models";
import {
  agentStatusInfo,
  candidateDefaultName,
  endpointKindLabel,
  type AgentEndpointType,
  type AgentSourceRuntimeStatus,
} from "./agentSource";

export interface DetectedCandidate {
  candidate: ssh_agent_svc.Candidate;
  status: AgentSourceRuntimeStatus;
  identityCount?: number;
}

/**
 * 检测到的 SSH Agent 候选区：发现候选并展示探测状态。
 * 候选不会自动保存；"添加" 打开同一个预填对话框后才持久化来源。
 */
export function DetectedSourcesSection({
  candidates,
  loading,
  onRefresh,
  onAdd,
}: {
  candidates: DetectedCandidate[];
  loading: boolean;
  onRefresh: () => void;
  onAdd: (candidate: ssh_agent_svc.Candidate) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="rounded-lg border border-dashed bg-muted/10">
      <div className="flex items-center px-3 py-2.5">
        <div className="min-w-0">
          <div className="text-xs font-medium">{t("agentSource.detected")}</div>
          <div className="text-[11px] text-muted-foreground">{t("agentSource.detectedHint")}</div>
        </div>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto h-7 shrink-0 gap-1.5 text-primary"
          onClick={onRefresh}
          aria-label={t("agentSource.redetect")}
        >
          {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          {t("agentSource.redetect")}
        </Button>
      </div>
      <div className="border-t px-3 py-1">
        {loading && candidates.length === 0 ? (
          <div className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            {t("agentSource.detecting")}
          </div>
        ) : candidates.length === 0 ? (
          <div className="py-2 text-xs text-muted-foreground">{t("agentSource.noCandidates")}</div>
        ) : (
          candidates.map((c) => (
            <CandidateRow
              key={`${c.candidate.endpoint_type}\u0000${c.candidate.endpoint}`}
              candidate={c}
              onAdd={onAdd}
            />
          ))
        )}
      </div>
    </div>
  );
}

function CandidateRow({
  candidate,
  onAdd,
}: {
  candidate: DetectedCandidate;
  onAdd: (candidate: ssh_agent_svc.Candidate) => void;
}) {
  const { t } = useTranslation();
  const info = agentStatusInfo(candidate.status, t);
  const StatusIcon = info.Icon;
  const detail =
    candidate.status === "ok"
      ? `${endpointKindLabel(candidate.candidate.endpoint_type, t)} · ${t("agentSource.statusOkWithCount", {
          count: candidate.identityCount ?? 0,
        })}`
      : `${endpointKindLabel(candidate.candidate.endpoint_type, t)} · ${info.label}`;
  return (
    <div className="flex items-center gap-3 border-b py-2 last:border-0">
      <PlugZap
        className={cn("h-3.5 w-3.5 shrink-0", candidate.status === "ok" ? "text-success" : "text-muted-foreground")}
      />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-medium">
          {candidateDefaultName(candidate.candidate.endpoint_type as AgentEndpointType, candidate.candidate.endpoint)}
        </div>
        <div className="truncate text-[11px] text-muted-foreground">
          <span className={cn("inline-flex items-center gap-1", info.tone)}>
            <StatusIcon className={cn("h-3 w-3", info.spin && "animate-spin")} />
            {detail}
          </span>
        </div>
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="h-7 shrink-0 gap-1 text-primary"
        onClick={() => onAdd(candidate.candidate)}
      >
        <Plus className="h-3.5 w-3.5" />
        {t("agentSource.addCandidate")}
      </Button>
    </div>
  );
}
