import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Input,
  Label,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Separator,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  ConfirmDialog,
} from "@opskat/ui";
import { DetectOpsctl } from "../../../wailsjs/go/system/System";
import {
  GetOpsctlInstallDir,
  InstallOpsctl,
  DetectSkills,
  InstallSkills,
  InstallSkillTarget,
  UninstallSkill,
  GetSkillPreview,
  GetDataDir,
  GetAppVersion,
  OpenDirectory,
  GetPluginReferenceDir,
} from "../../../wailsjs/go/system/System";
import {
  ListAIProviders,
  CreateAIProvider,
  UpdateAIProvider,
  DeleteAIProvider,
  SetActiveAIProvider,
} from "../../../wailsjs/go/ai/AI";
import { ai } from "../../../wailsjs/go/models";
import {
  Check,
  Loader2,
  ExternalLink,
  ChevronDown,
  ChevronUp,
  Info,
  FolderOpen,
  Pencil,
  RefreshCw,
  Trash2,
  Plus,
} from "lucide-react";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { AIProviderForm, type AIProviderFormValues, type ReasoningEffort } from "@/components/ai/AIProviderForm";
import { useAIStore } from "@/stores/aiStore";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

// 与后端 universalSkillKey 对应：通用目录（~/.agents/skills）作为一个安装目标参与安装/卸载
const UNIVERSAL_KEY = "universal";

type SkillAgent = { key: string; name: string };
type SkillTarget = { key: string; name: string; installed: boolean; path: string };
type SkillInstallInfo = {
  universalPath: string;
  universalInstalled: boolean;
  universalAgents: SkillAgent[];
  standalone: SkillTarget[];
};

function IntegrationSection() {
  const { t } = useTranslation();
  const [opsctlInfo, setOpsctlInfo] = useState<{
    installed: boolean;
    path: string;
    version: string;
    embedded: boolean;
  }>({ installed: false, path: "", version: "", embedded: false });
  const [skillInfo, setSkillInfo] = useState<SkillInstallInfo | null>(null);
  const [installDir, setInstallDir] = useState("");
  const [installing, setInstalling] = useState(false);
  const [skillInstalling, setSkillInstalling] = useState(false);
  const [installingKey, setInstallingKey] = useState("");
  const [uninstallTarget, setUninstallTarget] = useState<SkillTarget | null>(null);
  const [uninstallingKey, setUninstallingKey] = useState("");
  const [skillPreview, setSkillPreview] = useState("");
  const [showPreview, setShowPreview] = useState(false);
  const [dataDir, setDataDir] = useState("");
  const [appVersion, setAppVersion] = useState("");

  const detect = useCallback(async () => {
    try {
      const [info, skills, dir, dd, ver] = await Promise.all([
        DetectOpsctl(),
        DetectSkills(),
        GetOpsctlInstallDir(),
        GetDataDir(),
        GetAppVersion(),
      ]);
      setOpsctlInfo(info);
      setSkillInfo(skills || null);
      setInstallDir(dir);
      setDataDir(dd);
      setAppVersion(ver);
    } catch {
      // detection is optional
    }
  }, []);

  useEffect(() => {
    // 挂载时探测:detect 的 setState 均在 await 之后,经 async 边界调用避免 effect 内同步 setState
    void (async () => {
      await detect();
    })();
  }, [detect]);

  const handleInstallCLI = async () => {
    setInstalling(true);
    try {
      await InstallOpsctl(installDir);
      notifySuccess(t("integration.installSuccess"));
      await detect();
      toast.info(`${t("integration.pathHint")}: ${installDir}`);
    } catch (e: unknown) {
      toast.error(`${t("integration.installFailed")}: ${errMsg(e)}`);
    } finally {
      setInstalling(false);
    }
  };

  const handleInstallSkill = async () => {
    setSkillInstalling(true);
    try {
      await InstallSkills();
      notifySuccess(t("integration.skillInstallSuccess"));
      await detect();
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setSkillInstalling(false);
    }
  };

  const handleInstallTarget = async (key: string) => {
    setInstallingKey(key);
    try {
      await InstallSkillTarget(key);
      notifySuccess(t("integration.skillInstallSuccess"));
      await detect();
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setInstallingKey("");
    }
  };

  const handleUninstallSkill = async () => {
    if (!uninstallTarget) return;
    setUninstallingKey(uninstallTarget.key);
    try {
      await UninstallSkill(uninstallTarget.key);
      notifySuccess(t("integration.skillUninstallSuccess"));
      setUninstallTarget(null);
      await detect();
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setUninstallingKey("");
    }
  };

  // 通用目录和单独安装目标各自独立，任一装上就算这张卡片已生效
  const anySkillInstalled =
    !!skillInfo && (skillInfo.universalInstalled || skillInfo.standalone.some((s) => s.installed));
  const allSkillsInstalled =
    !!skillInfo && skillInfo.universalInstalled && skillInfo.standalone.every((s) => s.installed);

  const handlePreview = async () => {
    if (showPreview) {
      setShowPreview(false);
      return;
    }
    try {
      const content = await GetSkillPreview();
      setSkillPreview(content);
      setShowPreview(true);
    } catch {
      // preview is optional
    }
  };

  return (
    <>
      {/* opsctl CLI */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base">{t("integration.cli")}</CardTitle>
              <CardDescription>{t("integration.cliDesc")}</CardDescription>
            </div>
            <div className="flex items-center gap-2">
              {opsctlInfo.installed ? (
                <span className="inline-flex items-center gap-1 text-xs font-medium text-success">
                  <Check className="h-3.5 w-3.5" />
                  {t("integration.installed")}
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">{t("integration.notInstalled")}</span>
              )}
              <Button variant="ghost" size="icon" className="h-7 w-7" onClick={detect}>
                <RefreshCw className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {opsctlInfo.installed && (
            <div className="space-y-3">
              <div className="grid gap-1.5 text-sm">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">{t("integration.version")}</span>
                  <span className="font-mono text-xs">{opsctlInfo.version || "unknown"}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">{t("integration.path")}</span>
                  <span className="font-mono text-xs truncate max-w-[300px]">{opsctlInfo.path}</span>
                </div>
              </div>
              {appVersion && opsctlInfo.version && opsctlInfo.version !== appVersion && (
                <div className="flex items-center gap-2 rounded-md bg-warning/15 border border-warning/30 px-3 py-2 text-xs text-warning">
                  <Info className="h-3.5 w-3.5 shrink-0" />
                  <span>{t("integration.versionMismatch", { appVersion, cliVersion: opsctlInfo.version })}</span>
                </div>
              )}
              {opsctlInfo.embedded && (
                <div className="space-y-2">
                  <div className="grid gap-1.5">
                    <Label className="text-sm">{t("integration.installDir")}</Label>
                    <Input
                      value={installDir}
                      onChange={(e) => setInstallDir(e.target.value)}
                      className="font-mono text-xs h-8"
                    />
                  </div>
                  <Button onClick={handleInstallCLI} disabled={installing} size="sm" variant="outline">
                    {installing ? (
                      <>
                        <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                        {t("integration.installing")}
                      </>
                    ) : (
                      t("integration.reinstall")
                    )}
                  </Button>
                </div>
              )}
            </div>
          )}

          {!opsctlInfo.installed && (
            <div className="space-y-3">
              {opsctlInfo.embedded ? (
                <div className="space-y-2">
                  <div className="grid gap-1.5">
                    <Label className="text-sm">{t("integration.installDir")}</Label>
                    <Input
                      value={installDir}
                      onChange={(e) => setInstallDir(e.target.value)}
                      className="font-mono text-xs h-8"
                    />
                  </div>
                  <Button onClick={handleInstallCLI} disabled={installing} size="sm">
                    {installing ? (
                      <>
                        <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                        {t("integration.installing")}
                      </>
                    ) : (
                      t("integration.install")
                    )}
                  </Button>
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">{t("integration.noEmbedded")}</p>
              )}
              <Separator />
              <div className="space-y-1">
                <p className="text-sm font-medium">{t("integration.manualInstall")}</p>
                <p className="text-xs text-muted-foreground">{t("integration.manualInstallHint")}</p>
                <Button
                  variant="link"
                  size="sm"
                  className="h-auto p-0 text-xs"
                  onClick={() => BrowserOpenURL("https://github.com/opskat/opskat/releases")}
                >
                  <ExternalLink className="h-3 w-3 mr-1" />
                  GitHub Releases
                </Button>
              </div>
            </div>
          )}

          <Separator />
          <div className="space-y-1">
            <div className="flex justify-between text-sm">
              <span className="text-muted-foreground">{t("integration.dataDir")}</span>
              <span className="font-mono text-xs truncate max-w-[300px]">{dataDir}</span>
            </div>
            <p className="text-xs text-muted-foreground">{t("integration.dataDirDesc")}</p>
          </div>
        </CardContent>
      </Card>

      {/* AI Skill */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="text-base">{t("integration.skill")}</CardTitle>
              <CardDescription>{t("integration.skillDesc")}</CardDescription>
            </div>
            <div className="flex items-center gap-2">
              {anySkillInstalled && (
                <span className="inline-flex items-center gap-1 text-xs font-medium text-success">
                  <Check className="h-3.5 w-3.5" />
                  {t("integration.skillInstalled")}
                </span>
              )}
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* 通用安装（~/.agents/skills，一次安装多 agent 生效） */}
          <div className="rounded-lg border p-3 space-y-2">
            <div className="flex items-center justify-between gap-2">
              <div className="min-w-0">
                <p className="text-sm font-medium">{t("integration.skillUniversalTitle")}</p>
                <p className="text-xs text-muted-foreground font-mono truncate">{skillInfo?.universalPath ?? ""}</p>
              </div>
              <div className="flex items-center gap-1 shrink-0">
                {skillInfo?.universalInstalled && (
                  <button
                    type="button"
                    className="shrink-0 p-1.5 rounded hover:bg-muted text-muted-foreground hover:text-foreground"
                    onClick={() => OpenDirectory(skillInfo.universalPath)}
                    title={t("integration.openDir")}
                  >
                    <FolderOpen className="h-3.5 w-3.5" />
                  </button>
                )}
                <Button
                  variant="outline"
                  size="sm"
                  disabled={installingKey === UNIVERSAL_KEY || uninstallingKey === UNIVERSAL_KEY}
                  onClick={() => handleInstallTarget(UNIVERSAL_KEY)}
                >
                  {installingKey === UNIVERSAL_KEY ? (
                    <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                  ) : skillInfo?.universalInstalled ? (
                    t("integration.skillUpdate")
                  ) : (
                    t("integration.skillInstall")
                  )}
                </Button>
                {skillInfo?.universalInstalled && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-8 px-2 text-destructive hover:text-destructive"
                    disabled={uninstallingKey === UNIVERSAL_KEY}
                    onClick={() =>
                      setUninstallTarget({
                        key: UNIVERSAL_KEY,
                        name: t("integration.skillUniversalTitle"),
                        installed: true,
                        path: skillInfo.universalPath,
                      })
                    }
                  >
                    {uninstallingKey === UNIVERSAL_KEY ? (
                      <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                    ) : (
                      <Trash2 className="h-3.5 w-3.5 mr-1" />
                    )}
                    {t("integration.skillUninstall")}
                  </Button>
                )}
              </div>
            </div>
            <div>
              <p className="text-xs text-muted-foreground mb-1.5">{t("integration.skillUniversalAgents")}</p>
              <div className="flex flex-wrap gap-1">
                {(skillInfo?.universalAgents ?? []).map((a) => (
                  <span key={a.key} className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                    {a.name}
                  </span>
                ))}
              </div>
            </div>
          </div>

          {/* 单独安装（不读取共享目录的工具） */}
          {skillInfo && skillInfo.standalone.length > 0 && (
            <div className="rounded-lg border p-3 space-y-2">
              <div>
                <p className="text-sm font-medium">{t("integration.skillStandaloneTitle")}</p>
                <p className="text-xs text-muted-foreground">{t("integration.skillStandaloneDesc")}</p>
              </div>
              <div className="space-y-2">
                {skillInfo.standalone.map((s) => (
                  <div key={s.key} className="flex items-center justify-between gap-2 text-sm">
                    <div className="min-w-0">
                      <p className="font-medium">{s.name}</p>
                      <p className="text-xs text-muted-foreground font-mono truncate">{s.path}</p>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      {s.installed && (
                        <button
                          type="button"
                          className="shrink-0 p-1.5 rounded hover:bg-muted text-muted-foreground hover:text-foreground"
                          onClick={() => OpenDirectory(s.path)}
                          title={t("integration.openDir")}
                        >
                          <FolderOpen className="h-3.5 w-3.5" />
                        </button>
                      )}
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={installingKey === s.key || uninstallingKey === s.key}
                        onClick={() => handleInstallTarget(s.key)}
                      >
                        {installingKey === s.key ? (
                          <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                        ) : s.installed ? (
                          t("integration.skillUpdate")
                        ) : (
                          t("integration.skillInstall")
                        )}
                      </Button>
                      {s.installed && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-8 px-2 text-destructive hover:text-destructive"
                          disabled={uninstallingKey === s.key}
                          onClick={() => setUninstallTarget(s)}
                        >
                          {uninstallingKey === s.key ? (
                            <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                          ) : (
                            <Trash2 className="h-3.5 w-3.5 mr-1" />
                          )}
                          {t("integration.skillUninstall")}
                        </Button>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="flex gap-2">
            <Button onClick={handleInstallSkill} disabled={skillInstalling} size="sm">
              {skillInstalling ? (
                <>
                  <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                  {t("integration.skillInstalling")}
                </>
              ) : allSkillsInstalled ? (
                t("integration.skillUpdate")
              ) : (
                t("integration.skillInstall")
              )}
            </Button>
            <Button variant="outline" size="sm" onClick={handlePreview}>
              {showPreview ? <ChevronUp className="h-3.5 w-3.5 mr-1" /> : <ChevronDown className="h-3.5 w-3.5 mr-1" />}
              {t("integration.skillPreview")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                const dir = await GetPluginReferenceDir();
                OpenDirectory(dir);
              }}
            >
              <FolderOpen className="h-3.5 w-3.5 mr-1" />
              {t("integration.openDir")}
            </Button>
          </div>

          {showPreview && (
            <pre className="text-xs bg-muted p-3 rounded-md overflow-auto max-h-[300px] whitespace-pre-wrap">
              {skillPreview}
            </pre>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={!!uninstallTarget}
        onOpenChange={(open) => {
          if (!open) setUninstallTarget(null);
        }}
        title={t("integration.skillUninstallConfirmTitle")}
        description={t("integration.skillUninstallConfirmDesc", { name: uninstallTarget?.name ?? "" })}
        cancelText={t("action.cancel")}
        confirmText={t("integration.skillUninstall")}
        onConfirm={handleUninstallSkill}
      />
    </>
  );
}

export function AISettingsSection() {
  const { t } = useTranslation();
  const [providers, setProviders] = useState<ai.AIProviderInfo[]>([]);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<ai.AIProviderInfo | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ai.AIProviderInfo | null>(null);
  const [saving, setSaving] = useState(false);

  const loadProviders = useCallback(async () => {
    try {
      const list = await ListAIProviders();
      setProviders(list || []);
      await useAIStore.getState().checkConfigured();
    } catch (e) {
      toast.error(errMsg(e));
    }
  }, []);

  useEffect(() => {
    // 同 IntegrationSection:加载体的 setState 均在 await 之后,经 async 边界调用
    void (async () => {
      await loadProviders();
    })();
  }, [loadProviders]);

  const openAddDialog = () => {
    setEditingProvider(null);
    setDialogOpen(true);
  };

  const openEditDialog = (provider: ai.AIProviderInfo) => {
    setEditingProvider(provider);
    setDialogOpen(true);
  };

  const handleSave = async (values: AIProviderFormValues) => {
    setSaving(true);
    try {
      if (editingProvider) {
        await UpdateAIProvider(
          editingProvider.id,
          values.name,
          values.type,
          values.apiBase,
          values.apiKey,
          values.model,
          values.maxOutputTokens,
          values.contextWindow,
          values.reasoningEnabled,
          values.reasoningEffort
        );
      } else {
        const created = await CreateAIProvider(
          values.name,
          values.type,
          values.apiBase,
          values.apiKey,
          values.model,
          values.maxOutputTokens,
          values.contextWindow,
          values.reasoningEnabled,
          values.reasoningEffort
        );
        if (providers.length === 0 && created.id) {
          await SetActiveAIProvider(created.id);
        }
      }
      notifySuccess(t("settings.saved"));
      setDialogOpen(false);
      await loadProviders();
    } catch (e) {
      toast.error(errMsg(e));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await DeleteAIProvider(deleteTarget.id);
      notifySuccess(t("settings.saved"));
      setDeleteTarget(null);
      await loadProviders();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  const handleSetActive = async (id: number) => {
    try {
      await SetActiveAIProvider(id);
      await loadProviders();
    } catch (e) {
      toast.error(errMsg(e));
    }
  };

  return (
    <>
      {/* Provider list */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("settings.providers")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {providers.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("settings.noProviders")}</p>
          ) : (
            <div className="space-y-2">
              {providers.map((provider) => (
                <div
                  key={provider.id}
                  className={`flex items-center justify-between rounded-md border p-3 ${
                    provider.isActive ? "border-primary bg-primary/5" : ""
                  }`}
                >
                  <div className="flex items-center gap-2 min-w-0">
                    {provider.isActive && <Check className="h-4 w-4 text-primary shrink-0" />}
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium truncate">{provider.name}</span>
                        <span className="text-xs text-muted-foreground">
                          {provider.type === "anthropic" ? t("settings.anthropic") : t("settings.openai")}
                        </span>
                      </div>
                      <span className="text-xs text-muted-foreground">{provider.model}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    {!provider.isActive && (
                      <Button variant="ghost" size="sm" onClick={() => handleSetActive(provider.id)}>
                        {t("settings.setActive")}
                      </Button>
                    )}
                    <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => openEditDialog(provider)}>
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-8 w-8 text-destructive hover:text-destructive"
                      onClick={() => setDeleteTarget(provider)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
          <Button variant="outline" size="sm" onClick={openAddDialog}>
            <Plus className="h-3.5 w-3.5 mr-1.5" />
            {t("settings.addProvider")}
          </Button>
        </CardContent>
      </Card>

      {/* Add/Edit Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{editingProvider ? t("settings.editProvider") : t("settings.addProvider")}</DialogTitle>
            <DialogDescription>
              {editingProvider ? t("settings.editProvider") : t("settings.addProvider")}
            </DialogDescription>
          </DialogHeader>
          <AIProviderForm
            key={editingProvider?.id ?? "new"}
            initialValues={
              editingProvider
                ? {
                    name: editingProvider.name,
                    type: editingProvider.type,
                    apiBase: editingProvider.apiBase,
                    apiKey: editingProvider.apiKey || "",
                    maskedApiKey: editingProvider.maskedApiKey,
                    model: editingProvider.model,
                    maxOutputTokens: editingProvider.maxOutputTokens,
                    contextWindow: editingProvider.contextWindow,
                    reasoningEffort: (editingProvider.reasoningEffort ||
                      (editingProvider.reasoningEnabled ? "medium" : "none")) as ReasoningEffort,
                  }
                : undefined
            }
            isEditing={!!editingProvider}
            showTypeSelector={true}
            onSave={handleSave}
            saving={saving}
            submitLabel={t("action.save")}
          />
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t("settings.deleteProvider")}
        description={t("settings.deleteProviderConfirm")}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={handleDelete}
      />

      {/* CLI Integration */}
      <IntegrationSection />
    </>
  );
}
