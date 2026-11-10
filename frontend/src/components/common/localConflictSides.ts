import { StatLocalPaths } from "../../../wailsjs/go/system/System";
import type { FileConflictSide } from "./FileConflictDialog";

/**
 * 上传冲突里「将要写入」的一侧：读取本地路径的大小与修改时间。与 paths 一一对应；
 * 某项取不到（例如已被删除）时该项为空对象，对话框显示占位符。
 */
export async function localConflictSides(paths: string[]): Promise<FileConflictSide[]> {
  if (!paths.length) return [];
  const stats = await StatLocalPaths(paths);
  return stats.map((stat) =>
    stat.error ? {} : { isDir: stat.isDir, size: stat.isDir ? undefined : stat.size, modTime: stat.modTime }
  );
}
