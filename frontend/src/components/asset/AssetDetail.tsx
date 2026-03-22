import { useTranslation } from "react-i18next";
import { Server, Pencil, Trash2, TerminalSquare, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { useAssetStore } from "@/stores/assetStore";
import { asset_entity } from "../../../wailsjs/go/models";

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
  password?: string;
  key_id?: number;
  key_source?: string;
  private_keys?: string[];
  jump_host_id?: number;
  forwarded_ports?: {
    type: string;
    local_host: string;
    local_port: number;
    remote_host: string;
    remote_port: number;
  }[];
  proxy?: {
    type: string;
    host: string;
    port: number;
    username?: string;
    password?: string;
  } | null;
}

interface AssetDetailProps {
  asset: asset_entity.Asset;
  isConnecting?: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onConnect: () => void;
}

export function AssetDetail({ asset, isConnecting, onEdit, onDelete, onConnect }: AssetDetailProps) {
  const { t } = useTranslation();
  const { assets } = useAssetStore();

  let sshConfig: SSHConfig | null = null;
  try {
    sshConfig = JSON.parse(asset.Config || "{}");
  } catch {
    /* ignore */
  }

  const jumpHostName = sshConfig?.jump_host_id
    ? assets.find((a) => a.ID === sshConfig!.jump_host_id)?.Name || `ID:${sshConfig.jump_host_id}`
    : null;

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-3 border-b">
        <div className="flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary/10">
            <Server className="h-4 w-4 text-primary" />
          </div>
          <div>
            <h2 className="font-semibold leading-tight">{asset.Name}</h2>
            <span className="text-xs text-muted-foreground uppercase">
              {asset.Type}
            </span>
          </div>
        </div>
        <div className="flex gap-1.5">
          {asset.Type === "ssh" && (
            <Button size="sm" className="h-8 gap-1.5" onClick={onConnect} disabled={isConnecting}>
              {isConnecting ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <TerminalSquare className="h-3.5 w-3.5" />
              )}
              {t("ssh.connect")}
            </Button>
          )}
          <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onEdit}>
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8 text-destructive hover:text-destructive"
            onClick={onDelete}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
      <div className="flex-1 p-4 space-y-4 overflow-y-auto">
        {sshConfig && (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
              SSH Connection
            </h3>
            <div className="grid grid-cols-2 gap-4 text-sm">
              <InfoItem label={t("asset.host")} value={sshConfig.host} mono />
              <InfoItem label={t("asset.port")} value={String(sshConfig.port)} mono />
              <InfoItem label={t("asset.username")} value={sshConfig.username} mono />
              <InfoItem
                label={t("asset.authType")}
                value={
                  sshConfig.auth_type === "password"
                    ? t("asset.authPassword") + (sshConfig.password ? " ●" : "")
                    : sshConfig.auth_type === "key"
                    ? t("asset.authKey") + (sshConfig.key_source === "managed" ? ` (${t("asset.keySourceManaged")})` : sshConfig.key_source === "file" ? ` (${t("asset.keySourceFile")})` : "")
                    : sshConfig.auth_type
                }
              />
            </div>
          </div>
        )}

        {/* Private Keys */}
        {sshConfig?.private_keys && sshConfig.private_keys.length > 0 && (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
              {t("asset.privateKeys")}
            </h3>
            <div className="space-y-1">
              {sshConfig.private_keys.map((key, i) => (
                <p key={i} className="text-sm font-mono text-muted-foreground">{key}</p>
              ))}
            </div>
          </div>
        )}

        {/* Jump Host */}
        {jumpHostName && (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
              {t("asset.jumpHost")}
            </h3>
            <p className="text-sm font-mono">{jumpHostName}</p>
          </div>
        )}

        {/* Proxy */}
        {sshConfig?.proxy && (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
              {t("asset.proxy")}
            </h3>
            <div className="grid grid-cols-2 gap-4 text-sm">
              <InfoItem label={t("asset.proxyType")} value={sshConfig.proxy.type.toUpperCase()} />
              <InfoItem
                label={t("asset.proxyHost")}
                value={`${sshConfig.proxy.host}:${sshConfig.proxy.port}`}
                mono
              />
              {sshConfig.proxy.username && (
                <InfoItem label={t("asset.proxyUsername")} value={sshConfig.proxy.username} />
              )}
            </div>
          </div>
        )}

        {/* Forwarded Ports */}
        {sshConfig?.forwarded_ports && sshConfig.forwarded_ports.length > 0 && (
          <div className="rounded-xl border bg-card p-4">
            <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-3">
              {t("asset.forwardedPorts")}
            </h3>
            <div className="space-y-2">
              {sshConfig.forwarded_ports.map((fp, i) => (
                <div key={i} className="flex items-center gap-2 text-sm font-mono">
                  <span className="text-xs uppercase text-muted-foreground w-14">{fp.type}</span>
                  <span>{fp.local_host}:{fp.local_port}</span>
                  <span className="text-muted-foreground">→</span>
                  <span>{fp.remote_host}:{fp.remote_port}</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {asset.Description && (
          <>
            <Separator />
            <div className="text-sm">
              <span className="text-muted-foreground">
                {t("asset.description")}
              </span>
              <p className="mt-1">{asset.Description}</p>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function InfoItem({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <span className="text-xs text-muted-foreground">{label}</span>
      <p className={cn("mt-0.5 text-sm", mono && "font-mono")}>{value}</p>
    </div>
  );
}
