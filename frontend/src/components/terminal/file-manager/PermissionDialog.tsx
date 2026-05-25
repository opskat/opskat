import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { File, Folder, Shield } from "lucide-react";
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  cn,
} from "@opskat/ui";
import { SFTPApplyPermissions, SFTPProperties } from "../../../../wailsjs/go/ssh/SSH";
import { sftp_svc } from "../../../../wailsjs/go/models";
import { formatBytes } from "./utils";
import { type PermissionTarget } from "./types";

interface PermissionDialogProps {
  sessionId: string;
  target: PermissionTarget | null;
  onClose: () => void;
  onSaved: () => void;
}

type PermKey = "or" | "ow" | "ox" | "gr" | "gw" | "gx" | "tr" | "tw" | "tx";

const bitValues: Record<PermKey, number> = {
  or: 400,
  ow: 200,
  ox: 100,
  gr: 40,
  gw: 20,
  gx: 10,
  tr: 4,
  tw: 2,
  tx: 1,
};

function normalizeMode(mode: string) {
  const digits = mode.replace(/[^0-7]/g, "").slice(-3);
  return digits.padStart(3, "0");
}

function modeToBits(mode: string): Record<PermKey, boolean> {
  const n = parseInt(normalizeMode(mode), 8) || 0;
  return Object.fromEntries(Object.entries(bitValues).map(([key, value]) => [key, (n & value) !== 0])) as Record<
    PermKey,
    boolean
  >;
}

function bitsToMode(bits: Record<PermKey, boolean>) {
  let total = 0;
  for (const [key, value] of Object.entries(bitValues) as Array<[PermKey, number]>) {
    if (bits[key]) total += value;
  }
  return total.toString().padStart(4, "0");
}

export function PermissionDialog({ sessionId, target, onClose, onSaved }: PermissionDialogProps) {
  const { t } = useTranslation();
  const [props, setProps] = useState<sftp_svc.FileProperties | null>(null);
  const [bits, setBits] = useState<Record<PermKey, boolean>>(modeToBits("644"));
  const [mode, setMode] = useState("644");
  const [owner, setOwner] = useState("");
  const [group, setGroup] = useState("");
  const [recursive, setRecursive] = useState(false);
  const [recursiveTarget, setRecursiveTarget] = useState("all");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!target) return;
    setError(null);
    SFTPProperties(sessionId, target.path)
      .then((next) => {
        setProps(next);
        const nextMode = normalizeMode(next.mode || "644");
        setMode(nextMode);
        setBits(modeToBits(nextMode));
        setOwner(next.uid ? String(next.uid) : "");
        setGroup(next.gid ? String(next.gid) : "");
      })
      .catch((e) => setError(String(e)));
  }, [sessionId, target]);

  const rows = useMemo(
    () =>
      [
        [t("sftp.permission.owner"), "or", "ow", "ox"],
        [t("sftp.permission.group"), "gr", "gw", "gx"],
        [t("sftp.permission.others"), "tr", "tw", "tx"],
      ] as Array<[string, PermKey, PermKey, PermKey]>,
    [t]
  );

  const updateBit = (key: PermKey, checked: boolean) => {
    const next = { ...bits, [key]: checked };
    setBits(next);
    setMode(bitsToMode(next));
  };

  const applyMode = (nextMode: string) => {
    const normalized = normalizeMode(nextMode);
    setMode(normalized);
    setBits(modeToBits(normalized));
  };

  const save = async () => {
    if (!target) return;
    setSaving(true);
    setError(null);
    try {
      await SFTPApplyPermissions(sessionId, {
        path: target.path,
        mode,
        owner: owner.trim(),
        group: group.trim(),
        recursive,
        recursiveTarget,
      });
      onSaved();
      onClose();
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={!!target} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl gap-3">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            <Shield className="h-4 w-4" /> {t("sftp.permission.title")}
          </DialogTitle>
        </DialogHeader>
        {target && (
          <div className="space-y-4 text-sm">
            <div className="flex items-center gap-2 rounded-md bg-muted/40 p-2">
              {target.entry.isDir ? <Folder className="h-4 w-4 text-primary" /> : <File className="h-4 w-4" />}
              <div className="min-w-0">
                <div className="truncate font-medium">{target.entry.name}</div>
                <div className="truncate text-xs text-muted-foreground">{target.path}</div>
              </div>
              {props && <div className="ml-auto text-xs text-muted-foreground">{formatBytes(props.size)}</div>}
            </div>

            <div className="rounded-md border p-3">
              <div className="mb-2 text-xs font-medium text-muted-foreground">{t("sftp.permission.matrix")}</div>
              <div className="grid grid-cols-4 gap-2 text-xs">
                <div />
                <div>{t("sftp.permission.read")}</div>
                <div>{t("sftp.permission.write")}</div>
                <div>{t("sftp.permission.execute")}</div>
                {rows.map(([label, r, w, x]) => (
                  <div className="contents" key={label}>
                    <div className="font-medium">{label}</div>
                    {[r, w, x].map((key) => (
                      <label key={key} className="flex items-center gap-1">
                        <Checkbox checked={bits[key]} onCheckedChange={(checked) => updateBit(key, checked === true)} />
                        {key.endsWith("r") ? "r=4" : key.endsWith("w") ? "w=2" : "x=1"}
                      </label>
                    ))}
                  </div>
                ))}
              </div>
            </div>

            <div className="rounded-md border p-3 space-y-3">
              <div className="grid grid-cols-[7rem_1fr] items-center gap-2">
                <Label>{t("sftp.permission.octalMode")}</Label>
                <Input value={mode} onChange={(e) => applyMode(e.target.value)} className="h-8 font-mono" />
              </div>
              <div className="flex flex-wrap gap-1">
                {[
                  [t("sftp.permission.presetWebStatic"), "644"],
                  [t("sftp.permission.presetDirectory"), "755"],
                  [t("sftp.permission.presetPrivateKey"), "600"],
                  [t("sftp.permission.presetExecutable"), "755"],
                ].map(([label, value]) => (
                  <Button key={label} type="button" variant="outline" size="xs" onClick={() => applyMode(value)}>
                    {label} → {value}
                  </Button>
                ))}
              </div>
            </div>

            <div className="rounded-md border p-3 space-y-2">
              <div className="text-xs font-medium text-muted-foreground">{t("sftp.permission.chown")}</div>
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <Label className="text-xs">{t("sftp.permission.ownerUser")}</Label>
                  <Input
                    value={owner}
                    onChange={(e) => setOwner(e.target.value)}
                    placeholder={t("sftp.permission.ownerUserPlaceholder")}
                    className="h-8"
                  />
                </div>
                <div>
                  <Label className="text-xs">{t("sftp.permission.ownerGroup")}</Label>
                  <Input
                    value={group}
                    onChange={(e) => setGroup(e.target.value)}
                    placeholder={t("sftp.permission.ownerGroupPlaceholder")}
                    className="h-8"
                  />
                </div>
              </div>
            </div>

            {target.entry.isDir && (
              <div className="rounded-md border p-3 space-y-2">
                <label className="flex items-center gap-2 text-xs font-medium">
                  <Checkbox checked={recursive} onCheckedChange={(checked) => setRecursive(checked === true)} />
                  {t("sftp.permission.recursive")}
                </label>
                <div className={cn("grid gap-1 pl-6 text-xs", !recursive && "opacity-40 pointer-events-none")}>
                  {[
                    ["all", t("sftp.permission.recursiveAll")],
                    ["files", t("sftp.permission.recursiveFiles")],
                    ["dirs", t("sftp.permission.recursiveDirs")],
                  ].map(([value, label]) => (
                    <label key={value} className="flex items-center gap-2">
                      <input
                        type="radio"
                        checked={recursiveTarget === value}
                        onChange={() => setRecursiveTarget(value)}
                      />
                      {label}
                    </label>
                  ))}
                </div>
              </div>
            )}

            {error && <div className="rounded bg-destructive/10 p-2 text-xs text-destructive">{error}</div>}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            {t("action.cancel")}
          </Button>
          <Button onClick={save} disabled={saving || !target}>
            {t("action.ok")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
