import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ClipboardEvent as ReactClipboardEvent,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, FolderOpen, Loader2, Maximize2, RefreshCw, ScreenShare } from "lucide-react";
import { Button, ConfirmDialog } from "@opskat/ui";
import { toast } from "sonner";
import { asset_entity } from "../../../wailsjs/go/models";
import {
  ConnectRemoteDesktop,
  DisconnectRemoteDesktop,
  EncodeVNCClipboardText,
  StartRemoteDesktopStream,
} from "../../../wailsjs/go/remote_desktop/RemoteDesktop";
import { DisconnectSSH, OpenSFTPSession } from "../../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../../wailsjs/runtime";
import { FileManagerPanel } from "@/components/terminal/FileManagerPanel";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
import { decodeVNCClipboardText, pasteVNCClipboardText } from "@/lib/vncClipboard";
import type RFB from "@novnc/novnc/lib/rfb";

interface RemoteDesktopPanelProps {
  tabId: string;
  asset: asset_entity.Asset;
}

interface RemoteDesktopSession {
  id: string;
  assetId: number;
  assetType: string;
  assetName: string;
  username?: string;
  password?: string;
  fileSshAssetId: number;
  fileEnabled: boolean;
  fileStatus: string;
  status: string;
}

export function RemoteDesktopPanel({ tabId, asset }: RemoteDesktopPanelProps) {
  const { t } = useTranslation();
  const vncContainerRef = useRef<HTMLDivElement | null>(null);
  const rfbRef = useRef<RFB | null>(null);
  const errorRef = useRef("");
  const serverApprovalRef = useRef(false);
  const scaleViewportRef = useRef(true);
  const keyboardPasteRef = useRef(false);
  const [session, setSession] = useState<RemoteDesktopSession | null>(null);
  const [status, setStatus] = useState("idle");
  const [error, setError] = useState("");
  const [scaleViewport, setScaleViewport] = useState(true);
  const [fileOpen, setFileOpen] = useState(false);
  const [fileWidth, setFileWidth] = useState(320);
  const [fileSessionId, setFileSessionId] = useState("");
  const [serverFingerprint, setServerFingerprint] = useState("");

  const connect = useCallback(async () => {
    setStatus("connecting");
    setError("");
    errorRef.current = "";
    serverApprovalRef.current = false;
    setServerFingerprint("");
    if (rfbRef.current) {
      try {
        rfbRef.current.disconnect();
      } catch {
        // ignore stale noVNC instance cleanup
      }
      rfbRef.current = null;
    }
    try {
      const next = (await ConnectRemoteDesktop(asset.ID)) as RemoteDesktopSession;
      setSession(next);
      setStatus("connecting");
    } catch (e) {
      const message = String(e);
      errorRef.current = message;
      setError(message);
      setStatus("error");
    }
  }, [asset.ID]);

  useEffect(() => {
    const timer = window.setTimeout(() => void connect(), 0);
    return () => window.clearTimeout(timer);
  }, [connect]);

  useEffect(() => {
    if (!session || !vncContainerRef.current) return;
    let disposed = false;
    let connectionStatePoll: number | undefined;
    const container = vncContainerRef.current;
    container.innerHTML = "";
    setStatus("connecting");
    const channel = new WailsRfbChannel(session.id);
    const markVNCConnected = () => {
      if (disposed) return;
      errorRef.current = "";
      setError("");
      setStatus("connected");
    };
    import("@novnc/novnc/lib/rfb")
      .then(({ default: RFBClient }) => {
        if (disposed || !container) {
          channel.close();
          return;
        }
        const rfb = new RFBClient(container, channel, {
          credentials: { username: session.username || "", password: session.password || "" },
        });
        rfb.scaleViewport = scaleViewportRef.current;
        rfb.clipViewport = true;
        rfb.resizeSession = false;
        rfb.background = "#000";
        rfb.addEventListener("connect", markVNCConnected);
        rfb.addEventListener("desktopname", markVNCConnected);
        rfb.addEventListener("capabilities", markVNCConnected);
        rfb.addEventListener("disconnect", (event) => {
          const e = event as CustomEvent<{ clean?: boolean }>;
          if (e.detail?.clean) {
            if (!disposed) setStatus("closed");
            return;
          }
          const message = errorRef.current || t("remoteDesktop.vncDisconnected");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("securityfailure", (event) => {
          const e = event as CustomEvent<{ status?: number; reason?: string }>;
          if (e.detail?.reason) {
            console.warn("VNC security failure", { status: e.detail?.status, reason: e.detail.reason });
          }
          const message = t("remoteDesktop.vncSecurityFailed");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("credentialsrequired", () => {
          const message = t("remoteDesktop.vncCredentialsRequired");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("serververification", (event) => {
          const e = event as CustomEvent<{ publickey?: Uint8Array }>;
          const publicKey = e.detail?.publickey;
          if (!publicKey) {
            const message = t("remoteDesktop.vncServerVerificationFailed");
            errorRef.current = message;
            setError(message);
            setStatus("error");
            return;
          }
          void window.crypto.subtle.digest("SHA-256", new Uint8Array(publicKey)).then((digest) => {
            if (disposed) return;
            const fingerprint = Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0"))
              .join(":")
              .toUpperCase();
            setServerFingerprint(fingerprint);
          });
        });
        rfb.addEventListener("clipboard", (event) => {
          const e = event as CustomEvent<{ text?: string }>;
          ClipboardSetText(decodeVNCClipboardText(e.detail?.text || "")).catch(() => {});
        });
        rfbRef.current = rfb;
        // 两阶段:先 markOpen(触发 onopen → noVNC 就绪),再启动后端读 pump,
        // 保证前端已订阅事件、noVNC 已就绪之后字节才开始流动,不丢 RFB 握手首包。
        channel.markOpen();
        void StartRemoteDesktopStream(session.id).catch((e) => {
          if (disposed) return;
          const message = String(e);
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        connectionStatePoll = window.setInterval(() => {
          if (!disposed && rfb._rfbConnectionState === "connected") {
            markVNCConnected();
            if (connectionStatePoll) window.clearInterval(connectionStatePoll);
          }
        }, 250);
        window.setTimeout(() => {
          if (connectionStatePoll) window.clearInterval(connectionStatePoll);
        }, 15000);
      })
      .catch((e) => {
        const message = String(e);
        errorRef.current = message;
        setError(message);
        setStatus("error");
      });
    return () => {
      disposed = true;
      channel.close();
      if (rfbRef.current) {
        try {
          rfbRef.current.disconnect();
        } catch {
          // ignore stale noVNC instance cleanup
        }
        rfbRef.current = null;
      }
      if (connectionStatePoll) window.clearInterval(connectionStatePoll);
      container.innerHTML = "";
    };
  }, [session, t]);

  useEffect(() => {
    scaleViewportRef.current = scaleViewport;
    if (rfbRef.current) {
      rfbRef.current.scaleViewport = scaleViewport;
    }
  }, [scaleViewport]);

  useEffect(() => {
    return () => {
      if (session?.id) DisconnectRemoteDesktop(session.id);
    };
  }, [session?.id]);

  useEffect(() => {
    return () => {
      if (fileSessionId) DisconnectSSH(fileSessionId);
    };
  }, [fileSessionId]);

  const openFiles = async () => {
    if (!session?.fileEnabled || !session.fileSshAssetId) return;
    setFileOpen((v) => !v);
    if (!fileSessionId) {
      try {
        setFileSessionId(await OpenSFTPSession(session.fileSshAssetId));
      } catch (e) {
        toast.error(String(e));
      }
    }
  };

  const pasteTextToVNC = async (text: string) => {
    const rfb = rfbRef.current;
    if (!rfb || !text) return;
    const clipboardSet = await pasteVNCClipboardText(rfb, text, EncodeVNCClipboardText);
    // When the text couldn't be placed on the remote clipboard it was typed
    // directly via keysyms; a follow-up Ctrl+V would paste stale clipboard data.
    if (rfbRef.current !== rfb || !clipboardSet) return;
    rfb.sendKey(0xffe3, "ControlLeft", true);
    rfb.sendKey(0x76, "KeyV", true);
    rfb.sendKey(0x76, "KeyV", false);
    rfb.sendKey(0xffe3, "ControlLeft", false);
  };

  const pasteToVNC = async () => {
    try {
      const text = await ClipboardGetText();
      await pasteTextToVNC(text);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const handleVNCPaste = (event: ReactClipboardEvent<HTMLDivElement>) => {
    if (!rfbRef.current) return;
    if (keyboardPasteRef.current) {
      event.preventDefault();
      return;
    }
    const text = event.clipboardData.getData("text/plain");
    if (!text) return;
    event.preventDefault();
    void pasteTextToVNC(text);
  };

  const handleVNCKeyDownCapture = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (!(event.ctrlKey || event.metaKey) || event.altKey || event.key.toLowerCase() !== "v") return;
    event.preventDefault();
    event.stopPropagation();
    keyboardPasteRef.current = true;
    void pasteToVNC().finally(() => {
      keyboardPasteRef.current = false;
    });
  };

  const approveVNCServer = () => {
    serverApprovalRef.current = true;
    setServerFingerprint("");
    rfbRef.current?.approveServer();
  };

  const cancelVNCServerVerification = () => {
    setServerFingerprint("");
    const message = t("remoteDesktop.vncServerVerificationCancelled");
    errorRef.current = message;
    setError(message);
    setStatus("error");
    rfbRef.current?.disconnect();
  };

  return (
    <div className="flex h-full min-h-0 bg-background">
      <ConfirmDialog
        open={!!serverFingerprint}
        onOpenChange={(open) => {
          if (open) return;
          if (serverApprovalRef.current) {
            serverApprovalRef.current = false;
            return;
          }
          cancelVNCServerVerification();
        }}
        title={t("remoteDesktop.verifyServerTitle")}
        description={
          <span className="block space-y-2">
            <span className="block">{t("remoteDesktop.verifyServerDesc")}</span>
            <code className="block break-all font-mono text-xs text-foreground">{serverFingerprint}</code>
          </span>
        }
        cancelText={t("action.cancel")}
        confirmText={t("remoteDesktop.approveServer")}
        confirmTestId="confirm-vnc-server"
        variant="default"
        onConfirm={approveVNCServer}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-11 shrink-0 items-center justify-between border-b px-3">
          <div className="flex min-w-0 items-center gap-2">
            <ScreenShare className="h-4 w-4 text-muted-foreground" />
            <span className="truncate text-sm font-medium">{asset.Name}</span>
            <span className="text-xs uppercase text-muted-foreground">{asset.Type}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Button variant="outline" size="sm" className="h-8" onClick={() => setScaleViewport((v) => !v)}>
              {scaleViewport ? t("remoteDesktop.scaleOn") : t("remoteDesktop.scaleOff")}
            </Button>
            <Button variant="outline" size="sm" className="h-8" onClick={pasteToVNC}>
              {t("remoteDesktop.pasteText")}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              onClick={() => vncContainerRef.current?.requestFullscreen()}
            >
              <Maximize2 className="h-4 w-4" />
            </Button>
            <Button variant="ghost" size="icon" className="h-8 w-8" onClick={connect}>
              <RefreshCw className="h-4 w-4" />
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-8 gap-1.5"
              onClick={openFiles}
              disabled={!session?.fileEnabled}
            >
              <FolderOpen className="h-4 w-4" />
              {t("remoteDesktop.files")}
            </Button>
          </div>
        </div>
        <div className="relative min-h-0 flex-1 overflow-hidden bg-black">
          {status === "connecting" && (
            <div className="absolute inset-0 z-10 flex items-center justify-center text-sm text-white/70">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              {t("remoteDesktop.connecting")}
            </div>
          )}
          {error && (
            <div className="absolute left-3 top-3 z-20 flex max-w-xl items-start gap-2 rounded border border-destructive/30 bg-background p-3 text-sm text-destructive shadow">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}
          <div
            ref={vncContainerRef}
            tabIndex={0}
            className="h-full w-full outline-none"
            onKeyDownCapture={handleVNCKeyDownCapture}
            onPaste={handleVNCPaste}
          />
        </div>
        {session && !session.fileEnabled && (
          <div className="border-t px-3 py-2 text-xs text-muted-foreground">{session.fileStatus}</div>
        )}
      </div>
      {fileOpen && fileSessionId && (
        <FileManagerPanel
          assetId={session?.fileSshAssetId}
          tabId={tabId}
          sessionId={fileSessionId}
          isActive
          isOpen
          width={fileWidth}
          onWidthChange={setFileWidth}
        />
      )}
    </div>
  );
}
