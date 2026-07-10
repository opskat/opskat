import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertTriangle,
  Clipboard,
  ClipboardCheck,
  Keyboard,
  Loader2,
  Maximize2,
  Minimize2,
  Monitor,
  Power,
  RefreshCw,
  Scaling,
  Settings2,
} from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  cn,
} from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { Segmented } from "@/components/asset/fields";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { CloseRDP, ConnectRDP, ResizeRDP, SendRDPInput, SetRDPClipboard } from "../../../wailsjs/go/rdp/RDP";
import type { asset_entity } from "../../../wailsjs/go/models";
import { SCANCODE, chordSequence, clampRemoteSize, formatDuration } from "./rdpInput";

interface RDPConfig {
  host?: string;
  port?: number;
  username?: string;
  domain?: string;
  width?: number;
  height?: number;
  clipboard?: boolean;
}

interface RDPEvent {
  type: "connecting" | "connected" | "frame" | "error" | "closed";
  sessionId: string;
  message?: string;
  error?: string;
  width?: number;
  height?: number;
  data?: string;
}

const PTR_MOVE = 0x0800;
const PTR_DOWN = 0x8000;
const PTR_BUTTON1 = 0x1000;
const PTR_BUTTON2 = 0x2000;
const PTR_BUTTON3 = 0x4000;

const CONTROL_SCANCODES: Record<string, number> = {
  Enter: SCANCODE.enter,
  Backspace: SCANCODE.backspace,
  Tab: SCANCODE.tab,
  Escape: SCANCODE.esc,
  Space: SCANCODE.space,
};

type ViewMode = "fit" | "actual";
type Status = "connecting" | "connected" | "error" | "closed";

const STATUS_PILL: Record<Status, string> = {
  connecting: "border-amber-500/25 bg-amber-500/10 text-amber-400",
  connected: "border-emerald-500/25 bg-emerald-500/10 text-emerald-400",
  error: "border-red-500/25 bg-red-500/10 text-red-400",
  closed: "border-border bg-muted text-muted-foreground",
};

function parseConfig(asset: asset_entity.Asset): RDPConfig {
  try {
    return JSON.parse(asset.Config || "{}");
  } catch {
    return {};
  }
}

function base64ToBytes(data: string): Uint8ClampedArray<ArrayBuffer> {
  const bin = atob(data);
  const out = new Uint8ClampedArray(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function buttonFlag(button: number): number {
  if (button === 2) return PTR_BUTTON2;
  if (button === 1) return PTR_BUTTON3;
  return PTR_BUTTON1;
}

export function RDPPanel({ asset, onEdit }: { asset: asset_entity.Asset; onEdit?: () => void }) {
  const { t } = useTranslation();
  const cfg = useMemo(() => parseConfig(asset), [asset]);
  const host = cfg.host || "";
  const port = cfg.port || 3389;
  const clipboardEnabled = cfg.clipboard !== false;

  const rootRef = useRef<HTMLDivElement | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sessionIdRef = useRef("");
  const viewModeRef = useRef<ViewMode>("fit");
  const requestedSizeRef = useRef(clampRemoteSize(cfg.width || 1280, cfg.height || 720));
  const frameSizeRef = useRef({ width: cfg.width || 1280, height: cfg.height || 720 });
  const [frameSize, setFrameSize] = useState(frameSizeRef.current);
  const [viewMode, setViewMode] = useState<ViewMode>("fit");
  const [status, setStatus] = useState<Status>("connecting");
  const [error, setError] = useState("");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [connectedAt, setConnectedAt] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [reconnectNonce, setReconnectNonce] = useState(0);

  useEffect(() => {
    viewModeRef.current = viewMode;
  }, [viewMode]);

  const updateFrameSize = useCallback((width: number, height: number) => {
    const next = { width, height };
    frameSizeRef.current = next;
    setFrameSize((current) => (current.width === width && current.height === height ? current : next));
  }, []);

  // Keep the remote resolution matched to the current viewport (Fit mode only).
  const syncViewportSize = useCallback(() => {
    if (viewModeRef.current !== "fit") return;
    const sessionId = sessionIdRef.current;
    const viewport = viewportRef.current;
    if (!sessionId || !viewport) return;
    const rect = viewport.getBoundingClientRect();
    const desired = clampRemoteSize(rect.width, rect.height);
    if (desired.width === requestedSizeRef.current.width && desired.height === requestedSizeRef.current.height) return;
    requestedSizeRef.current = desired;
    void ResizeRDP(sessionId, desired.width, desired.height).catch(() => undefined);
  }, []);

  const drawFrame = useCallback(
    (width: number, height: number, data: string) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      if (canvas.width !== width) canvas.width = width;
      if (canvas.height !== height) canvas.height = height;
      updateFrameSize(width, height);
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      const bytes = base64ToBytes(data);
      if (bytes.length !== width * height * 4) return;
      ctx.putImageData(new ImageData(bytes, width, height), 0, 0);
    },
    [updateFrameSize]
  );

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;

    async function connect() {
      setStatus("connecting");
      setError("");
      const initial = clampRemoteSize(cfg.width || 1280, cfg.height || 720);
      requestedSizeRef.current = initial;
      try {
        const sessionId = await ConnectRDP({ assetId: asset.ID, width: initial.width, height: initial.height });
        if (cancelled) {
          await CloseRDP(sessionId).catch(() => undefined);
          return;
        }
        sessionIdRef.current = sessionId;
        setStatus("connected");
        setConnectedAt(Date.now());
        syncViewportSize();
        unsubscribe = EventsOn(`rdp:event:${sessionId}`, (event: RDPEvent) => {
          if (event.type === "connecting") {
            setStatus("connecting");
            return;
          }
          if (event.type === "connected") {
            setStatus("connected");
            if (event.width && event.height) updateFrameSize(event.width, event.height);
            syncViewportSize();
            return;
          }
          if (event.type === "error") {
            setStatus("error");
            setError(event.error || t("asset.rdpError"));
            return;
          }
          if (event.type === "closed") {
            setStatus((current) => (current === "error" ? current : "closed"));
            return;
          }
          if (event.type === "frame" && event.data && event.width && event.height) {
            drawFrame(event.width, event.height, event.data);
          }
        });
      } catch (e) {
        if (!cancelled) {
          setStatus("error");
          setError(String(e));
        }
      }
    }

    connect();
    return () => {
      cancelled = true;
      unsubscribe?.();
      const sessionId = sessionIdRef.current;
      sessionIdRef.current = "";
      if (sessionId) void CloseRDP(sessionId).catch(() => undefined);
    };
    // Reconnect only when the target/resolution changes or reconnect() is invoked;
    // drawFrame/syncViewportSize/updateFrameSize are stable and t must not retrigger a reconnect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asset.ID, cfg.width, cfg.height, reconnectNonce]);

  // Follow later viewport size changes (window resize / pane drag), debounced.
  useEffect(() => {
    const viewport = viewportRef.current;
    if (!viewport || typeof ResizeObserver === "undefined") return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const ro = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = setTimeout(() => syncViewportSize(), 250);
    });
    ro.observe(viewport);
    return () => {
      clearTimeout(timer);
      ro.disconnect();
    };
  }, [syncViewportSize]);

  // Connection uptime ticker.
  useEffect(() => {
    if (status !== "connected" || connectedAt === null) return;
    const tick = () => setElapsed(Math.floor((Date.now() - connectedAt) / 1000));
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [status, connectedAt]);

  useEffect(() => {
    const handler = () => setIsFullscreen(!!document.fullscreenElement);
    document.addEventListener("fullscreenchange", handler);
    return () => document.removeEventListener("fullscreenchange", handler);
  }, []);

  function reconnect() {
    setReconnectNonce((n) => n + 1);
  }

  function disconnect() {
    const sessionId = sessionIdRef.current;
    sessionIdRef.current = "";
    if (sessionId) void CloseRDP(sessionId).catch(() => undefined);
    setStatus("closed");
    setConnectedAt(null);
  }

  function toggleFullscreen() {
    const el = rootRef.current;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    else void el.requestFullscreen?.().catch(() => undefined);
  }

  function sendChord(scancodes: number[]) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    for (const step of chordSequence(scancodes)) {
      void SendRDPInput({ sessionId, kind: "key", scancode: step.scancode, pressed: step.pressed }).catch(
        () => undefined
      );
    }
  }

  function remotePoint(e: React.MouseEvent<HTMLCanvasElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const { width, height } = frameSizeRef.current;
    return {
      x: Math.max(0, Math.min(width - 1, Math.round(((e.clientX - rect.left) / rect.width) * width))),
      y: Math.max(0, Math.min(height - 1, Math.round(((e.clientY - rect.top) / rect.height) * height))),
    };
  }

  function sendMouse(e: React.MouseEvent<HTMLCanvasElement>, buttons: number) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    const p = remotePoint(e);
    void SendRDPInput({ sessionId, kind: "mouse", x: p.x, y: p.y, buttons }).catch(() => undefined);
  }

  function handlePaste(e: React.ClipboardEvent<HTMLCanvasElement>) {
    const sessionId = sessionIdRef.current;
    const text = e.clipboardData.getData("text/plain");
    if (!sessionId || !text) return;
    e.preventDefault();
    void SetRDPClipboard(sessionId, text).catch(() => undefined);
  }

  function handleKey(e: React.KeyboardEvent<HTMLCanvasElement>, pressed: boolean) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    if (CONTROL_SCANCODES[e.key]) {
      e.preventDefault();
      void SendRDPInput({ sessionId, kind: "key", scancode: CONTROL_SCANCODES[e.key], pressed }).catch(
        () => undefined
      );
      return;
    }
    if (pressed && e.key.length === 1 && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      const codepoint = e.key.charCodeAt(0);
      void SendRDPInput({ sessionId, kind: "unicode", codepoint, pressed: true })
        .then(() => SendRDPInput({ sessionId, kind: "unicode", codepoint, pressed: false }))
        .catch(() => undefined);
    }
  }

  const connected = status === "connected";
  const canvasStyle =
    viewMode === "fit"
      ? ({ width: "100%", height: "100%", objectFit: "contain" } as const)
      : ({ width: `${frameSize.width}px`, height: `${frameSize.height}px` } as const);

  const specialKeys: { testid: string; label: string; scancodes: number[] }[] = [
    { testid: "rdp-key-cad", label: "Ctrl + Alt + Del", scancodes: [SCANCODE.ctrl, SCANCODE.alt, SCANCODE.del] },
    { testid: "rdp-key-alt-tab", label: "Alt + Tab", scancodes: [SCANCODE.alt, SCANCODE.tab] },
    { testid: "rdp-key-esc", label: "Esc", scancodes: [SCANCODE.esc] },
  ];

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col bg-background" data-testid="rdp-panel">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/30 px-3 text-xs">
        <Monitor className="h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate font-medium text-foreground">{asset.Name}</span>
        {host && (
          <span className="shrink-0 whitespace-nowrap rounded border bg-background/50 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
            {host}:{port}
          </span>
        )}
        <span
          data-testid="rdp-status"
          className={cn(
            "flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium",
            STATUS_PILL[status]
          )}
        >
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
          {status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
        </span>

        <div className="ml-auto flex shrink-0 items-center gap-2">
          <Segmented<ViewMode>
            value={viewMode}
            onChange={setViewMode}
            aria-label={t("asset.rdpViewMode")}
            className="h-7 w-[116px] rounded-md p-0.5"
            options={[
              { value: "fit", label: t("asset.rdpFit"), testid: "rdp-view-fit" },
              { value: "actual", label: t("asset.rdpActual"), testid: "rdp-view-actual" },
            ]}
          />

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 gap-1.5 px-2"
                data-testid="rdp-special-keys"
                disabled={!connected}
              >
                <Keyboard className="h-3.5 w-3.5" />
                Ctrl+Alt+Del
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {specialKeys.map((k) => (
                <DropdownMenuItem key={k.testid} data-testid={k.testid} onSelect={() => sendChord(k.scancodes)}>
                  {k.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          <Button
            type="button"
            variant="outline"
            size="icon"
            className="h-7 w-7"
            data-testid="rdp-fullscreen"
            title={isFullscreen ? t("asset.rdpExitFullscreen") : t("asset.rdpFullscreen")}
            onClick={toggleFullscreen}
          >
            {isFullscreen ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
          </Button>

          <span
            data-testid="rdp-clipboard"
            data-state={clipboardEnabled ? "on" : "off"}
            title={clipboardEnabled ? t("asset.rdpClipboardOn") : t("asset.rdpClipboardOff")}
            className={cn(
              "flex h-7 w-7 items-center justify-center rounded-md border",
              clipboardEnabled ? "text-emerald-400" : "text-muted-foreground/60"
            )}
          >
            {clipboardEnabled ? <ClipboardCheck className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
          </span>

          <Button
            type="button"
            variant="outline"
            size="icon"
            className="h-7 w-7 text-red-400 hover:text-red-400"
            data-testid="rdp-disconnect"
            title={t("asset.rdpDisconnect")}
            disabled={!connected}
            onClick={disconnect}
          >
            <Power className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      <div
        ref={viewportRef}
        className={`relative min-h-0 flex-1 bg-black ${viewMode === "actual" ? "overflow-auto" : "overflow-hidden"}`}
      >
        {status === "connecting" && (
          <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background text-sm text-muted-foreground">
            <Loader2 className="h-6 w-6 animate-spin text-primary" />
            <div className="font-medium text-foreground">{t("asset.rdpConnecting")}</div>
            {host && <div className="font-mono text-xs">{host}:{port}</div>}
          </div>
        )}
        {(status === "error" || status === "closed") && (
          <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-3 bg-background px-6 text-center">
            {status === "error" ? (
              <AlertTriangle className="h-8 w-8 text-destructive" />
            ) : (
              <Power className="h-8 w-8 text-muted-foreground" />
            )}
            <div className="text-base font-semibold text-foreground">
              {status === "error" ? t("asset.rdpError") : t("asset.rdpDisconnected")}
            </div>
            {status === "error" && error && (
              <div className="max-w-xl break-words text-sm text-muted-foreground">{error}</div>
            )}
            <div className="mt-1 flex items-center gap-2.5">
              <Button type="button" size="sm" className="gap-1.5" data-testid="rdp-reconnect" onClick={reconnect}>
                <RefreshCw className="h-3.5 w-3.5" />
                {t("asset.rdpReconnect")}
              </Button>
              {onEdit && (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="gap-1.5"
                  data-testid="rdp-edit"
                  onClick={onEdit}
                >
                  <Settings2 className="h-3.5 w-3.5" />
                  {t("asset.rdpEditConnection")}
                </Button>
              )}
            </div>
          </div>
        )}
        <canvas
          ref={canvasRef}
          data-testid="rdp-canvas"
          tabIndex={0}
          className={`outline-none ${viewMode === "actual" ? "block max-w-none" : "h-full w-full"}`}
          style={{ ...canvasStyle, imageRendering: "auto" }}
          onContextMenu={(e) => e.preventDefault()}
          onMouseMove={(e) => sendMouse(e, PTR_MOVE)}
          onMouseDown={(e) => {
            e.currentTarget.focus();
            sendMouse(e, PTR_DOWN | buttonFlag(e.button));
          }}
          onMouseUp={(e) => sendMouse(e, buttonFlag(e.button))}
          onWheel={(e) => {
            const sessionId = sessionIdRef.current;
            if (!sessionId) return;
            e.preventDefault();
            const p = remotePoint(e);
            void SendRDPInput({
              sessionId,
              kind: "wheel",
              x: p.x,
              y: p.y,
              delta: e.deltaY < 0 ? 1 : -1,
            }).catch(() => undefined);
          }}
          onKeyDown={(e) => handleKey(e, true)}
          onKeyUp={(e) => handleKey(e, false)}
          onPaste={handlePaste}
        />
      </div>

      <div className="flex h-6 shrink-0 items-center gap-3 border-t bg-muted/30 px-3 text-[11px] text-muted-foreground">
        <span className="flex items-center gap-1.5 font-mono">
          <Scaling className="h-3 w-3" />
          {frameSize.width} × {frameSize.height}
        </span>
        {viewMode === "fit" && (
          <span className="rounded border px-1.5 py-px text-[11px] text-sky-300/90">{t("asset.rdpAutoFit")}</span>
        )}
        {connected && <span className="ml-auto font-mono tabular-nums">{formatDuration(elapsed)}</span>}
      </div>
    </div>
  );
}
