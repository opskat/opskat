import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { Shield, Server, Network, Settings2, Keyboard, Palette, Loader2, Lock, Blocks } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Button,
  Label,
  Switch,
  Separator,
} from "@opskat/ui";
import { backup_svc } from "../../../wailsjs/go/models";
import { SecretInput } from "@/components/SecretInput";
import { useAssetStore } from "@/stores/assetStore";
import { useShortcutStore } from "@/stores/shortcutStore";
import { useTerminalThemeStore } from "@/stores/terminalThemeStore";
import { errorMessage } from "@/lib/errorMessage";

/** Where a backup comes from (local file, Gist, WebDAV): how to decrypt-preview it and how to import it. */
export interface BackupImportSource {
  title: string;
  /** Shown above the password field while the backup is still encrypted. */
  passwordPrompt: string;
  successMessage: string;
  encrypted: boolean;
  /** Summary already known without a password (unencrypted file). */
  initialSummary: backup_svc.BackupSummary | null;
  preview: (password: string) => Promise<backup_svc.BackupSummary>;
  execute: (password: string, opts: backup_svc.ImportOptions) => Promise<backup_svc.ImportResult>;
}

interface BackupImportDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Mount a fresh dialog (new `key`) per import so password, summary and toggles start from the defaults. */
  source: BackupImportSource;
}

async function applyImportResult(result: backup_svc.ImportResult) {
  if (result.shortcuts) {
    try {
      const parsed = JSON.parse(result.shortcuts);
      localStorage.setItem("keyboard_shortcuts", JSON.stringify(parsed));
      const store = useShortcutStore.getState();
      store.resetAll();
      for (const [action, binding] of Object.entries(parsed)) {
        store.updateShortcut(action as never, binding as never);
      }
    } catch {
      // ignore parse errors
    }
  }

  if (result.custom_themes) {
    try {
      const themes = JSON.parse(result.custom_themes);
      const store = useTerminalThemeStore.getState();
      for (const theme of themes) {
        // 已有同 id 的主题就原地更新:先删再加会把正在使用的主题切回默认。
        if (store.customThemes.some((existing) => existing.id === theme.id)) store.updateCustomTheme(theme);
        else store.addCustomTheme(theme);
      }
    } catch {
      // ignore parse errors
    }
  }
}

export function BackupImportDialog({ open, onOpenChange, source }: BackupImportDialogProps) {
  const { t } = useTranslation();
  const { refresh } = useAssetStore();

  const [password, setPassword] = useState("");
  const [decrypting, setDecrypting] = useState(false);
  const [decryptError, setDecryptError] = useState("");
  const [summary, setSummary] = useState<backup_svc.BackupSummary | null>(source.initialSummary);
  const [importing, setImporting] = useState(false);

  // module toggles
  const [importAssets, setImportAssets] = useState(true);
  const [importCredentials, setImportCredentials] = useState(false);
  const [importForwards, setImportForwards] = useState(true);
  const [importPolicyGroups, setImportPolicyGroups] = useState(false);
  const [importShortcuts, setImportShortcuts] = useState(false);
  const [importThemes, setImportThemes] = useState(false);
  const [mode, setMode] = useState<"merge" | "replace">("merge");

  const needsDecrypt = source.encrypted && !summary;

  const handleDecrypt = async () => {
    setDecrypting(true);
    setDecryptError("");
    try {
      setSummary(await source.preview(password));
    } catch (e) {
      setDecryptError(errorMessage(e));
    } finally {
      setDecrypting(false);
    }
  };

  const handleImport = async () => {
    setImporting(true);
    try {
      const opts = new backup_svc.ImportOptions({
        import_assets: importAssets,
        import_credentials: importCredentials,
        import_forwards: importForwards,
        import_policy_groups: importPolicyGroups,
        import_shortcuts: importShortcuts,
        import_themes: importThemes,
        mode,
      });
      const result = await source.execute(password, opts);
      await applyImportResult(result);
      await refresh();

      const parts: string[] = [];
      if (result.assets_imported > 0) parts.push(`${result.assets_imported} assets`);
      if (result.groups_imported > 0) parts.push(`${result.groups_imported} groups`);
      if (result.credentials_imported > 0) parts.push(`${result.credentials_imported} credentials`);
      if (result.policy_groups_imported > 0) parts.push(`${result.policy_groups_imported} policy groups`);
      if (result.forwards_imported > 0) parts.push(`${result.forwards_imported} forwards`);
      if (result.custom_types_imported > 0) parts.push(`${result.custom_types_imported} custom types`);
      notifySuccess(source.successMessage + (parts.length > 0 ? `: ${parts.join(", ")}` : ""));

      onOpenChange(false);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setImporting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{source.title}</DialogTitle>
        </DialogHeader>

        {/* Decrypt step */}
        {needsDecrypt && (
          <div className="space-y-3">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Lock className="h-4 w-4" />
              <span>{source.passwordPrompt}</span>
            </div>
            <div className="flex items-center gap-2">
              <SecretInput
                wrapperClassName="flex-1"
                placeholder={t("backup.password")}
                value={password}
                aria-invalid={decryptError ? true : undefined}
                onChange={(e) => {
                  setPassword(e.target.value);
                  setDecryptError("");
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && password && !decrypting) handleDecrypt();
                }}
              />
              <Button onClick={handleDecrypt} disabled={decrypting || !password}>
                {decrypting && <Loader2 className="h-4 w-4 mr-1 animate-spin" />}
                {t("backup.decryptPreview")}
              </Button>
            </div>
            {decryptError && (
              <p role="alert" className="text-xs text-destructive">
                {decryptError}
              </p>
            )}
          </div>
        )}

        {/* Summary & module selection */}
        {summary && (
          <div className="space-y-4">
            <div className="text-sm text-muted-foreground">
              {t("backup.backupFrom")}: {summary.exported_at?.split("T")[0]}
              <span className="ml-2 text-xs">(v{summary.version})</span>
            </div>

            <Separator />

            <div className="space-y-3">
              {/* Assets */}
              {(summary.asset_count > 0 || summary.group_count > 0) && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Server className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-assets" className="text-sm">
                      {t("backup.importAssets")}
                      <span className="ml-1 text-xs text-muted-foreground">
                        ({summary.asset_count} assets, {summary.group_count} groups)
                      </span>
                    </Label>
                  </div>
                  <Switch id="backup-import-assets" checked={importAssets} onCheckedChange={setImportAssets} />
                </div>
              )}

              {/* Credentials */}
              {summary.includes_credentials && summary.credential_count > 0 && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Shield className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-credentials" className="text-sm">
                      {t("backup.importCredentials")}
                      <span className="ml-1 text-xs text-muted-foreground">
                        ({summary.credential_count} credentials)
                      </span>
                    </Label>
                  </div>
                  <Switch
                    id="backup-import-credentials"
                    checked={importCredentials}
                    onCheckedChange={setImportCredentials}
                  />
                </div>
              )}

              {/* Port Forwarding */}
              {summary.forward_count > 0 && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Network className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-forwards" className="text-sm">
                      {t("backup.importForwards")}
                      <span className="ml-1 text-xs text-muted-foreground">({summary.forward_count} configs)</span>
                    </Label>
                  </div>
                  <Switch id="backup-import-forwards" checked={importForwards} onCheckedChange={setImportForwards} />
                </div>
              )}

              {/* Custom types — tied to importAssets, not a separate toggle: the types
                  used by imported assets come along automatically. */}
              {summary.custom_type_count > 0 && (
                <div className="flex items-center justify-between" data-testid="backup-summary-custom-types">
                  <div className="flex items-center gap-2">
                    <Blocks className="h-4 w-4 text-muted-foreground" />
                    <Label className="text-sm">
                      {t("backup.importCustomTypes")}
                      <span className="ml-1 text-xs text-muted-foreground">({summary.custom_type_count} types)</span>
                    </Label>
                  </div>
                </div>
              )}

              {/* Policy Groups */}
              {summary.policy_group_count > 0 && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Settings2 className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-policy-groups" className="text-sm">
                      {t("backup.importPolicyGroups")}
                      <span className="ml-1 text-xs text-muted-foreground">({summary.policy_group_count} groups)</span>
                    </Label>
                  </div>
                  <Switch
                    id="backup-import-policy-groups"
                    checked={importPolicyGroups}
                    onCheckedChange={setImportPolicyGroups}
                  />
                </div>
              )}

              {/* Shortcuts */}
              {summary.has_shortcuts && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Keyboard className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-shortcuts" className="text-sm">
                      {t("backup.importShortcuts")}
                    </Label>
                  </div>
                  <Switch id="backup-import-shortcuts" checked={importShortcuts} onCheckedChange={setImportShortcuts} />
                </div>
              )}

              {/* Custom Themes */}
              {summary.has_custom_themes && (
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Palette className="h-4 w-4 text-muted-foreground" />
                    <Label htmlFor="backup-import-themes" className="text-sm">
                      {t("backup.importThemes")}
                    </Label>
                  </div>
                  <Switch id="backup-import-themes" checked={importThemes} onCheckedChange={setImportThemes} />
                </div>
              )}
            </div>

            <Separator />

            {/* Import Mode */}
            <div className="space-y-2">
              <Label className="text-sm font-medium">{t("backup.importMode")}</Label>
              <div className="flex gap-4">
                <label className="flex items-center gap-1.5 text-sm cursor-pointer">
                  <input type="radio" name="importMode" checked={mode === "merge"} onChange={() => setMode("merge")} />
                  {t("backup.modeMerge")}
                </label>
                <label className="flex items-center gap-1.5 text-sm cursor-pointer">
                  <input
                    type="radio"
                    name="importMode"
                    checked={mode === "replace"}
                    onChange={() => setMode("replace")}
                  />
                  {t("backup.modeReplace")}
                </label>
              </div>
              {mode === "replace" && <p className="text-xs text-destructive">{t("backup.replaceWarning")}</p>}
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          {summary && (
            <Button
              variant={mode === "replace" ? "destructive" : "default"}
              onClick={handleImport}
              disabled={importing}
            >
              {importing && <Loader2 className="h-4 w-4 mr-1 animate-spin" />}
              {importing ? t("backup.importing") : t("backup.import")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
