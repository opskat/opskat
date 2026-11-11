import { useEffect, useState, type RefObject } from "react";
import { registerFileManagerDropTarget } from "../terminalFileDropCoordinator";

interface UseNativeFileDropOptions {
  currentPathRef: RefObject<string>;
  isActive: boolean;
  isOpen: boolean;
  panelRef: RefObject<HTMLDivElement | null>;
  /** 一次放下的全部本地路径与当时的远端目录；同名检查要一次看到整批，才能「应用到其余冲突」。 */
  uploadLocalPaths: (localPaths: string[], remoteDir: string) => Promise<void>;
}

export function useNativeFileDrop({
  currentPathRef,
  isActive,
  isOpen,
  panelRef,
  uploadLocalPaths,
}: UseNativeFileDropOptions) {
  const [isDragOver, setIsDragOver] = useState(false);

  useEffect(() => {
    if (!isOpen || !isActive) return;
    // 协调器对一次放下的每个路径同步调用一次 startUploadFile，这里攒到同一个微任务里整批交出。
    let batch: { remoteDir: string; paths: string[] } | null = null;
    return registerFileManagerDropTarget({
      getRect: () => panelRef.current?.getBoundingClientRect(),
      getRemoteDir: () => currentPathRef.current,
      startUploadFile: (localPath, remoteDir) => {
        setIsDragOver(false);
        if (batch) {
          batch.paths.push(localPath);
          return;
        }
        const pending = { remoteDir, paths: [localPath] };
        batch = pending;
        queueMicrotask(() => {
          batch = null;
          void uploadLocalPaths(pending.paths, pending.remoteDir);
        });
      },
    });
  }, [currentPathRef, isActive, isOpen, panelRef, uploadLocalPaths]);

  useEffect(() => {
    const el = panelRef.current;
    if (!el || !isOpen || !isActive) return;
    const observer = new MutationObserver(() => {
      setIsDragOver(el.classList.contains("wails-drop-target-active"));
    });
    observer.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [isActive, isOpen, panelRef]);

  return isDragOver;
}
