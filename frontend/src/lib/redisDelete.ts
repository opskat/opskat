import { toast } from "sonner";
import type { redis_svc } from "../../wailsjs/go/models";

type Translate = (key: string, opts?: Record<string, unknown>) => string;

/**
 * Report the failed keys of a RedisDeleteKeys result. The call resolves (does not reject) when
 * some keys fail — cluster deletes run one key at a time — so callers must read failures from the
 * result. Each failed key is listed with Redis's own error (e.g. `CLUSTERDOWN`) verbatim.
 */
export function toastRedisDeleteFailures(t: Translate, result: redis_svc.RedisDeleteResult): void {
  const failed = result.failed ?? [];
  if (failed.length === 0) return;
  toast.error(
    t("query.redisDeleteKeysFailed", {
      deleted: result.deleted,
      count: failed.length,
      keys: failed.map((f) => f.key).join(", "),
    }),
    { description: failed.map((f) => `${f.key}: ${f.error}`).join("\n") }
  );
}

/**
 * Whether `key` is gone after a RedisDeleteKeys call: true unless the key is listed as failed.
 * `deleted` is Redis's DEL count, so a key removed elsewhere after the scan counts 0 without
 * failing — it is still gone and must leave the list.
 */
export function redisKeyDeleted(result: redis_svc.RedisDeleteResult, key: string): boolean {
  return !(result.failed ?? []).some((f) => f.key === key);
}
