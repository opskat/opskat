import { useTranslation } from "react-i18next";
import { useEtcdStore } from "@/stores/etcdStore";

export function EtcdResultTable() {
  const { t } = useTranslation();
  const result = useEtcdStore((s) => s.lastResult);

  if (!result) {
    return <div className="p-3 text-xs text-muted-foreground">{t("etcd.query.placeholder")}</div>;
  }

  const kvs = result.kvs ?? [];

  return (
    <div className="flex h-full flex-col" data-testid="etcd-result-table">
      <div className="border-b px-3 py-1.5 text-[11px] text-muted-foreground">
        op={result.op} · count={result.count} · revision={result.revision}
      </div>
      <div className="flex-1 overflow-auto">
        <table className="w-full border-collapse text-[11px]">
          <thead className="sticky top-0 bg-muted/40">
            <tr>
              <th className="border-b px-2 py-1 text-left">KEY</th>
              <th className="border-b px-2 py-1 text-left">VALUE</th>
              <th className="border-b px-2 py-1 text-right">MOD REV</th>
              <th className="border-b px-2 py-1 text-right">VERSION</th>
              <th className="border-b px-2 py-1 text-right">LEASE</th>
            </tr>
          </thead>
          <tbody>
            {kvs.map((kv, i) => (
              <tr key={`${kv.key}-${i}`} className="hover:bg-accent/40">
                <td className="px-2 py-1 font-mono">{kv.key}</td>
                <td className="break-all px-2 py-1 font-mono">{kv.value}</td>
                <td className="px-2 py-1 text-right">{kv.modRevision}</td>
                <td className="px-2 py-1 text-right">{kv.version}</td>
                <td className="px-2 py-1 text-right">{kv.lease || ""}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
