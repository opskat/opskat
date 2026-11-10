import { useCallback } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { OSSListObjects } from "../../../wailsjs/go/oss/OSS";
import type { oss_svc } from "../../../wailsjs/go/models";
import {
  useFileConflictResolver,
  type FileConflictItem,
  type FileConflictKind,
  type FileConflictSide,
} from "@/components/common/FileConflictDialog";
import { errorMessage } from "@/lib/errorMessage";

export interface OssWriteTarget {
  /** 想写入的对象 key。 */
  key: string;
  /** 将要写入的这一项的元数据，取不到时留空。 */
  incoming: FileConflictSide;
}

export function ossObjectSide(object: Pick<oss_svc.ObjectItem, "size" | "lastModified">): FileConflictSide {
  return { size: object.size, modTime: object.lastModified * 1000 };
}

/** 「保留两者」的候选名：沿用 SFTP 的 _copy / _copy2 / ... 规则，只改 key 的最后一段。 */
export function keepBothKey(key: string, attempt: number): string {
  const slash = key.lastIndexOf("/") + 1;
  const base = key.slice(slash);
  const dot = base.lastIndexOf(".");
  const ext = dot > 0 ? base.slice(dot) : "";
  const stem = ext ? base.slice(0, dot) : base;
  return `${key.slice(0, slash)}${stem}${attempt === 0 ? "_copy" : `_copy${attempt + 1}`}${ext}`;
}

const MAX_KEEP_BOTH_ATTEMPTS = 1000;

/**
 * 写对象之前的同名检查：按 key 精确前缀列 1 条（只有真实的列举失败才会报错，「不存在」不会被误判成失败），
 * 有冲突就弹 FileConflictDialog。返回与 targets 一一对应的最终 key；null 表示跳过（用户选择跳过，
 * 或检查失败 —— 检查失败时报错并中止该项，绝不默认覆盖）。
 */
export function useOssTargetResolver(assetId: number, bucket: string) {
  const { t } = useTranslation();
  const { resolveConflicts, conflictDialog } = useFileConflictResolver();

  const find = useCallback(
    async (key: string): Promise<oss_svc.ObjectItem | undefined> => {
      const page = await OSSListObjects({ assetId, bucket, prefix: key, maxKeys: 1, continuationToken: "" });
      return page.objects?.find((object) => object.key === key);
    },
    [assetId, bucket]
  );

  const firstFreeKey = useCallback(
    async (key: string): Promise<string | undefined> => {
      for (let attempt = 0; attempt < MAX_KEEP_BOTH_ATTEMPTS; attempt++) {
        const candidate = keepBothKey(key, attempt);
        if (!(await find(candidate))) return candidate;
      }
      return undefined;
    },
    [find]
  );

  const resolveTargets = useCallback(
    async (targets: OssWriteTarget[], kind: FileConflictKind = "transfer"): Promise<(string | null)[]> => {
      const result: (string | null)[] = targets.map(() => null);
      const conflicts: { index: number; item: FileConflictItem; keepBoth?: string }[] = [];
      for (const [index, target] of targets.entries()) {
        try {
          const existing = await find(target.key);
          if (!existing) {
            result[index] = target.key;
            continue;
          }
          const keepBoth = await firstFreeKey(target.key);
          conflicts.push({
            index,
            keepBoth,
            item: {
              targetPath: target.key,
              existing: ossObjectSide(existing),
              incoming: target.incoming,
              keepBothName: keepBoth?.split("/").pop(),
            },
          });
        } catch (e) {
          toast.error(t("fileConflict.checkFailed", { path: target.key, error: errorMessage(e) }));
        }
      }

      const decisions = await resolveConflicts(
        conflicts.map((conflict) => conflict.item),
        kind
      );
      decisions.forEach((decision, i) => {
        const { index, keepBoth } = conflicts[i];
        if (decision === "overwrite") result[index] = targets[index].key;
        if (decision === "keepBoth") result[index] = keepBoth ?? null;
      });
      return result;
    },
    [find, firstFreeKey, resolveConflicts, t]
  );

  return { resolveTargets, conflictDialog };
}
