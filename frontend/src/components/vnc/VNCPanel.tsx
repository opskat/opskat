import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ClipboardEvent as ReactClipboardEvent,
  type KeyboardEvent as ReactKeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { asset_entity } from "../../../wailsjs/go/models";
import {
  CheckVNCServerKey,
  ConnectVNC,
  DisconnectVNC,
  EncodeVNCClipboardText,
  StartVNCStream,
  TrustVNCServerKey,
} from "../../../wailsjs/go/vnc/VNC";
import { DisconnectSSH, OpenSFTPSession } from "../../../wailsjs/go/ssh/SSH";
import { ClipboardGetText, ClipboardSetText } from "../../../wailsjs/runtime";
import { FileManagerPanel } from "@/components/terminal/FileManagerPanel";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
import { decodeVNCClipboardText, pasteVNCClipboardText } from "@/lib/vncClipboard";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";
import { ServerIdentityPrompt } from "@/components/remote/ServerIdentityPrompt";
import type { RemoteStatus } from "@/components/remote/remoteChrome";
import { VNCToolbar, type VNCSpecialKey, type VNCViewMode } from "./VNCToolbar";
import type RFB from "@novnc/novnc";
import { securityPolicyForVNCEncryption, type VNCEncryptionPolicy } from "@/lib/vncSecurity";
import {
  startVNCClient,
  vncClientFailureMessageKey,
  type VNCClientHandle,
  type VNCNegotiatedSecurity,
  type VNCServerKeyCheck,
} from "@/lib/vncClient";

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
  encryption?: VNCEncryptionPolicy;
}

function parseVNCConnection(configJSON: string): { host: string; port: number; encryption?: VNCEncryptionPolicy } {
  try {
    const cfg: VNCConnectionConfig = JSON.parse(configJSON || "{}");
    return { host: cfg.host || "", port: cfg.port || 5900, encryption: cfg.encryption };
  } catch {
    return { host: "", port: 5900 };
  }
}

export function VNCPanel({ tabId, asset, onEdit }: VNCPanelProps) {
  const { t } = useTranslation();
  const { host, port, encryption } = parseVNCConnection(asset.Config);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const vncContainerRef = useRef<HTMLDivElement | null>(null);
  const rfbRef = useRef<RFB | null>(null);
  const clientRef = useRef<VNCClientHandle | null>(null);
  const trustDecisionRef = useRef<((trusted: boolean) => void) | null>(null);
  const connectAttemptRef = useRef(0);
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
  const [serverIdentity, setServerIdentity] = useState<VNCServerKeyCheck | null>(null);
  const [negotiatedSecurity, setNegotiatedSecurity] = useState<VNCNegotiatedSecurity | null>(null);
  const [connectedAt, setConnectedAt] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [resolution, setResolution] = useState<{ width: number; height: number }>({ width: 0, height: 0 });
  const [isFullscreen, setIsFullscreen] = useState(false);

  const connect = useCallback(async () => {
    const attempt = ++connectAttemptRef.current;
    setStatus("connecting");
    setError("");
    errorRef.current = "";
    setServerIdentity(null);
    setNegotiatedSecurity(null);
    setConnectedAt(null);
    setResolution({ width: 0, height: 0 });
    trustDecisionRef.current?.(false);
    trustDecisionRef.current = null;
    clientRef.current?.cleanup();
    clientRef.current = null;
    rfbRef.current = null;
    try {
      const next = (await ConnectVNC(asset.ID)) as VNCSession;
      if (attempt !== connectAttemptRef.current) {
        void DisconnectVNC(next.id);
        return;
      }
      setSession(next);
      setStatus("connecting");
    } catch (e) {
      if (attempt !== connectAttemptRef.current) return;
      const message = String(e);
      errorRef.current = message;
      setError(message);
      setStatus("error");
    }
  }, [asset.ID]);

  useEffect(
    () => () => {
      connectAttemptRef.current++;
    },
    []
  );

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
    try {
      const client = startVNCClient({
        target: container,
        source: channel,
        sessionId: session.id,
        username: session.username,
        password: session.password,
        securityPolicy: securityPolicyForVNCEncryption(encryption),
        checkServerKey: CheckVNCServerKey,
        trustServerKey: TrustVNCServerKey,
        requestServerTrust: (check) =>
          new Promise<boolean>((resolve) => {
            if (disposed) {
              resolve(false);
              return;
            }
            trustDecisionRef.current = resolve;
            setServerIdentity(check);
          }),
        openSource: () => channel.markOpen(),
        startTransport: () => StartVNCStream(session.id),
        closeTransport: () => void DisconnectVNC(session.id),
        onNegotiatedSecurity: (security) => {
          if (!disposed) setNegotiatedSecurity(security);
        },
        onConnected: markVNCConnected,
        onDisconnected: (clean) => {
          if (!disposed && clean) setStatus("closed");
        },
        onFailure: (failure) => {
          if (disposed) return;
          setServerIdentity(null);
          trustDecisionRef.current = null;
          const message = tRef.current(vncClientFailureMessageKey(failure.code));
          errorRef.current = message;
          setError(message);
          setStatus("error");
        },
        onClipboard: (text) => {
          if (!clipboardEnabledRef.current) return;
          ClipboardSetText(decodeVNCClipboardText(text)).catch((error) => toast.error(String(error)));
        },
      });
      client.rfb.scaleViewport = scaleViewportRef.current;
      client.rfb.clipViewport = true;
      client.rfb.resizeSession = false;
      client.rfb.background = "#000";
      clientRef.current = client;
      rfbRef.current = client.rfb;
      void client.result.catch(() => undefined);
    } catch (e) {
      channel.close();
      void DisconnectVNC(session.id);
      const message = String(e);
      errorRef.current = message;
      window.queueMicrotask(() => {
        if (disposed) return;
        setError(message);
        setStatus("error");
      });
    }
    return () => {
      disposed = true;
      trustDecisionRef.current?.(false);
      trustDecisionRef.current = null;
      clientRef.current?.cleanup();
      clientRef.current = null;
      rfbRef.current = null;
      container.innerHTML = "";
    };
  }, [encryption, session]);

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
    trustDecisionRef.current?.(false);
    trustDecisionRef.current = null;
    setServerIdentity(null);
    clientRef.current?.cleanup();
    if (session?.id) DisconnectVNC(session.id);
    setStatus("closed");
    setConnectedAt(null);
  };

  const approveVNCServer = () => {
    const decide = trustDecisionRef.current;
    trustDecisionRef.current = null;
    setServerIdentity(null);
    decide?.(true);
  };

  const cancelVNCServerVerification = () => {
    const decide = trustDecisionRef.current;
    trustDecisionRef.current = null;
    setServerIdentity(null);
    decide?.(false);
  };

  const connected = status === "connected";
  const filesEnabled = !!session?.fileSshAssetId;
  const securityStatus =
    connected && negotiatedSecurity ? (
      <span className="flex items-center gap-1.5">
        <span className="font-mono text-foreground">{negotiatedSecurity.name}</span>
        <span className={negotiatedSecurity.sessionEncrypted ? "text-info" : "text-warning"}>
          {t(
            negotiatedSecurity.sessionEncrypted
              ? "vnc.security.sessionEncrypted"
              : negotiatedSecurity.authenticationEncrypted
                ? "vnc.security.authenticationOnly"
                : "vnc.security.unencrypted",
            { aesBits: negotiatedSecurity.aesBits }
          )}
        </span>
      </span>
    ) : null;

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
            status={serverIdentity ? "connecting" : status}
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

          {serverIdentity && (
            <div className="absolute inset-0 z-30 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
              <div className="text-base font-semibold text-foreground">{t("vnc.verifyServerTitle")}</div>
              <div className="max-w-md text-sm text-muted-foreground">
                {serverIdentity.state === "changed" ? t("vnc.verifyServerChangedDesc") : t("vnc.verifyServerDesc")}
              </div>
              <ServerIdentityPrompt
                identity={{
                  host: serverIdentity.host,
                  port: serverIdentity.port,
                  keyType: "VNC RSA SHA-256",
                  fingerprint: serverIdentity.newFingerprint,
                  oldFingerprint: serverIdentity.oldFingerprint,
                  isChanged: serverIdentity.state === "changed",
                }}
                changedWarning={t("vnc.serverKeyChangedWarning")}
                oldFingerprintLabel={t("vnc.oldFingerprint")}
                rejectLabel={t("action.cancel")}
                trustLabel={serverIdentity.state === "changed" ? t("vnc.replaceServerKey") : t("vnc.trustAndConnect")}
                trustDestructive={serverIdentity.state === "changed"}
                onReject={cancelVNCServerVerification}
                onTrust={approveVNCServer}
                testIdPrefix="vnc-verify"
              />
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
          securityStatus || (connected && clipboardEnabled) ? (
            <>
              {securityStatus}
              {connected && clipboardEnabled && <span className="text-info">{t("vnc.clipboardSynced")}</span>}
            </>
          ) : undefined
        }
      />
    </div>
  );
}
