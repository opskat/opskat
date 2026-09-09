import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, FileText, Loader2 } from "lucide-react";
import { Button } from "@opskat/ui";
import { CodeEditor, type CodeEditorLanguage } from "@/components/CodeEditor";
import { readExternalEditSessionText } from "@/lib/externalEditApi";
import type { EditorTabMeta } from "@/stores/tabStore";

const LANGUAGE_BY_EXTENSION: Record<string, CodeEditorLanguage> = {
  js: "javascript",
  json: "json",
  md: "markdown",
  mjs: "javascript",
  sh: "shell",
  sql: "sql",
  ts: "javascript",
  yaml: "yaml",
  yml: "yaml",
};

function languageOf(remotePath: string): CodeEditorLanguage {
  const name = remotePath.split("/").filter(Boolean).at(-1) ?? "";
  const extension = name.includes(".") ? name.split(".").pop() : "";
  return LANGUAGE_BY_EXTENSION[(extension ?? "").toLowerCase()] ?? "plaintext";
}

interface RemoteFileEditorTabProps {
  meta: EditorTabMeta;
}

/**
 * 内置编辑器 tab：占满主区、右侧不挂文件面板（要看目录树切回终端 tab）。
 * 文本按会话记录的编码由后端解码后读入，这里只做呈现。
 */
export function RemoteFileEditorTab({ meta }: RemoteFileEditorTabProps) {
  const { t } = useTranslation();
  const [reloadToken, setReloadToken] = useState(0);
  // 读取结果连同它属于哪一次读取一起存：换会话 / 重试时不用在 effect 里同步清空旧内容，
  // 只要 key 对不上就还是「读取中」。
  const loadKey = `${meta.sessionId}#${reloadToken}`;
  const [loaded, setLoaded] = useState<{ key: string; text: string; failed: boolean } | null>(null);

  useEffect(() => {
    let cancelled = false;
    readExternalEditSessionText(meta.sessionId)
      .then((value) => {
        if (!cancelled) setLoaded({ key: loadKey, text: value, failed: false });
      })
      .catch(() => {
        // 具体失败原因带着本地副本路径，不适合直接展示；这里只给出可重试的结论。
        if (!cancelled) setLoaded({ key: loadKey, text: "", failed: true });
      });
    return () => {
      cancelled = true;
    };
  }, [loadKey, meta.sessionId]);

  const current = loaded?.key === loadKey ? loaded : null;
  const handleRetry = useCallback(() => setReloadToken((token) => token + 1), []);
  const handleChange = useCallback(
    (value: string) => setLoaded((state) => (state ? { ...state, text: value } : state)),
    []
  );

  return (
    <div className="flex h-full flex-col bg-background" data-testid="remote-file-editor">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
        <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="shrink-0 font-medium text-foreground">{meta.assetName}</span>
        <span className="truncate text-muted-foreground" title={meta.remotePath}>
          {meta.remotePath}
        </span>
      </div>
      <div className="min-h-0 flex-1">
        {current?.failed ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-sm text-muted-foreground">
            <AlertTriangle className="h-5 w-5 text-destructive" />
            <span>{t("externalEdit.builtIn.loadFailed")}</span>
            <Button size="sm" variant="outline" onClick={handleRetry}>
              {t("action.retry")}
            </Button>
          </div>
        ) : !current ? (
          <div className="flex h-full items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : (
          <CodeEditor
            language={languageOf(meta.remotePath)}
            onChange={handleChange}
            testId="remote-file-editor-content"
            value={current.text}
          />
        )}
      </div>
    </div>
  );
}
