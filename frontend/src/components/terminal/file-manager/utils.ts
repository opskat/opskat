import { sftp_svc } from "../../../../wailsjs/go/models";

export const HANDLE_PX = 4;

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + " " + units[i];
}

export function formatDate(timestamp: number): string {
  const d = new Date(timestamp * 1000);
  return d.toLocaleDateString(undefined, {
    month: "2-digit",
    day: "2-digit",
  });
}

export function normalizeRemotePath(basePath: string, nextPath: string): string {
  const raw = nextPath.trim();
  if (!raw) return basePath || "/";
  const combined = raw.startsWith("/") ? raw : `${basePath === "/" ? "" : basePath}/${raw}`;
  const parts = combined.split("/");
  const normalized: string[] = [];
  for (const part of parts) {
    if (!part || part === ".") continue;
    if (part === "..") {
      normalized.pop();
      continue;
    }
    normalized.push(part);
  }
  return "/" + normalized.join("/");
}

export function getEntryPath(currentPath: string, entry: sftp_svc.FileEntry): string {
  return currentPath === "/" ? "/" + entry.name : currentPath + "/" + entry.name;
}

export function getParentPath(currentPath: string): string {
  return currentPath.replace(/\/[^/]+\/?$/, "") || "/";
}

export function getPathBaseName(path: string): string {
  const normalized = normalizeRemotePath("/", path);
  if (normalized === "/") return "";
  const segments = normalized.split("/").filter(Boolean);
  return segments.at(-1) ?? "";
}

export function canMovePathToDirectory(sourcePath: string, targetDirPath: string): boolean {
  const source = normalizeRemotePath("/", sourcePath);
  const target = normalizeRemotePath("/", targetDirPath);
  if (!source || source === "/" || !target) return false;
  if (source === target) return false;
  if (getParentPath(source) === target) return false;
  return !target.startsWith(`${source}/`);
}

export function splitNameForRename(name: string): { stemLength: number } {
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return { stemLength: name.length };
  return { stemLength: dot };
}

export function getChildPath(parentPath: string, name: string): string {
  return parentPath === "/" ? "/" + name : parentPath + "/" + name;
}

export function joinRemotePath(parentPath: string, name: string): string {
  return normalizeRemotePath(parentPath, name);
}

export function sortEntries(entries: sftp_svc.FileEntry[]): sftp_svc.FileEntry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir && !b.isDir) return -1;
    if (!a.isDir && b.isDir) return 1;
    return a.name.localeCompare(b.name);
  });
}
