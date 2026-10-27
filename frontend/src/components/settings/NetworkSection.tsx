import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Info, Loader2, Save } from "lucide-react";
import { toast } from "sonner";
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  cn,
} from "@opskat/ui";
import { SecretInput } from "@/components/SecretInput";
import { notifySuccess } from "@/lib/notify";
import { GetNetworkProxySettings, SaveNetworkProxySettings } from "../../../wailsjs/go/system/System";
import type { system } from "../../../wailsjs/go/models";
import {
  networkProxyFormFromView,
  toNetworkProxyInput,
  validateNetworkProxyForm,
  type FieldCode,
  type NetworkProxyForm,
  type NetworkProxyMode,
  type NetworkProxyType,
} from "./networkProxyForm";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));
const invalidCls = "border-destructive focus-visible:ring-destructive/40";

type Field = "host" | "port" | "noProxy";

// 「网络」页签里的「网络代理」卡片。只有点「保存」才提交；切换模式、编辑字段都只改本地表单。
export function NetworkSection() {
  const { t } = useTranslation();
  const [form, setForm] = useState<NetworkProxyForm | null>(null);
  const [passwordSet, setPasswordSet] = useState(false);
  const [envVars, setEnvVars] = useState<system.NetworkProxyEnvVar[]>([]);
  const [touched, setTouched] = useState<Partial<Record<Field, boolean>>>({});
  const [saving, setSaving] = useState(false);

  const apply = useCallback((view: system.NetworkProxyView) => {
    setForm(networkProxyFormFromView(view));
    setPasswordSet(view.passwordSet);
    setEnvVars(view.envVars);
  }, []);

  useEffect(() => {
    GetNetworkProxySettings()
      .then(apply)
      .catch((e: unknown) => toast.error(errMsg(e)));
  }, [apply]);

  if (!form) return null;

  const errors = validateNetworkProxyForm(form);
  const update = (patch: Partial<NetworkProxyForm>, field?: Field) => {
    setForm({ ...form, ...patch });
    if (field) setTouched((prev) => ({ ...prev, [field]: true }));
  };

  const handleSave = async () => {
    if (Object.keys(errors).length > 0) {
      setTouched({ host: true, port: true, noProxy: true });
      return;
    }
    setSaving(true);
    try {
      await SaveNetworkProxySettings(toNetworkProxyInput(form));
      notifySuccess(t("network.proxy.saved"));
      apply(await GetNetworkProxySettings());
    } catch (e: unknown) {
      toast.error(errMsg(e));
    } finally {
      setSaving(false);
    }
  };

  const fieldError = (field: "host" | "port"): string | undefined => {
    const code: FieldCode | undefined = touched[field] ? errors[field] : undefined;
    if (!code) return undefined;
    if (field === "host")
      return t(code === "required" ? "network.proxy.error.hostRequired" : "network.proxy.error.hostInvalid");
    return t(
      code === "required"
        ? "network.proxy.error.portRequired"
        : code === "invalid"
          ? "network.proxy.error.portInvalid"
          : "network.proxy.error.portRange"
    );
  };
  const hostError = fieldError("host");
  const portError = fieldError("port");
  const noProxyError = touched.noProxy ? errors.noProxy : undefined;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("network.proxy.title")}</CardTitle>
        <CardDescription>{t("network.proxy.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex justify-between items-center text-sm">
          <span className="text-muted-foreground">{t("network.proxy.mode")}</span>
          <Select value={form.mode} onValueChange={(v) => update({ mode: v as NetworkProxyMode })}>
            <SelectTrigger className="w-[200px] h-8 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="env">{t("network.proxy.modeEnv")}</SelectItem>
              <SelectItem value="direct">{t("network.proxy.modeDirect")}</SelectItem>
              <SelectItem value="custom">{t("network.proxy.modeCustom")}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {form.mode === "env" && <EnvStatus envVars={envVars} />}

        {form.mode === "custom" && (
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label>{t("network.proxy.type")}</Label>
              <Select value={form.type} onValueChange={(v) => update({ type: v as NetworkProxyType })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="http">HTTP</SelectItem>
                  <SelectItem value="socks5">SOCKS5</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid gap-1.5">
              <div className="flex items-start gap-3">
                <div className="grid flex-1 gap-1.5">
                  <Label htmlFor="network-proxy-host">{t("network.proxy.host")}</Label>
                  <Input
                    id="network-proxy-host"
                    className={cn(hostError && invalidCls)}
                    value={form.host}
                    placeholder="127.0.0.1"
                    aria-invalid={hostError ? true : undefined}
                    onChange={(e) => update({ host: e.target.value }, "host")}
                  />
                  {hostError && (
                    <p role="alert" className="text-xs text-destructive">
                      {hostError}
                    </p>
                  )}
                </div>
                <div className="grid w-[110px] shrink-0 gap-1.5">
                  <Label htmlFor="network-proxy-port">{t("network.proxy.port")}</Label>
                  <Input
                    id="network-proxy-port"
                    className={cn(
                      "[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none",
                      portError && invalidCls
                    )}
                    type="number"
                    value={form.port}
                    placeholder="7890"
                    aria-invalid={portError ? true : undefined}
                    onChange={(e) => update({ port: e.target.value }, "port")}
                  />
                  {portError && (
                    <p role="alert" className="text-xs text-destructive">
                      {portError}
                    </p>
                  )}
                </div>
              </div>
              <p className="text-xs text-muted-foreground">{t("network.proxy.hostHint")}</p>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid gap-1.5">
                <Label htmlFor="network-proxy-username">{t("network.proxy.username")}</Label>
                <Input
                  id="network-proxy-username"
                  value={form.username}
                  placeholder={t("network.proxy.optional")}
                  onChange={(e) => update({ username: e.target.value })}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="network-proxy-password">{t("network.proxy.password")}</Label>
                <SecretInput
                  id="network-proxy-password"
                  value={form.password}
                  placeholder={passwordSet ? t("asset.passwordUnchanged") : t("network.proxy.optional")}
                  onChange={(e) => update({ password: e.target.value })}
                />
              </div>
            </div>

            <div className="grid gap-1.5">
              <div className="flex items-center gap-1.5">
                <Label htmlFor="network-proxy-noproxy">{t("network.proxy.noProxy")}</Label>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Info className="h-3.5 w-3.5 cursor-help text-muted-foreground" />
                  </TooltipTrigger>
                  <TooltipContent className="max-w-xs">{t("network.proxy.noProxyTip")}</TooltipContent>
                </Tooltip>
              </div>
              <Textarea
                id="network-proxy-noproxy"
                className={cn("font-mono text-xs", noProxyError && invalidCls)}
                rows={6}
                value={form.noProxy}
                aria-invalid={noProxyError ? true : undefined}
                onChange={(e) => update({ noProxy: e.target.value }, "noProxy")}
              />
              {noProxyError ? (
                <p role="alert" className="text-xs text-destructive">
                  {t("network.proxy.error.noProxyLine", { line: noProxyError.line })}
                </p>
              ) : (
                <p className="text-xs text-muted-foreground">{t("network.proxy.noProxyHint")}</p>
              )}
            </div>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" className="gap-1" disabled={saving} onClick={() => void handleSave()}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            {t("action.save")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function EnvStatus({ envVars }: { envVars: system.NetworkProxyEnvVar[] }) {
  const { t } = useTranslation();
  if (envVars.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-md bg-warning/15 border border-warning/30 px-3 py-2 text-xs text-warning">
        <Info className="h-3.5 w-3.5 shrink-0" />
        <span>{t("network.proxy.envNone")}</span>
      </div>
    );
  }
  return (
    <div className="grid gap-1.5">
      <p className="text-xs text-muted-foreground">{t("network.proxy.envDetected")}</p>
      <pre className="text-xs bg-muted p-2 rounded-md font-mono">
        {envVars.map((v) => `${v.name}=${v.value}`).join("\n")}
      </pre>
    </div>
  );
}
