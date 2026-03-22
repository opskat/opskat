import { useState, useEffect, useCallback, useRef } from "react";
import { useTranslation } from "react-i18next";
import {
  Folder,
  File,
  Loader2,
  ArrowUp,
  Home,
  RefreshCw,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { SFTPListDir, SFTPGetwd } from "../../../wailsjs/go/main/App";
import { sftp_svc } from "../../../wailsjs/go/models";

interface RemoteFileBrowserProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sessionId: string;
  mode: "upload" | "download";
  onConfirm: (remotePath: string, isDir: boolean, uploadType?: "file" | "dir") => void;
}

function formatSize(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + " " + units[i];
}

function formatDate(timestamp: number): string {
  const d = new Date(timestamp * 1000);
  return d.toLocaleDateString() + " " + d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function RemoteFileBrowser({
  open,
  onOpenChange,
  sessionId,
  mode,
  onConfirm,
}: RemoteFileBrowserProps) {
  const { t } = useTranslation();
  const [currentPath, setCurrentPath] = useState("/");
  const [pathInput, setPathInput] = useState("/");
  const [entries, setEntries] = useState<sftp_svc.FileEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [selectedIsDir, setSelectedIsDir] = useState(false);

  // 记住每个 session 上次浏览的目录
  const lastPathRef = useRef<Record<string, string>>({});

  const loadDir = useCallback(
    async (dirPath: string) => {
      setLoading(true);
      setError(null);
      setSelected(null);
      setSelectedIsDir(false);
      try {
        const result = await SFTPListDir(sessionId, dirPath);
        setEntries(result || []);
        setCurrentPath(dirPath);
        setPathInput(dirPath);
        lastPathRef.current[sessionId] = dirPath;
      } catch (e) {
        setError(String(e));
      } finally {
        setLoading(false);
      }
    },
    [sessionId]
  );

  useEffect(() => {
    if (!open || !sessionId) return;
    const lastPath = lastPathRef.current[sessionId];
    if (lastPath) {
      // 有记忆，直接加载上次目录
      loadDir(lastPath);
    } else {
      // 首次打开，获取远程 home 目录
      SFTPGetwd(sessionId)
        .then((home) => loadDir(home || "/"))
        .catch(() => loadDir("/"));
    }
  }, [open, sessionId, loadDir]);

  const navigateTo = (path: string) => {
    loadDir(path);
  };

  const goUp = () => {
    if (currentPath === "/") return;
    const parent = currentPath.replace(/\/[^/]+\/?$/, "") || "/";
    navigateTo(parent);
  };

  const handlePathSubmit = () => {
    const p = pathInput.trim();
    if (p) navigateTo(p);
  };

  const handleEntryClick = (entry: sftp_svc.FileEntry) => {
    const fullPath = currentPath === "/" ? "/" + entry.name : currentPath + "/" + entry.name;
    setSelected(fullPath);
    setSelectedIsDir(entry.isDir);
  };

  const handleEntryDoubleClick = (entry: sftp_svc.FileEntry) => {
    if (entry.isDir) {
      const fullPath = currentPath === "/" ? "/" + entry.name : currentPath + "/" + entry.name;
      navigateTo(fullPath);
    }
  };

  const handleUpload = (uploadType: "file" | "dir") => {
    const targetDir = selected && selectedIsDir ? selected : currentPath;
    onConfirm(targetDir.endsWith("/") ? targetDir : targetDir + "/", true, uploadType);
    onOpenChange(false);
  };

  const handleDownload = () => {
    if (!selected) return;
    onConfirm(selected, selectedIsDir);
    onOpenChange(false);
  };

  const dialogTitle =
    mode === "upload"
      ? t("sftp.selectUploadTarget")
      : t("sftp.selectDownloadFile");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>{dialogTitle}</DialogTitle>
        </DialogHeader>

        {/* Path bar */}
        <div className="flex items-center gap-1">
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={goUp}
            disabled={currentPath === "/"}
            title={t("sftp.parentDir")}
          >
            <ArrowUp className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => navigateTo("/")}
            title={t("sftp.home")}
          >
            <Home className="h-3.5 w-3.5" />
          </Button>
          <Input
            className="h-7 text-xs flex-1"
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") handlePathSubmit();
            }}
          />
          <Button
            variant="ghost"
            size="icon-xs"
            onClick={() => loadDir(currentPath)}
            title={t("sftp.retry")}
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
        </div>

        {/* File list */}
        <ScrollArea className="flex-1 min-h-0 border rounded-md" style={{ height: "320px" }}>
          {loading && (
            <div className="flex items-center justify-center h-full py-12">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          )}
          {error && (
            <div className="flex flex-col items-center justify-center h-full py-12 gap-2">
              <span className="text-sm text-destructive">{t("sftp.loadError")}</span>
              <span className="text-xs text-muted-foreground max-w-xs text-center">{error}</span>
              <Button variant="outline" size="xs" onClick={() => loadDir(currentPath)}>
                {t("sftp.retry")}
              </Button>
            </div>
          )}
          {!loading && !error && entries.length === 0 && (
            <div className="flex items-center justify-center h-full py-12">
              <span className="text-sm text-muted-foreground">{t("sftp.empty")}</span>
            </div>
          )}
          {!loading && !error && entries.length > 0 && (
            <div className="text-xs">
              {/* Parent directory */}
              {currentPath !== "/" && (
                <div
                  className="flex items-center gap-2 px-3 py-1.5 cursor-pointer hover:bg-muted/50"
                  onDoubleClick={goUp}
                >
                  <Folder className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                  <span className="flex-1">..</span>
                </div>
              )}
              {entries.map((entry) => {
                const fullPath = currentPath === "/"
                  ? "/" + entry.name
                  : currentPath + "/" + entry.name;
                const isSelected = selected === fullPath;
                return (
                  <div
                    key={entry.name}
                    className={`flex items-center gap-2 px-3 py-1.5 cursor-pointer transition-colors ${
                      isSelected
                        ? "bg-primary/10 text-primary"
                        : "hover:bg-muted/50"
                    }`}
                    onClick={() => handleEntryClick(entry)}
                    onDoubleClick={() => handleEntryDoubleClick(entry)}
                  >
                    {entry.isDir ? (
                      <Folder className="h-3.5 w-3.5 text-primary/70 shrink-0" />
                    ) : (
                      <File className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                    )}
                    <span className="flex-1 truncate">{entry.name}</span>
                    {!entry.isDir && (
                      <span className="text-muted-foreground shrink-0">
                        {formatSize(entry.size)}
                      </span>
                    )}
                    <span className="text-muted-foreground shrink-0 w-28 text-right">
                      {formatDate(entry.modTime)}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </ScrollArea>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("action.cancel")}
          </Button>
          {mode === "upload" ? (
            <>
              <Button onClick={() => handleUpload("file")}>
                {t("sftp.upload")}
              </Button>
              <Button onClick={() => handleUpload("dir")}>
                {t("sftp.uploadDir")}
              </Button>
            </>
          ) : (
            <Button onClick={handleDownload} disabled={!selected}>
              {t("action.confirm")}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
