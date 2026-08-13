// The DB oracle as specs use it: the read-only queries from ./db-queries.js, plus
// the Playwright-flavoured polling that only makes sense inside a test.
//
// The statements themselves live in db-queries.js so `oracle.mjs` can read a live
// sandbox with exactly the same queries — one owner, no drift between "what a spec
// asserts" and "what you read by hand". Add new read-only helpers there.
import { expect } from "@playwright/test";

import * as queries from "./db-queries.js";

const { dbPath, maxAuditId } = queries;

export interface AssetRow {
  id: number;
  name: string;
  type: string;
  status: number;
}

export interface AuditRow {
  id: number;
  source: string;
  tool_name: string;
  asset_id: number;
  asset_name: string;
  command: string;
  request: string;
  result: string;
  error: string;
  success: number;
  decision: string;
  decision_source: string;
  matched_pattern: string;
  conversation_id: number;
  session_id: string;
}

export interface GrantItemRow {
  id: number;
  grant_session_id: string;
  tool_name: string;
  asset_id: number;
  asset_name: string;
  command: string;
}

export interface AIProviderRow {
  id: number;
  name: string;
  type: string;
  api_base: string;
  api_key: string;
  model: string;
  is_active: number;
}

// Typed views over the shared statements. The row shapes are owned here (specs are
// the only typed consumer; the CLI prints whatever comes back), the SQL is owned by
// db-queries.js — so neither file can change a column without the other noticing.
export { dbPath, maxAuditId };

export const findAssetByName = queries.findAssetByName as (name: string) => AssetRow | undefined;
export const listAssets = queries.listAssets as () => AssetRow[];
export const findAuditLogs = queries.findAuditLogs as (
  filter?: { assetName?: string; toolName?: string; sinceId?: number }
) => AuditRow[];
export const findApprovedGrantItems = queries.findApprovedGrantItems as (
  assetName: string
) => GrantItemRow[];
export const findAIProviderByName = queries.findAIProviderByName as (
  name: string
) => AIProviderRow | undefined;

/**
 * 等审计行落库，返回满足断言的那一份快照。审计断言一律走这里。
 *
 * 审计行不是工具调用的同步产物：runner.auditMiddleware 在工具返回**之后**用一个独立
 * goroutine 写（internal/ai/runner/hooks.go）。所以"资产已经软删除了""对话已经出结果了"
 * 都不代表审计行已经在表里——先等别的副作用、再同步读审计表，是等错了信号。
 * 这里等的信号和读的数据是同一个，也不会在等到之后重查一次（重查可能多出刚落库的行）。
 */
export async function waitForAuditLogs(
  filter: { assetName?: string; toolName?: string },
  count: number,
  opts: { timeout?: number } = {}
): Promise<AuditRow[]> {
  let rows: AuditRow[] = [];
  await expect
    .poll(
      () => {
        rows = findAuditLogs(filter);
        return rows.length;
      },
      { timeout: opts.timeout ?? 60_000 }
    )
    .toBe(count);
  return rows;
}
