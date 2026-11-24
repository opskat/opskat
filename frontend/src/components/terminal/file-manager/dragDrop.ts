import { useCallback, useRef, useState } from "react";
import { canMovePathToDirectory } from "./utils";

/** 列表行 HTML5 拖动时随 dataTransfer 带出的被拖路径(JSON 数组)。 */
export const SFTP_DRAG_PATHS_MIME = "application/x-opskat-sftp-paths";

/** 落点连同"会搬多少个条目"一起存:高亮之外还要说清这一放会执行什么。 */
export interface SftpDropTarget {
  path: string;
  count: number;
}

/**
 * 命中元素所在的目录落点。两类元素可以接住拖放:列表里的目录(含折叠链上的每一段,
 * 它们各自带 dir + path 标记)与目录树里的目录行。
 */
export function dropTargetPathOf(target: EventTarget | null): string | null {
  if (!(target instanceof Element)) return null;
  const entry = target.closest<HTMLElement>('[data-sftp-entry-dir="true"][data-sftp-entry-path]');
  if (entry) return entry.dataset.sftpEntryPath ?? null;
  return target.closest<HTMLElement>("[data-sftp-tree-path]")?.dataset.sftpTreePath ?? null;
}

/** 本次拖动的源路径:同一面板内发起的拖动直接读登记值,否则从 dataTransfer 里解析。 */
export function readDraggedPaths(event: React.DragEvent, draggedPaths: string[]): string[] {
  if (draggedPaths.length) return draggedPaths;
  const raw = event.dataTransfer.getData(SFTP_DRAG_PATHS_MIME);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
  } catch {
    // 别的应用拖进来的同名 MIME 不是我们的格式:当作没有可移动的条目。
    return [];
  }
}

export function movableDraggedPaths(event: React.DragEvent, draggedPaths: string[], targetDirPath: string): string[] {
  return readDraggedPaths(event, draggedPaths).filter((path) => canMovePathToDirectory(path, targetDirPath));
}

/**
 * 文件面板里一次拖动的共享状态:列表发起拖动,落点可能在列表里,也可能在目录树上,
 * 状态栏还要读落点说明——所以放在面板这一层,由列表与目录树共同读写。
 */
export function useSftpDragState() {
  const draggedPathsRef = useRef<string[]>([]);
  const [dropTarget, setDropTargetState] = useState<SftpDropTarget | null>(null);
  // dragover 每帧都在触发:落点没变就必须交回同一个对象,否则每一帧都换 prop 标识,
  // 把整目录所有行的 memo 全部打掉。
  const setDropTarget = useCallback((next: SftpDropTarget | null) => {
    setDropTargetState((prev) => {
      if (!prev || !next) return prev === next ? prev : next;
      return prev.path === next.path && prev.count === next.count ? prev : next;
    });
  }, []);
  return { draggedPathsRef, dropTarget, setDropTarget };
}
