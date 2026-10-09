import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@opskat/ui";
import { GetExtensionMirror, SetExtensionMirror } from "../../../wailsjs/go/system/System";
import { EXTENSION_MIRROR_SETTING_ID } from "./downloadMirror";

const PRESET_HOST = "ghcr.nju.edu.cn";
type Mode = "direct" | "preset" | "custom";

const modeOf = (host: string): Mode => (host === "" ? "direct" : host === PRESET_HOST ? "preset" : "custom");
const errMsg = (e: unknown) => (e instanceof Error ? e.message : String(e));

// 「扩展下载」设置：选择扩展 OCI 包的拉取主机。已保存值为主机名（空 = 直连 ghcr.io）。
export function ExtensionMirrorSettings() {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>("direct");
  const [customHost, setCustomHost] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    void GetExtensionMirror()
      .then((host) => {
        const stored = host ?? "";
        setMode(modeOf(stored));
        if (modeOf(stored) === "custom") setCustomHost(stored);
      })
      .catch((e: unknown) => setError(errMsg(e)));
  }, []);

  const save = async (host: string) => {
    try {
      await SetExtensionMirror(host);
      setError("");
    } catch (e: unknown) {
      setError(errMsg(e));
    }
  };

  const handleModeChange = (value: string) => {
    const next = value as Mode;
    setMode(next);
    setError("");
    if (next === "direct") void save("");
    else if (next === "preset") void save(PRESET_HOST);
  };

  const handleCustomSave = () => {
    const host = customHost.trim();
    if (host === "") {
      setError(t("extension.mirror.hostRequired"));
      return;
    }
    void save(host);
  };

  return (
    <Card id={EXTENSION_MIRROR_SETTING_ID}>
      <CardHeader>
        <CardTitle className="text-base">{t("extension.mirror.title")}</CardTitle>
        <CardDescription>{t("extension.mirror.hint")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="flex justify-between items-center text-sm">
          <span className="text-muted-foreground">{t("extension.mirror.source")}</span>
          <Select value={mode} onValueChange={handleModeChange}>
            <SelectTrigger className="w-[200px] h-8 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="direct">{t("extension.mirror.direct")}</SelectItem>
              <SelectItem value="preset">{PRESET_HOST}</SelectItem>
              <SelectItem value="custom">{t("extension.mirror.custom")}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {mode === "custom" && (
          <Input
            className="h-8 text-xs"
            placeholder={t("extension.mirror.customPlaceholder")}
            value={customHost}
            aria-invalid={error ? true : undefined}
            onChange={(e) => setCustomHost(e.target.value)}
            onBlur={handleCustomSave}
            onKeyDown={(e) => e.key === "Enter" && handleCustomSave()}
          />
        )}
        {error && (
          <p role="alert" className="text-xs text-destructive">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
