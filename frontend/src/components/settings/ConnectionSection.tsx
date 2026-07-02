import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from "@opskat/ui";
import { GetSSHConnectionSettings, SetSSHConnectionSettings } from "../../../wailsjs/go/system/System";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";

const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

// Keep in sync with internal/app/system/ssh_settings.go validation.
const KEEPALIVE_MIN = 5;
const KEEPALIVE_MAX = 3600;
const KEEPALIVE_DEFAULT = 30;

// ConnectionSection is the global default for SSH idle keep-alive. It is the
// only network knob we expose (TCP_NODELAY / SO_KEEPALIVE / connect timeout stay
// at their built-in defaults). Individual assets override this in their form's
// Advanced tab. Rendered inside the Terminal settings tab.
export function ConnectionSection() {
  const { t } = useTranslation();
  const [seconds, setSeconds] = useState(KEEPALIVE_DEFAULT);
  // Edited-but-not-yet-committed text, so the user can clear the input while
  // typing without it snapping back.
  const [text, setText] = useState(String(KEEPALIVE_DEFAULT));

  useEffect(() => {
    GetSSHConnectionSettings()
      .then((s) => {
        if (!s) return;
        setSeconds(s.keepAliveIntervalSeconds);
        setText(String(s.keepAliveIntervalSeconds));
      })
      .catch(() => {});
  }, []);

  const commit = async () => {
    const n = Number(text);
    if (!Number.isFinite(n) || !Number.isInteger(n) || n < KEEPALIVE_MIN || n > KEEPALIVE_MAX) {
      toast.error(t("connection.rangeError", { min: KEEPALIVE_MIN, max: KEEPALIVE_MAX }));
      setText(String(seconds));
      return;
    }
    if (n === seconds) {
      setText(String(n));
      return;
    }
    const prev = seconds;
    setSeconds(n);
    setText(String(n));
    try {
      await SetSSHConnectionSettings({ keepAliveIntervalSeconds: n });
      notifySuccess(t("connection.saved"));
    } catch (e: unknown) {
      setSeconds(prev);
      setText(String(prev));
      toast.error(errMsg(e));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("connection.title")}</CardTitle>
        <CardDescription>{t("connection.desc")}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="grid gap-2">
          <Label>{t("connection.keepAliveInterval")}</Label>
          <div className="flex items-center gap-2">
            <Input
              type="number"
              min={KEEPALIVE_MIN}
              max={KEEPALIVE_MAX}
              value={text}
              onChange={(e) => setText(e.target.value)}
              onBlur={commit}
              className="w-32"
            />
            <span className="text-sm text-muted-foreground">{t("connection.secondsUnit")}</span>
          </div>
          <p className="text-xs text-muted-foreground">{t("connection.keepAliveIntervalHint")}</p>
        </div>
      </CardContent>
    </Card>
  );
}
