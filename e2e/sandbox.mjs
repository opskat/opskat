// The verification sandbox launcher: `node sandbox.mjs up | down | status`.
//
// It is the ONE thing you start. `up` brings an isolated OpsKat online with a headless
// browser attached, records the session, and **returns** — nothing to keep in the
// foreground. After that you drive the live app with `drive.mjs` and read its
// side-effects with `oracle.mjs`, so a one-off check never costs a spec file and a
// full harness restart.
//
//   make dev-sandbox                 # up (idempotent — a second call just reports)
//   node e2e/drive.mjs snapshot      # …then drive it from anywhere
//   make dev-sandbox-down            # when you're done
//
// Isolation: the app boots with OPSKAT_DATA_DIR pointing at this checkout's own
// sandbox directory and OPSKAT_E2E=1. That combination is enforced, not assumed —
// `resolveBootstrap` in main.go refuses to start a run marked OPSKAT_E2E=1 whose data
// dir resolves to the real one, so this cannot be talked into opening your live
// inventory.
//
// Concurrency: ports, data dir, log and session file are all scoped to this checkout
// (harness/env.js), so several worktrees can each hold a sandbox open at once.
//
// Usage / workflow: docs/VERIFICATION.md
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync, unlinkSync } from "node:fs";
import { createConnection } from "node:net";
import { join } from "node:path";

import {
  SANDBOX_MASTER_KEY,
  loadDotEnv,
  mockServers,
  ports,
  prepareFrontendDist,
  reapOrphanVite,
  repoRoot,
  sandboxDataDir,
  sessionFile,
  spawnBrowserHost,
  waitForCdp,
  webserverLogPath,
  workspaceId,
} from "./harness/env.js";

const argv = process.argv.slice(2);
const command = argv.find((a) => !a.startsWith("--")) ?? "up";
const flag = (name) => argv.includes(`--${name}`);
const option = (name, fallback) => {
  const hit = argv.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
};

const PORTS = ports();

if (flag("help")) {
  process.stdout.write(`Usage: node e2e/sandbox.mjs [up|down|status] [options]

  up        start the sandbox and return (default; idempotent)
  down      stop it — the data dir survives unless you pass --reset
  status    report what is running for this checkout

  --reset          wipe the sandbox data dir (with up: before starting)
  --mocks          also start the in-harness redis / ssh / openai mocks
  --headed         show the browser window (default: headless)
  --no-browser     don't launch Chromium (drive.mjs will start its own on demand)
  --port=N         app port (default ${PORTS.sandboxApp}, allocated per checkout)
  --data-dir=PATH  use a different isolated data dir
`);
  process.exit(0);
}

const dataDir = option("data-dir", sandboxDataDir());
const port = Number(option("port", PORTS.sandboxApp));
const url = `http://localhost:${port}`;
const logFile = webserverLogPath("sandbox");

switch (command) {
  case "up":
    await up();
    break;
  case "down":
    await down();
    break;
  case "status":
    status();
    break;
  default:
    process.stderr.write(`unknown command "${command}" — expected up, down or status\n`);
    process.exit(1);
}

async function up() {
  const existing = readSession();
  if (existing && (await portOpen(existing.port))) {
    log("already up — reporting the running session");
    return report(existing, "reused");
  }
  if (existing) clearSession(); // stale: recorded, but nothing is listening

  if (flag("reset")) {
    rmSync(dataDir, { recursive: true, force: true });
    log(`reset ${dataDir}`);
  }
  mkdirSync(join(dataDir, "logs"), { recursive: true });
  prepareFrontendDist();
  if (loadDotEnv()) log("loaded .env (real verification targets available as E2E_*)");

  const appEnv = {
    ...process.env,
    OPSKAT_DATA_DIR: dataDir,
    OPSKAT_MASTER_KEY: SANDBOX_MASTER_KEY,
    OPSKAT_E2E: "1",
    OPSKAT_EXTENSIONS: process.env.OPSKAT_EXTENSIONS ?? "0",
  };

  const pids = {};
  if (flag("mocks")) {
    for (const mock of mockServers()) {
      pids[mock.name] = spawnDetached(mock.command, { cwd: mock.cwd });
      if (!(await waitForTcp(mock.port, 60_000))) {
        return fail(`${mock.name} never opened :${mock.port}`, pids);
      }
      appEnv[mock.env] = String(mock.port);
      log(`${mock.name} on :${mock.port}`);
    }
  }

  log("starting the app (first run builds Go + Vite, this takes a few minutes)");
  log(`build output → ${logFile}`);
  // Output to a file rather than a pipe: `wails dev` orphans its vite child, and an
  // inherited pipe the orphan keeps open would stop this process from ever exiting.
  pids.wails = spawnDetached(`wails dev -devserver localhost:${port} > "${logFile}" 2>&1`, {
    cwd: repoRoot,
    env: appEnv,
  });
  if (!(await waitForHttp(url, 600_000))) return fail(`the app never answered on ${url}`, pids);

  if (!flag("no-browser")) {
    // A persistent profile, so localStorage (language, layout, open tabs) survives a
    // restart the way it would for a real user.
    pids.browser = spawnBrowserHost({
      cdpPort: PORTS.sandboxCdp,
      profile: join(dataDir, "browser-profile"),
      url,
      headed: flag("headed"),
    });
    if (!(await waitForCdp(PORTS.sandboxCdp))) {
      return fail("Chromium's DevTools endpoint never came up", pids);
    }
  }

  const session = {
    workspaceId,
    repoRoot,
    url,
    port,
    dataDir,
    cdpEndpoint: pids.browser ? `http://127.0.0.1:${PORTS.sandboxCdp}` : null,
    headed: flag("headed"),
    logFile,
    pids,
  };
  writeFileSync(sessionFile(), JSON.stringify(session, null, 2));
  report(session, "ready");
}

async function down() {
  const session = readSession();
  if (!session) {
    log("nothing recorded for this checkout");
    // Sweep anyway: a killed `up` can leave a tree behind with no session file.
    reapOrphanVite();
    return;
  }
  for (const [name, pid] of Object.entries(session.pids ?? {})) killTree(pid, name);
  // `wails dev` orphans vite into its own process group, so the group kill can miss
  // it; give the tree a moment, then sweep by command line (scoped to this checkout,
  // so a concurrently-running worktree is never touched).
  await new Promise((r) => setTimeout(r, 1500));
  reapOrphanVite();
  clearSession();
  if (flag("reset")) {
    rmSync(session.dataDir, { recursive: true, force: true });
    log(`reset ${session.dataDir}`);
  } else {
    log(`sandbox data kept at ${session.dataDir}`);
  }
  log("down");
}

function status() {
  const session = readSession();
  if (!session) {
    process.stdout.write(
      `no sandbox recorded for ${workspaceId} (${repoRoot})\nstart one: make dev-sandbox\n`,
    );
    process.exit(1);
  }
  const procs = Object.entries(session.pids ?? {}).map(
    ([name, pid]) => `  ${name.padEnd(11)} pid ${pid} ${running(pid) ? "running" : "GONE"}`,
  );
  process.stdout.write(
    `workspace  ${session.workspaceId}\n` +
      `app        ${session.url}\n` +
      `data dir   ${session.dataDir}\n` +
      `browser    ${session.cdpEndpoint ?? "none"}` +
      `${session.cdpEndpoint ? (session.headed ? " (headed)" : " (headless)") : ""}\n` +
      `${procs.join("\n")}\n`,
  );
}

function report(session, state) {
  process.stdout.write(
    `\n${"─".repeat(66)}\n` +
      `  sandbox ${state} — isolated, your real OpsKat data is untouched\n` +
      `${"─".repeat(66)}\n` +
      `  workspace   ${session.workspaceId}  (${session.repoRoot})\n` +
      `  app         ${session.url}\n` +
      `  data dir    ${session.dataDir}\n` +
      `  database    ${join(session.dataDir, "opskat.db")}\n` +
      `  app logs    ${join(session.dataDir, "logs")}\n` +
      `  build log   ${session.logFile}\n` +
      `  browser     ${session.cdpEndpoint ?? "not started (--no-browser)"}` +
      `${session.cdpEndpoint ? (session.headed ? " (headed)" : " (headless)") : ""}\n` +
      `${"─".repeat(66)}\n` +
      "  drive it:   node e2e/drive.mjs snapshot\n" +
      "  read it:    node e2e/oracle.mjs assets\n" +
      "  stop it:    make dev-sandbox-down\n" +
      `${"─".repeat(66)}\n\n`,
  );
}

// Children are fully detached and unref'd, so `up` can exit while they keep running.
// `detached` also gives each its own process group, which is what lets `down` take the
// whole `sh → wails → go/vite` tree down with one signal instead of orphaning it.
function spawnDetached(command, opts) {
  const child = spawn(command, {
    shell: true,
    stdio: "ignore",
    detached: process.platform !== "win32",
    ...opts,
  });
  child.unref();
  return child.pid;
}

function killTree(pid, name) {
  if (!pid || !running(pid)) return;
  try {
    if (process.platform === "win32") {
      spawn("taskkill", ["/pid", String(pid), "/T", "/F"], { stdio: "ignore" });
    } else {
      process.kill(-pid, "SIGTERM");
    }
    log(`stopped ${name}`);
  } catch {
    // already gone, or never got a process group of its own
  }
}

function running(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function fail(message, pids) {
  log(`${message}; see ${logFile}`);
  for (const [name, pid] of Object.entries(pids)) killTree(pid, name);
  reapOrphanVite();
  process.exit(1);
}

function readSession() {
  if (!existsSync(sessionFile())) return null;
  try {
    return JSON.parse(readFileSync(sessionFile(), "utf8"));
  } catch {
    return null;
  }
}

function clearSession() {
  try {
    unlinkSync(sessionFile());
  } catch {
    // already gone
  }
}

function portOpen(target) {
  return new Promise((resolve) => {
    const socket = createConnection({ port: target, host: "127.0.0.1" })
      .on("connect", () => (socket.end(), resolve(true)))
      .on("error", () => resolve(false));
  });
}

function waitForTcp(target, timeoutMs) {
  return poll(timeoutMs, () => portOpen(target));
}

function waitForHttp(target, timeoutMs) {
  return poll(timeoutMs, async () => {
    try {
      await fetch(target);
      return true;
    } catch {
      return false;
    }
  });
}

async function poll(timeoutMs, probe) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await probe()) return true;
    await new Promise((r) => setTimeout(r, 500));
  }
  return false;
}

function log(message) {
  process.stdout.write(`[sandbox] ${message}\n`);
}
