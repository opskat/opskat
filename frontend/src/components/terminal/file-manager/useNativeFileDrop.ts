import { useEffect, useState, type MutableRefObject, type RefObject } from "react";
import { registerFileManagerDropTarget } from "../terminalFileDropCoordinator";

interface UseNativeFileDropOptions {
  currentPathRef: MutableRefObject<string>;
  isActive: boolean;
  isOpen: boolean;
  panelRef: RefObject<HTMLDivElement | null>;
  tabId: string;
  sessionId: string;
  startUploadFile: (
    target: { tabId: string; sessionId: string },
    localPath: string,
    remotePath: string
  ) => Promise<string | null>;
}

export function useNativeFileDrop({
  currentPathRef,
  isActive,
  isOpen,
  panelRef,
  tabId,
  sessionId,
  startUploadFile,
}: UseNativeFileDropOptions) {
  const [isDragOver, setIsDragOver] = useState(false);

  useEffect(() => {
    if (!isOpen || !isActive) return;
    return registerFileManagerDropTarget({
      getRect: () => panelRef.current?.getBoundingClientRect(),
      getRemoteDir: () => currentPathRef.current + "/",
      startUploadFile: (localPath, remotePath) => {
        setIsDragOver(false);
        void startUploadFile({ tabId, sessionId }, localPath, remotePath);
      },
    });
  }, [currentPathRef, isActive, isOpen, panelRef, sessionId, startUploadFile, tabId]);

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
