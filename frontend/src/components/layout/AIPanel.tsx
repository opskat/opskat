import { useState, useRef, useEffect } from "react";
import {
  Send,
  PanelRightClose,
  Settings2,
  Trash2,
  Loader2,
  Bot,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import Markdown from "react-markdown";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useAIStore } from "@/stores/aiStore";

interface AIPanelProps {
  collapsed: boolean;
  onToggle: () => void;
}

export function AIPanel({ collapsed, onToggle }: AIPanelProps) {
  const { t } = useTranslation();
  const { messages, sending, configured, send, clear } = useAIStore();
  const [input, setInput] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  if (collapsed) return null;

  const handleSend = () => {
    const text = input.trim();
    if (!text || sending) return;
    setInput("");
    send(text);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="flex h-full w-80 flex-col border-l">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b">
        <div className="flex items-center gap-1.5">
          <Bot className="h-3.5 w-3.5 text-primary" />
          <span className="text-sm font-medium">{t("ai.title")}</span>
        </div>
        <div className="flex gap-0.5">
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={clear}
            title={t("action.delete")}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6"
            onClick={onToggle}
          >
            <PanelRightClose className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Not configured */}
      {!configured && (
        <div className="flex-1 flex items-center justify-center p-4">
          <div className="text-center space-y-2">
            <Settings2 className="h-8 w-8 text-muted-foreground mx-auto" />
            <p className="text-sm text-muted-foreground">
              {t("ai.notConfigured")}
            </p>
          </div>
        </div>
      )}

      {/* Messages */}
      {configured && (
        <ScrollArea className="flex-1">
          <div ref={scrollRef} className="p-3 space-y-3">
            {messages.length === 0 && (
              <p className="text-sm text-muted-foreground text-center mt-8">
                {t("ai.placeholder")}
              </p>
            )}
            {messages.map((msg, i) => (
              <div
                key={i}
                className={`text-sm ${
                  msg.role === "user" ? "text-right" : ""
                }`}
              >
                {msg.role === "user" ? (
                  <div className="inline-block rounded-xl rounded-br-sm bg-primary px-3 py-2 text-primary-foreground max-w-[85%] text-left shadow-sm">
                    {msg.content}
                  </div>
                ) : (
                  <div className="rounded-xl rounded-bl-sm bg-muted/60 border border-border/50 px-3 py-2 max-w-[95%] prose prose-sm dark:prose-invert prose-p:my-1 prose-pre:my-1 shadow-sm">
                    {msg.streaming && msg.content === "" ? (
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
                    ) : (
                      <>
                        <Markdown>{msg.content}</Markdown>
                        {msg.streaming && (
                          <Loader2 className="h-3 w-3 animate-spin inline-block ml-1" />
                        )}
                      </>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        </ScrollArea>
      )}

      {/* Input */}
      {configured && (
        <div className="border-t p-3">
          <div className="flex gap-2">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={t("ai.placeholder")}
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
      )}
    </div>
  );
}
