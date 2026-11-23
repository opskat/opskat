import { useTranslation } from "react-i18next";
import { AsyncState } from "@/components/common/AsyncState";
import { GetSSHPoolConnections } from "../../../wailsjs/go/ssh/SSH";
import { sshpool } from "../../../wailsjs/go/models";
import { formatAuditTimeFull } from "./auditFilters";
import { useLatestFetch } from "./useAuditData";

const EMPTY_POOL: sshpool.PoolEntryInfo[] = [];

/** 连接池页；refreshKey 变化时重新拉取（顶栏刷新按钮） */
export function AuditPoolView({ refreshKey }: { refreshKey: number }) {
  const { t } = useTranslation();
  const pool = useLatestFetch(`pool-${refreshKey}`, () => GetSSHPoolConnections(), EMPTY_POOL);
  const entries = pool.data;

  return (
    <div className="flex-1 overflow-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-background border-b">
          <tr className="text-left text-muted-foreground">
            <th className="px-4 py-2 font-medium">{t("audit.poolAsset")}</th>
            <th className="px-4 py-2 font-medium">{t("audit.poolRefCount")}</th>
            <th className="px-4 py-2 font-medium">{t("audit.poolLastUsed")}</th>
          </tr>
        </thead>
        <tbody>
          {(entries.length === 0 || pool.status === "error") && (
            <tr>
              <td colSpan={3}>
                <AsyncState
                  status={pool.status}
                  error={pool.error}
                  onRetry={pool.reload}
                  className="py-12"
                  empty={t("audit.poolEmpty")}
                />
              </td>
            </tr>
          )}
          {pool.status !== "error" &&
            entries.map((entry) => (
              <tr key={entry.asset_id} className="border-b hover:bg-muted/50 transition-colors">
                <td className="px-4 py-2 font-mono">{entry.asset_id}</td>
                <td className="px-4 py-2">
                  <span
                    className={`inline-block px-1.5 py-0.5 text-xs rounded font-mono ${
                      entry.ref_count > 0 ? "bg-success/15 text-success" : "bg-muted"
                    }`}
                  >
                    {entry.ref_count}
                  </span>
                </td>
                <td className="px-4 py-2 text-xs text-muted-foreground">{formatAuditTimeFull(entry.last_used)}</td>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
}
