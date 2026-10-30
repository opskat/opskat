import { useState } from "react";
import { ExecuteSchemaStatements } from "../../../../wailsjs/go/query/Query";
import type { query_svc } from "../../../../wailsjs/go/models";

/** 一次保存要执行的内容:确认框标题、驱动生成的语句(就是要执行的),与草稿里删除的已有列和索引。 */
export interface SavePlan {
  title: string;
  statements: string[];
  droppedColumns: string[];
  droppedIndexes: string[];
}

/**
 * 保存流程的状态。confirm:等用户确认;running:执行中;rolledBack:整批在事务里执行、有语句失败、已整批
 * 回滚(表结构不变);partial:有语句失败且没有回滚(逐条执行停在中途,或回滚本身失败),已执行的留下;
 * failed:整批没能开始执行(绑定被拒),什么都没执行。全部成功后回到 idle。
 */
export type SaveFlowState<P extends SavePlan> =
  | { phase: "idle" }
  | { phase: "confirm" | "running"; plan: P }
  | { phase: "rolledBack"; plan: P; result: query_svc.SchemaBatchResult }
  | { phase: "partial"; plan: P; result: query_svc.SchemaBatchResult }
  | { phase: "failed"; plan: P; error: string };

/** 执行后表结构变了:全部成功,或停在中途(已执行的留下)。调用方据此重新读取、刷新树。 */
export type SaveOutcome<P extends SavePlan> =
  { kind: "success" } | { kind: "partial"; state: Extract<SaveFlowState<P>, { phase: "partial" }> };

interface SaveFlowOptions<P extends SavePlan> {
  assetId: number;
  database: string;
  /** 初始状态(改名后接手的设计器带着上一次的结果视图)。 */
  initial?: SaveFlowState<P>;
  onExecuted: (plan: P, outcome: SaveOutcome<P>) => void;
}

const IDLE = { phase: "idle" } as const;

/**
 * 保存流程:open 打开确认,confirm 把确认里列出的语句整批交给 ExecuteSchemaStatements(按驱动声明整批
 * 原子执行或逐条执行),按结果进入对应的结果视图;close 关掉对话框(执行中不能关)。
 */
export function useSaveFlow<P extends SavePlan>({ assetId, database, initial, onExecuted }: SaveFlowOptions<P>) {
  const [state, setState] = useState<SaveFlowState<P>>(initial ?? IDLE);

  const open = (plan: P) => setState({ phase: "confirm", plan });
  const close = () => setState((s) => (s.phase === "running" ? s : IDLE));

  const confirm = async () => {
    if (state.phase !== "confirm") return;
    const { plan } = state;
    setState({ phase: "running", plan });
    let result: query_svc.SchemaBatchResult;
    try {
      result = await ExecuteSchemaStatements(assetId, database, plan.statements);
    } catch (err) {
      setState({ phase: "failed", plan, error: String(err) });
      return;
    }
    if (!result.statements.some((s) => s.status === "failed")) {
      setState(IDLE);
      onExecuted(plan, { kind: "success" });
      return;
    }
    if (result.rolledBack) {
      setState({ phase: "rolledBack", plan, result });
      return;
    }
    const partial = { phase: "partial" as const, plan, result };
    setState(partial);
    onExecuted(plan, { kind: "partial", state: partial });
  };

  return { state, open, confirm, close };
}
