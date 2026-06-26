import { useTranslation } from "react-i18next";
import { CircleCheck, CircleX } from "lucide-react";
import { useEtcdStore } from "@/stores/etcdStore";

export function EtcdResultTable() {
  const { t } = useTranslation();
  const result = useEtcdStore((s) => s.lastResult);
  const meta = useEtcdStore((s) => s.lastMeta);

  if (!meta) {
    return <div className="p-3 text-xs text-muted-foreground">{t("etcd.query.noResult")}</div>;
  }

  const kvs = result?.kvs ?? [];

  return (
    <div className="flex h-full flex-col" data-testid="etcd-result-table">
      {/* 状态栏 */}
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-1.5 text-[11px]">
        {meta.ok ? (
          <>
            <CircleCheck className="size-3 text-success" />
            <span className="font-medium text-success">{t("etcd.query.execSuccess")}</span>
            <span className="text-muted-foreground">·</span>
            <span>{meta.count} keys</span>
            <span className="text-muted-foreground">·</span>
            <span className="text-muted-foreground">{t("etcd.query.elapsed", { ms: meta.elapsedMs })}</span>
            <span className="text-muted-foreground">·</span>
            <span className="font-mono text-muted-foreground">op={meta.op}</span>
            {result?.revision ? (
              <>
                <span className="text-muted-foreground">·</span>
                <span className="font-mono text-muted-foreground">rev={result.revision}</span>
              </>
            ) : null}
          </>
        ) : (
          <>
            <CircleX className="size-3 text-destructive" />
            <span className="font-medium text-destructive">{t("etcd.query.execFailed")}</span>
            <span className="text-muted-foreground">·</span>
            <span className="text-muted-foreground">{t("etcd.query.elapsed", { ms: meta.elapsedMs })}</span>
            <span className="text-muted-foreground">·</span>
            <span className="break-all text-destructive">{meta.error}</span>
          </>
        )}
      </div>

      {/* 结果表 */}
      <div className="flex-1 overflow-auto">
        {kvs.length === 0 ? (
          <div className="p-3 text-xs text-muted-foreground">{meta.ok ? "∅" : ""}</div>
        ) : (
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
        )}
      </div>
    </div>
  );
}
