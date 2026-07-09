import { useEffect, useMemo, useRef, useState } from "react";
import { Loader2, Monitor, PlugZap } from "lucide-react";
import { Button } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { CloseRDP, ConnectRDP, ResizeRDP, SendRDPInput, SetRDPClipboard } from "../../../wailsjs/go/rdp/RDP";
import type { asset_entity } from "../../../wailsjs/go/models";

interface RDPConfig {
  width?: number;
  height?: number;
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
  Enter: 0x1c,
  Backspace: 0x0e,
  Tab: 0x0f,
  Escape: 0x01,
  Space: 0x39,
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

export function RDPPanel({ asset }: { asset: asset_entity.Asset }) {
  const { t } = useTranslation();
  const cfg = useMemo(() => parseConfig(asset), [asset]);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sessionIdRef = useRef("");
  const frameSizeRef = useRef({ width: cfg.width || 1280, height: cfg.height || 720 });
  const [status, setStatus] = useState<"connecting" | "connected" | "error" | "closed">("connecting");
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;

    async function connect() {
      setStatus("connecting");
      setError("");
      try {
        const sessionId = await ConnectRDP({ assetId: asset.ID, width: cfg.width || 1280, height: cfg.height || 720 });
        if (cancelled) {
          await CloseRDP(sessionId).catch(() => undefined);
          return;
        }
        sessionIdRef.current = sessionId;
        setStatus("connected");
        unsubscribe = EventsOn(`rdp:event:${sessionId}`, (event: RDPEvent) => {
          if (event.type === "connected") {
            setStatus("connected");
            if (event.width && event.height) frameSizeRef.current = { width: event.width, height: event.height };
            return;
          }
          if (event.type === "error") {
            setStatus("error");
            setError(event.error || "RDP connection failed");
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
  }, [asset.ID, cfg.height, cfg.width]);

  function drawFrame(width: number, height: number, data: string) {
    const canvas = canvasRef.current;
    if (!canvas) return;
    if (canvas.width !== width) canvas.width = width;
    if (canvas.height !== height) canvas.height = height;
    frameSizeRef.current = { width, height };
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const bytes = base64ToBytes(data);
    if (bytes.length !== width * height * 4) return;
    ctx.putImageData(new ImageData(bytes, width, height), 0, 0);
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

  async function resizeToCanvas() {
    const sessionId = sessionIdRef.current;
    const canvas = canvasRef.current;
    if (!sessionId || !canvas) return;
    const rect = canvas.getBoundingClientRect();
    const width = Math.max(640, Math.round(rect.width * window.devicePixelRatio));
    const height = Math.max(480, Math.round(rect.height * window.devicePixelRatio));
    await ResizeRDP(sessionId, width, height);
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-background" data-testid="rdp-panel">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b bg-muted/30 px-3 text-xs text-muted-foreground">
        <Monitor className="h-4 w-4" />
        <span className="font-medium text-foreground">{asset.Name}</span>
        <span data-testid="rdp-status">
          {status === "connected" ? t("asset.rdpConnected") : t(`asset.rdpStatus.${status}`)}
        </span>
        <Button type="button" variant="ghost" size="sm" className="ml-auto h-7 px-2" onClick={resizeToCanvas}>
          <PlugZap className="mr-1 h-3.5 w-3.5" />
          {t("asset.rdpResize")}
        </Button>
      </div>
      <div className="relative min-h-0 flex-1 overflow-hidden bg-black">
        {status === "connecting" && (
          <div className="absolute inset-0 z-10 flex items-center justify-center gap-2 bg-background text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            {t("asset.rdpConnecting")}
          </div>
        )}
        {status === "error" && (
          <div className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 bg-background px-6 text-center text-sm">
            <div className="font-medium text-destructive">{t("asset.rdpError")}</div>
            <div className="max-w-xl break-words text-muted-foreground">{error}</div>
          </div>
        )}
        <canvas
          ref={canvasRef}
          data-testid="rdp-canvas"
          tabIndex={0}
          className="h-full w-full outline-none"
          style={{ imageRendering: "auto" }}
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
    </div>
  );
}
