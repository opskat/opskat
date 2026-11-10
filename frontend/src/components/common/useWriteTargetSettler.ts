import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { useFileConflictResolver, type FileConflictItem, type FileConflictKind } from "./FileConflictDialog";
import { localConflictSides } from "./localConflictSides";
import { errorMessage } from "@/lib/errorMessage";

/** 一个已确认同名的写入目标，等待用户决定。 */
export interface PendingWriteConflict {
  /** 在调用方 targets 里的下标。 */
  index: number;
  item: FileConflictItem;
  /** 选「保留两者」时的完整目标；没有则该项不能保留两者。 */
  keepBoth?: string;
  /** 上传的本地来源：用它的大小与修改时间作为 incoming 一侧。 */
  localPath?: string;
}

/**
 * SFTP 与 OSS 写入前同名检查共用的后半段：各后端只负责查出哪些目标空闲、哪些已被占用，
 * 之后的「补本地一侧元数据 → 弹冲突对话框 → 决定映射回目标 → 同批去重」都在这里。
 *
 * settle 返回与 desired 一一对应的最终目标；null 表示跳过。同一批里两项落到同一目标时
 * （同名的两个本地文件，或「保留两者」的名字恰好是同批另一项的名字），后一项跳过并报错，
 * 绝不让同批互相覆盖。
 */
export function useWriteTargetSettler() {
  const { t } = useTranslation();
  const { resolveConflicts, conflictDialog } = useFileConflictResolver();

  const settle = useCallback(
    async (
      desired: string[],
      free: (string | null)[],
      conflicts: PendingWriteConflict[],
      kind: FileConflictKind
    ): Promise<(string | null)[]> => {
      const local = conflicts.filter((conflict) => conflict.localPath);
      try {
        const sides = await localConflictSides(local.map((conflict) => conflict.localPath!));
        local.forEach((conflict, i) => {
          conflict.item.incoming = sides[i];
        });
      } catch (e) {
        toast.error(errorMessage(e));
        return desired.map(() => null);
      }

      const result = [...free];
      const decisions = await resolveConflicts(
        conflicts.map((conflict) => conflict.item),
        kind
      );
      decisions.forEach((decision, i) => {
        const { index, keepBoth } = conflicts[i];
        if (decision === "overwrite") result[index] = desired[index];
        if (decision === "keepBoth") result[index] = keepBoth ?? null;
      });

      const claimed = new Set<string>();
      return result.map((target) => {
        if (target === null) return null;
        if (claimed.has(target)) {
          toast.error(t("fileConflict.duplicateTarget", { path: target }));
          return null;
        }
        claimed.add(target);
        return target;
      });
    },
    [resolveConflicts, t]
  );

  return { settle, conflictDialog };
}
