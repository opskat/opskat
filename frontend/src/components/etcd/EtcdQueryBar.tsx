import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Input, Button } from "@opskat/ui";
import { Loader2, Play } from "lucide-react";
import { useEtcdStore } from "@/stores/etcdStore";
import type { etcd_svc } from "../../../wailsjs/go/models";

export interface EtcdQueryBarProps {
  assetId: number;
  /** 执行成功后回调，让上层（EtcdPanel）可以切到结果视图等 */
  onResult?: () => void;
  /**
   * 调用方注入的破坏性命令确认回调。返回 false = 用户取消，
   * 当前命令立即放弃执行，不进入 store / 不进 history。
   */
  onDestructive?: (command: string) => Promise<boolean>;
}

const TEMPLATES = [
  "get / --prefix --limit=50",
  "get /config",
  "put /flags/example true",
  "del /tmp/key",
  "member list",
  "endpoint status",
];

// 解析一行命令字符串成后端 ExecRequest。
// 后端 etcd_svc.ParseCommand 是权威实现；这里是 v1 的本地最简版，足够 query bar 用。
function parseCommand(line: string, assetId: number): etcd_svc.ExecRequest {
  const parts = line.split(/\s+/).filter(Boolean);
  const opPart = (parts[0] ?? "").toLowerCase();

  const req = {
    AssetID: assetId,
    Op: opPart,
    Key: "",
    Value: "",
    Prefix: false,
    Limit: 0,
    Revision: 0,
    LeaseID: 0,
    Args: {} as Record<string, unknown>,
    ApprovalID: "",
    Source: "query",
  } as unknown as etcd_svc.ExecRequest;

  const positional: string[] = [];
  for (const tok of parts.slice(1)) {
    if (tok.startsWith("--")) {
      const flag = tok.slice(2);
      const eq = flag.indexOf("=");
      const name = eq >= 0 ? flag.slice(0, eq) : flag;
      const val = eq >= 0 ? flag.slice(eq + 1) : "";
      if (name === "prefix") req.Prefix = true;
      else if (name === "limit") req.Limit = Number(val);
      else if (name === "revision") req.Revision = Number(val);
    } else {
      positional.push(tok);
    }
  }

  // 复合命令归一：member list → op=member_list；endpoint status → endpoint_status
  if (["member", "endpoint", "lease", "user", "role"].includes(opPart) && positional.length > 0) {
    req.Op = `${opPart}_${positional[0].toLowerCase()}`;
    positional.shift();
  }

  if (positional.length > 0) req.Key = positional[0];
  if (positional.length > 1) req.Value = positional.slice(1).join(" ");

  return req;
}

function isDestructive(line: string): boolean {
  const op = line.trim().split(/\s+/)[0]?.toLowerCase();
  return op === "put" || op === "del" || op === "txn";
}

export function EtcdQueryBar({ assetId, onResult, onDestructive }: EtcdQueryBarProps) {
  const { t } = useTranslation();
  const exec = useEtcdStore((s) => s.exec);
  const history = useEtcdStore((s) => s.queryHistory);
  const [cmd, setCmd] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");

  async function run() {
    const trimmed = cmd.trim();
    if (!trimmed || running) return;
    setError("");

    if (isDestructive(trimmed) && onDestructive) {
      const ok = await onDestructive(trimmed);
      if (!ok) return;
    }

    setRunning(true);
    try {
      const req = parseCommand(trimmed, assetId);
      await exec(req);
      onResult?.();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(msg);
      toast.error(`${t("etcd.query.execFailed")}: ${msg}`);
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="flex flex-col gap-1 border-b p-2">
      <div className="flex gap-2">
        <Input
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          placeholder={t("etcd.query.placeholder")}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void run();
            }
          }}
          className="h-8 flex-1 font-mono text-xs"
          data-testid="etcd-query-input"
        />
        <Button onClick={() => void run()} disabled={running} size="sm" data-testid="etcd-query-execute">
          {running ? <Loader2 className="size-3 animate-spin" /> : <Play className="size-3" />}
          {t("etcd.query.execute")}
        </Button>
      </div>
      <div className="flex flex-wrap gap-1">
        {TEMPLATES.map((tmpl) => (
          <button
            key={tmpl}
            type="button"
            className="rounded border bg-background px-1.5 py-0.5 text-[10px] text-muted-foreground hover:bg-accent"
            onClick={() => setCmd(tmpl)}
          >
            {tmpl}
          </button>
        ))}
      </div>
      {history.length > 0 && (
        <div className="flex flex-wrap items-center gap-1">
          <span className="text-[10px] text-muted-foreground">{t("etcd.query.history")}:</span>
          {history.slice(0, 6).map((h) => (
            <button
              key={h}
              type="button"
              className="rounded border bg-background px-1.5 py-0.5 text-[10px] text-muted-foreground hover:bg-accent"
              onClick={() => setCmd(h)}
            >
              {h}
            </button>
          ))}
        </div>
      )}
      {error && <div className="text-[11px] text-destructive">{error}</div>}
    </div>
  );
}
