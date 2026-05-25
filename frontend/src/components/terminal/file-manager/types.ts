import { sftp_svc } from "../../../../wailsjs/go/models";

export interface CtxMenuState {
  x: number;
  y: number;
  entry: sftp_svc.FileEntry | null;
  canExternalEdit?: boolean;
  selectedEntries: sftp_svc.FileEntry[];
}

export interface DeleteTarget {
  paths: Array<{ path: string; name: string; isDir: boolean }>;
}

export type ClipboardMode = "copy" | "cut";

export interface ClipboardItem {
  sessionId: string;
  path: string;
  name: string;
  isDir: boolean;
  size: number;
}

export interface ClipboardState {
  mode: ClipboardMode;
  items: ClipboardItem[];
}

export interface RenameTarget {
  entry: sftp_svc.FileEntry;
  path: string;
}

export interface PermissionTarget {
  entry: sftp_svc.FileEntry;
  path: string;
}
