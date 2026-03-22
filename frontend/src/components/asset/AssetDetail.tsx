import { useTranslation } from "react-i18next";
import { Server, Pencil, Trash2, TerminalSquare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { asset_entity } from "../../../wailsjs/go/models";

interface SSHConfig {
  host: string;
  port: number;
  username: string;
  auth_type: string;
}

interface AssetDetailProps {
  asset: asset_entity.Asset;
  onEdit: () => void;
  onDelete: () => void;
  onConnect: () => void;
}

export function AssetDetail({ asset, onEdit, onDelete, onConnect }: AssetDetailProps) {
  const { t } = useTranslation();

  let sshConfig: SSHConfig | null = null;
  try {
    sshConfig = JSON.parse(asset.Config || "{}");
  } catch {
    /* ignore */
  }

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
            <Button size="sm" className="h-8 gap-1.5" onClick={onConnect}>
              <TerminalSquare className="h-3.5 w-3.5" />
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
      <div className="flex-1 p-4 space-y-4">
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
                    ? t("asset.authPassword")
                    : t("asset.authKey")
                }
              />
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
