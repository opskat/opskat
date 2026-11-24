import { useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn, useResizeHandle } from "@opskat/ui";
import { useSFTPStore } from "@/stores/sftpStore";
import { useElementSize } from "./useElementSize";

/** 面板宽到这个值才把目录树放到列表左边;更窄时放上面——默认 280px 放不下两列。 */
export const SIDE_BY_SIDE_MIN_PANEL_PX = 440;
const TREE_MIN_WIDTH_PX = 120;
const LIST_MIN_WIDTH_PX = 200;
/** 上下排列时两边都至少要能放下一个完整行(28px)外加余量。 */
const TREE_MIN_HEIGHT_PX = 56;
const LIST_MIN_HEIGHT_PX = 80;
const DEFAULT_TREE_HEIGHT_RATIO = 0.4;
/** 还没测出盒子高度前的估算值,只影响首帧。 */
const SPLIT_SIZE_FALLBACK = { width: 0, height: 400 };

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}

interface FileManagerSplitProps {
  /** 面板宽度(与面板自身的拖宽同源),决定左右还是上下排列。 */
  panelWidth: number;
  tree: ReactNode;
  list: ReactNode;
  /** 上下排列时列表上方的小标题,标明列表显示的是哪个目录。 */
  caption: ReactNode;
}

/**
 * 文件面板里目录树与文件列表的排列:宽面板左右、窄面板上下,各有一条可拖的分隔条;
 * 树可整个收起。开合与两种排列下的分隔条位置是全局偏好,放在 sftpStore 里跨重启保留。
 */
export function FileManagerSplit({ panelWidth, tree, list, caption }: FileManagerSplitProps) {
  const { t } = useTranslation();
  const visible = useSFTPStore((s) => s.folderTreeVisible);
  const storedWidth = useSFTPStore((s) => s.folderTreeWidth);
  const storedHeight = useSFTPStore((s) => s.folderTreeHeight);
  const setFolderTreeWidth = useSFTPStore((s) => s.setFolderTreeWidth);
  const setFolderTreeHeight = useSFTPStore((s) => s.setFolderTreeHeight);
  const [boxRef, box] = useElementSize<HTMLDivElement>(SPLIT_SIZE_FALLBACK);
  const treeRef = useRef<HTMLDivElement>(null);

  const sideBySide = panelWidth >= SIDE_BY_SIDE_MIN_PANEL_PX;
  const maxTreeWidth = Math.max(TREE_MIN_WIDTH_PX, panelWidth - LIST_MIN_WIDTH_PX);
  const treeWidth = clamp(storedWidth, TREE_MIN_WIDTH_PX, maxTreeWidth);
  const maxTreeHeight = Math.max(TREE_MIN_HEIGHT_PX, box.height - LIST_MIN_HEIGHT_PX);
  const treeHeight = clamp(
    storedHeight ?? Math.round(box.height * DEFAULT_TREE_HEIGHT_RATIO),
    TREE_MIN_HEIGHT_PX,
    maxTreeHeight
  );

  const widthHandle = useResizeHandle({
    axis: "x",
    defaultSize: treeWidth,
    currentSize: treeWidth,
    minSize: TREE_MIN_WIDTH_PX,
    maxSize: maxTreeWidth,
    targetRef: treeRef,
    onResizeEnd: setFolderTreeWidth,
  });
  const heightHandle = useResizeHandle({
    axis: "y",
    defaultSize: treeHeight,
    currentSize: treeHeight,
    minSize: TREE_MIN_HEIGHT_PX,
    maxSize: maxTreeHeight,
    targetRef: treeRef,
    onResizeEnd: setFolderTreeHeight,
  });
  const handle = sideBySide ? widthHandle : heightHandle;

  return (
    <div
      ref={boxRef}
      data-testid="sftp-split"
      data-layout={sideBySide ? "side" : "stacked"}
      className={cn("flex min-h-0 flex-1", sideBySide ? "flex-row" : "flex-col")}
    >
      {visible && (
        <>
          <div
            ref={treeRef}
            className="flex min-h-0 min-w-0 shrink-0 flex-col"
            style={sideBySide ? { width: treeWidth } : { height: treeHeight }}
          >
            {tree}
          </div>
          <div
            data-testid="sftp-tree-splitter"
            role="separator"
            aria-orientation={sideBySide ? "vertical" : "horizontal"}
            aria-label={t("sftp.folderTree.resize")}
            className={cn(
              "shrink-0 transition-colors hover:bg-primary/20",
              sideBySide ? "w-1 cursor-col-resize border-l" : "h-1 cursor-row-resize border-t",
              handle.isResizing && "bg-primary/30"
            )}
            onMouseDown={handle.handleMouseDown}
          />
        </>
      )}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {visible && !sideBySide && caption}
        {list}
      </div>
    </div>
  );
}
