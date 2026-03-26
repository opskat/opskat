import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAIStore } from "@/stores/aiStore";
import { CreateAIProvider, SetActiveAIProvider } from "../../../wailsjs/go/app/App";
import { Bot, Loader2, ArrowRight } from "lucide-react";
import { toast } from "sonner";

export function AISetupWizard() {
  const { t } = useTranslation();

  // Step 1: Provider type selection
  const [providerType, setProviderType] = useState<"openai" | "anthropic" | null>(null);

  // Step 2: Configuration
  const [name, setName] = useState("");
  const [apiBase, setApiBase] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState("");
  const [completing, setCompleting] = useState(false);

  const selectProvider = (type: "openai" | "anthropic") => {
    setProviderType(type);
    if (type === "openai") {
      setName(t("setup.openAICompatible"));
      setApiBase("https://api.openai.com/v1");
    } else {
      setName("Anthropic");
      setApiBase("https://api.anthropic.com");
    }
    setApiKey("");
    setModel("");
  };

  const handleComplete = async () => {
    if (!providerType) return;
    setCompleting(true);
    try {
      const created = await CreateAIProvider(name, providerType, apiBase, apiKey, model);
      await SetActiveAIProvider(created.id);
      await useAIStore.getState().checkConfigured();
      await useAIStore.getState().fetchConversations();
    } catch (e: unknown) {
      toast.error(String(e));
    } finally {
      setCompleting(false);
    }
  };

  return (
    <div className="flex h-full overflow-y-auto">
      <div className="m-auto w-full max-w-lg p-6 space-y-6">
        {/* Header */}
        <div className="text-center space-y-2">
          <Bot className="h-10 w-10 text-primary mx-auto" />
          <h2 className="text-lg font-semibold">{t("setup.title")}</h2>
          <p className="text-sm text-muted-foreground">{t("setup.subtitle")}</p>
        </div>

        {/* Step 1: Provider Type Selection */}
        <div className="space-y-2">
          <h3 className="text-sm font-medium">{t("setup.selectProviderType")}</h3>
          <div className="grid grid-cols-2 gap-3">
            <button
              onClick={() => selectProvider("openai")}
              className={`rounded-lg border-2 p-4 text-left transition-all hover:border-primary/50 ${
                providerType === "openai" ? "border-primary bg-primary/5" : "border-border"
              }`}
            >
              <div className="flex items-center gap-2 mb-2">
                <Bot className="h-5 w-5" />
                <span className="font-medium text-sm">{t("setup.openAICompatible")}</span>
              </div>
              <p className="text-xs text-muted-foreground">{t("setup.openAICompatibleDesc")}</p>
            </button>

            <button
              onClick={() => selectProvider("anthropic")}
              className={`rounded-lg border-2 p-4 text-left transition-all hover:border-primary/50 ${
                providerType === "anthropic" ? "border-primary bg-primary/5" : "border-border"
              }`}
            >
              <div className="flex items-center gap-2 mb-2">
                <Bot className="h-5 w-5" />
                <span className="font-medium text-sm">{t("settings.anthropic")}</span>
              </div>
              <p className="text-xs text-muted-foreground">{t("setup.anthropicDesc")}</p>
            </button>
          </div>
        </div>

        {/* Step 2: Configuration */}
        {providerType && (
          <div className="space-y-4">
            <div className="rounded-lg border p-4 space-y-3">
              <div className="grid gap-2">
                <Label className="text-xs">{t("settings.providerName")}</Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} className="h-8 text-sm" />
              </div>
              <div className="grid gap-2">
                <Label className="text-xs">{t("settings.apiBase")}</Label>
                <Input value={apiBase} onChange={(e) => setApiBase(e.target.value)} className="h-8 text-sm" />
              </div>
              <div className="grid gap-2">
                <Label className="text-xs">{t("settings.apiKey")}</Label>
                <Input
                  type="password"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  className="h-8 text-sm"
                />
              </div>
              <div className="grid gap-2">
                <Label className="text-xs">{t("settings.model")}</Label>
                <Input value={model} onChange={(e) => setModel(e.target.value)} className="h-8 text-sm" />
              </div>
            </div>

            <Button
              onClick={handleComplete}
              disabled={!apiBase.trim() || !apiKey.trim() || completing}
              className="w-full"
            >
              {completing ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <ArrowRight className="h-4 w-4 mr-2" />}
              {t("setup.complete")}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
