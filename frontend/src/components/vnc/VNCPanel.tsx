import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ClipboardEvent as ReactClipboardEvent,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { Fingerprint } from "lucide-react";
import { Button } from "@opskat/ui";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { asset_entity } from "../../../wailsjs/go/models";
import { ConnectVNC, DisconnectVNC, EncodeVNCClipboardText, StartVNCStream } from "../../../wailsjs/go/vnc/VNC";
import { DisconnectSSH, OpenSFTPSession } from "../../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../../wailsjs/runtime";
import { FileManagerPanel } from "@/components/terminal/FileManagerPanel";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
import { decodeVNCClipboardText, pasteVNCClipboardText } from "@/lib/vncClipboard";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";
import type { RemoteStatus } from "@/components/remote/remoteChrome";
import { VNCToolbar, type VNCSpecialKey, type VNCViewMode } from "./VNCToolbar";
import type RFB from "@novnc/novnc/lib/rfb";

interface VNCPanelProps {
  tabId: string;
  asset: asset_entity.Asset;
  onEdit?: () => void;
}

interface VNCSession {
  id: string;
  username?: string;
  password?: string;
  fileSshAssetId: number;
}

interface VNCConnectionConfig {
  host?: string;
  port?: number;
}

function parseVNCEndpoint(configJSON: string): { host: string; port: number } {
  try {
    const cfg: VNCConnectionConfig = JSON.parse(configJSON || "{}");
    return { host: cfg.host || "", port: cfg.port || 5900 };
  } catch {
    return { host: "", port: 5900 };
  }
}

export function VNCPanel({ tabId, asset, onEdit }: VNCPanelProps) {
  const { t } = useTranslation();
  const { host, port } = parseVNCEndpoint(asset.Config);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const vncContainerRef = useRef<HTMLDivElement | null>(null);
  const rfbRef = useRef<RFB | null>(null);
  const errorRef = useRef("");
  const scaleViewportRef = useRef(true);
  const keyboardPasteRef = useRef(false);
  const clipboardEnabledRef = useRef(true);
  const tRef = useRef(t);
  const [session, setSession] = useState<VNCSession | null>(null);
  const [status, setStatus] = useState<RemoteStatus>("connecting");
  const [error, setError] = useState("");
  const [viewMode, setViewMode] = useState<VNCViewMode>("fit");
  const [clipboardEnabled, setClipboardEnabled] = useState(true);
  const [fileOpen, setFileOpen] = useState(false);
  const [fileWidth, setFileWidth] = useState(320);
  const [fileSessionId, setFileSessionId] = useState("");
  const [serverFingerprint, setServerFingerprint] = useState("");
  const [connectedAt, setConnectedAt] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [resolution, setResolution] = useState<{ width: number; height: number }>({ width: 0, height: 0 });
  const [isFullscreen, setIsFullscreen] = useState(false);

  const connect = useCallback(async () => {
    setStatus("connecting");
    setError("");
    errorRef.current = "";
    setServerFingerprint("");
    setConnectedAt(null);
    setResolution({ width: 0, height: 0 });
    if (rfbRef.current) {
      try {
        rfbRef.current.disconnect();
      } catch {
        // ignore stale noVNC instance cleanup
      }
      rfbRef.current = null;
    }
    try {
      const next = (await ConnectVNC(asset.ID)) as VNCSession;
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
    tRef.current = t;
  }, [t]);

  useEffect(() => {
    if (!session || !vncContainerRef.current) return;
    let disposed = false;
    let connectionStatePoll: number | undefined;
    const container = vncContainerRef.current;
    container.innerHTML = "";
    setStatus("connecting");
    const channel = new WailsRfbChannel(session.id);
    const readResolution = () => {
      const canvas = container.querySelector("canvas");
      if (canvas && canvas.width > 0) setResolution({ width: canvas.width, height: canvas.height });
    };
    const markVNCConnected = () => {
      if (disposed) return;
      errorRef.current = "";
      setError("");
      setStatus("connected");
      setConnectedAt((prev) => prev ?? Date.now());
      readResolution();
      window.requestAnimationFrame(() => {
        if (!disposed) readResolution();
      });
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
          const message = errorRef.current || tRef.current("vnc.disconnected");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("securityfailure", (event) => {
          const e = event as CustomEvent<{ status?: number; reason?: string }>;
          if (e.detail?.reason) {
            console.warn("VNC security failure", { status: e.detail?.status, reason: e.detail.reason });
          }
          const message = tRef.current("vnc.securityFailed");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("credentialsrequired", () => {
          const message = tRef.current("vnc.credentialsRequired");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("serververification", (event) => {
          const e = event as CustomEvent<{ publickey?: Uint8Array }>;
          const publicKey = e.detail?.publickey;
          if (!publicKey) {
            const message = tRef.current("vnc.serverVerificationFailed");
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
          if (!clipboardEnabledRef.current) return;
          const e = event as CustomEvent<{ text?: string }>;
          ClipboardSetText(decodeVNCClipboardText(e.detail?.text || "")).catch((error) => toast.error(String(error)));
        });
        rfbRef.current = rfb;
        // 两阶段:先 markOpen(触发 onopen → noVNC 就绪),再启动后端读 pump,
        // 保证前端已订阅事件、noVNC 已就绪之后字节才开始流动,不丢 RFB 握手首包。
        channel.markOpen();
        void StartVNCStream(session.id).catch((e) => {
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
  }, [session]);

  useEffect(() => {
    const fit = viewMode === "fit";
    scaleViewportRef.current = fit;
    if (rfbRef.current) rfbRef.current.scaleViewport = fit;
  }, [viewMode]);

  useEffect(() => {
    if (status !== "connected" || connectedAt === null) return;
    const tick = () => setElapsed(Math.floor((Date.now() - connectedAt) / 1000));
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [status, connectedAt]);

  useEffect(() => {
    const onChange = () => setIsFullscreen(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", onChange);
    return () => document.removeEventListener("fullscreenchange", onChange);
  }, []);

  useEffect(() => {
    return () => {
      if (session?.id) DisconnectVNC(session.id);
    };
  }, [session?.id]);

  useEffect(() => {
    return () => {
      if (fileSessionId) DisconnectSSH(fileSessionId);
    };
  }, [fileSessionId]);

  const openFiles = async () => {
    if (!session?.fileSshAssetId) return;
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
    if (!rfb || !text || !clipboardEnabledRef.current) return;
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
    if (!rfbRef.current || !clipboardEnabledRef.current) return;
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
    if (!clipboardEnabledRef.current) return;
    event.preventDefault();
    event.stopPropagation();
    keyboardPasteRef.current = true;
    void pasteToVNC().finally(() => {
      keyboardPasteRef.current = false;
    });
  };

  const sendSpecialKey = (key: VNCSpecialKey) => {
    const rfb = rfbRef.current;
    if (!rfb) return;
    if (key === "ctrl-alt-del") {
      rfb.sendCtrlAltDel();
      return;
    }
    if (key === "alt-tab") {
      rfb.sendKey(0xffe9, "AltLeft", true);
      rfb.sendKey(0xff09, "Tab", true);
      rfb.sendKey(0xff09, "Tab", false);
      rfb.sendKey(0xffe9, "AltLeft", false);
      return;
    }
    rfb.sendKey(0xff1b, "Escape", true);
    rfb.sendKey(0xff1b, "Escape", false);
  };

  const toggleClipboard = () => {
    const next = !clipboardEnabledRef.current;
    clipboardEnabledRef.current = next;
    setClipboardEnabled(next);
    notifySuccess(t(next ? "vnc.clipboardEnabled" : "vnc.clipboardDisabled"));
  };

  const toggleFullscreen = () => {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
    } else {
      void panelRef.current?.requestFullscreen();
    }
  };

  const disconnectSession = () => {
    const rfb = rfbRef.current;
    if (rfb) {
      try {
        rfb.disconnect();
      } catch {
        // ignore stale noVNC instance cleanup
      }
    }
    if (session?.id) DisconnectVNC(session.id);
    setStatus("closed");
    setConnectedAt(null);
  };

  const approveVNCServer = () => {
    setServerFingerprint("");
    rfbRef.current?.approveServer();
  };

  const cancelVNCServerVerification = () => {
    setServerFingerprint("");
    const message = t("vnc.serverVerificationCancelled");
    errorRef.current = message;
    setError(message);
    setStatus("error");
    rfbRef.current?.disconnect();
  };

  const connected = status === "connected";
  const filesEnabled = !!session?.fileSshAssetId;

  return (
    <div ref={panelRef} className="flex h-full min-h-0 flex-col bg-background">
      <VNCToolbar
        assetName={asset.Name}
        host={host}
        port={port}
        status={status}
        statusLabel={t(`vnc.status.${status}`)}
        viewMode={viewMode}
        clipboardEnabled={clipboardEnabled}
        filesEnabled={filesEnabled}
        filesOpen={fileOpen && !!fileSessionId}
        isFullscreen={isFullscreen}
        onViewModeChange={setViewMode}
        onSendSpecialKey={sendSpecialKey}
        onToggleClipboard={toggleClipboard}
        onToggleFiles={() => void openFiles()}
        onToggleFullscreen={toggleFullscreen}
        onDisconnect={disconnectSession}
      />

      <div className="flex min-h-0 flex-1">
        <div className="relative min-h-0 min-w-0 flex-1 overflow-hidden bg-black">
          <RemoteConnectionOverlay
            status={serverFingerprint ? "connecting" : status}
            error={error}
            host={host}
            port={port}
            labels={{
              connecting: t("vnc.connecting"),
              error: t("vnc.errorTitle"),
              closed: t("vnc.disconnectedTitle"),
              reconnect: t("vnc.reconnect"),
              edit: onEdit ? t("vnc.editConnection") : undefined,
            }}
            onReconnect={() => void connect()}
            onEdit={onEdit}
            reconnectTestId="vnc-reconnect"
            editTestId="vnc-edit"
          />

          {serverFingerprint && (
            <div className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
              <Fingerprint className="h-8 w-8 text-primary" />
              <div className="text-base font-semibold text-foreground">{t("vnc.verifyServerTitle")}</div>
              <div className="max-w-md text-sm text-muted-foreground">{t("vnc.verifyServerDesc")}</div>
              <div className="w-full max-w-md rounded-lg border bg-muted/30 p-3 text-left">
                <div className="mb-2 flex items-center gap-2 text-xs text-muted-foreground">
                  <Fingerprint className="h-3.5 w-3.5" />
                  RSA SHA-256
                </div>
                <code className="select-text block break-all font-mono text-xs text-foreground">
                  {serverFingerprint}
                </code>
              </div>
              <div className="mt-1 flex items-center gap-2.5">
                <Button
                  variant="outline"
                  size="sm"
                  data-testid="vnc-verify-cancel"
                  onClick={cancelVNCServerVerification}
                >
                  {t("action.cancel")}
                </Button>
                <Button size="sm" data-testid="vnc-verify-approve" onClick={approveVNCServer}>
                  {t("vnc.approveServer")}
                </Button>
              </div>
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

      <RemoteStatusBar
        width={resolution.width}
        height={resolution.height}
        showFit={viewMode === "fit"}
        fitLabel={t("vnc.autoFit")}
        connected={connected}
        elapsed={elapsed}
        extra={
          connected && clipboardEnabled ? <span className="text-info">{t("vnc.clipboardSynced")}</span> : undefined
        }
      />
    </div>
  );
}
