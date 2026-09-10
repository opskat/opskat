// Read-only `node:sqlite` queries against a run's `opskat.db` — the persistence
// oracle. It is deliberately *independent* of the app's own service layer, which is
// what lets it catch "the UI said OK but nothing was written" bugs.
//
// Shared by `db.ts` (specs, which add Playwright's polling on top) and `oracle.mjs`
// (the CLI you read a live sandbox with) — one owner, so the interactive workflow
// never grows a second, drifting copy of these statements. CommonJS for the same
// reason as harness/env.js: Playwright transpiles everything under the project root.
//
// The data dir comes from `process.env.OPSKAT_DATA_DIR` at call time, so a spec gets
// the harness's temp dir and the CLI gets the sandbox's persistent one.
const { DatabaseSync } = require("node:sqlite");
const { join } = require("node:path");

function dbPath() {
  const dataDir = process.env.OPSKAT_DATA_DIR;
  if (!dataDir) throw new Error("OPSKAT_DATA_DIR not set");
  return join(dataDir, "opskat.db");
}

// Runs `fn` against a read-only handle. Read-only is not a formality: the schema
// belongs to /migrations/, and a writable handle here would let a verification run
// corrupt the very state it is checking.
function query(fn) {
  const db = new DatabaseSync(dbPath(), { readOnly: true });
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    return fn(db);
  } finally {
    db.close();
  }
}

function findAssetByName(name) {
  return query((db) =>
    db.prepare("SELECT id, name, type, status FROM assets WHERE name = ?").get(name),
  );
}

function listAssets() {
  return query((db) =>
    db.prepare("SELECT id, name, type, status FROM assets ORDER BY id").all(),
  );
}

const AUDIT_COLUMNS =
  "id, source, tool_name, asset_id, asset_name, command, request, result, error, success," +
  " decision, decision_source, matched_pattern, conversation_id, session_id";

// Every AI tool call lands one audit_logs row (internal/ai/runner/hooks.go →
// audit.DefaultAuditWriter). Reading it back independently of the app is what proves
// "what the user approved is what got recorded as executed".
function findAuditLogs(filter = {}) {
  const where = [];
  const args = [];
  if (filter.assetName) {
    where.push("asset_name = ?");
    args.push(filter.assetName);
  }
  if (filter.toolName) {
    where.push("tool_name = ?");
    args.push(filter.toolName);
  }
  if (filter.sinceId) {
    where.push("id > ?");
    args.push(filter.sinceId);
  }
  const sql =
    `SELECT ${AUDIT_COLUMNS} FROM audit_logs` +
    (where.length ? ` WHERE ${where.join(" AND ")}` : "") +
    " ORDER BY id";
  return query((db) => db.prepare(sql).all(...args));
}

// The highest audit id right now — take one before exercising a flow so you can read
// back only the rows that flow produced, instead of re-reading the whole table.
function maxAuditId() {
  return query((db) => db.prepare("SELECT COALESCE(MAX(id), 0) AS id FROM audit_logs").get()).id;
}

// Grants persisted by an "allow all / remember" approval
// (permission.HandleConfirm → SaveGrantPattern). Only approved sessions count —
// a pending row would not auto-approve the next command.
function findApprovedGrantItems(assetName) {
  return query((db) =>
    db
      .prepare(
        "SELECT i.id, i.grant_session_id, i.tool_name, i.asset_id, i.asset_name, i.command" +
          " FROM grant_items i JOIN grant_sessions s ON s.id = i.grant_session_id" +
          " WHERE i.asset_name = ? AND s.status = 2 ORDER BY i.id",
      )
      .all(assetName),
  );
}

function findAIProviderByName(name) {
  return query((db) =>
    db
      .prepare("SELECT id, name, type, api_base, api_key, model, is_active FROM ai_providers WHERE name = ?")
      .get(name),
  );
}

// What the app actually persisted for one asset: the config JSON it wrote, the
// policy it attached, and which extension owns the type. Separate from
// findAssetByName because that one is also `oracle.mjs assets`' table output, which
// these long JSON blobs would drown.
function findAssetPersistenceByName(name) {
  return query((db) =>
    db
      .prepare("SELECT id, name, type, config, command_policy, extension_name FROM assets WHERE name = ?")
      .get(name),
  );
}

module.exports = {
  dbPath,
  query,
  findAssetByName,
  findAssetPersistenceByName,
  listAssets,
  findAuditLogs,
  maxAuditId,
  findApprovedGrantItems,
  findAIProviderByName,
};
