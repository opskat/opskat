import { useEffect, useRef, useState, type RefObject } from "react";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore } from "@/stores/ossTransferStore";

export interface UseOssFileDropOptions {
  dropRef: RefObject<HTMLElement | null>;
  tabId: string;
  assetId: number;
  bucket: string;
  prefix: string;
  active: boolean;
}

export function useOssFileDrop({ dropRef, tabId, assetId, bucket, prefix, active }: UseOssFileDropOptions): boolean {
  const [isDragOver, setIsDragOver] = useState(false);
  const startUploadPath = useOssTransferStore((s) => s.startUploadPath);

  // 用 ref 保存最新上传上下文，避免把 prefix/bucket 放进注册依赖里反复注册/退订。
  const ctx = useRef({ tabId, assetId, bucket, prefix });
  useEffect(() => {
    ctx.current = { tabId, assetId, bucket, prefix };
  });

  useEffect(() => {
    if (!active) return;
    return registerFileDropTarget({
      getRect: () => dropRef.current?.getBoundingClientRect() ?? null,
      onDrop: (paths) => {
        setIsDragOver(false);
        const c = ctx.current;
        for (const p of paths) void startUploadPath(c.tabId, c.assetId, c.bucket, c.prefix, p);
      },
    });
  }, [active, dropRef, startUploadPath]);

  // Wails 在原生拖拽经过带 `--wails-drop-target: drop` 的元素时切换 wails-drop-target-active 类。
  useEffect(() => {
    const el = dropRef.current;
    if (!el || !active) return;
    const observer = new MutationObserver(() => {
      setIsDragOver(el.classList.contains("wails-drop-target-active"));
    });
    observer.observe(el, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [active, dropRef]);

  return isDragOver;
}
