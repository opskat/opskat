import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";

export interface AssetRow {
  id: number;
  name: string;
  type: string;
  status: number;
}

export function dbPath(): string {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (!dataDir) throw new Error("OPSKAT_DATA_DIR not set");
  return join(dataDir, "opskat.db");
}

// Runs `fn` against a read-only handle on the e2e temp opskat.db.
function query<T>(fn: (db: DatabaseSync) => T): T {
  const db = new DatabaseSync(dbPath(), { readOnly: true });
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    return fn(db);
  } finally {
    db.close();
  }
}

// Opens the e2e temp opskat.db read-only and looks up an asset by name.
// Independent of the app's service layer — proves the row really hit disk.
// Uses Node's built-in node:sqlite (no native dependency).
export function findAssetByName(name: string): AssetRow | undefined {
  return query(
    (db) => db.prepare("SELECT id, name, type, status FROM assets WHERE name = ?").get(name) as AssetRow | undefined
  );
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

// Audit oracle for the AI specs: every AI tool call lands one audit_logs row
// (internal/ai/runner/hooks.go → audit.DefaultAuditWriter). Reading it back
// independently of the app is what proves "what the user approved is what got
// recorded as executed".
export function findAuditLogs(filter: { assetName?: string; toolName?: string } = {}): AuditRow[] {
  const where: string[] = [];
  const args: string[] = [];
  if (filter.assetName) {
    where.push("asset_name = ?");
    args.push(filter.assetName);
  }
  if (filter.toolName) {
    where.push("tool_name = ?");
    args.push(filter.toolName);
  }
  const sql =
    "SELECT id, source, tool_name, asset_id, asset_name, command, request, result, error, success, decision," +
    " decision_source, matched_pattern, conversation_id, session_id FROM audit_logs" +
    (where.length ? ` WHERE ${where.join(" AND ")}` : "") +
    " ORDER BY id";
  return query((db) => db.prepare(sql).all(...args) as unknown as AuditRow[]);
}

export interface GrantItemRow {
  id: number;
  grant_session_id: string;
  tool_name: string;
  asset_id: number;
  asset_name: string;
  command: string;
}

// Grants persisted by an "allow all / remember" approval
// (permission.HandleConfirm → SaveGrantPattern). Only approved sessions count —
// a pending row would not auto-approve the next command.
export function findApprovedGrantItems(assetName: string): GrantItemRow[] {
  return query(
    (db) =>
      db
        .prepare(
          "SELECT i.id, i.grant_session_id, i.tool_name, i.asset_id, i.asset_name, i.command" +
            " FROM grant_items i JOIN grant_sessions s ON s.id = i.grant_session_id" +
            " WHERE i.asset_name = ? AND s.status = 2 ORDER BY i.id"
        )
        .all(assetName) as unknown as GrantItemRow[]
  );
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

export function findAIProviderByName(name: string): AIProviderRow | undefined {
  return query(
    (db) =>
      db
        .prepare("SELECT id, name, type, api_base, api_key, model, is_active FROM ai_providers WHERE name = ?")
        .get(name) as AIProviderRow | undefined
  );
}
