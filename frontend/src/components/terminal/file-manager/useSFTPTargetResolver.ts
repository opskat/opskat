import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { SFTPStatTargets } from "../../../../wailsjs/go/ssh/SSH";
import {
  useFileConflictResolver,
  type FileConflictItem,
  type FileConflictKind,
  type FileConflictSide,
} from "@/components/common/FileConflictDialog";
import { errorMessage } from "@/lib/errorMessage";
import { getPathBaseName } from "./utils";

export interface SFTPWriteTarget {
  /** 想写入的远端完整路径。 */
  targetPath: string;
  /** 将要写入的这一项的元数据（modTime 为 Unix 毫秒），取不到时留空。 */
  incoming: FileConflictSide;
  /** 目标就是源本身（原地复制）：不问用户，直接用 _copy 名字。 */
  duplicateInPlace?: boolean;
}

/** 远端条目（modTime 为 Unix 秒）在冲突对话框里的一侧。 */
export function sftpEntrySide(entry: { isDir: boolean; size: number; modTime: number }): FileConflictSide {
  return { isDir: entry.isDir, size: entry.size, modTime: entry.modTime * 1000 };
}

/**
 * 写远端之前的同名检查：一次 SFTPStatTargets 查完所有目标，有冲突就弹 FileConflictDialog。
 * 返回与 targets 一一对应的最终目标路径；null 表示跳过（用户选择跳过，或检查失败 —— 检查失败
 * 时报错并中止该项，绝不默认覆盖）。
 */
export function useSFTPTargetResolver(sessionId: string) {
  const { t } = useTranslation();
  const { resolveConflicts, conflictDialog } = useFileConflictResolver();

  const resolveTargets = useCallback(
    async (targets: SFTPWriteTarget[], kind: FileConflictKind = "transfer"): Promise<(string | null)[]> => {
      if (!targets.length) return [];
      let stats;
      try {
        stats = await SFTPStatTargets(
          sessionId,
          targets.map((target) => target.targetPath)
        );
      } catch (e) {
        toast.error(errorMessage(e));
        return targets.map(() => null);
      }

      const result: (string | null)[] = targets.map(() => null);
      const conflicts: { index: number; item: FileConflictItem }[] = [];
      stats.forEach((stat, index) => {
        const target = targets[index];
        if (stat.error) {
          toast.error(t("fileConflict.checkFailed", { path: stat.path, error: stat.error }));
          return;
        }
        if (!stat.exists) {
          result[index] = target.targetPath;
          return;
        }
        if (target.duplicateInPlace) {
          result[index] = stat.keepBothPath ?? null;
          return;
        }
        conflicts.push({
          index,
          item: {
            targetPath: stat.path,
            existing: sftpEntrySide(stat),
            incoming: target.incoming,
            keepBothName: stat.keepBothPath ? getPathBaseName(stat.keepBothPath) : undefined,
          },
        });
      });

      const decisions = await resolveConflicts(
        conflicts.map((conflict) => conflict.item),
        kind
      );
      decisions.forEach((decision, i) => {
        const { index } = conflicts[i];
        const stat = stats[index];
        if (decision === "overwrite") result[index] = targets[index].targetPath;
        if (decision === "keepBoth") result[index] = stat.keepBothPath ?? null;
      });
      return result;
    },
    [resolveConflicts, sessionId, t]
  );

  return { resolveTargets, conflictDialog };
}
