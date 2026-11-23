import { useTranslation } from "react-i18next";
import { ShieldCheck } from "lucide-react";
import { AsyncState } from "@/components/common/AsyncState";
import { ListAuditSessionGrants } from "../../../wailsjs/go/system/System";
import { audit_svc } from "../../../wailsjs/go/models";
import { useLatestFetch } from "./useAuditData";

const NO_GRANTS: audit_svc.SessionGrant[] = [];

interface AuditSessionGrantsProps {
  sessionId: string;
  refreshKey: number;
}

/**
 * 选中会话时：该会话全部「始终允许」授权，与当前页与其它筛选无关；refreshKey 变化时重新拉取。
 * 按会话重新挂载：换会话时不沿用上一个会话的授权，刷新同一会话时才保留旧数据。
 */
export function AuditSessionGrants({ sessionId, refreshKey }: AuditSessionGrantsProps) {
  return <SessionGrants key={sessionId} sessionId={sessionId} refreshKey={refreshKey} />;
}

function SessionGrants({ sessionId, refreshKey }: AuditSessionGrantsProps) {
  const { t } = useTranslation();
  const grants = useLatestFetch(
    `grants-${sessionId}-${refreshKey}`,
    () => ListAuditSessionGrants(sessionId),
    NO_GRANTS
  );

  if (grants.status !== "ready") {
    return (
      <div className="px-4 border-b">
        <AsyncState status={grants.status} error={grants.error} onRetry={grants.reload} className="py-2" />
      </div>
    );
  }
  if (grants.data.length === 0) return null;
  return (
    <div className="px-4 py-2 border-b bg-info/10 text-xs" data-testid="audit-session-grants">
      <div className="flex items-center gap-1.5 font-medium text-info">
        <ShieldCheck className="h-3.5 w-3.5" />
        {t("audit.sessionPatterns")} · {grants.data.length}
      </div>
      <div className="flex flex-wrap gap-1.5 mt-1.5">
        {grants.data.map((g, i) => (
          <span key={i} className="inline-flex items-center gap-1 bg-info/15 text-info px-2 py-0.5 rounded font-mono">
            {g.asset_name && <span className="opacity-80">{g.asset_name}:</span>}
            {g.patterns}
          </span>
        ))}
      </div>
    </div>
  );
}
