import { useEffect, useMemo, useRef } from "react";
import { ClassifySQLStatements } from "../../../wailsjs/go/query/Query";
import { fromDriverStatements, type ClassifiedSqlStatement } from "@/lib/sqlStatements";

/** 驱动对一段文本的切分：切出的语句，或驱动给出的错误（切不了、驱动不可用）。 */
export type DriverSplit =
  { text: string; statements: ClassifiedSqlStatement[]; error?: undefined } | { text: string; error: string };

/** 编辑器用的驱动切分：资产的驱动声明了语句切分分类时才启用。返回的对象引用稳定。 */
export interface DriverStatements {
  /** 资产的驱动是否声明了语句切分分类。 */
  enabled(): boolean;
  /** 最近一次取到的切分；它对应的不是 text（已过期或还没取到）时为 undefined。 */
  resultFor(text: string): DriverSplit | undefined;
  /** 输入停顿后调用：向驱动取 text 的切分，取到时通知 onChange 登记的回调。未启用时什么也不做。 */
  refresh(text: string): void;
  /** 执行时调用：立即向驱动取 text 的切分；驱动出错或不可用时以它给出的原因拒绝。 */
  split(text: string): Promise<ClassifiedSqlStatement[]>;
  /** 登记切分结果到达时的回调（编辑器挂载时登记一次）。 */
  onChange(listener: () => void): void;
}

/**
 * 资产 assetId 的驱动切分。enabled 由调用方按驱动描述给出（不比较驱动 id）。只保留最新一次
 * refresh 的结果：更早发出、更晚到达的答复丢弃。
 */
export function useDriverStatements(assetId: number, enabled: boolean): DriverStatements {
  const config = useRef({ assetId, enabled });
  useEffect(() => {
    config.current = { assetId, enabled };
  }, [assetId, enabled]);
  const latest = useRef<DriverSplit | null>(null);
  const seq = useRef(0);
  const listener = useRef(() => {});

  return useMemo<DriverStatements>(() => {
    const split = async (text: string) =>
      fromDriverStatements(text, await ClassifySQLStatements(config.current.assetId, text));
    const settle = (id: number, result: DriverSplit) => {
      if (id !== seq.current) return;
      latest.current = result;
      listener.current();
    };
    return {
      enabled: () => config.current.enabled,
      resultFor: (text) => (latest.current?.text === text ? latest.current : undefined),
      refresh(text) {
        if (!config.current.enabled) return;
        const id = ++seq.current;
        split(text).then(
          (statements) => settle(id, { text, statements }),
          (e: unknown) => settle(id, { text, error: String(e) })
        );
      },
      split,
      onChange(next) {
        listener.current = next;
      },
    };
  }, []);
}
