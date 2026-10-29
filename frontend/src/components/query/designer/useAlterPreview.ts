import { useEffect, useState } from "react";
import { GetAlterTableStatements, GetCreateTableStatements } from "../../../../wailsjs/go/query/Query";
import type { dbdriver } from "../../../../wailsjs/go/models";

/** 草稿停止变化多久之后再请驱动生成语句。 */
export const PREVIEW_DEBOUNCE_MS = 300;

/**
 * SQL 预览的状态。blocked:草稿有阻止保存的问题,不请求驱动;loading:等待防抖或驱动返回;
 * ready:驱动按执行次序给出的语句与改表后的表名;error:驱动的原始错误。
 */
export type AlterPreview =
  | { status: "blocked" }
  | { status: "loading" }
  | { status: "ready"; statements: string[]; table: string }
  | { status: "error"; error: string };

interface PreviewResult {
  key: string;
  preview: AlterPreview;
}

export interface AlterPreviewRequest {
  assetId: number;
  database: string;
  table: string;
  request: dbdriver.AlterTableRequest;
  /** 草稿有阻止保存的问题时为 false:不请求驱动。 */
  enabled: boolean;
}

/**
 * 按草稿请驱动生成语句:草稿停下 PREVIEW_DEBOUNCE_MS 后调用 run,结果带着发起时的 key(请求的全部参数),
 * 只有与当前草稿一致的结果才算数(最新的请求胜出)。key 为 null 表示不请求(草稿有阻止保存的问题)。
 */
function useStatementPreview(
  key: string | null,
  run: () => Promise<{ statements: string[]; table: string }>
): AlterPreview {
  const [result, setResult] = useState<PreviewResult | null>(null);

  useEffect(() => {
    if (key === null) return;
    let alive = true;
    const timer = setTimeout(() => {
      run().then(
        (r) => alive && setResult({ key, preview: { status: "ready", statements: r.statements, table: r.table } }),
        (err: unknown) => alive && setResult({ key, preview: { status: "error", error: String(err) } })
      );
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
    // 请求的全部参数都在 key 里。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  if (key === null) return { status: "blocked" };
  return result?.key === key ? result.preview : { status: "loading" };
}

/** 按草稿请驱动生成改表语句(见 useStatementPreview)。 */
export function useAlterPreview({ assetId, database, table, request, enabled }: AlterPreviewRequest): AlterPreview {
  const key = enabled ? JSON.stringify([assetId, database, table, request]) : null;
  return useStatementPreview(key, () =>
    GetAlterTableStatements(assetId, database, table, request).then((r) => ({
      statements: r.statements ?? [],
      table: r.table,
    }))
  );
}

export interface CreatePreviewRequest {
  assetId: number;
  database: string;
  /** 不是新建表时为 null:不请求驱动。 */
  request: dbdriver.CreateTableRequest | null;
  /** 草稿有阻止保存的问题时为 false:不请求驱动。 */
  enabled: boolean;
}

/** 按草稿请驱动生成建表语句(见 useStatementPreview);建出的表名就是请求里的表名。 */
export function useCreatePreview({ assetId, database, request, enabled }: CreatePreviewRequest): AlterPreview {
  const key = enabled && request ? JSON.stringify([assetId, database, request]) : null;
  return useStatementPreview(key, () =>
    GetCreateTableStatements(assetId, database, request!).then((statements) => ({
      statements: statements ?? [],
      table: request!.name,
    }))
  );
}
