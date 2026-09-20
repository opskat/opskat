import { K8S_LOG_INITIAL_TAIL_LINES } from "./k8sLogPagination";

export const MAX_LOG_CHUNKS = 2000;

export interface LogBufferState {
  container: string;
  loadedTailLines: number;
  olderPrefix: string;
  historyExhausted: boolean;
  chunks: string[];
}

export interface LogTabState {
  logStreamID: string | null;
  logContainer: string;
  logError: string | null;
  currentPod?: string;
  logBuffers?: Record<string, LogBufferState>;
}

export type LogTabStateUpdate = Partial<LogTabState> | ((prev: LogTabState) => LogTabState);

export function buildLogBufferKey(podName: string, container: string) {
  return `${podName}::${container}`;
}

export function createEmptyLogBuffer(container: string): LogBufferState {
  return {
    container,
    loadedTailLines: K8S_LOG_INITIAL_TAIL_LINES,
    olderPrefix: "",
    historyExhausted: false,
    chunks: [],
  };
}
