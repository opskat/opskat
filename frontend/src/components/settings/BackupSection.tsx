import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@opskat/ui";
import {
  SelectImportFile,
  PreviewImportFile,
  ExecuteImportFile,
  StartGitHubDeviceFlow,
  WaitGitHubDeviceAuth,
  CancelGitHubAuth,
  GetGitHubUser,
  ExportToGist,
  ListBackupGists,
  PreviewGistBackup,
  ImportFromGist,
  GetGitHubToken,
  GetStoredGitHubUser,
  SaveGitHubToken,
  ClearGitHubToken,
  GetWebDAVConfig,
  SaveWebDAVConfig,
  ClearWebDAVConfig,
  TestWebDAVConfig,
  ListWebDAVBackups,
  ExportToWebDAV,
  PreviewWebDAVBackup,
  ImportFromWebDAV,
} from "../../../wailsjs/go/system/System";
import { backup_svc } from "../../../wailsjs/go/models";
import { ExportDialog, type WebDAVExportDefaults } from "@/components/settings/ExportDialog";
import { BackupImportDialog, type BackupImportSource } from "@/components/settings/BackupImportDialog";
import {
  Download,
  Upload,
  Github,
  LogOut,
  Loader2,
  Copy,
  ExternalLink,
  Shuffle,
  Cloud,
  Save,
  Trash2,
  RefreshCw,
} from "lucide-react";
import { toast } from "sonner";
import { notifyCopied, notifySuccess } from "@/lib/notify";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { SecretInput } from "@/components/SecretInput";
import { errorMessage } from "@/lib/errorMessage";

const githubTokenInvalidMarker = "[GITHUB_TOKEN_INVALID]";

const emptyWebDAVExportDefaults: WebDAVExportDefaults = {
  configured: false,
  password: "",
  includeCredentials: false,
  includeForwards: true,
  includePolicyGroups: true,
  includeShortcuts: false,
  includeThemes: false,
};

function PasswordInput({
  showGenerate,
  onGenerate,
  ...props
}: React.ComponentProps<typeof Input> & {
  showGenerate?: boolean;
  onGenerate?: (password: string) => void;
}) {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  return (
    <SecretInput
      {...props}
      reveal={visible}
      onRevealChange={setVisible}
      actions={
        showGenerate ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-7 w-7"
            title={t("credential.randomPassword")}
            onClick={() => {
              const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";
              const values = crypto.getRandomValues(new Uint8Array(20));
              const p = Array.from(values, (v) => charset[v % charset.length]).join("");
              setVisible(true);
              onGenerate?.(p);
            }}
          >
            <Shuffle className="h-3.5 w-3.5" />
          </Button>
        ) : undefined
      }
    />
  );
}

export function BackupSection() {
  const { t } = useTranslation();

  // File backup
  const [exportDialogOpen, setExportDialogOpen] = useState(false);
  const [exportDialogMode, setExportDialogMode] = useState<"file" | "gist" | "webdav">("file");

  // Import / restore (file, Gist, WebDAV share one dialog)
  const [importSource, setImportSource] = useState<BackupImportSource | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [importSeq, setImportSeq] = useState(0);

  // GitHub
  const [ghToken, setGhToken] = useState("");
  const [ghUser, setGhUser] = useState("");
  const [deviceFlowOpen, setDeviceFlowOpen] = useState(false);
  const [deviceFlowInfo, setDeviceFlowInfo] = useState<backup_svc.DeviceFlowInfo | null>(null);
  const [ghLoggingIn, setGhLoggingIn] = useState(false);

  // Gist
  const [gists, setGists] = useState<backup_svc.GistInfo[]>([]);
  const [selectedGistId, setSelectedGistId] = useState("");
  const [gistPushing, setGistPushing] = useState(false);

  // WebDAV
  const [webdavConfigured, setWebDAVConfigured] = useState(false);
  const [webdavURL, setWebDAVURL] = useState("");
  const [webdavAuthType, setWebDAVAuthType] = useState<"none" | "basic" | "bearer">("basic");
  const [webdavUsername, setWebDAVUsername] = useState("");
  const [webdavPassword, setWebDAVPassword] = useState("");
  const [webdavToken, setWebDAVToken] = useState("");
  const [webdavBackups, setWebDAVBackups] = useState<backup_svc.WebDAVBackupInfo[]>([]);
  const [selectedWebDAVBackup, setSelectedWebDAVBackup] = useState("");
  const [webdavSaving, setWebDAVSaving] = useState(false);
  const [webdavTesting, setWebDAVTesting] = useState(false);
  const [webdavPushing, setWebDAVPushing] = useState(false);
  const [webDAVExportDefaults, setWebDAVExportDefaults] = useState<WebDAVExportDefaults>(emptyWebDAVExportDefaults);

  // Load GitHub token on mount
  useEffect(() => {
    (async () => {
      try {
        const token = await GetGitHubToken();
        const user = await GetStoredGitHubUser();
        if (token) {
          setGhToken(token);
          setGhUser(user || "");
          GetGitHubUser(token)
            .then((u) => {
              setGhUser(u.login);
              SaveGitHubToken(token, u.login).catch(() => {});
            })
            .catch((e: unknown) => {
              if (errorMessage(e).includes(githubTokenInvalidMarker)) {
                setGhToken("");
                setGhUser("");
                ClearGitHubToken().catch(() => {});
              }
            });
        }
      } catch {
        /* not configured */
      }
    })();
  }, []);

  const loadGists = useCallback(async () => {
    if (!ghToken) return;
    try {
      const list = await ListBackupGists(ghToken);
      setGists(list || []);
    } catch {
      setGists([]);
    }
  }, [ghToken]);

  useEffect(() => {
    // 加载体的 setState 均在 await 之后,经 async 边界调用避免 effect 内同步 setState
    void (async () => {
      await loadGists();
    })();
  }, [loadGists]);

  useEffect(() => {
    (async () => {
      try {
        const cfg = await GetWebDAVConfig();
        if (!cfg) return;
        setWebDAVURL(cfg.url || "");
        const known: ReadonlyArray<"none" | "basic" | "bearer"> = ["none", "basic", "bearer"];
        const incoming = cfg.authType ?? "";
        setWebDAVAuthType(
          known.includes(incoming as "none" | "basic" | "bearer") ? (incoming as "none" | "basic" | "bearer") : "basic"
        );
        setWebDAVUsername(cfg.username || "");
        setWebDAVPassword(cfg.password || "");
        setWebDAVToken(cfg.token || "");
        setWebDAVConfigured(!!cfg.configured);
        setWebDAVExportDefaults({
          configured: !!cfg.exportDefaultsConfigured,
          password: cfg.exportPassword || "",
          includeCredentials: !!cfg.exportIncludeCredentials,
          includeForwards: cfg.exportDefaultsConfigured ? !!cfg.exportIncludeForwards : true,
          includePolicyGroups: cfg.exportDefaultsConfigured ? !!cfg.exportIncludePolicyGroups : true,
          includeShortcuts: !!cfg.exportIncludeShortcuts,
          includeThemes: !!cfg.exportIncludeThemes,
        });
      } catch {
        /* not configured */
      }
    })();
  }, []);

  const loadWebDAVBackups = useCallback(async () => {
    if (!webdavConfigured) return;
    try {
      const list = await ListWebDAVBackups();
      const backups = list || [];
      setWebDAVBackups(backups);
      setSelectedWebDAVBackup((current) =>
        current && backups.some((backup) => backup.name === current) ? current : (backups[0]?.name ?? "")
      );
    } catch {
      setWebDAVBackups([]);
      setSelectedWebDAVBackup("");
    }
  }, [webdavConfigured]);

  useEffect(() => {
    // 同上:经 async 边界调用
    void (async () => {
      await loadWebDAVBackups();
    })();
  }, [loadWebDAVBackups]);

  const openImport = (source: BackupImportSource) => {
    setImportSource(source);
    setImportSeq((n) => n + 1);
    setImportOpen(true);
  };

  // --- File backup ---
  const handleFileExport = () => {
    setExportDialogMode("file");
    setExportDialogOpen(true);
  };

  const handleFileImport = async () => {
    try {
      const info = await SelectImportFile();
      if (!info || !info.filePath) return;
      const { filePath } = info;
      openImport({
        title: t("backup.importTitle"),
        passwordPrompt: t("backup.encryptedFile"),
        successMessage: t("backup.importSuccess"),
        encrypted: info.encrypted,
        initialSummary: info.summary ?? null,
        preview: (password) => PreviewImportFile(filePath, password),
        execute: (password, opts) => ExecuteImportFile(filePath, password, opts),
      });
    } catch (e: unknown) {
      toast.error(errorMessage(e));
    }
  };

  // --- GitHub Auth ---
  const handleGitHubLogin = async () => {
    setGhLoggingIn(true);
    try {
      const info = await StartGitHubDeviceFlow();
      setDeviceFlowInfo(info);
      setDeviceFlowOpen(true);

      const token = await WaitGitHubDeviceAuth(info.deviceCode, info.interval);
      setDeviceFlowOpen(false);
      setGhToken(token);

      const user = await GetGitHubUser(token);
      setGhUser(user.login);
      await SaveGitHubToken(token, user.login);
      notifySuccess(t("backup.gistLoggedIn", { user: user.login }));
    } catch (e: unknown) {
      if (!errorMessage(e).includes("\u53D6\u6D88")) {
        toast.error(errorMessage(e));
      }
    } finally {
      setDeviceFlowOpen(false);
      setGhLoggingIn(false);
    }
  };

  const handleGitHubLogout = () => {
    setGhToken("");
    setGhUser("");
    setGists([]);
    ClearGitHubToken().catch(() => {});
  };

  const handleCancelDeviceFlow = () => {
    CancelGitHubAuth().catch(() => {});
    setDeviceFlowOpen(false);
  };

  // --- Gist ---
  const handleGistPush = () => {
    setExportDialogMode("gist");
    setExportDialogOpen(true);
  };

  const handleGistExport = async (password: string, opts: backup_svc.ExportOptions) => {
    if (!password) {
      throw new Error(t("backup.passwordRequired"));
    }
    setGistPushing(true);
    try {
      const gistId = selectedGistId === "__new__" ? "" : selectedGistId;
      const result = await ExportToGist(password, ghToken, gistId, opts);
      if (result) {
        await loadGists();
        setSelectedGistId(result.id);
      }
    } finally {
      setGistPushing(false);
    }
  };

  const handleGistPull = () => {
    if (!selectedGistId || selectedGistId === "__new__") {
      toast.error(t("backup.gistNoBackup"));
      return;
    }
    const gistId = selectedGistId;
    const token = ghToken;
    openImport({
      title: t("backup.gistPull"),
      passwordPrompt: t("backup.enterPassword"),
      successMessage: t("backup.gistPullSuccess"),
      encrypted: true,
      initialSummary: null,
      preview: (password) => PreviewGistBackup(gistId, password, token),
      execute: (password, opts) => ImportFromGist(gistId, password, token, opts),
    });
  };

  // --- WebDAV ---
  const buildWebDAVInput = () => ({
    url: webdavURL.trim(),
    authType: webdavAuthType,
    username: webdavUsername.trim(),
    password: webdavPassword,
    token: webdavToken.trim(),
  });

  const handleWebDAVSave = async () => {
    if (!webdavURL.trim()) {
      toast.error(t("backup.webdavURLRequired"));
      return;
    }
    setWebDAVSaving(true);
    try {
      await SaveWebDAVConfig(buildWebDAVInput());
      setWebDAVConfigured(true);
      notifySuccess(t("backup.webdavSaved"));
    } catch (e: unknown) {
      toast.error(errorMessage(e));
    } finally {
      setWebDAVSaving(false);
    }
  };

  const handleWebDAVTest = async () => {
    if (!webdavURL.trim()) {
      toast.error(t("backup.webdavURLRequired"));
      return;
    }
    setWebDAVTesting(true);
    try {
      await TestWebDAVConfig(buildWebDAVInput());
      notifySuccess(t("backup.webdavTestSuccess"));
    } catch (e: unknown) {
      toast.error(errorMessage(e));
    } finally {
      setWebDAVTesting(false);
    }
  };

  const handleWebDAVClear = async () => {
    try {
      await ClearWebDAVConfig();
      setWebDAVConfigured(false);
      setWebDAVURL("");
      setWebDAVAuthType("basic");
      setWebDAVUsername("");
      setWebDAVPassword("");
      setWebDAVToken("");
      setWebDAVBackups([]);
      setSelectedWebDAVBackup("");
      setWebDAVExportDefaults(emptyWebDAVExportDefaults);
      notifySuccess(t("backup.webdavCleared"));
    } catch (e: unknown) {
      toast.error(errorMessage(e));
    }
  };

  const handleWebDAVPush = () => {
    if (!webdavConfigured) {
      toast.error(t("backup.webdavNotConfigured"));
      return;
    }
    setExportDialogMode("webdav");
    setExportDialogOpen(true);
  };

  const handleWebDAVExport = async (password: string, opts: backup_svc.ExportOptions) => {
    if (!password) {
      throw new Error(t("backup.passwordRequired"));
    }
    setWebDAVPushing(true);
    try {
      const result = await ExportToWebDAV(password, opts);
      await loadWebDAVBackups();
      if (result?.name) {
        setSelectedWebDAVBackup(result.name);
      }
      setWebDAVExportDefaults({
        configured: true,
        password,
        includeCredentials: opts.include_credentials,
        includeForwards: opts.include_forwards,
        includePolicyGroups: opts.include_policy_groups,
        includeShortcuts: opts.include_shortcuts,
        includeThemes: opts.include_themes,
      });
    } finally {
      setWebDAVPushing(false);
    }
  };

  const handleWebDAVPull = () => {
    if (!selectedWebDAVBackup) {
      toast.error(t("backup.webdavNoBackup"));
      return;
    }
    const name = selectedWebDAVBackup;
    openImport({
      title: t("backup.webdavPull"),
      passwordPrompt: t("backup.enterPassword"),
      successMessage: t("backup.webdavPullSuccess"),
      encrypted: true,
      initialSummary: null,
      preview: (password) => PreviewWebDAVBackup(name, password),
      execute: (password, opts) => ImportFromWebDAV(name, password, opts),
    });
  };

  return (
    <>
      {/* File backup */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("backup.file")}</CardTitle>
          <CardDescription>{t("backup.fileDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="flex gap-2">
          <Button onClick={handleFileExport} variant="outline" className="gap-1">
            <Download className="h-4 w-4" />
            {t("backup.export")}
          </Button>
          <Button onClick={handleFileImport} variant="outline" className="gap-1">
            <Upload className="h-4 w-4" />
            {t("backup.import")}
          </Button>
        </CardContent>
      </Card>

      {/* Gist backup */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-1.5">
            <Github className="h-4 w-4" />
            {t("backup.gist")}
          </CardTitle>
          <CardDescription>{t("backup.gistDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {!ghToken ? (
            <Button onClick={handleGitHubLogin} disabled={ghLoggingIn} variant="outline" className="gap-1">
              <Github className="h-4 w-4" />
              {ghLoggingIn ? t("backup.deviceFlowWaiting") : t("backup.gistLogin")}
            </Button>
          ) : (
            <>
              <div className="flex items-center justify-between">
                <span className="text-sm text-muted-foreground">{t("backup.gistLoggedIn", { user: ghUser })}</span>
                <Button variant="ghost" size="sm" onClick={handleGitHubLogout} className="gap-1">
                  <LogOut className="h-3.5 w-3.5" />
                  {t("backup.gistLogout")}
                </Button>
              </div>
              <div className="grid gap-2">
                <Label>{t("backup.gistSelect")}</Label>
                <Select value={selectedGistId} onValueChange={setSelectedGistId}>
                  <SelectTrigger>
                    <SelectValue placeholder={t("backup.gistSelect")} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__new__">{t("backup.gistCreateNew")}</SelectItem>
                    {gists.map((g) => (
                      <SelectItem key={g.id} value={g.id}>
                        {t("backup.gistUpdate", { desc: g.description })}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex gap-2">
                <Button onClick={handleGistPush} disabled={gistPushing} variant="outline" className="gap-1">
                  <Upload className="h-4 w-4" />
                  {gistPushing ? t("backup.gistPushing") : t("backup.gistPush")}
                </Button>
                <Button
                  onClick={handleGistPull}
                  disabled={!selectedGistId || selectedGistId === "__new__"}
                  variant="outline"
                  className="gap-1"
                >
                  <Download className="h-4 w-4" />
                  {t("backup.gistPull")}
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {/* WebDAV backup */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-1.5">
            <Cloud className="h-4 w-4" />
            {t("backup.webdav")}
          </CardTitle>
          <CardDescription>{t("backup.webdavDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label>{t("backup.webdavURL")}</Label>
              <Input
                value={webdavURL}
                onChange={(e) => setWebDAVURL(e.target.value)}
                placeholder={t("backup.webdavURLPlaceholder")}
              />
              <p className="text-xs text-muted-foreground">{t("backup.webdavDefaultDirectory", { dir: "opskat" })}</p>
            </div>
            <div className="grid gap-1.5">
              <Label>{t("backup.webdavAuthType")}</Label>
              <Select value={webdavAuthType} onValueChange={(v) => setWebDAVAuthType(v as "none" | "basic" | "bearer")}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">{t("backup.webdavAuthNone")}</SelectItem>
                  <SelectItem value="basic">{t("backup.webdavAuthBasic")}</SelectItem>
                  <SelectItem value="bearer">{t("backup.webdavAuthBearer")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {webdavAuthType === "basic" && (
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="grid gap-1.5">
                  <Label>{t("backup.webdavUsername")}</Label>
                  <Input
                    value={webdavUsername}
                    onChange={(e) => setWebDAVUsername(e.target.value)}
                    placeholder={t("backup.webdavUsernamePlaceholder")}
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label>{t("backup.webdavPassword")}</Label>
                  <PasswordInput value={webdavPassword} onChange={(e) => setWebDAVPassword(e.target.value)} />
                </div>
              </div>
            )}
            {webdavAuthType === "bearer" && (
              <div className="grid gap-1.5">
                <Label>{t("backup.webdavToken")}</Label>
                <PasswordInput value={webdavToken} onChange={(e) => setWebDAVToken(e.target.value)} />
              </div>
            )}
            <div className="flex flex-wrap gap-2">
              <Button onClick={handleWebDAVSave} disabled={webdavSaving} variant="outline" className="gap-1">
                {webdavSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                {t("backup.webdavSave")}
              </Button>
              <Button
                onClick={handleWebDAVTest}
                disabled={webdavTesting || !webdavURL.trim()}
                variant="outline"
                className="gap-1"
              >
                {webdavTesting ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
                {t("backup.webdavTest")}
              </Button>
              {webdavConfigured && (
                <Button onClick={handleWebDAVClear} variant="ghost" className="gap-1 text-muted-foreground">
                  <Trash2 className="h-4 w-4" />
                  {t("backup.webdavClear")}
                </Button>
              )}
            </div>
          </div>

          {webdavConfigured && (
            <>
              <div className="grid gap-2">
                <Label>{t("backup.webdavSelect")}</Label>
                <div className="flex gap-2">
                  <Select value={selectedWebDAVBackup} onValueChange={setSelectedWebDAVBackup}>
                    <SelectTrigger className="flex-1">
                      <SelectValue placeholder={t("backup.webdavNoBackup")} />
                    </SelectTrigger>
                    <SelectContent>
                      {webdavBackups.map((backup) => (
                        <SelectItem key={backup.name} value={backup.name}>
                          {t("backup.webdavBackupItem", { name: backup.name, size: backup.size })}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button onClick={loadWebDAVBackups} variant="outline" size="icon">
                    <RefreshCw className="h-4 w-4" />
                  </Button>
                </div>
              </div>
              <div className="flex gap-2">
                <Button onClick={handleWebDAVPush} disabled={webdavPushing} variant="outline" className="gap-1">
                  <Upload className="h-4 w-4" />
                  {webdavPushing ? t("backup.webdavPushing") : t("backup.webdavPush")}
                </Button>
                <Button onClick={handleWebDAVPull} disabled={!selectedWebDAVBackup} variant="outline" className="gap-1">
                  <Download className="h-4 w-4" />
                  {t("backup.webdavPull")}
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {/* Export dialog */}
      <ExportDialog
        open={exportDialogOpen}
        onOpenChange={setExportDialogOpen}
        mode={exportDialogMode}
        onGistExport={handleGistExport}
        onWebDAVExport={handleWebDAVExport}
        webDAVDefaults={webDAVExportDefaults}
      />

      {/* Backup import / restore dialog */}
      {importSource && (
        <BackupImportDialog key={importSeq} open={importOpen} onOpenChange={setImportOpen} source={importSource} />
      )}

      {/* GitHub Device Flow dialog */}
      <Dialog open={deviceFlowOpen}>
        <DialogContent className="sm:max-w-sm" showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>{t("backup.deviceFlow")}</DialogTitle>
            <DialogDescription>{t("backup.deviceFlowDesc")}</DialogDescription>
          </DialogHeader>
          {deviceFlowInfo && (
            <div className="space-y-4">
              <div className="flex items-center justify-center gap-2">
                <code className="rounded bg-muted px-3 py-2 text-2xl font-mono font-bold tracking-widest">
                  {deviceFlowInfo.userCode}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => {
                    navigator.clipboard.writeText(deviceFlowInfo.userCode);
                    notifyCopied(t("action.copied"));
                  }}
                >
                  <Copy className="h-4 w-4" />
                </Button>
              </div>
              <Button className="w-full gap-1" onClick={() => BrowserOpenURL(deviceFlowInfo.verificationUri)}>
                <ExternalLink className="h-4 w-4" />
                {t("backup.deviceFlowOpen")}
              </Button>
              <div className="flex items-center justify-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                {t("backup.deviceFlowWaiting")}
              </div>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={handleCancelDeviceFlow}>
              {t("action.cancel")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
