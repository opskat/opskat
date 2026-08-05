import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import { CheckCircle2, Loader2, PlugZap, X, XCircle } from "lucide-react";
import { ProbeAgentSource } from "../../../wailsjs/go/system/System";
import type { ssh_agent_svc } from "../../../wailsjs/go/models";
import { usePlatform } from "@/hooks/usePlatform";
import {
  AGENT_ENDPOINT_TYPES,
  endpointDefaultValue,
  endpointKindLabel,
  isEndpointStructurallyValid,
  isEndpointSupported,
  type AgentEndpointType,
} from "./agentSource";

export interface AgentSourceDraft {
  name: string;
  type: AgentEndpointType;
  endpoint: string;
  description: string;
}

type ProbeState = { state: "idle" | "loading" | "success" | "error"; count?: number };

/**
 * 来源对话框：名称 / 端点类型 / 端点 / 可选描述。
 * - 顶层添加打开空白字段；候选项添加打开预填字段（initial）。
 * - 名称与端点结构未完成前禁用保存；保存不要求探测成功；探测结果不持久化来源。
 * - 当前平台不兼容的端点类型在手动创建时禁用，编辑导入数据时保留。
 */
export function AgentSourceDialog({
  open,
  mode,
  initial,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  mode: "create" | "edit";
  initial?: AgentSourceDraft | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (input: ssh_agent_svc.SourceInput) => Promise<void>;
}) {
  const { t } = useTranslation();
  const platform = usePlatform();
  const [name, setName] = useState("");
  const [type, setType] = useState<AgentEndpointType>("environment");
  const [endpoint, setEndpoint] = useState("");
  const [description, setDescription] = useState("");
  const [probe, setProbe] = useState<ProbeState>({ state: "idle" });
  const [saving, setSaving] = useState(false);

  // 打开时从 initial 回填（空白创建 / 候选预填 / 编辑既有来源），并重置探测结果。
  // 渲染期对比上次值，等价于原 [open, initial] effect（EditCredentialDialog 同款模式）。
  const [prevSync, setPrevSync] = useState<{ open: boolean; initial?: AgentSourceDraft | null }>({ open: false });
  if (open !== prevSync.open || initial !== prevSync.initial) {
    setPrevSync({ open, initial });
    if (open) {
      setName(initial?.name ?? "");
      setType(initial?.type ?? "environment");
      setEndpoint(initial?.endpoint ?? "");
      setDescription(initial?.description ?? "");
      setProbe({ state: "idle" });
    }
  }

  const handleTypeChange = (next: AgentEndpointType) => {
    setType(next);
    setEndpoint(endpointDefaultValue(next));
    setProbe({ state: "idle" });
  };

  const valid = name.trim().length > 0 && isEndpointStructurallyValid(type, endpoint);

  const handleProbe = async () => {
    setProbe({ state: "loading" });
    try {
      const result = await ProbeAgentSource(type, endpoint);
      if (result.status === "ok") {
        setProbe({ state: "success", count: result.identity_count });
      } else {
        setProbe({ state: "error" });
      }
    } catch {
      setProbe({ state: "error" });
    }
  };

  const handleSave = async () => {
    if (!valid || saving) return;
    setSaving(true);
    try {
      await onSaved({
        name: name.trim(),
        endpoint_type: type,
        endpoint: endpoint.trim(),
        description: description.trim(),
      });
      onOpenChange(false);
    } finally {
      setSaving(false);
    }
  };

  const endpointLabel =
    type === "environment"
      ? t("agentSource.endpointEnv")
      : type === "unix_socket"
        ? t("agentSource.endpointUnix")
        : t("agentSource.endpointWindows");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{mode === "create" ? t("agentSource.addTitle") : t("agentSource.editTitle")}</DialogTitle>
          <DialogDescription>{t("agentSource.addDescription")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-2">
            <Label htmlFor="agent-source-name">{t("agentSource.name")}</Label>
            <Input
              id="agent-source-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("agentSource.namePlaceholder")}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="agent-source-type">{t("agentSource.type")}</Label>
            <Select value={type} onValueChange={(value) => handleTypeChange(value as AgentEndpointType)}>
              <SelectTrigger id="agent-source-type" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {AGENT_ENDPOINT_TYPES.map((option) => {
                  const disabled = mode === "create" && !isEndpointSupported(option, platform);
                  return (
                    <SelectItem key={option} value={option} disabled={disabled}>
                      {endpointKindLabel(option, t)}
                    </SelectItem>
                  );
                })}
              </SelectContent>
            </Select>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="agent-source-endpoint">{endpointLabel}</Label>
            <Input
              id="agent-source-endpoint"
              value={endpoint}
              onChange={(e) => setEndpoint(e.target.value)}
              className="font-mono text-xs"
            />
            <p className="text-[11px] leading-4 text-muted-foreground">{t("agentSource.saveHint")}</p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="agent-source-description">{t("agentSource.description")}</Label>
            <Input
              id="agent-source-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("agentSource.descriptionPlaceholder")}
            />
          </div>
          {probe.state === "success" && (
            <div className="flex items-start gap-2 rounded-md border border-success/30 bg-success/10 px-3 py-2 text-xs text-success">
              <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div>
                <span className="font-medium">{t("agentSource.testSuccess", { count: probe.count ?? 0 })}</span>
              </div>
            </div>
          )}
          {probe.state === "error" && (
            <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div>
                <span className="font-medium">{t("agentSource.testFail")}</span>
              </div>
            </div>
          )}
          <Button
            variant="outline"
            size="sm"
            className="w-fit gap-1.5"
            onClick={() => void handleProbe()}
            disabled={probe.state === "loading" || !isEndpointStructurallyValid(type, endpoint)}
          >
            {probe.state === "loading" ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <PlugZap className="h-3.5 w-3.5" />
            )}
            {t("agentSource.test")}
          </Button>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          <Button onClick={() => void handleSave()} disabled={!valid || saving}>
            {saving ? t("action.saving") : t("action.save")}
          </Button>
        </DialogFooter>
        <Button
          variant="ghost"
          size="icon"
          className="absolute top-4 right-4 h-6 w-6"
          onClick={() => onOpenChange(false)}
          aria-label={t("action.close")}
        >
          <X className="h-4 w-4" />
        </Button>
      </DialogContent>
    </Dialog>
  );
}
