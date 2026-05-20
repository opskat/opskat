import {
  GetExternalEditSettings,
  SaveExternalEditSettings,
  SelectExternalEditorExecutable,
  SelectExternalEditWorkspaceRoot,
  OpenExternalEdit as OpenExternalEditBinding,
  ListExternalEditSessions,
  SaveExternalEditSession,
  RefreshExternalEditSession,
  ResolveExternalEditConflict,
  CompareExternalEditSession,
  PrepareExternalEditMerge,
  ApplyExternalEditMerge,
  RecoverExternalEditSession,
  ContinueExternalEditSession,
} from "../../wailsjs/go/external_edit/ExternalEdit";

export interface ExternalEditEditorConfig {
  id: string;
  name: string;
  path: string;
  args?: string[];
}

export interface ExternalEditEditor {
  id: string;
  name: string;
  path: string;
  args?: string[];
  builtIn: boolean;
  available: boolean;
  default: boolean;
}

export interface ExternalEditSettings {
  defaultEditorId: string;
  workspaceRoot: string;
  cleanupRetentionDays: number;
  maxReadFileSizeMB: number;
  editors: ExternalEditEditor[];
  customEditors: ExternalEditEditorConfig[];
}

export interface ExternalEditSettingsInput {
  defaultEditorId: string;
  workspaceRoot: string;
  cleanupRetentionDays: number;
  maxReadFileSizeMB: number;
  customEditors: ExternalEditEditorConfig[];
}

export interface ExternalEditOpenRequest {
  assetId: number;
  sessionId: string;
  remotePath: string;
  editorId?: string;
}

export interface ExternalEditSession {
  id: string;
  assetId: number;
  assetName: string;
  documentKey: string;
  sessionId: string;
  remotePath: string;
  remoteRealPath: string;
  localPath: string;
  workspaceRoot: string;
  workspaceDir: string;
  editorId: string;
  editorName: string;
  editorPath: string;
  editorArgs?: string[];
  // `originalSha256` 保留现有 IPC 字段名，语义上等同于当前 document 的 baseHash。
  originalSha256: string;
  originalSize: number;
  originalModTime: number;
  originalEncoding: string;
  originalBom?: string;
  originalByteSample?: string;
  // `lastLocalSha256` 同样是兼容字段名，语义上等同于最近一次落盘的 localHash。
  lastLocalSha256: string;
  dirty: boolean;
  state: string;
  recordState?: "active" | "conflict" | "error" | "completed" | "abandoned";
  saveMode?: "auto_live" | "manual_restored";
  pendingReview?: boolean;
  hidden: boolean;
  expired: boolean;
  lastError?: {
    step: string;
    summary: string;
    suggestion: string;
    at: number;
  };
  resumeRequired?: boolean;
  mergeRemoteSha256?: string;
  sourceSessionId?: string;
  supersededBySessionId?: string;
  createdAt: number;
  updatedAt: number;
  lastLaunchedAt: number;
  lastSyncedAt: number;
}

export interface ExternalEditSaveResult {
  status: string;
  message?: string;
  session?: ExternalEditSession;
  conflict?: {
    documentKey: string;
    primaryDraftSessionId: string;
    latestSnapshotSessionId?: string;
  };
  automatic?: boolean;
}

export interface ExternalEditEvent {
  type: string;
  session?: ExternalEditSession;
  saveResult?: ExternalEditSaveResult;
  autoSave?: {
    documentKey: string;
    sessionId?: string;
    phase: "pending" | "running" | "idle";
  };
}

export interface ExternalEditCompareResult {
  documentKey: string;
  primaryDraftSessionId: string;
  latestSnapshotSessionId?: string;
  fileName: string;
  remotePath: string;
  localContent: string;
  remoteContent: string;
  readOnly: boolean;
  status?: string;
  message?: string;
  session?: ExternalEditSession;
  conflict?: {
    documentKey: string;
    primaryDraftSessionId: string;
    latestSnapshotSessionId?: string;
  };
}

export interface ExternalEditMergePrepareResult {
  documentKey: string;
  primaryDraftSessionId: string;
  fileName: string;
  remotePath: string;
  localContent: string;
  remoteContent: string;
  finalContent: string;
  remoteHash: string;
  session?: ExternalEditSession;
}

export interface ExternalEditMergeApplyRequest {
  sessionId: string;
  finalContent: string;
  remoteHash: string;
}

// 这里只保留最薄的一层调用封装，让 store / 组件共享同一批 IPC 名称，
// 同时把 Wails 生成绑定的具体路径集中在一个边界里。
export function getExternalEditSettings(): Promise<ExternalEditSettings> {
  return GetExternalEditSettings() as unknown as Promise<ExternalEditSettings>;
}

export function saveExternalEditSettings(input: ExternalEditSettingsInput): Promise<ExternalEditSettings> {
  return SaveExternalEditSettings(input as never) as unknown as Promise<ExternalEditSettings>;
}

export function selectExternalEditorExecutable(): Promise<string> {
  return SelectExternalEditorExecutable();
}

export function selectExternalEditWorkspaceRoot(): Promise<string> {
  return SelectExternalEditWorkspaceRoot();
}

export function openExternalEdit(req: ExternalEditOpenRequest): Promise<ExternalEditSession> {
  return OpenExternalEditBinding(req as never) as unknown as Promise<ExternalEditSession>;
}

export function listExternalEditSessions(): Promise<ExternalEditSession[]> {
  return ListExternalEditSessions() as unknown as Promise<ExternalEditSession[]>;
}

export function saveExternalEditSession(sessionId: string): Promise<ExternalEditSaveResult> {
  return SaveExternalEditSession(sessionId) as unknown as Promise<ExternalEditSaveResult>;
}

export function refreshExternalEditSession(sessionId: string): Promise<ExternalEditSession> {
  return RefreshExternalEditSession(sessionId) as unknown as Promise<ExternalEditSession>;
}

export function resolveExternalEditConflict(sessionId: string, resolution: string): Promise<ExternalEditSaveResult> {
  return ResolveExternalEditConflict(sessionId, resolution) as unknown as Promise<ExternalEditSaveResult>;
}

export function compareExternalEditSession(sessionId: string): Promise<ExternalEditCompareResult> {
  return CompareExternalEditSession(sessionId) as unknown as Promise<ExternalEditCompareResult>;
}

export function prepareExternalEditMerge(sessionId: string): Promise<ExternalEditMergePrepareResult> {
  return PrepareExternalEditMerge(sessionId) as unknown as Promise<ExternalEditMergePrepareResult>;
}

export function applyExternalEditMerge(req: ExternalEditMergeApplyRequest): Promise<ExternalEditSaveResult> {
  return ApplyExternalEditMerge(req as never) as unknown as Promise<ExternalEditSaveResult>;
}

export function recoverExternalEditSession(sessionId: string): Promise<ExternalEditSession> {
  return RecoverExternalEditSession(sessionId) as unknown as Promise<ExternalEditSession>;
}

export function continueExternalEditSession(sessionId: string): Promise<ExternalEditSession> {
  return ContinueExternalEditSession(sessionId) as unknown as Promise<ExternalEditSession>;
}
