import { useEffect, useRef, useState, type RefObject } from "react";
import { registerFileDropTarget } from "@/components/terminal/terminalFileDropCoordinator";
import { useOssTransferStore, type ResolveKeys } from "@/stores/ossTransferStore";

export interface UseOssFileDropOptions {
  dropRef: RefObject<HTMLElement | null>;
  tabId: string;
  assetId: number;
  bucket: string;
  prefix: string;
  active: boolean;
  /** 写入前的同名检查：返回与传入 key 一一对应的最终 key，null 表示跳过该项。 */
  resolveKeys: ResolveKeys;
}

export function useOssFileDrop({
  dropRef,
  tabId,
  assetId,
  bucket,
  prefix,
  active,
  resolveKeys,
}: UseOssFileDropOptions): boolean {
  const [isDragOver, setIsDragOver] = useState(false);
  const startUploadPaths = useOssTransferStore((s) => s.startUploadPaths);

  // 用 ref 保存最新上传上下文，避免把 prefix/bucket 放进注册依赖里反复注册/退订。
  const ctx = useRef({ tabId, assetId, bucket, prefix });
  const resolveRef = useRef(resolveKeys);
  useEffect(() => {
    ctx.current = { tabId, assetId, bucket, prefix };
    resolveRef.current = resolveKeys;
  });

  useEffect(() => {
    if (!active) return;
    return registerFileDropTarget({
      getRect: () => dropRef.current?.getBoundingClientRect() ?? null,
      onDrop: (paths) => {
        setIsDragOver(false);
        const c = ctx.current;
        void startUploadPaths(c.tabId, c.assetId, c.bucket, c.prefix, paths, resolveRef.current);
      },
    });
  }, [active, dropRef, startUploadPaths]);

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
