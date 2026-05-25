import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { File, Folder, Info } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@opskat/ui";
import { SFTPProperties } from "../../../../wailsjs/go/ssh/SSH";
import { sftp_svc } from "../../../../wailsjs/go/models";
import { formatBytes, formatDate } from "./utils";
import { type PermissionTarget } from "./types";

interface PropertiesDialogProps {
  sessionId: string;
  target: PermissionTarget | null;
  onClose: () => void;
}

function PropertiesBody({ sessionId, target }: { sessionId: string; target: PermissionTarget }) {
  const { t } = useTranslation();
  const [props, setProps] = useState<sftp_svc.FileProperties | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    SFTPProperties(sessionId, target.path)
      .then((next) => {
        if (!cancelled) setProps(next);
      })
      .catch((e) => {
        if (!cancelled) setError(String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [sessionId, target.path]);

  const row = (label: string, value?: string | number | null) => (
    <div className="grid grid-cols-[7rem_1fr] gap-2 py-1 text-sm">
      <div className="text-muted-foreground">{label}</div>
      <div className="break-all font-mono text-xs">{value ?? "-"}</div>
    </div>
  );

  const approx = (truncated: boolean | undefined) => (truncated ? ` ${t("sftp.properties.approximate")}` : "");

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 rounded-md bg-muted/40 p-2">
        {target.entry.isDir ? <Folder className="h-4 w-4 text-primary" /> : <File className="h-4 w-4" />}
        <div className="min-w-0 truncate font-medium">{target.entry.name}</div>
      </div>
      {error && <div className="rounded bg-destructive/10 p-2 text-xs text-destructive">{error}</div>}
      {!error && !props && (
        <div className="py-4 text-center text-sm text-muted-foreground">{t("sftp.properties.loading")}</div>
      )}
      {props && (
        <div className="rounded-md border p-3">
          {row(t("sftp.properties.path"), props.path)}
          {row(
            t("sftp.properties.type"),
            props.isDir ? t("sftp.properties.typeFolder") : t("sftp.properties.typeFile")
          )}
          {row(
            t("sftp.properties.size"),
            `${t("sftp.properties.sizeBytes", { display: formatBytes(props.size), bytes: props.size })}${approx(props.truncated)}`
          )}
          {props.isDir && row(t("sftp.properties.children"), `${props.childCount ?? 0}${approx(props.truncated)}`)}
          {row(t("sftp.properties.mode"), props.mode)}
          {row(t("sftp.properties.owner"), props.uid)}
          {row(t("sftp.properties.group"), props.gid)}
          {row(t("sftp.properties.modified"), formatDate(props.modTime))}
        </div>
      )}
    </div>
  );
}

export function PropertiesDialog({ sessionId, target, onClose }: PropertiesDialogProps) {
  const { t } = useTranslation();

  return (
    <Dialog open={!!target} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Info className="h-4 w-4" /> {t("sftp.properties.title")}
          </DialogTitle>
        </DialogHeader>
        {target && <PropertiesBody key={target.path} sessionId={sessionId} target={target} />}
      </DialogContent>
    </Dialog>
  );
}
