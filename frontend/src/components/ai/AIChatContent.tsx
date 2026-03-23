import { useState, useRef, useEffect } from "react";
import { Send, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import Markdown from "react-markdown";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useAIStore, type ChatMessage } from "@/stores/aiStore";
import { ToolBlock } from "@/components/ai/ToolBlock";
import { AISetupWizard } from "@/components/ai/AISetupWizard";

interface AIChatContentProps {
  tabId: string;
}

export function AIChatContent({ tabId }: AIChatContentProps) {
  const { t } = useTranslation();
  const { configured, sendToTab } = useAIStore();
  const tabState = useAIStore((s) => s.tabStates[tabId]) || {
    messages: [],
    sending: false,
  };
  const { messages, sending } = tabState;

  const [input, setInput] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const composingRef = useRef(false);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // 切换 tab 时自动聚焦输入框
  useEffect(() => {
    textareaRef.current?.focus();
  }, [tabId]);

  const handleSend = () => {
    const text = input.trim();
    if (!text || sending) return;
    setInput("");
    sendToTab(tabId, text);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (composingRef.current || e.nativeEvent.isComposing) return;
    if (e.key === "Enter") {
      if (e.ctrlKey || e.metaKey || e.shiftKey) {
        // Ctrl+Enter / Cmd+Enter / Shift+Enter: newline
        return;
      }
      e.preventDefault();
      handleSend();
    }
  };

  // 未配置：显示引导设置
  if (!configured) {
    return <AISetupWizard />;
  }

  return (
    <div className="flex h-full flex-col">
      {/* 消息区 */}
      <ScrollArea className="flex-1 min-h-0 overflow-hidden">
        <div className="max-w-3xl mx-auto p-4 space-y-4">
          {messages.length === 0 && (
            <p className="text-sm text-muted-foreground text-center mt-16">
              {t("ai.placeholder")}
            </p>
          )}
          {messages.map((msg, i) => (
            <div
              key={i}
              className={`text-sm ${msg.role === "user" ? "text-right" : ""}`}
            >
              {msg.role === "user" ? (
                <div className="inline-block rounded-xl rounded-br-sm bg-primary px-3 py-2 text-primary-foreground max-w-[85%] text-left shadow-sm break-words">
                  {msg.content}
                </div>
              ) : (
                <AssistantMessage msg={msg} />
              )}
            </div>
          ))}
          <div ref={bottomRef} />
        </div>
      </ScrollArea>

      {/* 输入区 */}
      <div className="border-t p-3">
        <div className="max-w-3xl mx-auto flex gap-2">
          <textarea
            ref={textareaRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            onCompositionStart={() => (composingRef.current = true)}
            onCompositionEnd={() => {
              // Delay clearing the flag so the Enter keyDown that ends
              // the IME composition doesn't trigger send
              requestAnimationFrame(() => {
                composingRef.current = false;
              });
            }}
            placeholder={t("ai.sendPlaceholder")}
            rows={1}
            className="flex-1 max-h-32 rounded-xl border border-input bg-background px-3 py-2 text-sm outline-none focus:border-ring focus:ring-1 focus:ring-ring/50 resize-none transition-colors duration-150 placeholder:text-muted-foreground/60"
            style={{ fieldSizing: "content" } as React.CSSProperties}
          />
          <Button
            size="icon"
            className="h-9 w-9 shrink-0 rounded-xl"
            onClick={handleSend}
            disabled={sending || !input.trim()}
          >
            {sending ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Send className="h-4 w-4" />
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}

// Assistant 消息：渲染结构化内容块
function AssistantMessage({ msg }: { msg: ChatMessage }) {
  const hasBlocks = msg.blocks && msg.blocks.length > 0;
  const isEmpty = !hasBlocks && msg.content === "";

  if (msg.streaming && isEmpty) {
    return (
      <div className="rounded-xl rounded-bl-sm bg-muted/60 border border-border/50 px-3 py-2 max-w-[95%] shadow-sm">
        <div className="flex items-center gap-1 py-1">
          <span
            className="h-1.5 w-1.5 rounded-full bg-muted-foreground/50 animate-bounce"
            style={{ animationDelay: "0ms" }}
          />
          <span
            className="h-1.5 w-1.5 rounded-full bg-muted-foreground/50 animate-bounce"
            style={{ animationDelay: "150ms" }}
          />
          <span
            className="h-1.5 w-1.5 rounded-full bg-muted-foreground/50 animate-bounce"
            style={{ animationDelay: "300ms" }}
          />
        </div>
      </div>
    );
  }

  if (hasBlocks) {
    return (
      <div className="rounded-xl rounded-bl-sm bg-muted/60 border border-border/50 px-3 py-2 max-w-[95%] min-w-0 overflow-hidden shadow-sm">
        {msg.blocks.map((block, idx) =>
          block.type === "text" ? (
            <div
              key={idx}
              className="prose prose-sm dark:prose-invert prose-p:my-1 prose-pre:my-1 overflow-x-auto break-words"
            >
              <Markdown>{block.content}</Markdown>
            </div>
          ) : (
            <ToolBlock key={idx} block={block} />
          )
        )}
        {msg.streaming && (
          <Loader2 className="h-3 w-3 animate-spin inline-block ml-1 mb-1" />
        )}
      </div>
    );
  }

  return (
    <div className="rounded-xl rounded-bl-sm bg-muted/60 border border-border/50 px-3 py-2 max-w-[95%] min-w-0 overflow-hidden break-words prose prose-sm dark:prose-invert prose-p:my-1 prose-pre:my-1 prose-pre:overflow-x-auto shadow-sm">
      <Markdown>{msg.content}</Markdown>
      {msg.streaming && (
        <Loader2 className="h-3 w-3 animate-spin inline-block ml-1" />
      )}
    </div>
  );
}
