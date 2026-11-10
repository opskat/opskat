import { useState, useEffect, useCallback } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import {
  Plus,
  Play,
  Square,
  Pencil,
  Trash2,
  CircleCheck,
  CircleAlert,
  CircleDot,
  CircleMinus,
  Loader2,
} from "lucide-react";
import {
  Button,
  Input,
  Label,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  ConfirmDialog,
} from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { AsyncState } from "@/components/common/AsyncState";
import { notifySuccess } from "@/lib/notify";
import { ListForwardConfigs } from "../../../wailsjs/go/ssh/SSH";
import {
  CreateForwardConfig,
  UpdateForwardConfig,
  DeleteForwardConfig,
  StartForwardConfig,
  StopForwardConfig,
} from "../../../wailsjs/go/ssh/SSH";
import { ssh as ssh_models, forward_entity } from "../../../wailsjs/go/models";

// 编辑中的规则（无 id）
interface EditRule {
  type: string;
  localHost: string;
  localPort: string;
  remoteHost: string;
  remotePort: string;
}

const emptyRule = (): EditRule => ({
  type: "local",
  localHost: "127.0.0.1",
  localPort: "",
  remoteHost: "127.0.0.1",
  remotePort: "",
});

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

const isPort = (v: string) => /^\d+$/.test(v.trim()) && Number(v) >= 1 && Number(v) <= 65535;

// 按规则类型校验，返回错误文案键；通过返回 null
function validateRule(r: EditRule): string | null {
  if (!isPort(r.localPort)) return "forward.ruleError.localPort";
  if (r.type !== "dynamic" && (!r.remoteHost.trim() || !isPort(r.remotePort))) return "forward.ruleError.remote";
  return null;
}

export function PortForwardPage() {
  const { t } = useTranslation();
  const [configs, setConfigs] = useState<ssh_models.ForwardConfigWithStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [ruleErrors, setRuleErrors] = useState<Record<number, string>>({});
  const [pendingIds, setPendingIds] = useState<Set<number>>(new Set());

  const withPending = useCallback(
    async (id: number, failKey: string, fn: () => Promise<void>) => {
      setPendingIds((prev) => {
        const next = new Set(prev);
        next.add(id);
        return next;
      });
      try {
        await fn();
      } catch (e) {
        toast.error(`${t(failKey)}: ${errMsg(e)}`);
      } finally {
        setPendingIds((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    },
    [t]
  );

  // 编辑弹窗
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editId, setEditId] = useState<number | null>(null); // null = 新建
  const [editName, setEditName] = useState("");
  const [editAssetId, setEditAssetId] = useState(0);
  const [editRules, setEditRules] = useState<EditRule[]>([emptyRule()]);

  // 加载配置列表:setState 全在 promise 回调中,effect 可直接调用(loading 初值即为 true)
  const loadConfigs = useCallback(() => {
    return ListForwardConfigs()
      .then((list) => {
        setConfigs(list || []);
        setLoadError(null);
      })
      .catch((e) => setLoadError(errMsg(e)))
      .finally(() => setLoading(false));
  }, []);

  // 手动/操作后刷新(事件路径):同步置 loading 后拉取
  const refresh = useCallback(() => {
    setLoading(true);
    return loadConfigs();
  }, [loadConfigs]);

  useEffect(() => {
    void loadConfigs();
  }, [loadConfigs]);

  const openCreate = () => {
    setEditId(null);
    setEditName("");
    setEditAssetId(0);
    setEditRules([emptyRule()]);
    setRuleErrors({});
    setDialogOpen(true);
  };

  const openEdit = (cfg: ssh_models.ForwardConfigWithStatus) => {
    setEditId(cfg.id);
    setEditName(cfg.name);
    setEditAssetId(cfg.assetId);
    setEditRules(
      cfg.rules.length > 0
        ? cfg.rules.map((r) => ({
            type: r.type,
            localHost: r.localHost,
            localPort: String(r.localPort),
            remoteHost: r.remoteHost,
            remotePort: r.type === "dynamic" ? "" : String(r.remotePort),
          }))
        : [emptyRule()]
    );
    setRuleErrors({});
    setDialogOpen(true);
  };

  const handleSave = async () => {
    if (saving || !editName || !editAssetId) return;
    const errors: Record<number, string> = {};
    editRules.forEach((r, i) => {
      const err = validateRule(r);
      if (err) errors[i] = err;
    });
    setRuleErrors(errors);
    if (Object.keys(errors).length > 0) return;
    const rules = editRules.map(
      (r) =>
        new forward_entity.ForwardRule({
          type: r.type,
          localHost: r.localHost,
          localPort: parseInt(r.localPort, 10),
          remoteHost: r.type === "dynamic" ? "" : r.remoteHost,
          remotePort: r.type === "dynamic" ? 0 : parseInt(r.remotePort, 10),
        })
    );
    setSaving(true);
    try {
      if (editId) {
        await UpdateForwardConfig(editId, editName, editAssetId, rules);
      } else {
        await CreateForwardConfig(editName, editAssetId, rules);
      }
    } catch (e) {
      toast.error(`${t("forward.saveFailed")}: ${errMsg(e)}`);
      return;
    } finally {
      setSaving(false);
    }
    setDialogOpen(false);
    notifySuccess(t("forward.saved"));
    void refresh();
  };

  const [deleteTarget, setDeleteTarget] = useState<{ id: number; name: string } | null>(null);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await DeleteForwardConfig(deleteTarget.id);
    } catch (e) {
      toast.error(`${t("forward.deleteFailed")}: ${errMsg(e)}`);
      return;
    }
    setDeleteTarget(null);
    void refresh();
  };

  const handleStart = (id: number) =>
    withPending(id, "forward.startFailed", async () => {
      await StartForwardConfig(id);
      await refresh();
    });

  const handleStop = (id: number) =>
    withPending(id, "forward.stopFailed", async () => {
      await StopForwardConfig(id);
      await refresh();
    });

  const handleAssetChange = (cfg: ssh_models.ForwardConfigWithStatus, assetId: number) =>
    withPending(cfg.id, "forward.assetChangeFailed", async () => {
      const wasRunning = cfg.status !== "stopped";
      const rules = cfg.rules.map(
        (r) =>
          new forward_entity.ForwardRule({
            type: r.type,
            localHost: r.localHost,
            localPort: r.localPort,
            remoteHost: r.remoteHost,
            remotePort: r.remotePort,
          })
      );
      await UpdateForwardConfig(cfg.id, cfg.name, assetId, rules);
      if (wasRunning) {
        await StartForwardConfig(cfg.id);
      }
      await refresh();
    });

  const updateRule = (idx: number, field: keyof EditRule, value: string) => {
    setEditRules((prev) => prev.map((r, i) => (i === idx ? { ...r, [field]: value } : r)));
    setRuleErrors((prev) => {
      const { [idx]: _drop, ...rest } = prev;
      return rest;
    });
  };

  const statusIcon = (status: string) => {
    switch (status) {
      case "running":
        return <CircleCheck className="h-4 w-4 text-success" />;
      case "partial":
        return <CircleDot className="h-4 w-4 text-warning" />;
      case "error":
        return <CircleAlert className="h-4 w-4 text-destructive" />;
      default:
        return <CircleMinus className="h-4 w-4 text-muted-foreground" />;
    }
  };

  const statusLabel = (status: string) => {
    switch (status) {
      case "running":
        return t("forward.running");
      case "partial":
        return t("forward.partial");
      case "error":
        return t("forward.error");
      default:
        return t("forward.stopped");
    }
  };

  return (
    <div className="absolute inset-0 bg-background flex flex-col">
      <div className="px-4 py-3 border-b flex items-center justify-between">
        <h2 className="font-semibold">{t("nav.forward")}</h2>
        <Button size="sm" className="gap-1" onClick={openCreate}>
          <Plus className="h-3.5 w-3.5" />
          {t("forward.create")}
        </Button>
      </div>

      <div className="flex-1 overflow-y-auto p-4">
        <AsyncState
          status={loading ? "loading" : loadError ? "error" : "ready"}
          error={loadError}
          onRetry={() => void refresh()}
          empty={configs.length === 0 ? t("forward.empty") : undefined}
          className="py-12"
        >
          <div className="grid gap-4 max-w-3xl mx-auto">
            {configs.map((cfg) => {
              const pending = pendingIds.has(cfg.id);
              return (
                <div key={cfg.id} className="border rounded-lg p-4">
                  {/* 卡片头部 */}
                  <div className="flex items-center gap-3 mb-3">
                    <span className="font-medium text-sm flex-1">{cfg.name}</span>

                    {/* 资产选择（直接在卡片上切换） */}
                    <div className={pending ? "pointer-events-none opacity-50" : undefined}>
                      <AssetSelect
                        value={cfg.assetId}
                        onValueChange={(v) => handleAssetChange(cfg, v)}
                        filterType="ssh"
                        className="h-7 w-44 text-xs"
                      />
                    </div>

                    {/* 状态 */}
                    <div className="flex items-center gap-1 text-xs min-w-20">
                      {pending ? (
                        <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                      ) : (
                        statusIcon(cfg.status)
                      )}
                      <span>{pending ? t("forward.processing") : statusLabel(cfg.status)}</span>
                    </div>

                    {/* 操作按钮 */}
                    {cfg.status === "stopped" ? (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => handleStart(cfg.id)}
                        title={t("forward.start")}
                        disabled={pending}
                      >
                        {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                      </Button>
                    ) : (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => handleStop(cfg.id)}
                        title={t("forward.stop")}
                        disabled={pending}
                      >
                        {pending ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                          <Square className="h-3.5 w-3.5" />
                        )}
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => openEdit(cfg)}
                      title={t("forward.edit")}
                      disabled={pending}
                    >
                      <Pencil className="h-3.5 w-3.5" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => setDeleteTarget({ id: cfg.id, name: cfg.name })}
                      title={t("forward.delete")}
                      disabled={pending}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>

                  {/* 规则列表 */}
                  <div className="space-y-1">
                    {cfg.rules.map((rule) => {
                      const prefix = t(`forward.type.${rule.type}`);
                      const label =
                        rule.type === "dynamic"
                          ? `${prefix}  ${rule.localHost}:${rule.localPort}`
                          : `${prefix}  ${rule.localHost}:${rule.localPort} \u2192 ${rule.remoteHost}:${rule.remotePort}`;
                      return (
                        <div key={rule.id} className="flex items-center gap-2 text-xs font-mono text-muted-foreground">
                          {rule.status === "running" ? (
                            <CircleCheck className="h-3 w-3 text-success shrink-0" />
                          ) : rule.status === "error" ? (
                            <span title={rule.error} className="cursor-help shrink-0">
                              <CircleAlert className="h-3 w-3 text-destructive" />
                            </span>
                          ) : (
                            <CircleMinus className="h-3 w-3 text-muted-foreground/40 shrink-0" />
                          )}
                          <span>{label}</span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        </AsyncState>
      </div>

      {/* 新建/编辑弹窗 */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editId ? t("forward.edit") : t("forward.create")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-1.5">
              <Label>{t("forward.name")}</Label>
              <Input
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
                placeholder={t("forward.namePlaceholder")}
              />
            </div>
            <div className="space-y-1.5">
              <Label>{t("forward.asset")}</Label>
              <AssetSelect
                value={editAssetId}
                onValueChange={setEditAssetId}
                filterType="ssh"
                placeholder={t("forward.selectAsset")}
              />
            </div>
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label>{t("forward.rules")}</Label>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-6 gap-1 text-xs"
                  onClick={() => setEditRules([...editRules, emptyRule()])}
                >
                  <Plus className="h-3 w-3" />
                  {t("forward.addRule")}
                </Button>
              </div>
              {editRules.map((rule, i) => (
                <div key={i} className="space-y-0.5">
                  <div className="flex items-center gap-1.5 text-xs">
                    <Select value={rule.type} onValueChange={(v) => updateRule(i, "type", v)}>
                      <SelectTrigger className="h-7 w-32 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="local">{t("forward.type.local")}</SelectItem>
                        <SelectItem value="remote">{t("forward.type.remote")}</SelectItem>
                        <SelectItem value="dynamic">{t("forward.type.dynamic")}</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      className="h-7 text-xs w-24"
                      value={rule.localHost}
                      onChange={(e) => updateRule(i, "localHost", e.target.value)}
                      placeholder="127.0.0.1"
                    />
                    <span>:</span>
                    <Input
                      className="h-7 text-xs w-16"
                      value={rule.localPort}
                      onChange={(e) => updateRule(i, "localPort", e.target.value)}
                      placeholder={t("forward.port")}
                    />
                    {rule.type !== "dynamic" && (
                      <>
                        <span className="text-muted-foreground">&rarr;</span>
                        <Input
                          className="h-7 text-xs w-24"
                          value={rule.remoteHost}
                          onChange={(e) => updateRule(i, "remoteHost", e.target.value)}
                          placeholder="127.0.0.1"
                        />
                        <span>:</span>
                        <Input
                          className="h-7 text-xs w-16"
                          value={rule.remotePort}
                          onChange={(e) => updateRule(i, "remotePort", e.target.value)}
                          placeholder={t("forward.port")}
                        />
                      </>
                    )}
                    {editRules.length > 1 && (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => setEditRules(editRules.filter((_, j) => j !== i))}
                      >
                        <Trash2 className="h-3 w-3" />
                      </Button>
                    )}
                  </div>
                  {ruleErrors[i] && (
                    <p role="alert" className="text-xs text-destructive">
                      {t(ruleErrors[i])}
                    </p>
                  )}
                </div>
              ))}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>
              {t("forward.cancel")}
            </Button>
            <Button onClick={handleSave} disabled={!editName || !editAssetId || saving}>
              {t("forward.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirm */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t("forward.deleteConfirmTitle")}
        description={t("forward.deleteConfirmDesc", { name: deleteTarget?.name ?? "" })}
        cancelText={t("action.cancel")}
        confirmText={t("action.delete")}
        onConfirm={handleDelete}
      />
    </div>
  );
}
