// Read the running sandbox's observable side-effects — the DB and the app's
// structured logs — without writing a spec.
//
// This is the "observe" half of interactive verification: `drive.mjs` acts, this
// reads back what actually landed on disk, through a path the UI does not share.
// A green screenshot is not evidence on its own; an `audit_logs` row is.
//
//   node e2e/oracle.mjs mark                    # baseline before you exercise a flow
//   …drive the app…
//   node e2e/oracle.mjs audit --since=41        # only what that flow produced
//   node e2e/oracle.mjs assets my-server
//   node e2e/oracle.mjs logs --tail=50 --grep=ssh
//
// It resolves the sandbox's data dir from the session file that `sandbox.mjs` writes,
// so it needs no arguments. Point it elsewhere with --data-dir=PATH.
//
// Usage / workflow: docs/VERIFICATION.md
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { sandboxDataDir, sessionFile } from "./harness/env.js";

const [command, ...rest] = process.argv.slice(2);
const args = rest.filter((a) => !a.startsWith("--"));
const option = (name, fallback) => {
  const hit = rest.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
};

const USAGE = `Usage: node e2e/oracle.mjs <command> [args] [--flags]

  assets [name]             every asset, or one by name
  audit [--since=ID]        audit_logs rows  [--asset=NAME] [--tool=NAME]
  mark                      current max audit id, to use as --since later
  grants <assetName>        approved grant items ("allow all" / "remember")
  providers <name>          an AI provider row
  tables                    tables in the database
  sql <SELECT …>            any read-only query (SELECT / PRAGMA / WITH only)
  logs [--tail=N]           the app's structured log  [--grep=TEXT] [--level=error]
  where                     the paths this is reading

  --data-dir=PATH           read a different run (default: the live sandbox)
  --json                    raw JSON instead of a table
`;

if (!command || process.argv.includes("--help")) {
  process.stdout.write(USAGE);
  process.exit(command ? 0 : 1);
}

// Resolve the data dir before touching db-queries: its dbPath() reads this at call
// time, which is exactly what lets specs and this CLI share the same statements.
const session = readSession();
const dataDir = option("data-dir", session?.dataDir ?? sandboxDataDir());
process.env.OPSKAT_DATA_DIR = dataDir;

const db = await import("./fixtures/db-queries.js");

try {
  run();
} catch (error) {
  process.stderr.write(`✗ ${command}: ${error.message.split("\n")[0]}\n`);
  if (!existsSync(join(dataDir, "opskat.db"))) {
    process.stderr.write(`  no database at ${dataDir} — is the sandbox running? (make dev-sandbox)\n`);
  }
  process.exit(1);
}

function run() {
  switch (command) {
    case "assets":
      return show(args[0] ? [db.findAssetByName(args[0])].filter(Boolean) : db.listAssets());

    case "audit":
      return show(
        db.findAuditLogs({
          assetName: option("asset"),
          toolName: option("tool"),
          sinceId: option("since") ? Number(option("since")) : undefined,
        }),
      );

    case "mark":
      return process.stdout.write(`${db.maxAuditId()}\n`);

    case "grants":
      return show(db.findApprovedGrantItems(need(0, "grants <assetName>")));

    case "providers":
      return show([db.findAIProviderByName(need(0, "providers <name>"))].filter(Boolean));

    case "tables":
      return show(
        db.query((h) =>
          h.prepare("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name").all(),
        ),
      );

    case "sql": {
      const statement = need(0, "sql <SELECT …>");
      // The handle is already read-only, so this only turns a confusing SQLITE_READONLY
      // into a clear message — it is not the thing keeping the database safe.
      if (!/^\s*(select|pragma|with)\b/i.test(statement)) {
        throw new Error("only SELECT / PRAGMA / WITH are allowed — the oracle never writes");
      }
      return show(db.query((h) => h.prepare(statement).all()));
    }

    case "logs":
      return showLogs();

    case "where":
      return process.stdout.write(
        `data dir   ${dataDir}\n` +
          `database   ${join(dataDir, "opskat.db")}\n` +
          `logs       ${join(dataDir, "logs")}\n` +
          `session    ${session ? "live" : "no running sandbox — reading the dir directly"}\n`,
      );

    default:
      process.stderr.write(`unknown command "${command}"\n\n${USAGE}`);
      process.exit(1);
  }
}

// The app writes one JSON object per line (bootstrap.InitLogger). Filtering here
// rather than telling you to pipe through jq keeps the evidence you paste into a
// report identical to the command you ran.
function showLogs() {
  const dir = join(dataDir, "logs");
  if (!existsSync(dir)) throw new Error(`no logs directory at ${dir}`);
  const files = readdirSync(dir)
    .filter((f) => f.endsWith(".log"))
    .sort();
  if (!files.length) throw new Error(`no .log files under ${dir}`);
  const grep = option("grep");
  const level = option("level");
  const tail = Number(option("tail", 40));
  const lines = readFileSync(join(dir, files[files.length - 1]), "utf8")
    .split("\n")
    .filter(Boolean)
    .filter((line) => (grep ? line.includes(grep) : true))
    .filter((line) => (level ? new RegExp(`"level"\\s*:\\s*"${level}"`).test(line) : true));
  process.stdout.write(`${lines.slice(-tail).join("\n") || "(no matching lines)"}\n`);
}

function show(rows) {
  if (rest.includes("--json")) return process.stdout.write(`${JSON.stringify(rows, null, 2)}\n`);
  if (!rows.length) return process.stdout.write("(no rows)\n");
  process.stdout.write(`${table(rows)}\n${rows.length} row(s)\n`);
}

// A plain aligned table: long values are truncated so a wide audit row stays readable,
// and --json is there when the full value is what you need.
function table(rows) {
  const columns = Object.keys(rows[0]);
  const cell = (row, column) => {
    const value = row[column];
    const text = value === null || value === undefined ? "" : String(value).replace(/\s+/g, " ");
    return text.length > 48 ? `${text.slice(0, 45)}…` : text;
  };
  const widths = columns.map((c) => Math.max(c.length, ...rows.map((r) => cell(r, c).length)));
  const line = (cells) => cells.map((v, i) => v.padEnd(widths[i])).join("  ").trimEnd();
  return [line(columns), line(widths.map((w) => "─".repeat(w))), ...rows.map((r) => line(columns.map((c) => cell(r, c))))].join("\n");
}

function readSession() {
  const file = sessionFile();
  if (!existsSync(file)) return null;
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch {
    return null;
  }
}

function need(index, form) {
  if (args[index] === undefined) throw new Error(`missing argument — ${form}`);
  return args[index];
}
