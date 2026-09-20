import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Download, Loader2, ScrollText, Search } from "lucide-react";
import { Button } from "@opskat/ui";
import { toast } from "sonner";
import { notifySuccess } from "@/lib/notify";
import { FetchK8sPodLogsTail, SaveK8sPodLogs, StartK8sPodLogs, StopK8sPodLogs } from "../../../wailsjs/go/k8s/K8s";
import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime";
import { K8sSectionCard } from "./K8sSectionCard";
import { K8sLogTerminal, type K8sLogTerminalHandle } from "./K8sLogTerminal";
import {
  extractOlderLogLines,
  K8S_LOG_INITIAL_TAIL_LINES,
  K8S_LOG_LOAD_MORE_LINES,
  K8S_LOG_MAX_TAIL_LINES,
} from "./k8sLogPagination";
import {
  buildLogBufferKey,
  createEmptyLogBuffer,
  MAX_LOG_CHUNKS,
  type LogTabState,
  type LogTabStateUpdate,
} from "./k8sLogState";

function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

interface K8sLogsPanelProps {
  assetId: number;
  containers: { name: string }[];
  namespace: string;
  podName: string;
  state: LogTabState;
  onStateChange: (update: LogTabStateUpdate) => void;
  pods?: { name: string }[];
  onSwitchPod?: (podName: string) => void;
}

export function K8sLogsPanel({
  assetId,
  containers,
  namespace,
  podName,
  state,
  onStateChange,
  pods,
  onSwitchPod,
}: K8sLogsPanelProps) {
  const { t } = useTranslation();
  const terminalRef = useRef<K8sLogTerminalHandle>(null);
  const [downloading, setDownloading] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const myStreamIDRef = useRef<string | null>(null);
  const eventNamesRef = useRef<{ data: string; err: string; end: string } | null>(null);
  const onStateChangeRef = useRef(onStateChange);
  const logBuffersRef = useRef(state.logBuffers);
  const loadingOlderRef = useRef(false);
  const activeContainer = state.logContainer || containers[0]?.name || "";
  // eslint-disable-next-line react-hooks/refs
  onStateChangeRef.current = onStateChange;
  // eslint-disable-next-line react-hooks/refs
  logBuffersRef.current = state.logBuffers;

  const getBuffer = useCallback(
    (key: string) => {
      const existing = logBuffersRef.current?.[key];
      if (existing) return existing;
      return createEmptyLogBuffer(activeContainer);
    },
    [activeContainer]
  );

  const offEvents = useCallback(() => {
    const names = eventNamesRef.current;
    if (!names) return;
    EventsOff(names.data);
    EventsOff(names.err);
    EventsOff(names.end);
    eventNamesRef.current = null;
  }, []);

  const teardownStream = useCallback(() => {
    if (myStreamIDRef.current) {
      StopK8sPodLogs(myStreamIDRef.current);
      myStreamIDRef.current = null;
    }
    offEvents();
    onStateChangeRef.current({ logStreamID: null });
  }, [offEvents]);

  const start = useCallback(() => {
    const bufferKey = buildLogBufferKey(podName, activeContainer);
    const buffer = getBuffer(bufferKey);
    const cachedChunks = buffer.chunks;
    teardownStream();
    if (cachedChunks.length === 0 && !buffer.olderPrefix) {
      terminalRef.current?.clear();
    }
    onStateChangeRef.current((prev) => {
      const existing = prev.logBuffers?.[bufferKey] || createEmptyLogBuffer(activeContainer);
      return {
        ...prev,
        logError: null,
        logBuffers: {
          ...(prev.logBuffers || {}),
          [bufferKey]: existing,
        },
      };
    });

    StartK8sPodLogs(assetId, namespace, podName, activeContainer, K8S_LOG_INITIAL_TAIL_LINES)
      .then((streamID: string) => {
        myStreamIDRef.current = streamID;
        onStateChangeRef.current({ logStreamID: streamID });

        const dataEvent = "k8s:log:" + streamID;
        const errEvent = "k8s:logerr:" + streamID;
        const endEvent = "k8s:logend:" + streamID;
        eventNamesRef.current = { data: dataEvent, err: errEvent, end: endEvent };

        EventsOn(dataEvent, (data: string) => {
          if (myStreamIDRef.current !== streamID) return;
          terminalRef.current?.write(base64ToBytes(data));
          onStateChangeRef.current((prev) => {
            const existing = prev.logBuffers?.[bufferKey] || createEmptyLogBuffer(activeContainer);
            const chunks = [...existing.chunks, data];
            const nextChunks = chunks.length > MAX_LOG_CHUNKS ? chunks.slice(chunks.length - MAX_LOG_CHUNKS) : chunks;
            return {
              ...prev,
              logBuffers: {
                ...(prev.logBuffers || {}),
                [bufferKey]: {
                  ...existing,
                  chunks: nextChunks,
                },
              },
            };
          });
        });

        EventsOn(errEvent, (err: string) => {
          if (myStreamIDRef.current !== streamID) return;
          if (err === "context canceled" || err.includes("context canceled")) return;
          onStateChangeRef.current({ logError: err });
        });

        EventsOn(endEvent, () => {
          if (myStreamIDRef.current !== streamID) return;
          myStreamIDRef.current = null;
          onStateChangeRef.current({ logStreamID: null });
          offEvents();
        });
      })
      .catch((e: unknown) => {
        onStateChangeRef.current({ logError: String(e) });
      });
  }, [activeContainer, assetId, getBuffer, namespace, podName, teardownStream, offEvents]);

  const loadOlderLogs = useCallback(async () => {
    if (!activeContainer || loadingOlderRef.current) return;
    const bufferKey = buildLogBufferKey(podName, activeContainer);
    const buffer = getBuffer(bufferKey);
    if (buffer.historyExhausted) return;
    if (buffer.loadedTailLines >= K8S_LOG_MAX_TAIL_LINES) return;

    const previousTail = buffer.loadedTailLines;
    const nextTail = Math.min(previousTail + K8S_LOG_LOAD_MORE_LINES, K8S_LOG_MAX_TAIL_LINES);
    loadingOlderRef.current = true;
    setLoadingOlder(true);
    try {
      const snapshot = await FetchK8sPodLogsTail(assetId, namespace, podName, activeContainer, nextTail);
      const { older, reachedStart } = extractOlderLogLines(snapshot, previousTail, nextTail);
      if (older) {
        await terminalRef.current?.prepend(older);
      }
      onStateChangeRef.current((prev) => {
        const existing = prev.logBuffers?.[bufferKey] || createEmptyLogBuffer(activeContainer);
        return {
          ...prev,
          logBuffers: {
            ...(prev.logBuffers || {}),
            [bufferKey]: {
              ...existing,
              loadedTailLines: nextTail,
              olderPrefix: existing.olderPrefix + older,
              historyExhausted: reachedStart || nextTail >= K8S_LOG_MAX_TAIL_LINES,
            },
          },
        };
      });
    } catch (e: unknown) {
      toast.error(`${t("asset.k8sPodLogsLoadOlderError")}: ${String(e)}`);
    } finally {
      loadingOlderRef.current = false;
      setLoadingOlder(false);
    }
  }, [activeContainer, assetId, getBuffer, namespace, podName, t]);

  useEffect(() => {
    return () => {
      if (myStreamIDRef.current) {
        StopK8sPodLogs(myStreamIDRef.current);
        myStreamIDRef.current = null;
      }
      offEvents();
    };
  }, [offEvents]);

  const handleDownloadLogs = async () => {
    if (downloading) return;
    setDownloading(true);
    try {
      const saved = await SaveK8sPodLogs(assetId, namespace, podName, activeContainer);
      if (saved) {
        notifySuccess(t("asset.k8sPodLogsDownloaded"));
      }
    } catch (e: unknown) {
      toast.error(`${t("asset.k8sPodLogsDownloadError")}: ${String(e)}`);
    } finally {
      setDownloading(false);
    }
  };

  useEffect(() => {
    teardownStream();
    terminalRef.current?.clear();
    const bufferKey = buildLogBufferKey(podName, activeContainer);
    const buffer = logBuffersRef.current?.[bufferKey];
    if (buffer?.olderPrefix) {
      terminalRef.current?.write(buffer.olderPrefix);
    }
    const chunks = buffer?.chunks || [];
    for (const chunk of chunks) {
      terminalRef.current?.write(base64ToBytes(chunk));
    }

    if (!activeContainer) return;
    start();
  }, [activeContainer, podName, start, teardownStream]);

  return (
    <K8sSectionCard className="flex flex-col h-full">
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground flex items-center gap-1.5">
          <ScrollText className="h-3.5 w-3.5" />
          {t("asset.k8sPodLogs")}
        </h4>
        <div className="flex items-center gap-2">
          {containers.length > 1 && (
            <select
              className="h-7 rounded-md border bg-background px-2 text-xs"
              value={activeContainer}
              onChange={(e) => onStateChange({ logContainer: e.target.value })}
            >
              {containers.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.name}
                </option>
              ))}
            </select>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={() => terminalRef.current?.toggleSearch()}
            title={t("asset.k8sPodLogsSearch")}
          >
            <Search className="h-3.5 w-3.5" />
            {t("asset.k8sPodLogsSearch")}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={handleDownloadLogs}
            disabled={downloading}
            title={t("asset.k8sPodLogsDownload")}
          >
            {downloading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}
            {t("asset.k8sPodLogsDownload")}
          </Button>
        </div>
      </div>
      {pods && pods.length > 0 && onSwitchPod && (
        <div className="flex items-center gap-2 mb-3">
          <span className="text-xs text-muted-foreground">Pod:</span>
          <select
            className="h-7 rounded-md border bg-background px-2 text-xs flex-1 min-w-0"
            value={podName}
            onChange={(e) => {
              const newPod = e.target.value;
              if (newPod !== podName) {
                onSwitchPod(newPod);
              }
            }}
          >
            {pods.map((p) => (
              <option key={p.name} value={p.name}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
      )}
      {state.logError && (
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive mb-3">
          {t("asset.k8sPodLogsError")}: {state.logError}
        </div>
      )}
      <div className="relative flex flex-1 min-h-0 flex-col">
        <K8sLogTerminal ref={terminalRef} onReachTop={loadOlderLogs} />
        {loadingOlder && (
          <div
            role="status"
            aria-live="polite"
            className="pointer-events-none absolute left-1/2 top-2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full border bg-popover/95 px-2.5 py-1 text-xs text-muted-foreground shadow-sm animate-in fade-in-0 slide-in-from-top-1 duration-150"
          >
            <Loader2 className="size-3 animate-spin" aria-hidden />
            {t("asset.k8sPodLogsLoadingOlder")}
          </div>
        )}
      </div>
    </K8sSectionCard>
  );
}
