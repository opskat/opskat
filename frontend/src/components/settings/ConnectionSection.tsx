import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label, Switch } from "@opskat/ui";
import { GetSSHConnectionSettings, SetSSHConnectionSettings } from "../../../wailsjs/go/system/System";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

// Keep these in sync with internal/app/system/ssh_settings.go validation.
const KEEPALIVE_MIN = 5;
const KEEPALIVE_MAX = 3600;
const TIMEOUT_MIN = 5;
const TIMEOUT_MAX = 600;

interface ConnectionSettings {
  tcpNoDelay: boolean;
  tcpKeepAlive: boolean;
  keepAliveIntervalSeconds: number;
  connectTimeoutSeconds: number;
}

const DEFAULTS: ConnectionSettings = {
  tcpNoDelay: true,
  tcpKeepAlive: true,
  keepAliveIntervalSeconds: 20,
  connectTimeoutSeconds: 30,
};

export function ConnectionSection() {
  const { t } = useTranslation();
  const [settings, setSettings] = useState<ConnectionSettings>(DEFAULTS);
  // Edited-but-not-yet-committed text for the two number fields, so the user can
  // clear the input while typing without it snapping back.
  const [keepAliveText, setKeepAliveText] = useState(String(DEFAULTS.keepAliveIntervalSeconds));
  const [timeoutText, setTimeoutText] = useState(String(DEFAULTS.connectTimeoutSeconds));

  useEffect(() => {
    GetSSHConnectionSettings()
      .then((s) => {
        if (!s) return;
        setSettings(s);
        setKeepAliveText(String(s.keepAliveIntervalSeconds));
        setTimeoutText(String(s.connectTimeoutSeconds));
      })
      .catch(() => {});
  }, []);

  // persist applies a full settings object and rolls the UI back on failure so
  // the displayed state always matches what the backend accepted.
  const persist = async (next: ConnectionSettings) => {
    const prev = settings;
    setSettings(next);
    try {
      await SetSSHConnectionSettings(next);
      notifySuccess(t("connection.saved"));
    } catch (e: unknown) {
      setSettings(prev);
      setKeepAliveText(String(prev.keepAliveIntervalSeconds));
      setTimeoutText(String(prev.connectTimeoutSeconds));
      toast.error(errMsg(e));
    }
  };

  const commitNumber = (
    text: string,
    min: number,
    max: number,
    key: "keepAliveIntervalSeconds" | "connectTimeoutSeconds",
    resetText: (v: string) => void
  ) => {
    const n = Number(text);
    if (!Number.isFinite(n) || !Number.isInteger(n) || n < min || n > max) {
      toast.error(t("connection.rangeError", { min, max }));
      resetText(String(settings[key]));
      return;
    }
    if (n === settings[key]) {
      resetText(String(n));
      return;
    }
    resetText(String(n));
    void persist({ ...settings, [key]: n });
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("connection.title")}</CardTitle>
        <CardDescription>{t("connection.desc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* TCP_NODELAY */}
        <div className="flex items-start justify-between gap-4">
          <div className="grid gap-1">
            <Label>{t("connection.tcpNoDelay")}</Label>
            <p className="text-xs text-muted-foreground">{t("connection.tcpNoDelayHint")}</p>
          </div>
          <Switch checked={settings.tcpNoDelay} onCheckedChange={(v) => persist({ ...settings, tcpNoDelay: v })} />
        </div>

        {/* SO_KEEPALIVE */}
        <div className="flex items-start justify-between gap-4">
          <div className="grid gap-1">
            <Label>{t("connection.tcpKeepAlive")}</Label>
            <p className="text-xs text-muted-foreground">{t("connection.tcpKeepAliveHint")}</p>
          </div>
          <Switch checked={settings.tcpKeepAlive} onCheckedChange={(v) => persist({ ...settings, tcpKeepAlive: v })} />
        </div>

        {/* SSH keepalive heartbeat interval */}
        <div className="grid gap-2">
          <Label>{t("connection.keepAliveInterval")}</Label>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              min={KEEPALIVE_MIN}
              max={KEEPALIVE_MAX}
              value={keepAliveText}
              onChange={(e) => setKeepAliveText(e.target.value)}
              onBlur={() =>
                commitNumber(keepAliveText, KEEPALIVE_MIN, KEEPALIVE_MAX, "keepAliveIntervalSeconds", setKeepAliveText)
              }
              className="w-32"
            />
            <span className="text-sm text-muted-foreground">{t("connection.secondsUnit")}</span>
          </div>
          <p className="text-xs text-muted-foreground">{t("connection.keepAliveIntervalHint")}</p>
        </div>

        {/* Max connection timeout */}
        <div className="grid gap-2">
          <Label>{t("connection.connectTimeout")}</Label>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              min={TIMEOUT_MIN}
              max={TIMEOUT_MAX}
              value={timeoutText}
              onChange={(e) => setTimeoutText(e.target.value)}
              onBlur={() =>
                commitNumber(timeoutText, TIMEOUT_MIN, TIMEOUT_MAX, "connectTimeoutSeconds", setTimeoutText)
              }
              className="w-32"
            />
            <span className="text-sm text-muted-foreground">{t("connection.secondsUnit")}</span>
          </div>
          <p className="text-xs text-muted-foreground">{t("connection.connectTimeoutHint")}</p>
        </div>
      </CardContent>
    </Card>
  );
}
