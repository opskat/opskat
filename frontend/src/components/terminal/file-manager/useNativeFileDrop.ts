import { useEffect, useState, type MutableRefObject, type RefObject } from "react";
import { OnFileDrop, OnFileDropOff } from "../../../../wailsjs/runtime/runtime";

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
  onDropOutside?: (paths: string[]) => void;
}

export function useNativeFileDrop({
  currentPathRef,
  isActive,
  isOpen,
  panelRef,
  tabId,
  sessionId,
  startUploadFile,
  onDropOutside,
}: UseNativeFileDropOptions) {
  const [isDragOver, setIsDragOver] = useState(false);

  useEffect(() => {
    if (!isOpen || !isActive) return;
    const handler = (x: number, y: number, paths: string[]) => {
      setIsDragOver(false);
      const rect = panelRef.current?.getBoundingClientRect();
      const isPanelDrop = rect && x >= rect.left && x <= rect.right && y >= rect.top && y <= rect.bottom;
      if (!isPanelDrop) {
        onDropOutside?.(paths);
        return;
      }
      for (const path of paths) {
        startUploadFile({ tabId, sessionId }, path, currentPathRef.current + "/");
      }
    };
    OnFileDrop(handler, true);
    return () => {
      OnFileDropOff();
    };
  }, [currentPathRef, isActive, isOpen, onDropOutside, panelRef, sessionId, startUploadFile, tabId]);

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
