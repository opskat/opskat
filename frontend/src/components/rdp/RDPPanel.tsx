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
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, cn } from "@opskat/ui";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Segmented } from "@/components/asset/fields";
import { notifySuccess } from "@/lib/notify";
import { useRDPStore } from "@/stores/rdpStore";
import { ClipboardGetText, ClipboardSetText, EventsOn } from "../../../wailsjs/runtime/runtime";
import {
  CloseRDP,
  ConnectRDP,
  ResizeRDP,
  SendRDPInput,
  SetRDPClipboard,
  SetRDPClipboardEnabled,
  SetRDPClipboardFilesFromLocal,
} from "../../../wailsjs/go/rdp/RDP";
import type { asset_entity } from "../../../wailsjs/go/models";
import {
  SCANCODE,
  chordSequence,
  clampRemoteSize,
  formatDuration,
  planKeyDown,
  remotePoint,
  scancodeFor,
} from "./rdpInput";
import { decodeFrameBytes } from "./rdpFrame";
import { pointerCursorStyle } from "./rdpCursor";

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
  type:
    | "connecting"
    | "connected"
    | "frame"
    | "pointer"
    | "clipboard"
    | "clipboard-files"
    | "clipboard-error"
    | "error"
    | "closed";
  sessionId: string;
  message?: string;
  error?: string;
  // Full framebuffer dimensions; a frame event carries the dirty sub-region
  // at (x, y) sized rectWidth×rectHeight. A pointer event reuses width/height
  // and data for the cursor image, anchored at (hotspotX, hotspotY).
  width?: number;
  height?: number;
  x?: number;
  y?: number;
  rectWidth?: number;
  rectHeight?: number;
  data?: string;
  text?: string;
  count?: number;
  pointerType?: string;
  hotspotX?: number;
  hotspotY?: number;
}

const PTR_MOVE = 0x0800;
const PTR_DOWN = 0x8000;
const PTR_BUTTON1 = 0x1000;
const PTR_BUTTON2 = 0x2000;
const PTR_BUTTON3 = 0x4000;

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
  const configuredClipboardEnabled = cfg.clipboard !== false;

  const rootRef = useRef<HTMLDivElement | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sessionIdRef = useRef("");
  const viewModeRef = useRef<ViewMode>("fit");
  const requestedSizeRef = useRef(clampRemoteSize(cfg.width || 1280, cfg.height || 720));
  const frameSizeRef = useRef({ width: cfg.width || 1280, height: cfg.height || 720 });
  // Physical keys we've forwarded a scancode press for, so every key is released
  // exactly once (on keyup or blur) and a modifier can never stick down remotely.
  const pressedKeysRef = useRef<Set<string>>(new Set());
  // Serialize keyboard sends: Wails runs each bound-method call on its own
  // goroutine, so unserialized press/release events can reach the remote out of
  // order (release before press) and a chord or keystroke silently no-ops.
  const keyQueueRef = useRef<Promise<unknown>>(Promise.resolve());
  // Mouse buttons cross the same boundary and must retain press/release order.
  const mouseButtonQueueRef = useRef<Promise<unknown>>(Promise.resolve());
  // Track buttons until a canvas, window-level, or blur release is observed.
  const pressedMouseButtonsRef = useRef<Set<number>>(new Set());
  const lastMousePointRef = useRef({ x: 0, y: 0 });
  // Mouse moves are coalesced to one send per animation frame: forwarding every
  // DOM mousemove floods the IPC bridge (~120 calls/s while dragging) for
  // positions the remote will never render.
  const pendingMoveRef = useRef<{ x: number; y: number } | null>(null);
  const moveRafRef = useRef(0);
  const [frameSize, setFrameSize] = useState({ width: cfg.width || 1280, height: cfg.height || 720 });
  const [viewMode, setViewMode] = useState<ViewMode>("fit");
  const [status, setStatus] = useState<Status>("connecting");
  const [error, setError] = useState("");
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [clipboardEnabled, setClipboardEnabled] = useState(configuredClipboardEnabled);
  const [connectedAt, setConnectedAt] = useState<number | null>(null);
  const [cursorStyle, setCursorStyle] = useState("default");
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

  // Frame events carry only the dirty sub-region of the framebuffer, so each
  // putImageData touches just the changed pixels; a full frame is the special
  // case where the rect covers everything (connect, resize).
  const drawFrame = useCallback(
    (frame: {
      width: number;
      height: number;
      x?: number;
      y?: number;
      rectWidth?: number;
      rectHeight?: number;
      data: string;
    }) => {
      const canvas = canvasRef.current;
      if (!canvas) return;
      if (canvas.width !== frame.width) canvas.width = frame.width;
      if (canvas.height !== frame.height) canvas.height = frame.height;
      updateFrameSize(frame.width, frame.height);
      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      const rectWidth = frame.rectWidth || frame.width;
      const rectHeight = frame.rectHeight || frame.height;
      const bytes = decodeFrameBytes(frame.data);
      if (bytes.length !== rectWidth * rectHeight * 4) return;
      ctx.putImageData(new ImageData(bytes, rectWidth, rectHeight), frame.x || 0, frame.y || 0);
    },
    [updateFrameSize]
  );

  useEffect(() => {
    let cancelled = false;
    let unsubscribe: (() => void) | null = null;

    async function connect() {
      setStatus("connecting");
      setError("");
      // Connect at the current viewport size so the session starts already fitted —
      // avoids an immediate resize/reconnect on connect (that path is fragile and can
      // drop the first framebuffer). Later window changes are handled by the observer.
      const rect = viewportRef.current?.getBoundingClientRect();
      const initial =
        rect && rect.width > 1 && rect.height > 1
          ? clampRemoteSize(rect.width, rect.height)
          : clampRemoteSize(cfg.width || 1280, cfg.height || 720);
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
        useRDPStore.getState().setAssetConnected(asset.ID, true);
        unsubscribe = EventsOn(`rdp:event:${sessionId}`, (event: RDPEvent) => {
          if (event.type === "connecting") {
            setStatus("connecting");
            return;
          }
          if (event.type === "connected") {
            setStatus("connected");
            useRDPStore.getState().setAssetConnected(asset.ID, true);
            if (event.width && event.height) updateFrameSize(event.width, event.height);
            return;
          }
          if (event.type === "error") {
            setStatus("error");
            setError(event.error || t("asset.rdpError"));
            useRDPStore.getState().setAssetConnected(asset.ID, false);
            return;
          }
          if (event.type === "closed") {
            setStatus((current) => (current === "error" ? current : "closed"));
            useRDPStore.getState().setAssetConnected(asset.ID, false);
            return;
          }
          if (event.type === "clipboard" && event.text !== undefined) {
            void ClipboardSetText(event.text)
              .then((written) => {
                if (!written) throw new Error(t("asset.rdpClipboardWriteFailed"));
                notifySuccess(t("asset.rdpClipboardReceived"));
              })
              .catch((e) => toast.error(`${t("asset.rdpClipboardWriteFailed")}: ${String(e)}`));
            return;
          }
          if (event.type === "clipboard-files") {
            notifySuccess(t("asset.rdpClipboardFilesReceived", { count: event.count || 0 }));
            return;
          }
          if (event.type === "clipboard-error") {
            toast.error(`${t("asset.rdpClipboardReceiveFailed")}: ${event.error || t("asset.rdpError")}`);
            return;
          }
          if (event.type === "pointer") {
            setCursorStyle(pointerCursorStyle(event));
            return;
          }
          if (event.type === "frame" && event.data && event.width && event.height) {
            drawFrame({
              width: event.width,
              height: event.height,
              x: event.x,
              y: event.y,
              rectWidth: event.rectWidth,
              rectHeight: event.rectHeight,
              data: event.data,
            });
          }
        });
      } catch (e) {
        if (!cancelled) {
          setStatus("error");
          setError(String(e));
          useRDPStore.getState().setAssetConnected(asset.ID, false);
        }
      }
    }

    connect();
    return () => {
      cancelled = true;
      unsubscribe?.();
      const sessionId = sessionIdRef.current;
      sessionIdRef.current = "";
      useRDPStore.getState().setAssetConnected(asset.ID, false);
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
    if (!isFullscreen) return;
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Escape") setIsFullscreen(false);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [isFullscreen]);

  useEffect(() => {
    const releaseButtonOutsideCanvas = (event: MouseEvent) => {
      if (!pressedMouseButtonsRef.current.delete(event.button)) return;
      const canvas = canvasRef.current;
      const point = canvas
        ? remotePoint(event, canvas.getBoundingClientRect(), frameSizeRef.current, viewModeRef.current)
        : lastMousePointRef.current;
      queueMouseInput(point, buttonFlag(event.button));
    };
    const releaseAllButtons = () => {
      const point = lastMousePointRef.current;
      for (const button of pressedMouseButtonsRef.current) queueMouseInput(point, buttonFlag(button));
      pressedMouseButtonsRef.current.clear();
    };
    window.addEventListener("mouseup", releaseButtonOutsideCanvas);
    window.addEventListener("blur", releaseAllButtons);
    return () => {
      window.removeEventListener("mouseup", releaseButtonOutsideCanvas);
      window.removeEventListener("blur", releaseAllButtons);
      cancelAnimationFrame(moveRafRef.current);
    };
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
    useRDPStore.getState().setAssetConnected(asset.ID, false);
  }

  function toggleFullscreen() {
    setIsFullscreen((current) => !current);
  }

  async function toggleClipboard() {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    const enabled = !clipboardEnabled;
    try {
      await SetRDPClipboardEnabled(sessionId, enabled);
      setClipboardEnabled(enabled);
      notifySuccess(t(enabled ? "asset.rdpClipboardEnabled" : "asset.rdpClipboardDisabled"));
    } catch (e) {
      toast.error(`${t("asset.rdpClipboardToggleFailed")}: ${String(e)}`);
    }
  }

  async function sendChord(scancodes: number[]) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    // Await each key before sending the next: Wails dispatches every bound-method
    // call on its own goroutine, so firing the press/release scancodes concurrently
    // lets the remote receive them out of order (release before press) and the chord
    // silently no-ops — the cause of "sometimes works, sometimes doesn't".
    for (const step of chordSequence(scancodes)) {
      try {
        await SendRDPInput({ sessionId, kind: "key", scancode: step.scancode, pressed: step.pressed });
      } catch {
        return;
      }
    }
  }

  function eventRemotePoint(e: React.MouseEvent<HTMLCanvasElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    return remotePoint(e, rect, frameSizeRef.current, viewModeRef.current);
  }

  function queueMouseInput(point: { x: number; y: number }, buttons: number) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    mouseButtonQueueRef.current = mouseButtonQueueRef.current
      .catch(() => undefined)
      .then(() => SendRDPInput({ sessionId, kind: "mouse", x: point.x, y: point.y, buttons }));
    void mouseButtonQueueRef.current.catch(() => undefined);
  }

  // Button presses/releases go through the serialized queue with their own
  // coordinates, so any move still pending for this frame is stale — drop it.
  function sendMouseButton(e: React.MouseEvent<HTMLCanvasElement>, buttons: number) {
    const point = eventRemotePoint(e);
    lastMousePointRef.current = point;
    pendingMoveRef.current = null;
    queueMouseInput(point, buttons);
  }

  function queueMouseMove(e: React.MouseEvent<HTMLCanvasElement>) {
    const point = eventRemotePoint(e);
    lastMousePointRef.current = point;
    pendingMoveRef.current = point;
    if (moveRafRef.current) return;
    moveRafRef.current = requestAnimationFrame(() => {
      moveRafRef.current = 0;
      const pending = pendingMoveRef.current;
      pendingMoveRef.current = null;
      const sessionId = sessionIdRef.current;
      if (!pending || !sessionId) return;
      void SendRDPInput({ sessionId, kind: "mouse", x: pending.x, y: pending.y, buttons: PTR_MOVE }).catch(
        () => undefined
      );
    });
  }

  function handlePaste(e: React.ClipboardEvent<HTMLCanvasElement>) {
    const sessionId = sessionIdRef.current;
    const text = e.clipboardData.getData("text/plain");
    if (!sessionId || !clipboardEnabled || !text) return;
    e.preventDefault();
    void SetRDPClipboard(sessionId, text)
      .then(() => {
        void sendChord([SCANCODE.ctrl, SCANCODE.v]);
        notifySuccess(t("asset.rdpClipboardSent"));
      })
      .catch((e) => toast.error(`${t("asset.rdpClipboardSendFailed")}: ${String(e)}`));
  }

  async function syncLocalClipboard(
    sessionId: string
  ): Promise<{ kind: "files"; count: number } | { kind: "text" } | null> {
    const fileCount = await SetRDPClipboardFilesFromLocal(sessionId);
    if (fileCount > 0) return { kind: "files", count: fileCount };

    const text = await ClipboardGetText();
    if (!text) return null;
    await SetRDPClipboard(sessionId, text);
    return { kind: "text" };
  }

  async function pasteLocalClipboard(sessionId: string) {
    try {
      const synced = await syncLocalClipboard(sessionId);
      if (!synced) return;
      await sendChord([SCANCODE.ctrl, SCANCODE.v]);
      notifySuccess(
        synced.kind === "files"
          ? t("asset.rdpClipboardFilesSent", { count: synced.count })
          : t("asset.rdpClipboardSent")
      );
    } catch (e) {
      toast.error(`${t("asset.rdpClipboardSendFailed")}: ${String(e)}`);
    }
  }

  function handleContextMenu(e: React.MouseEvent<HTMLCanvasElement>) {
    e.preventDefault();
    const sessionId = sessionIdRef.current;
    if (!sessionId || !clipboardEnabled) return;
    void syncLocalClipboard(sessionId).catch((e) => toast.error(`${t("asset.rdpClipboardSendFailed")}: ${String(e)}`));
  }

  // Enqueue a keyboard event behind the previous one so the remote always
  // receives presses and releases in order (see keyQueueRef).
  function queueKeyInput(
    event: { kind: "key"; scancode: number; extended?: boolean } | { kind: "unicode"; codepoint: number },
    pressed: boolean
  ) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    keyQueueRef.current = keyQueueRef.current
      .catch(() => undefined)
      .then(() => SendRDPInput({ sessionId, ...event, pressed }));
    void keyQueueRef.current.catch(() => undefined);
  }

  // Release every physical key we're still holding — called on blur so a modifier
  // held while focus leaves the canvas doesn't stay stuck down on the remote.
  function releaseAllKeys() {
    const pressed = pressedKeysRef.current;
    for (const code of pressed) {
      const sc = scancodeFor(code);
      if (sc) queueKeyInput({ kind: "key", scancode: sc.scancode, extended: sc.extended }, false);
    }
    pressed.clear();
  }

  function handleKey(e: React.KeyboardEvent<HTMLCanvasElement>, pressed: boolean) {
    const sessionId = sessionIdRef.current;
    if (!sessionId) return;
    if (pressed && clipboardEnabled && e.key.toLowerCase() === "v" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      e.stopPropagation();
      void pasteLocalClipboard(sessionId);
      return;
    }

    if (!pressed) {
      // Mirror any scancode press we sent, even if the modifier state changed
      // meanwhile (e.g. Ctrl released before the letter), so nothing sticks down.
      const sc = scancodeFor(e.code);
      if (sc && pressedKeysRef.current.delete(e.code)) {
        e.preventDefault();
        queueKeyInput({ kind: "key", scancode: sc.scancode, extended: sc.extended }, false);
      }
      return;
    }

    const plan = planKeyDown(e.code, e.key, { ctrl: e.ctrlKey, alt: e.altKey, meta: e.metaKey });
    if (plan.kind === "scancode") {
      e.preventDefault();
      pressedKeysRef.current.add(e.code);
      queueKeyInput({ kind: "key", scancode: plan.scancode, extended: plan.extended }, true);
    } else if (plan.kind === "unicode") {
      e.preventDefault();
      queueKeyInput({ kind: "unicode", codepoint: plan.codepoint }, true);
      queueKeyInput({ kind: "unicode", codepoint: plan.codepoint }, false);
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
    <div
      ref={rootRef}
      className={cn(
        "flex h-full min-h-0 flex-col bg-background",
        isFullscreen && "fixed inset-0 z-[100] h-screen w-screen"
      )}
      data-testid="rdp-panel"
      data-fullscreen={isFullscreen}
    >
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
                <DropdownMenuItem key={k.testid} data-testid={k.testid} onSelect={() => void sendChord(k.scancodes)}>
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

          <Button
            type="button"
            variant="outline"
            size="icon"
            data-testid="rdp-clipboard"
            data-state={clipboardEnabled ? "on" : "off"}
            title={clipboardEnabled ? t("asset.rdpClipboardOn") : t("asset.rdpClipboardOff")}
            className={cn("h-7 w-7", clipboardEnabled ? "text-emerald-400" : "text-muted-foreground/60")}
            disabled={!connected}
            onClick={() => void toggleClipboard()}
          >
            {clipboardEnabled ? <ClipboardCheck className="h-3.5 w-3.5" /> : <Clipboard className="h-3.5 w-3.5" />}
          </Button>

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
            {host && (
              <div className="font-mono text-xs">
                {host}:{port}
              </div>
            )}
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
          style={{ ...canvasStyle, imageRendering: "auto", cursor: cursorStyle }}
          onContextMenu={handleContextMenu}
          onMouseMove={queueMouseMove}
          onMouseDown={(e) => {
            e.currentTarget.focus();
            pressedMouseButtonsRef.current.add(e.button);
            sendMouseButton(e, PTR_DOWN | buttonFlag(e.button));
          }}
          onMouseUp={(e) => {
            if (!pressedMouseButtonsRef.current.delete(e.button)) return;
            sendMouseButton(e, buttonFlag(e.button));
          }}
          onWheel={(e) => {
            const sessionId = sessionIdRef.current;
            if (!sessionId) return;
            e.preventDefault();
            const p = eventRemotePoint(e);
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
          onBlur={releaseAllKeys}
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
