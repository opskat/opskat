import { useMemo } from "react";
import { Cpu, ChevronDown, Check, Settings2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Button,
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  cn,
} from "@opskat/ui";
import { useAIStore } from "@/stores/aiStore";
import { useTabStore } from "@/stores/tabStore";

interface ModelSwitcherProps {
  /** 当前会话 id；未创建会话时为 null（预选暂存到 host tab）。 */
  conversationId: number | null;
  /** 承载该对话的 host tab id：侧边为 sideTabId，工作区为 tabId。 */
  hostTabId: string;
}

/**
 * ModelSwitcher —— composer 底栏的「按会话切换模型」入口（issue #246）。
 * 仅当配置了多个模型（AI Provider）时出现；作用域是单会话，切换只影响当前会话。
 */
export function ModelSwitcher({ conversationId, hostTabId }: ModelSwitcherProps) {
  const { t } = useTranslation();
  const providers = useAIStore((s) => s.providers);
  const activeProviderId = useAIStore((s) => s.activeProviderId);
  const conversations = useAIStore((s) => s.conversations);
  const pendingProviderByTab = useAIStore((s) => s.pendingProviderByTab);
  const selectConversationProvider = useAIStore((s) => s.selectConversationProvider);

  const currentProviderId = useMemo(() => {
    if (conversationId != null) {
      const conv = conversations.find((c) => c.ID === conversationId);
      if (conv?.ProviderID) return conv.ProviderID;
    } else {
      const pending = pendingProviderByTab[hostTabId];
      if (pending != null) return pending;
    }
    return activeProviderId;
  }, [conversationId, conversations, pendingProviderByTab, hostTabId, activeProviderId]);

  // #246 前置条件：只有配置了 >1 个模型时才需要切换器；0/1 个时保持底栏原样。
  if (providers.length <= 1) return null;

  const current = providers.find((p) => p.id === currentProviderId) ?? providers.find((p) => p.isActive);

  const openSettings = () => {
    const tabStore = useTabStore.getState();
    const existing = tabStore.tabs.find((tab) => tab.id === "settings");
    if (existing) {
      tabStore.activateTab("settings");
    } else {
      tabStore.openTab({
        id: "settings",
        type: "page",
        label: t("nav.settings"),
        meta: { type: "page", pageId: "settings" },
      });
    }
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-7 shrink-0 gap-1.5 rounded-lg px-2 text-xs font-normal text-muted-foreground hover:text-foreground"
          data-testid="ai-model-switcher"
          data-model={current?.model ?? ""}
          title={t("ai.switchModel")}
          aria-label={t("ai.switchModel")}
        >
          <Cpu className="h-3.5 w-3.5 text-primary" />
          <span className="max-w-[9rem] truncate font-medium text-foreground">{current?.model ?? ""}</span>
          <ChevronDown className="h-3 w-3 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="w-64">
        <DropdownMenuLabel className="flex items-center justify-between gap-2 py-1 font-normal">
          <span className="text-xs font-semibold text-muted-foreground">{t("ai.selectModel")}</span>
          <span className="text-[10px] text-muted-foreground/70">{t("ai.modelScopeHint")}</span>
        </DropdownMenuLabel>
        {providers.map((p) => {
          const isSelected = p.id === currentProviderId;
          return (
            <DropdownMenuItem
              key={p.id}
              className="gap-2"
              data-testid="ai-model-option"
              data-provider-id={p.id}
              data-model={p.model}
              onSelect={() => {
                if (isSelected) return;
                void selectConversationProvider({ conversationId, hostTabId, providerId: p.id });
              }}
            >
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{p.name}</div>
                <div className="truncate font-mono text-[11px] text-muted-foreground">{p.model}</div>
              </div>
              <Check className={cn("h-4 w-4 shrink-0 text-primary", !isSelected && "invisible")} />
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuSeparator />
        <DropdownMenuItem className="gap-2 text-muted-foreground" onSelect={openSettings}>
          <Settings2 className="h-3.5 w-3.5" />
          {t("ai.manageModels")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
