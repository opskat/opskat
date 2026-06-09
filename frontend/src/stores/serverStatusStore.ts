import { create } from "zustand";
import { GetSSHServerStatus } from "../../wailsjs/go/ssh/SSH";

export interface ServerStatusSnapshot {
  hostname?: string;
  os?: string;
  uptime?: string;
  cpuPercent?: number;
  load1?: number;
  load5?: number;
  load15?: number;
  memoryUsedBytes?: number;
  memoryTotalBytes?: number;
  diskMount?: string;
  diskUsedBytes?: number;
  diskTotalBytes?: number;
  collectedAt?: number;
}

export interface SessionStatusState {
  buffer: ServerStatusSnapshot[];
  paused: boolean;
  intervalMs: number;
  loading: boolean;
  error: string | null;
}

interface ServerStatusStore {
  sessions: Record<string, SessionStatusState>;
  activate: (sessionId: string) => void;
  deactivate: (sessionId: string) => void;
  setPaused: (sessionId: string, paused: boolean) => void;
  setSessionInterval: (sessionId: string, intervalMs: number) => void;
  refreshNow: (sessionId: string) => Promise<void>;
}

export const MAX_POINTS = 120;
export const DEFAULT_INTERVAL_MS = 5000;

const timers: Record<string, ReturnType<typeof setInterval>> = {};
const abortControllers: Record<string, AbortController> = {};

function isSessionGone(message: string): boolean {
  return message.includes("会话不存在") || message.includes("session not found");
}

export const useServerStatusStore = create<ServerStatusStore>((set, get) => {
  function patch(sessionId: string, partial: Partial<SessionStatusState>) {
    set((st) => {
      const cur = st.sessions[sessionId];
      if (!cur) return st;
      return { sessions: { ...st.sessions, [sessionId]: { ...cur, ...partial } } };
    });
  }

  async function tick(sessionId: string) {
    const cur = get().sessions[sessionId];
    if (!cur) return;

    // 取消前一个请求，避免并发导致数据乱序
    if (abortControllers[sessionId]) {
      abortControllers[sessionId].abort();
    }
    abortControllers[sessionId] = new AbortController();

    patch(sessionId, { loading: true });
    try {
      const result = (await GetSSHServerStatus(sessionId)) as ServerStatusSnapshot | null;
      set((st) => {
        const s = st.sessions[sessionId];
        if (!s) return st;

        // 时间戳校验：只追加比 buffer 最后一项更新的数据，防止乱序旧数据覆盖新数据
        let buffer = s.buffer;
        if (result) {
          const lastItem = buffer[buffer.length - 1];
          const lastTimestamp = lastItem?.collectedAt ?? 0;
          const newTimestamp = result.collectedAt ?? 0;
          if (newTimestamp >= lastTimestamp) {
            buffer = [...buffer, result].slice(-MAX_POINTS);
          }
          // 否则丢弃乱序的旧数据
        }

        return { sessions: { ...st.sessions, [sessionId]: { ...s, buffer, loading: false, error: null } } };
      });
    } catch (err) {
      const message = String(err);
      if (isSessionGone(message)) {
        get().deactivate(sessionId);
        return;
      }
      patch(sessionId, { loading: false, error: message });
    } finally {
      // 清理 abort controller
      if (abortControllers[sessionId]) {
        delete abortControllers[sessionId];
      }
    }
  }

  function startTimer(sessionId: string, intervalMs: number) {
    if (timers[sessionId]) clearInterval(timers[sessionId]);
    timers[sessionId] = setInterval(() => {
      void tick(sessionId);
    }, intervalMs);
  }

  function stopTimer(sessionId: string) {
    if (timers[sessionId]) {
      clearInterval(timers[sessionId]);
      delete timers[sessionId];
    }
  }

  return {
    sessions: {},

    activate: (sessionId) => {
      if (get().sessions[sessionId]) return; // idempotent: already active
      set((st) => ({
        sessions: {
          ...st.sessions,
          [sessionId]: { buffer: [], paused: false, intervalMs: DEFAULT_INTERVAL_MS, loading: false, error: null },
        },
      }));
      void tick(sessionId); // sample first frame immediately
      startTimer(sessionId, DEFAULT_INTERVAL_MS);
    },

    deactivate: (sessionId) => {
      stopTimer(sessionId);
      set((st) => {
        if (!st.sessions[sessionId]) return st;
        const next = { ...st.sessions };
        delete next[sessionId];
        return { sessions: next };
      });
    },

    setPaused: (sessionId, paused) => {
      patch(sessionId, { paused });
      if (paused) {
        stopTimer(sessionId);
        return;
      }
      const cur = get().sessions[sessionId];
      if (cur) {
        void tick(sessionId);
        startTimer(sessionId, cur.intervalMs);
      }
    },

    setSessionInterval: (sessionId, intervalMs) => {
      const cur = get().sessions[sessionId];
      if (!cur) return;
      patch(sessionId, { intervalMs });
      if (!cur.paused) startTimer(sessionId, intervalMs);
    },

    refreshNow: async (sessionId) => {
      await tick(sessionId);
    },
  };
});
