import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Separator } from "@/components/ui/separator";
import { useTheme } from "@/components/theme-provider";
import { useAIStore } from "@/stores/aiStore";
import { Bot, Palette, Check } from "lucide-react";

export function SettingsPage() {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();
  const { configure, configured, detectCLIs, localCLIs } = useAIStore();

  // AI Provider 表单
  const [providerType, setProviderType] = useState(
    localStorage.getItem("ai_provider_type") || "openai"
  );
  const [apiBase, setApiBase] = useState(
    localStorage.getItem("ai_api_base") || "https://api.openai.com/v1"
  );
  const [apiKey, setApiKey] = useState(
    localStorage.getItem("ai_api_key") || ""
  );
  const [model, setModel] = useState(
    localStorage.getItem("ai_model") || "gpt-4o"
  );
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    detectCLIs();
  }, [detectCLIs]);

  // 如果有已保存的配置，启动时自动 configure
  useEffect(() => {
    const savedType = localStorage.getItem("ai_provider_type");
    const savedBase = localStorage.getItem("ai_api_base");
    const savedKey = localStorage.getItem("ai_api_key");
    const savedModel = localStorage.getItem("ai_model");
    if (savedType && savedBase && savedKey && savedModel) {
      configure(savedType, savedBase, savedKey, savedModel);
    }
  }, [configure]);

  const handleSaveAI = async () => {
    localStorage.setItem("ai_provider_type", providerType);
    localStorage.setItem("ai_api_base", apiBase);
    localStorage.setItem("ai_api_key", apiKey);
    localStorage.setItem("ai_model", model);
    await configure(providerType, apiBase, apiKey, model);
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
  };

  const handleLanguageChange = (lng: string) => {
    i18n.changeLanguage(lng);
    localStorage.setItem("language", lng);
  };

  return (
    <div className="flex flex-col h-full">
      <div className="px-4 py-3 border-b">
        <h2 className="font-semibold">{t("nav.settings")}</h2>
      </div>
      <div className="flex-1 overflow-y-auto p-4 max-w-2xl">
        <Tabs defaultValue="ai" className="space-y-4">
          <TabsList>
            <TabsTrigger value="ai" className="gap-1">
              <Bot className="h-3.5 w-3.5" />
              AI Provider
            </TabsTrigger>
            <TabsTrigger value="appearance" className="gap-1">
              <Palette className="h-3.5 w-3.5" />
              {t("nav.settings")}
            </TabsTrigger>
          </TabsList>

          {/* AI Provider */}
          <TabsContent value="ai" className="space-y-4">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">AI Provider</CardTitle>
                <CardDescription>
                  {configured ? "✓ " + t("settings.configured") : t("ai.notConfigured")}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-2">
                  <Label>{t("settings.providerType")}</Label>
                  <Select value={providerType} onValueChange={setProviderType}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="openai">OpenAI Compatible</SelectItem>
                      <SelectItem value="local_cli">Local CLI</SelectItem>
                    </SelectContent>
                  </Select>
                </div>

                {providerType === "openai" && (
                  <>
                    <div className="grid gap-2">
                      <Label>API Base URL</Label>
                      <Input value={apiBase} onChange={(e) => setApiBase(e.target.value)} />
                    </div>
                    <div className="grid gap-2">
                      <Label>API Key</Label>
                      <Input
                        type="password"
                        value={apiKey}
                        onChange={(e) => setApiKey(e.target.value)}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label>{t("settings.model")}</Label>
                      <Input value={model} onChange={(e) => setModel(e.target.value)} />
                    </div>
                  </>
                )}

                {providerType === "local_cli" && (
                  <>
                    <div className="grid gap-2">
                      <Label>{t("settings.cliPath")}</Label>
                      <Input
                        value={apiBase}
                        onChange={(e) => setApiBase(e.target.value)}
                        placeholder="/usr/local/bin/claude"
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label>{t("settings.cliType")}</Label>
                      <Select value={model} onValueChange={setModel}>
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="claude">Claude Code</SelectItem>
                          <SelectItem value="codex">Codex</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    {localCLIs.length > 0 && (
                      <div className="text-sm text-muted-foreground">
                        {t("settings.detectedCLIs")}:{" "}
                        {localCLIs.map((c) => `${c.name} (${c.path})`).join(", ")}
                      </div>
                    )}
                  </>
                )}

                <Button onClick={handleSaveAI} className="gap-1">
                  {saved ? <Check className="h-4 w-4" /> : null}
                  {saved ? t("settings.saved") : t("action.save")}
                </Button>
              </CardContent>
            </Card>
          </TabsContent>

          {/* 外观和语言 */}
          <TabsContent value="appearance" className="space-y-4">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t("theme.toggle")}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="grid gap-2">
                  <Label>{t("theme.toggle")}</Label>
                  <Select value={theme} onValueChange={setTheme as (v: string) => void}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="light">{t("theme.light")}</SelectItem>
                      <SelectItem value="dark">{t("theme.dark")}</SelectItem>
                      <SelectItem value="system">{t("theme.system")}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <Separator />
                <div className="grid gap-2">
                  <Label>{t("language.label")}</Label>
                  <Select value={i18n.language} onValueChange={handleLanguageChange}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="zh-CN">{t("language.zh-CN")}</SelectItem>
                      <SelectItem value="en">{t("language.en")}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
