import { useState, useEffect, useCallback, useRef, type CSSProperties, type DragEvent } from "react";
import { toast } from "sonner";
import { notifyCopied, notifySuccess } from "@/lib/notify";
import { useTranslation } from "react-i18next";
import {
  Plus,
  Trash2,
  Copy,
  Key,
  FileKey,
  Download,
  Pencil,
  Lock,
  KeyRound,
  Shuffle,
  FileText,
  Upload,
  CheckCircle2,
  AlertCircle,
  Cable,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Button,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  Input,
  Label,
  Textarea,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  ConfirmDialog,
  cn,
} from "@opskat/ui";
import { OnFileDrop, OnFileDropOff } from "../../../wailsjs/runtime/runtime";
import {
  GenerateSSHKey,
  ImportSSHKeyPath,
  ImportSSHKeyPEM,
  ExportSSHPrivateKey,
  UpdateCredential,
  DeleteCredential,
  GetCredentialPublicKey,
  GetCredentialUsage,
  CreatePasswordCredential,
  UpdateCredentialPassword,
  UpdateCredentialPassphrase,
  ListCredentials,
  ListAgentSources,
  ProbeSavedAgentSource,
  InspectAgentSource,
  CopyAgentSourcePublicKey,
  GetAgentSourceUsage,
  DeleteAgentSource,
  CreateAgentSource,
  UpdateAgentSource,
  GetAgentSource,
} from "../../../wailsjs/go/system/System";
import { SelectSSHKeyFile } from "../../../wailsjs/go/ssh/SSH";
import { credential_entity, ssh as ssh_models, system, ssh_agent_svc } from "../../../wailsjs/go/models";
import { AgentSourceRow } from "@/components/settings/AgentSourceRow";
import { SecretInput } from "@/components/SecretInput";
import { AgentSourceDialog, type AgentSourceDraft } from "@/components/settings/AgentSourceDialog";
import { type AgentEndpointType, type AgentSourceRuntimeStatus } from "@/components/settings/agentSource";

function generatePassword(length = 20): string {
  const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*";
  const values = crypto.getRandomValues(new Uint8Array(length));
  return Array.from(values, (v) => charset[v % charset.length]).join("");
}

export function CredentialManager() {
  const { t } = useTranslation();
  const [credentials, setCredentials] = useState<credential_entity.Credential[]>([]);
  // 挂载即拉取,loading 初值为 true;同步 setLoading(true) 只保留在事件路径(fetchCredentials)
  const [loading, setLoading] = useState(true);
  const [generateOpen, setGenerateOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [createPasswordOpen, setCreatePasswordOpen] = useState(false);
  const [editingCred, setEditingCred] = useState<credential_entity.Credential | null>(null);
  const [deleteCred, setDeleteCred] = useState<credential_entity.Credential | null>(null);
  const [deleteUsage, setDeleteUsage] = useState<string[]>([]);
  const [changePasswordCred, setChangePasswordCred] = useState<credential_entity.Credential | null>(null);
  const [changePassphraseCred, setChangePassphraseCred] = useState<credential_entity.Credential | null>(null);

  // ---- SSH Agent 来源（与密码/SSH 密钥混排显示） ----
  const [agentSources, setAgentSources] = useState<system.AgentSourceSummary[]>([]);
  const [agentLoading, setAgentLoading] = useState(true);
  const [agentStatuses, setAgentStatuses] = useState<Record<number, AgentSourceRuntimeStatus>>({});
  const [agentCounts, setAgentCounts] = useState<Record<number, number>>({});
  const [expandedSource, setExpandedSource] = useState<number | null>(null);
  const [agentIdentities, setAgentIdentities] = useState<Record<number, ssh_agent_svc.IdentitySummary[]>>({});
  const [agentIdentitiesLoading, setAgentIdentitiesLoading] = useState<Record<number, boolean>>({});
  const [agentIdentityError, setAgentIdentityError] = useState<Record<number, string>>({});
  const [agentDialog, setAgentDialog] = useState<{
    open: boolean;
    mode: "create" | "edit";
    sourceId?: number;
    initial: AgentSourceDraft | null;
  }>({ open: false, mode: "create", initial: null });
  const [deleteSource, setDeleteSource] = useState<system.AgentSourceSummary | null>(null);
  const [deleteSourceUsage, setDeleteSourceUsage] = useState(0);

  const loadCredentials = useCallback(async () => {
    try {
      const result = await ListCredentials();
      setCredentials(result || []);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchCredentials = useCallback(() => {
    setLoading(true);
    void loadCredentials();
  }, [loadCredentials]);

  useEffect(() => {
    void loadCredentials();
  }, [loadCredentials]);

  // ---- SSH Agent 来源加载 / 探测 / 检查 ----
  const probeAgentSource = useCallback(async (id: number) => {
    setAgentStatuses((prev) => ({ ...prev, [id]: "loading" }));
    try {
      const result = await ProbeSavedAgentSource(id);
      setAgentStatuses((prev) => ({ ...prev, [id]: result.status as AgentSourceRuntimeStatus }));
      if (result.status === "ok") {
        const count = result.identity_count;
        if (count) setAgentCounts((prev) => ({ ...prev, [id]: count }));
      }
    } catch {
      setAgentStatuses((prev) => ({ ...prev, [id]: "unavailable" }));
    }
  }, []);

  const loadAgentIdentities = useCallback(async (id: number) => {
    setAgentIdentitiesLoading((prev) => ({ ...prev, [id]: true }));
    setAgentIdentityError((prev) => {
      const next = { ...prev };
      delete next[id];
      return next;
    });
    try {
      const result = await InspectAgentSource(id);
      const identities = result?.identities || [];
      setAgentIdentities((prev) => ({ ...prev, [id]: identities }));
      // 用检查结果同步运行时状态（可用且包含身份 / 可用但为空）
      setAgentStatuses((prev) => ({ ...prev, [id]: identities.length > 0 ? "ok" : "empty" }));
      if (identities.length > 0) setAgentCounts((prev) => ({ ...prev, [id]: identities.length }));
    } catch (e) {
      setAgentIdentityError((prev) => ({ ...prev, [id]: String(e) }));
    } finally {
      setAgentIdentitiesLoading((prev) => ({ ...prev, [id]: false }));
    }
  }, []);

  const loadAgentSources = useCallback(async () => {
    setAgentLoading(true);
    try {
      const result = await ListAgentSources();
      const list = result || [];
      setAgentSources(list);
      for (const source of list) void probeAgentSource(source.id);
    } finally {
      setAgentLoading(false);
    }
  }, [probeAgentSource]);

  useEffect(() => {
    void loadAgentSources();
  }, [loadAgentSources]);

  const refreshAgentSource = useCallback(
    (id: number) => {
      void probeAgentSource(id);
      // 刷新后清空已缓存身份，避免与新的运行时状态错位
      setAgentIdentities((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      if (expandedSource === id) void loadAgentIdentities(id);
    },
    [expandedSource, loadAgentIdentities, probeAgentSource]
  );

  const toggleAgentExpand = useCallback(
    (id: number) => {
      if (expandedSource === id) {
        // 折叠时清除有界身份摘要（折叠/关闭/来源变更时清）
        setExpandedSource(null);
        setAgentIdentities((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });
        setAgentIdentityError((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });
      } else {
        setExpandedSource(id);
        void loadAgentIdentities(id);
      }
    },
    [expandedSource, loadAgentIdentities]
  );

  const handleCopyAgentPublicKey = useCallback(
    async (id: number, fingerprint: string) => {
      try {
        const pubKey = await CopyAgentSourcePublicKey(id, fingerprint);
        await navigator.clipboard.writeText(pubKey);
        notifyCopied(t("agentSource.copiedPublicKey"));
      } catch (e) {
        toast.error(String(e));
      }
    },
    [t]
  );

  const openCreateAgentDialog = useCallback(() => {
    setAgentDialog({ open: true, mode: "create", initial: null });
  }, []);

  const openEditAgentDialog = useCallback(async (source: system.AgentSourceSummary) => {
    try {
      const full = await GetAgentSource(source.id);
      setAgentDialog({
        open: true,
        mode: "edit",
        sourceId: source.id,
        initial: {
          name: full.name,
          type: full.endpoint_type as AgentEndpointType,
          endpoint: full.endpoint,
          description: full.description || "",
        },
      });
    } catch (e) {
      toast.error(String(e));
    }
  }, []);

  const handleSaveAgentSource = useCallback(
    async (input: ssh_agent_svc.SourceInput) => {
      try {
        if (agentDialog.mode === "edit" && agentDialog.sourceId) {
          await UpdateAgentSource(agentDialog.sourceId, input);
          notifySuccess(t("agentSource.updateSuccess"));
        } else {
          await CreateAgentSource(input);
          notifySuccess(t("agentSource.createSuccess"));
        }
        void loadAgentSources();
      } catch (e) {
        toast.error(String(e));
      }
    },
    [agentDialog.mode, agentDialog.sourceId, loadAgentSources, t]
  );

  const handleDeleteSourceClick = useCallback(async (source: system.AgentSourceSummary) => {
    try {
      const usage = await GetAgentSourceUsage(source.id);
      setDeleteSourceUsage(usage || 0);
    } catch {
      setDeleteSourceUsage(0);
    }
    setDeleteSource(source);
  }, []);

  const handleDeleteSourceConfirm = useCallback(async () => {
    if (!deleteSource) return;
    try {
      await DeleteAgentSource(deleteSource.id);
      notifySuccess(t("agentSource.deleteSuccess"));
      setDeleteSource(null);
      void loadAgentSources();
    } catch (e) {
      toast.error(String(e));
    }
  }, [deleteSource, loadAgentSources, t]);

  const handleDeleteClick = async (cred: credential_entity.Credential) => {
    try {
      const usage = await GetCredentialUsage(cred.id);
      setDeleteUsage(usage || []);
    } catch {
      setDeleteUsage([]);
    }
    setDeleteCred(cred);
  };

  const handleDeleteConfirm = async () => {
    if (!deleteCred) return;
    try {
      await DeleteCredential(deleteCred.id);
      notifySuccess(deleteCred.type === "ssh_key" ? t("sshKey.deleteSuccess") : t("credential.deleteSuccess"));
      setDeleteCred(null);
      fetchCredentials();
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleCopyPublicKey = async (id: number) => {
    try {
      const pubKey = await GetCredentialPublicKey(id);
      await navigator.clipboard.writeText(pubKey);
      notifyCopied(t("sshKey.copied"));
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleExportPrivateKey = async (id: number) => {
    try {
      const exported = await ExportSSHPrivateKey(id);
      if (exported) notifySuccess(t("sshKey.exportSuccess"));
    } catch (e) {
      toast.error(String(e));
    }
  };

  const keyTypeLabel = (keyType: string, keySize: number) => {
    switch (keyType) {
      case "rsa":
        return `RSA${keySize ? ` ${keySize}` : ""}`;
      case "ed25519":
        return "ED25519";
      case "ecdsa":
        return `ECDSA${keySize ? ` P-${keySize}` : ""}`;
      default:
        return keyType;
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setImportOpen(true)}>
            <Download className="h-3.5 w-3.5" />
            {t("sshKey.import")}
          </Button>
          <Button variant="outline" size="sm" className="h-8 gap-1.5" onClick={() => setGenerateOpen(true)}>
            <KeyRound className="h-3.5 w-3.5" />
            {t("sshKey.generate")}
          </Button>
          <Button size="sm" className="h-8 gap-1.5" onClick={() => setCreatePasswordOpen(true)}>
            <Plus className="h-3.5 w-3.5" />
            {t("credential.createPassword")}
          </Button>
          <Button size="sm" className="h-8 gap-1.5" onClick={openCreateAgentDialog}>
            <Cable className="h-3.5 w-3.5" />
            {t("agentSource.add")}
          </Button>
        </div>
      </div>

      {credentials.length === 0 && agentSources.length === 0 && !loading && !agentLoading ? (
        <div className="text-center py-8 text-sm text-muted-foreground">
          <Key className="h-8 w-8 mx-auto mb-2 opacity-30" />
          {t("sshKey.empty")}
        </div>
      ) : (
        <div className="space-y-2">
          {credentials.map((cred) => (
            <div key={cred.id} className="flex items-center justify-between p-3 rounded-lg border bg-card group">
              <div className="flex items-center gap-3 min-w-0">
                {cred.type === "ssh_key" ? (
                  <FileKey className="h-4 w-4 text-muted-foreground shrink-0" />
                ) : (
                  <Lock className="h-4 w-4 text-muted-foreground shrink-0" />
                )}
                <div className="min-w-0">
                  <div className="text-sm font-medium truncate">
                    {cred.name}
                    {cred.type === "ssh_key" && cred.comment && cred.comment !== cred.name && (
                      <span className="ml-2 text-xs text-muted-foreground font-normal">({cred.comment})</span>
                    )}
                  </div>
                  <div className="text-xs text-muted-foreground flex gap-2">
                    {cred.type === "ssh_key" ? (
                      <>
                        <span>{keyTypeLabel(cred.keyType || "", cred.keySize || 0)}</span>
                        <span className="font-mono truncate max-w-48">{cred.fingerprint}</span>
                      </>
                    ) : (
                      <>
                        {cred.username && <span>{cred.username}</span>}
                        {cred.description && <span className="truncate max-w-48">{cred.description}</span>}
                        {!cred.username && !cred.description && <span>{t("credential.passwords")}</span>}
                      </>
                    )}
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                {cred.type === "ssh_key" && (
                  <>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => handleCopyPublicKey(cred.id)}
                        >
                          <Copy className="h-3.5 w-3.5" />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("sshKey.copyPublicKey")}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => handleExportPrivateKey(cred.id)}
                        >
                          <Download className="h-3.5 w-3.5" />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("sshKey.exportPrivateKey")}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-7 w-7"
                          onClick={() => setChangePassphraseCred(cred)}
                        >
                          <KeyRound className="h-3.5 w-3.5" />
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("sshKey.changePassphrase")}</TooltipContent>
                    </Tooltip>
                  </>
                )}
                {cred.type === "password" && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        onClick={() => setChangePasswordCred(cred)}
                      >
                        <KeyRound className="h-3.5 w-3.5" />
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>{t("credential.changePassword")}</TooltipContent>
                  </Tooltip>
                )}
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setEditingCred(cred)}>
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>{t("action.edit")}</TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-muted-foreground hover:text-destructive"
                      onClick={() => handleDeleteClick(cred)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>{t("action.delete")}</TooltipContent>
                </Tooltip>
              </div>
            </div>
          ))}
          {agentSources.map((source) => (
            <AgentSourceRow
              key={source.id}
              source={source}
              status={agentStatuses[source.id] ?? "loading"}
              identityCount={agentCounts[source.id]}
              expanded={expandedSource === source.id}
              identities={expandedSource === source.id ? (agentIdentities[source.id] ?? null) : null}
              identitiesLoading={!!agentIdentitiesLoading[source.id]}
              identitiesError={expandedSource === source.id ? (agentIdentityError[source.id] ?? null) : null}
              onToggle={() => toggleAgentExpand(source.id)}
              onRefresh={() => refreshAgentSource(source.id)}
              onEdit={() => void openEditAgentDialog(source)}
              onDelete={() => void handleDeleteSourceClick(source)}
              onCopyPublicKey={(fingerprint) => void handleCopyAgentPublicKey(source.id, fingerprint)}
            />
          ))}
        </div>
      )}

      {/* Dialogs */}
      <GenerateKeyDialog open={generateOpen} onOpenChange={setGenerateOpen} onSuccess={fetchCredentials} />
      <ImportKeyDialog open={importOpen} onOpenChange={setImportOpen} onSuccess={fetchCredentials} />
      <CreatePasswordDialog
        open={createPasswordOpen}
        onOpenChange={setCreatePasswordOpen}
        onSuccess={fetchCredentials}
      />
      <EditCredentialDialog
        open={!!editingCred}
        onOpenChange={(open) => !open && setEditingCred(null)}
        credential={editingCred}
        onSuccess={fetchCredentials}
      />
      <ChangePasswordDialog
        open={!!changePasswordCred}
        onOpenChange={(open) => !open && setChangePasswordCred(null)}
        credential={changePasswordCred}
        onSuccess={fetchCredentials}
      />
      <ChangePassphraseDialog
        open={!!changePassphraseCred}
        onOpenChange={(open) => !open && setChangePassphraseCred(null)}
        credential={changePassphraseCred}
        onSuccess={fetchCredentials}
      />
      <AgentSourceDialog
        open={agentDialog.open}
        mode={agentDialog.mode}
        initial={agentDialog.initial}
        onOpenChange={(open) => {
          if (!open) setAgentDialog((prev) => ({ ...prev, open: false }));
        }}
        onSaved={handleSaveAgentSource}
      />

      {/* Agent 来源删除确认：被引用时拒绝删除（后端同样强制） */}
      <ConfirmDialog
        open={!!deleteSource}
        onOpenChange={(open) => {
          if (!open) setDeleteSource(null);
        }}
        title={t("agentSource.deleteConfirmTitle")}
        description={
          deleteSourceUsage > 0
            ? t("agentSource.deleteConfirmUsage", {
                name: deleteSource?.name ?? "",
                count: deleteSourceUsage,
              })
            : t("agentSource.deleteConfirm", { name: deleteSource?.name ?? "" })
        }
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={() => void handleDeleteSourceConfirm()}
      />

      {/* Delete confirmation */}
      <Dialog open={!!deleteCred} onOpenChange={(open) => !open && setDeleteCred(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("credential.deleteConfirmTitle")}</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {deleteUsage.length > 0
              ? t("credential.deleteConfirmUsage", {
                  name: deleteCred?.name,
                  assets: deleteUsage.join(", "),
                })
              : t("credential.deleteConfirm", { name: deleteCred?.name })}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteCred(null)}>
              {t("action.cancel")}
            </Button>
            <Button variant="destructive" onClick={handleDeleteConfirm}>
              {t("action.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function GenerateKeyDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [username, setUsername] = useState("");
  const [keyType, setKeyType] = useState("ed25519");
  const [keySize, setKeySize] = useState(4096);
  const [passphrase, setPassphrase] = useState("");
  const [saving, setSaving] = useState(false);

  // 关闭时重置为默认值;所有关闭路径都经 handleOpenChange,下次打开即是干净状态
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setName("");
      setComment("");
      setUsername("");
      setKeyType("ed25519");
      setKeySize(4096);
      setPassphrase("");
    }
    onOpenChange(next);
  };

  const handleGenerate = async () => {
    setSaving(true);
    try {
      await GenerateSSHKey(name, comment, keyType, keySize, passphrase, username);
      notifySuccess(t("sshKey.generateSuccess"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const sizeOptions = () => {
    switch (keyType) {
      case "rsa":
        return [
          { value: 2048, label: "2048 bits" },
          { value: 4096, label: "4096 bits" },
        ];
      case "ecdsa":
        return [
          { value: 256, label: "P-256" },
          { value: 384, label: "P-384" },
          { value: 521, label: "P-521" },
        ];
      default:
        return [];
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("sshKey.generateTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t("sshKey.name")}</Label>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("sshKey.namePlaceholder")} />
          </div>
          <div className="grid gap-2">
            <Label>{t("sshKey.comment")}</Label>
            <Input
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              placeholder={t("sshKey.commentPlaceholder")}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("credential.username")}</Label>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t("credential.usernamePlaceholder")}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("sshKey.type")}</Label>
            <Select value={keyType} onValueChange={setKeyType}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ed25519">ED25519</SelectItem>
                <SelectItem value="rsa">RSA</SelectItem>
                <SelectItem value="ecdsa">ECDSA</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {sizeOptions().length > 0 && (
            <div className="grid gap-2">
              <Label>{t("sshKey.size")}</Label>
              <Select value={String(keySize)} onValueChange={(v) => setKeySize(Number(v))}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {sizeOptions().map((opt) => (
                    <SelectItem key={opt.value} value={String(opt.value)}>
                      {opt.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="grid gap-2">
            <Label>{t("sshKey.passphrase")}</Label>
            <SecretInput
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              placeholder={t("sshKey.passphrasePlaceholderOptional")}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleGenerate} disabled={saving || !name}>
            {saving ? t("sshKey.generating") : t("sshKey.generate")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ImportKeyDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const dropZoneRef = useRef<HTMLDivElement | null>(null);
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [username, setUsername] = useState("");
  const [pemContent, setPemContent] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [mode, setMode] = useState<"file" | "pem">("file");
  const [selectedFile, setSelectedFile] = useState<SelectedImportFile | null>(null);
  const [passphraseVisible, setPassphraseVisible] = useState(false);
  const [nativeDragOver, setNativeDragOver] = useState(false);
  const [browserDragOver, setBrowserDragOver] = useState(false);
  const [saving, setSaving] = useState(false);

  // 关闭时重置为默认值;所有关闭路径都经 handleOpenChange,下次打开即是干净状态
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setName("");
      setComment("");
      setUsername("");
      setPemContent("");
      setPassphrase("");
      setMode("file");
      setSelectedFile(null);
      setPassphraseVisible(false);
      setNativeDragOver(false);
      setBrowserDragOver(false);
    }
    onOpenChange(next);
  };

  const setDefaultNameFromPath = useCallback((path: string) => {
    setName((current) => current || fileNameFromPath(path));
  }, []);

  const selectImportFile = useCallback(
    (file: SelectedImportFile) => {
      setSelectedFile(file);
      setDefaultNameFromPath(file.path);
    },
    [setDefaultNameFromPath]
  );

  useEffect(() => {
    if (!open || mode !== "file") return;
    const handler = (_x: number, _y: number, paths: string[]) => {
      setNativeDragOver(false);
      const path = paths.find(Boolean);
      if (!path) return;
      selectImportFile({
        path,
        name: fileNameFromPath(path),
        source: "drop",
      });
    };
    OnFileDrop(handler, true);
    return () => {
      OnFileDropOff();
    };
  }, [mode, open, selectImportFile]);

  useEffect(() => {
    const el = dropZoneRef.current;
    if (!el || !open || mode !== "file") return;
    const observer = new MutationObserver(() => {
      setNativeDragOver(el.classList.contains("wails-drop-target-active"));
    });
    observer.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [mode, open]);

  const handleSelectFile = async () => {
    try {
      const info = await SelectSSHKeyFile();
      if (!info) return;
      selectImportFile(importFileFromLocalKey(info, "dialog"));
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleClearFile = () => {
    setSelectedFile(null);
  };

  const handleImportFile = async () => {
    if (!selectedFile) return;
    setSaving(true);
    try {
      await ImportSSHKeyPath(name, comment, selectedFile.path, passphrase, username);
      notifySuccess(t("sshKey.importSuccess"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleImportPEM = async () => {
    setSaving(true);
    try {
      await ImportSSHKeyPEM(name, comment, pemContent, passphrase, username);
      notifySuccess(t("sshKey.importSuccess"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const handleBrowserDrop = async (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setBrowserDragOver(false);
    const file = event.dataTransfer.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      setMode("pem");
      setPemContent(text);
      setName((current) => current || file.name);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const pemStatus = getPEMStatus(pemContent);
  const fileReady = !!selectedFile;
  const canImport =
    !saving && !!name.trim() && (mode === "file" ? fileReady : pemContent.trim().length > 0 && pemStatus === "valid");
  const isDragOver = nativeDragOver || browserDragOver;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent size="md">
        <DialogHeader>
          <DialogTitle>{t("sshKey.importTitle")}</DialogTitle>
          <DialogDescription>{t("sshKey.importDescription")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4 py-2">
          <Tabs value={mode} onValueChange={(value) => setMode(value as "file" | "pem")}>
            <div className="flex flex-col gap-2">
              <Label>{t("sshKey.importSource")}</Label>
              <TabsList className="grid h-10 w-full grid-cols-2">
                <TabsTrigger value="file" className="gap-1.5">
                  <FileKey className="h-3.5 w-3.5" />
                  {t("sshKey.importFile")}
                </TabsTrigger>
                <TabsTrigger value="pem" className="gap-1.5">
                  <FileText className="h-3.5 w-3.5" />
                  {t("sshKey.importPEM")}
                </TabsTrigger>
              </TabsList>
            </div>

            <TabsContent value="file" className="mt-3">
              <div className="flex flex-col gap-3">
                <div
                  ref={dropZoneRef}
                  className={cn(
                    "flex min-h-40 flex-col justify-center gap-3 rounded-lg border border-dashed bg-muted/30 p-4 transition-colors",
                    isDragOver && "border-primary bg-primary/5"
                  )}
                  style={{ "--wails-drop-target": "drop" } as CSSProperties}
                  onDragEnter={(event) => {
                    event.preventDefault();
                    setBrowserDragOver(true);
                  }}
                  onDragOver={(event) => {
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "copy";
                    setBrowserDragOver(true);
                  }}
                  onDragLeave={() => setBrowserDragOver(false)}
                  onDrop={handleBrowserDrop}
                >
                  {selectedFile ? (
                    <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
                      <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg border bg-background text-primary">
                        <FileKey className="h-5 w-5" />
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <CheckCircle2 className="h-4 w-4 shrink-0 text-success" />
                          <div className="truncate text-sm font-medium">{selectedFile.name}</div>
                        </div>
                        <div className="mt-1 truncate font-mono text-xs text-muted-foreground">{selectedFile.path}</div>
                        <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
                          <span className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-muted-foreground">
                            {selectedFile.isEncrypted ? (
                              <Lock className="h-3 w-3" />
                            ) : (
                              <CheckCircle2 className="h-3 w-3" />
                            )}
                            {selectedFileLabel(selectedFile, t)}
                          </span>
                          {selectedFile.source === "drop" && (
                            <span className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-muted-foreground">
                              <Upload className="h-3 w-3" />
                              {t("sshKey.dropped")}
                            </span>
                          )}
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-2 sm:justify-end">
                        <Button type="button" variant="outline" size="sm" onClick={handleSelectFile}>
                          {t("sshKey.changeFile")}
                        </Button>
                        <Button type="button" variant="ghost" size="sm" onClick={handleClearFile}>
                          {t("sshKey.removeFile")}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex flex-col items-center justify-center gap-3 text-center">
                      <div className="flex h-10 w-10 items-center justify-center rounded-lg border bg-background text-primary">
                        <Upload className="h-5 w-5" />
                      </div>
                      <div className="flex flex-col gap-1">
                        <div className="text-sm font-medium">
                          {isDragOver ? t("sshKey.dropActiveTitle") : t("sshKey.dropTitle")}
                        </div>
                        <div className="text-xs text-muted-foreground">{t("sshKey.dropDescription")}</div>
                      </div>
                      <Button type="button" variant="outline" size="sm" onClick={handleSelectFile}>
                        {t("sshKey.chooseFile")}
                      </Button>
                    </div>
                  )}
                </div>
              </div>
            </TabsContent>

            <TabsContent value="pem" className="mt-3">
              <div className="flex flex-col gap-2">
                <Label>{t("sshKey.privateKeyContent")}</Label>
                <Textarea
                  value={pemContent}
                  onChange={(e) => setPemContent(e.target.value)}
                  placeholder={t("sshKey.pemPlaceholder")}
                  rows={8}
                  className="min-h-44 font-mono text-xs"
                />
                {pemStatus !== "empty" && (
                  <div
                    className={cn(
                      "flex items-center gap-1.5 text-xs",
                      pemStatus === "valid" ? "text-success" : "text-warning"
                    )}
                  >
                    {pemStatus === "valid" ? (
                      <CheckCircle2 className="h-3.5 w-3.5" />
                    ) : (
                      <AlertCircle className="h-3.5 w-3.5" />
                    )}
                    {t(pemStatus === "valid" ? "sshKey.pemDetected" : "sshKey.pemInvalid")}
                  </div>
                )}
              </div>
            </TabsContent>
          </Tabs>

          <div className="flex flex-col gap-3">
            <div className="text-sm font-medium">{t("sshKey.saveInfo")}</div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label>{t("sshKey.name")}</Label>
                <Input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t("sshKey.namePlaceholder")}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>{t("credential.username")}</Label>
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder={t("credential.usernamePlaceholder")}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>{t("sshKey.passphrase")}</Label>
                <SecretInput
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                  placeholder={t("sshKey.passphrasePlaceholder")}
                  reveal={passphraseVisible}
                  onRevealChange={setPassphraseVisible}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>{t("sshKey.comment")}</Label>
                <Input
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                  placeholder={t("sshKey.commentPlaceholder")}
                />
              </div>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={mode === "file" ? handleImportFile : handleImportPEM} disabled={!canImport}>
            {saving ? t("sshKey.importing") : t("sshKey.import")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type ImportModeSource = "dialog" | "drop";

interface SelectedImportFile {
  path: string;
  name: string;
  source: ImportModeSource;
  keyType?: string;
  fingerprint?: string;
  isEncrypted?: boolean;
}

function importFileFromLocalKey(info: ssh_models.LocalSSHKeyInfo, source: ImportModeSource): SelectedImportFile {
  return {
    path: info.path,
    name: fileNameFromPath(info.path),
    source,
    keyType: info.keyType,
    fingerprint: info.fingerprint,
    isEncrypted: info.isEncrypted,
  };
}

function fileNameFromPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts.at(-1) || path || "id_opskat";
}

function getPEMStatus(content: string): "empty" | "valid" | "invalid" {
  const trimmed = content.trim();
  if (!trimmed) return "empty";
  return /-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(trimmed) && /-----END [A-Z ]*PRIVATE KEY-----/.test(trimmed)
    ? "valid"
    : "invalid";
}

function selectedFileLabel(
  file: SelectedImportFile,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (file.keyType) {
    return t(file.isEncrypted ? "sshKey.fileMetadataEncrypted" : "sshKey.fileMetadata", {
      type: file.keyType.toUpperCase(),
      fingerprint: file.fingerprint || t("sshKey.fingerprintPending"),
    });
  }
  return t(file.source === "drop" ? "sshKey.droppedFilePending" : "sshKey.fingerprintPending");
}

function CreatePasswordDialog({
  open,
  onOpenChange,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [description, setDescription] = useState("");
  const [saving, setSaving] = useState(false);
  const [visible, setVisible] = useState(false);

  // 关闭时重置为默认值;所有关闭路径都经 handleOpenChange,下次打开即是干净状态
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setName("");
      setUsername("");
      setPassword("");
      setDescription("");
      setVisible(false);
    }
    onOpenChange(next);
  };

  const handleCreate = async () => {
    setSaving(true);
    try {
      await CreatePasswordCredential(name, username, password, description);
      notifySuccess(t("credential.createSuccess"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("credential.createPasswordTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t("credential.name")}</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("credential.namePlaceholder")}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("credential.username")}</Label>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t("credential.usernamePlaceholder")}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("credential.password")}</Label>
            <SecretInput
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("credential.passwordPlaceholder")}
              reveal={visible}
              onRevealChange={setVisible}
              actions={
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  title={t("credential.randomPassword")}
                  onClick={() => {
                    setPassword(generatePassword());
                    setVisible(true);
                  }}
                >
                  <Shuffle className="h-3.5 w-3.5" />
                </Button>
              }
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("credential.description")}</Label>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("credential.descriptionPlaceholder")}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleCreate} disabled={saving || !name || !password}>
            {saving ? t("action.saving") : t("action.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EditCredentialDialog({
  open,
  onOpenChange,
  credential,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  credential: credential_entity.Credential | null;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [comment, setComment] = useState("");
  const [description, setDescription] = useState("");
  const [username, setUsername] = useState("");
  const [saving, setSaving] = useState(false);

  // 打开时从 credential 回填:渲染期对比上次值,等价于原 [open, credential] effect
  const [prevSync, setPrevSync] = useState<{ open: boolean; credential?: credential_entity.Credential | null }>({
    open: false,
  });
  if (open !== prevSync.open || credential !== prevSync.credential) {
    setPrevSync({ open, credential });
    if (open && credential) {
      setName(credential.name);
      setComment(credential.comment || "");
      setDescription(credential.description || "");
      setUsername(credential.username || "");
    }
  }

  const handleSave = async () => {
    if (!credential) return;
    setSaving(true);
    try {
      await UpdateCredential(credential.id, name, comment, description, username);
      notifySuccess(credential.type === "ssh_key" ? t("sshKey.updateSuccess") : t("credential.updateSuccess"));
      onOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  const isSSHKey = credential?.type === "ssh_key";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{isSSHKey ? t("sshKey.editTitle") : t("credential.editPasswordTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{isSSHKey ? t("sshKey.name") : t("credential.name")}</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={isSSHKey ? t("sshKey.namePlaceholder") : t("credential.namePlaceholder")}
            />
          </div>
          {isSSHKey ? (
            <>
              <div className="grid gap-2">
                <Label>{t("sshKey.comment")}</Label>
                <Input
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                  placeholder={t("sshKey.commentPlaceholder")}
                />
              </div>
              <div className="grid gap-2">
                <Label>{t("credential.username")}</Label>
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder={t("credential.usernamePlaceholder")}
                />
              </div>
            </>
          ) : (
            <>
              <div className="grid gap-2">
                <Label>{t("credential.username")}</Label>
                <Input
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder={t("credential.usernamePlaceholder")}
                />
              </div>
              <div className="grid gap-2">
                <Label>{t("credential.description")}</Label>
                <Input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder={t("credential.descriptionPlaceholder")}
                />
              </div>
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleSave} disabled={saving || !name}>
            {saving ? t("action.saving") : t("action.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChangePasswordDialog({
  open,
  onOpenChange,
  credential,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  credential: credential_entity.Credential | null;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const [password, setPassword] = useState("");
  const [saving, setSaving] = useState(false);
  const [visible, setVisible] = useState(false);

  // 关闭时重置为默认值;所有关闭路径都经 handleOpenChange,下次打开即是干净状态
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setPassword("");
      setVisible(false);
    }
    onOpenChange(next);
  };

  const handleSave = async () => {
    if (!credential) return;
    setSaving(true);
    try {
      await UpdateCredentialPassword(credential.id, password);
      notifySuccess(t("credential.passwordChanged"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("credential.changePasswordTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t("credential.newPassword")}</Label>
            <SecretInput
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("credential.newPasswordPlaceholder")}
              reveal={visible}
              onRevealChange={setVisible}
              actions={
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7"
                  title={t("credential.randomPassword")}
                  onClick={() => {
                    setPassword(generatePassword());
                    setVisible(true);
                  }}
                >
                  <Shuffle className="h-3.5 w-3.5" />
                </Button>
              }
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleSave} disabled={saving || !password}>
            {saving ? t("action.saving") : t("action.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ChangePassphraseDialog({
  open,
  onOpenChange,
  credential,
  onSuccess,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  credential: credential_entity.Credential | null;
  onSuccess: () => void;
}) {
  const { t } = useTranslation();
  const [oldPassphrase, setOldPassphrase] = useState("");
  const [newPassphrase, setNewPassphrase] = useState("");
  const [confirmPassphrase, setConfirmPassphrase] = useState("");
  const [saving, setSaving] = useState(false);
  const [visibleOld, setVisibleOld] = useState(false);
  const [visibleNew, setVisibleNew] = useState(false);
  const [visibleConfirm, setVisibleConfirm] = useState(false);

  // 关闭时重置为默认值;所有关闭路径都经 handleOpenChange,下次打开即是干净状态
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      setOldPassphrase("");
      setNewPassphrase("");
      setConfirmPassphrase("");
      setVisibleOld(false);
      setVisibleNew(false);
      setVisibleConfirm(false);
    }
    onOpenChange(next);
  };

  const handleSave = async () => {
    if (!credential) return;
    if (newPassphrase !== confirmPassphrase) {
      toast.error(t("sshKey.passphraseMismatch"));
      return;
    }
    setSaving(true);
    try {
      await UpdateCredentialPassphrase(credential.id, oldPassphrase, newPassphrase);
      notifySuccess(t("sshKey.passphraseChanged"));
      handleOpenChange(false);
      onSuccess();
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("sshKey.changePassphraseTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t("sshKey.oldPassphrase")}</Label>
            <p className="text-xs text-muted-foreground">{t("sshKey.oldPassphraseHint")}</p>
            <SecretInput
              value={oldPassphrase}
              onChange={(e) => setOldPassphrase(e.target.value)}
              placeholder={t("sshKey.passphrasePlaceholder")}
              reveal={visibleOld}
              onRevealChange={setVisibleOld}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("sshKey.newPassphrase")}</Label>
            <SecretInput
              value={newPassphrase}
              onChange={(e) => setNewPassphrase(e.target.value)}
              placeholder={t("sshKey.passphrasePlaceholder")}
              reveal={visibleNew}
              onRevealChange={setVisibleNew}
            />
          </div>
          <div className="grid gap-2">
            <Label>{t("sshKey.confirmPassphrase")}</Label>
            <SecretInput
              value={confirmPassphrase}
              onChange={(e) => setConfirmPassphrase(e.target.value)}
              placeholder={t("sshKey.passphrasePlaceholder")}
              reveal={visibleConfirm}
              onRevealChange={setVisibleConfirm}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? t("action.saving") : t("action.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
