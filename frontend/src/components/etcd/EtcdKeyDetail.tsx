import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useEtcdStore } from "@/stores/etcdStore";
import type { etcd_svc } from "../../../wailsjs/go/models";

export interface EtcdKeyDetailProps {
  assetId: number;
  selectedKey: string | null;
}

function buildGetRequest(assetId: number, key: string): etcd_svc.ExecRequest {
  return {
    AssetID: assetId,
    Op: "get",
    Key: key,
    Value: "",
    Prefix: false,
    Limit: 0,
    Revision: 0,
    LeaseID: 0,
    Args: {} as Record<string, unknown>,
    ApprovalID: "",
    Source: "query",
  } as unknown as etcd_svc.ExecRequest;
}

export function EtcdKeyDetail({ assetId, selectedKey }: EtcdKeyDetailProps) {
  const { t } = useTranslation();
  const exec = useEtcdStore((s) => s.exec);
  const [loading, setLoading] = useState(false);
  const [detail, setDetail] = useState<etcd_svc.EtcdKV | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    if (!selectedKey) return;
    let cancelled = false;
    setLoading(true);
    setErr("");
    setDetail(null);
    exec(buildGetRequest(assetId, selectedKey))
      .then((res) => {
        if (cancelled) return;
        setDetail(res.kvs?.[0] ?? null);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setErr(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [assetId, selectedKey, exec]);

  if (!selectedKey) {
    return <div className="p-3 text-xs text-muted-foreground">{t("etcd.query.placeholder")}</div>;
  }
  if (loading) return <div className="p-3 text-xs text-muted-foreground">…</div>;
  if (err) return <div className="p-3 text-xs text-destructive">{err}</div>;
  if (!detail) return <div className="p-3 text-xs text-muted-foreground">∅</div>;

  return (
    <div className="flex h-full flex-col gap-2 p-3 text-xs" data-testid="etcd-key-detail">
      <div className="break-all font-mono text-sm font-semibold">{detail.key}</div>
      <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
        <div>modRev: {detail.modRevision}</div>
        <div>createRev: {detail.createRevision}</div>
        <div>version: {detail.version}</div>
        <div>lease: {detail.lease || "—"}</div>
      </div>
      <pre className="flex-1 overflow-auto whitespace-pre-wrap break-all rounded border bg-muted/30 p-2 font-mono">
        {detail.value}
      </pre>
    </div>
  );
}
