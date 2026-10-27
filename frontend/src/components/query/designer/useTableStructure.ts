import { useCallback, useEffect, useState } from "react";
import { GetAlterTableModel, GetTableDDL, GetTableMetadata } from "../../../../wailsjs/go/query/Query";
import { structureFromAlterModel, structureFromMetadata, type TableStructure } from "./model";

/** 表结构从哪里读:改表模型(能改表时)或表元数据(只能查看时)。 */
export type StructureSource = "alterModel" | "metadata";

export type LoadState<T> = { status: "loading" } | { status: "ready"; value: T } | { status: "error"; error: string };

interface LoadResult<T> {
  key: string;
  state: LoadState<T>;
}

/**
 * 按 key 读一次:enabled 第一次为真之前不读,之后一直算数(切到后台再回来不重读);key 变了或 reload 后
 * 重新读,读完之前是 loading。结果带着发起时的 key,过期的结果不会被当成当前的。
 */
function useKeyedLoad<T>(key: string, enabled: boolean, load: () => Promise<T>) {
  const [nonce, setNonce] = useState(0);
  const [result, setResult] = useState<LoadResult<T> | null>(null);
  const [started, setStarted] = useState(enabled);
  if (enabled && !started) setStarted(true);
  const requestKey = `${key}#${nonce}`;

  useEffect(() => {
    if (!started) return;
    let alive = true;
    load().then(
      (value) => alive && setResult({ key: requestKey, state: { status: "ready", value } }),
      (err: unknown) => alive && setResult({ key: requestKey, state: { status: "error", error: String(err) } })
    );
    return () => {
      alive = false;
    };
    // load 由 key 决定:key 已包含它用到的全部参数。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requestKey, started]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const state: LoadState<T> = result?.key === requestKey ? result.state : { status: "loading" };
  return { state, reload };
}

export interface TableStructureRequest {
  assetId: number;
  database: string;
  table: string;
  /** 驱动描述还没到时为 null:先不读。 */
  source: StructureSource | null;
  /** 标签第一次到前台之前不读(恢复的标签不必一启动就连库)。 */
  enabled: boolean;
}

/**
 * 读设计器要展示的表结构:能改表时读改表模型(外键、以及模型不带的索引取自表元数据),只能查看时读
 * 表元数据,都映射成同一个 TableStructure。失败给出驱动的原始错误。
 */
export function useTableStructure({ assetId, database, table, source, enabled }: TableStructureRequest) {
  return useKeyedLoad<TableStructure>(
    JSON.stringify([assetId, database, table, source]),
    enabled && source !== null,
    async () => {
      if (source === "alterModel") {
        const [model, metadata] = await Promise.all([
          GetAlterTableModel(assetId, database, table),
          GetTableMetadata(assetId, database, table),
        ]);
        return structureFromAlterModel(model, metadata);
      }
      return structureFromMetadata(table, await GetTableMetadata(assetId, database, table));
    }
  );
}

/** 读驱动给出的建表语句(声明了 view_ddl 的驱动)。 */
export function useTableDDL(assetId: number, database: string, table: string) {
  return useKeyedLoad<string>(JSON.stringify([assetId, database, table]), true, () =>
    GetTableDDL(assetId, database, table)
  );
}
