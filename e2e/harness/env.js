// Shared harness facts — port allocation, isolated data dirs, `.env` loading,
// `frontend/dist` prep, Chromium spawning, orphan-vite reaping.
//
// Two things consume this: `playwright.config.ts` (the committed suite and the
// scratch config) and `sandbox.mjs` (the interactive verification sandbox), plus the
// `drive.mjs` / `oracle.mjs` tools. They live here rather than in any one consumer so
// the suite and the sandbox cannot drift into two different notions of "isolated" —
// the whole point of the sandbox is that it inherits the suite's isolation
// guarantees, not a lookalike copy.
//
// Everything here is scoped to *this checkout*, so several worktrees can verify at
// the same time without seeing each other: ports, data dirs, log files and the
// session file all carry the workspace id.
//
// CommonJS on purpose. Playwright's loader transpiles *every* module under the
// project root to CJS — including `.mjs` — and Node then refuses to run the result
// as ESM (`exports is not defined in ES module scope`). Authoring it as CJS is what
// lets both the transpiled `playwright.config.ts` and the plain-ESM tools import it.
//
// Conventions and rationale: docs/references/e2e-harness-guide.md
const { execFileSync, spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync, openSync, closeSync, unlinkSync } = require("node:fs");
const { homedir, tmpdir } = require("node:os");
const { join } = require("node:path");

const repoRoot = join(__dirname, "..", "..");

// A stable short id for this checkout. Every isolated resource below is named with
// it, which is what lets two worktrees run verification concurrently.
const workspaceId = createHash("sha256").update(repoRoot).digest("hex").slice(0, 8);

// Where per-workspace state lives. The registry sits beside the per-workspace data
// dirs so one directory holds everything verification owns.
const verifyRoot = process.env.OPSKAT_VERIFY_ROOT || join(homedir(), ".opskat-verify");
const registryFile = join(verifyRoot, "workspaces.json");

// Ports come in blocks of 8, starting at 34216 — never Wails' default 34115, so a dev
// server you (or the sibling `agentre` checkout) already have open can never be
// mistaken for ours. Each workspace owns one block:
//
//   +0 suite app   +1 redis mock   +2 ssh mock   +3 openai mock
//   +4 sandbox app +5 sandbox CDP  +6/+7 spare
//
// The sandbox gets its own app port rather than sharing the suite's: the suite's
// webServer entries run with `reuseExistingServer` locally, so sharing would let
// `make test-e2e` silently adopt a live sandbox and report green against an app it
// never built.
const PORT_BASE = 34216;
const BLOCK_SIZE = 8;
const MAX_BLOCKS = 32;

// Resolves this workspace's port block, claiming a free one on first use and
// remembering it. It must be *deterministic across processes* — Playwright
// re-evaluates its config in every worker, and drive.mjs runs as a fresh process per
// command — so the assignment is persisted rather than recomputed, and a plain hash
// is not enough (two worktrees could hash to the same block).
let cachedBase = null;
function portBase() {
  if (cachedBase !== null) return cachedBase;
  if (process.env.OPSKAT_PORT_BASE) {
    cachedBase = Number(process.env.OPSKAT_PORT_BASE);
    return cachedBase;
  }
  mkdirSync(verifyRoot, { recursive: true });
  const registry = withRegistryLock(() => {
    const current = readRegistry();
    if (current[repoRoot] === undefined) {
      const taken = new Set(Object.values(current));
      let index = 0;
      while (taken.has(index) && index < MAX_BLOCKS) index++;
      if (index >= MAX_BLOCKS) {
        throw new Error(
          `all ${MAX_BLOCKS} verification port blocks are claimed; prune ${registryFile}`,
        );
      }
      current[repoRoot] = index;
      writeFileSync(registryFile, JSON.stringify(current, null, 2));
    }
    return current;
  });
  cachedBase = PORT_BASE + registry[repoRoot] * BLOCK_SIZE;
  return cachedBase;
}

function ports() {
  const base = portBase();
  return {
    app: base,
    redisMock: base + 1,
    sshMock: base + 2,
    openaiMock: base + 3,
    sandboxApp: base + 4,
    // Chromium's DevTools endpoint. drive.mjs attaches over it rather than launching
    // its own browser, which is what lets each command be a separate short-lived
    // process while the page and its state persist.
    sandboxCdp: base + 5,
  };
}

function readRegistry() {
  if (!existsSync(registryFile)) return {};
  try {
    return JSON.parse(readFileSync(registryFile, "utf8"));
  } catch {
    return {};
  }
}

// Exclusive-create lock: two worktrees starting at the same moment must not both
// claim the same block. Spins briefly, then proceeds anyway — a stale lock from a
// killed process must not wedge every future run.
function withRegistryLock(fn) {
  const lock = `${registryFile}.lock`;
  let handle = null;
  for (let attempt = 0; attempt < 50; attempt++) {
    try {
      handle = openSync(lock, "wx");
      break;
    } catch {
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 20);
    }
  }
  try {
    return fn();
  } finally {
    if (handle !== null) {
      closeSync(handle);
      try {
        unlinkSync(lock);
      } catch {
        // someone else already cleaned it up
      }
    }
  }
}

class IsolationError extends Error {
  constructor(message) {
    super(message);
    this.name = "IsolationError";
  }
}

// Refuses any URL that is not this checkout's own sandbox or suite app.
//
// The Go gate in `resolveBootstrap` stops a verification run from *booting* on real
// data; this stops a driver from *reaching* an app that was booted some other way —
// your `make dev` on 34115, a colleague's build, another worktree's sandbox. Both are
// needed: one guards the data, the other guards the target.
function assertSandboxUrl(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    throw new IsolationError(`not a URL: ${url}`);
  }
  const host = parsed.hostname;
  if (host !== "localhost" && host !== "127.0.0.1" && host !== "[::1]") {
    throw new IsolationError(`refusing to drive ${parsed.origin}: verification is local-only`);
  }
  const allowed = Object.values(ports());
  if (!allowed.includes(Number(parsed.port))) {
    throw new IsolationError(
      `refusing to drive ${parsed.origin}: port ${parsed.port} is not this checkout's ` +
        `(${allowed.join(", ")}). ${Number(parsed.port) === 34115 ? "34115 is `make dev` — your real data." : "It belongs to another worktree or another app."}`,
    );
  }
  return url;
}

// Any non-empty string works — it is an Argon2id passphrase (`credential_svc.New`),
// not a fixed-length key. An explicit value short-circuits keychain access, so no
// verification run reads or writes the OS keychain.
const SUITE_MASTER_KEY = "opskat-e2e-master-key-do-not-use-in-prod";
const SANDBOX_MASTER_KEY = "opskat-sandbox-master-key-do-not-use-in-prod";

// The committed suite's data dir: deterministic, because Playwright re-evaluates its
// config in every worker process — a random `mkdtemp` would hand the DB-oracle worker
// a different path than the one the app (launched by the main process) wrote. Wiped
// before each run and removed after it by `run-e2e.mjs`.
function suiteDataDir() {
  return join(tmpdir(), `opskat-e2e-data-${workspaceId}`);
}

// The interactive sandbox's data dir. Under `$HOME` rather than `$TMPDIR` because it
// deliberately *persists* across launches — seed an asset once, reopen the sandbox
// tomorrow, it is still there — and rather than inside the repo because a `data/`
// directory beside the executable would flip the app into portable mode.
//
// Deliberately NOT derived from the app's real data dir: re-deriving
// `bootstrap.AppDataDir()`'s platform switch in JS would make this file a second
// owner of that fact, free to drift from the Go one. This path cannot collide with
// the real dir on any platform, and `resolveBootstrap` in `main.go` is the actual
// enforcement — it refuses to boot a verification run on the real dir.
function sandboxDataDir() {
  return process.env.OPSKAT_SANDBOX_DIR || join(verifyRoot, workspaceId);
}

// Where sandbox.mjs records the live session so drive.mjs / oracle.mjs can find it
// without being told. Under $TMPDIR because it describes a *running* process, not the
// persistent sandbox state.
function sessionFile() {
  return join(tmpdir(), `opskat-sandbox-${workspaceId}.json`);
}

function webserverLogPath(kind = "e2e") {
  return join(tmpdir(), `opskat-${kind}-${workspaceId}.log`);
}

// `wails dev` needs `frontend/dist` to exist for the //go:embed (mirrors `make dev`).
// Done in Node rather than as a `mkdir -p`/`touch` in a webServer command so the
// command stays shell-agnostic and runs on native Windows (cmd) too.
function prepareFrontendDist() {
  const distDir = join(repoRoot, "frontend", "dist");
  mkdirSync(distDir, { recursive: true });
  writeFileSync(join(distDir, ".keep"), "");
}

// Loads the gitignored repo-root `.env` into `process.env` so a verification run can
// read real targets (`E2E_SSH_*`, …) the same way it reads `OPSKAT_DATA_DIR`. No app
// code reads this file. Optional by design — absent on CI and on a fresh clone — and
// an already-set env var always wins over the file.
//
// Keep one `KEY=value` per line with no inline comments; the parse is deliberately
// trivial so the file stays readable as a list of targets.
function loadDotEnv() {
  const envFile = join(repoRoot, ".env");
  if (!existsSync(envFile)) return false;
  for (const line of readFileSync(envFile, "utf8").split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq < 1) continue;
    const key = trimmed.slice(0, eq).trim();
    if (process.env[key] === undefined) process.env[key] = trimmed.slice(eq + 1).trim();
  }
  return true;
}

// The in-repo extensions a verification run installs before the app boots.
//
// `notebook` is the reference extension (extensions/notebook): an asset type with a
// configSchema, tools and policy groups — i.e. everything the extension-facing UI
// renders. Anything driving that UI needs it present, so the harness ships it rather
// than each spec arranging its own install.
const HARNESS_EXTENSIONS = ["notebook"];

// Builds each extension's wasm guest and lays the result out in the run's data dir,
// exactly as an installed extension looks on disk (`<dataDir>/extensions/<name>/`) —
// the app scans that directory at boot, so nothing else has to run.
//
// Building here rather than in a CI step or a Makefile recipe keeps `pnpm test` the
// single entry point on every platform: CI already has the Go toolchain (it runs
// `wails dev`), and `GOOS=wasip1` needs no extra SDK. The go build cache makes the
// repeat cost negligible.
//
// The copy rule mirrors `make build-ext`: ship the whole extension directory except
// the Go sources it was built from and any previous local build output.
function installExtensions(dataDir, names = HARNESS_EXTENSIONS) {
  for (const name of names) {
    const source = join(repoRoot, "extensions", name);
    const target = join(dataDir, "extensions", name);
    mkdirSync(target, { recursive: true });
    execFileSync("go", ["build", "-buildmode=c-shared", "-o", join(target, "main.wasm"), `./extensions/${name}`], {
      cwd: repoRoot,
      env: { ...process.env, GOOS: "wasip1", GOARCH: "wasm" },
      stdio: "inherit",
    });
    cpSync(source, target, {
      recursive: true,
      filter: (path) => !path.endsWith(".go") && !path.startsWith(join(source, "dist")),
    });
  }
  return names;
}

// Starts the browser host (see harness/browser-host.mjs) detached, so the caller can
// exit while the browser keeps running, and returns its pid for `sandbox.mjs down`.
//
// Headless by default — verification should not steal focus or flash windows,
// especially with several worktrees verifying at once.
function spawnBrowserHost({ cdpPort, profile, url, headed = false }) {
  mkdirSync(profile, { recursive: true });
  const child = spawn(
    process.execPath,
    [join(__dirname, "browser-host.mjs"), String(cdpPort), profile, url, headed ? "headed" : "headless"],
    { stdio: "ignore", detached: process.platform !== "win32" },
  );
  child.unref();
  return child.pid;
}

async function waitForCdp(cdpPort, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${cdpPort}/json/version`, {
        signal: AbortSignal.timeout(2000),
      });
      if (response.ok) return true;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  return false;
}

// `wails dev` orphans its vite child on shutdown (a separate process group on Unix),
// which a parent's group-kill misses. Reap it by command line, scoped to THIS
// checkout's frontend so neither a sibling repo (e.g. agentre) nor another worktree
// verifying concurrently is ever touched. Best-effort: no match → non-zero exit →
// ignored.
function reapOrphanVite() {
  const frontend = join(repoRoot, "frontend");
  try {
    if (process.platform === "win32") {
      // No pkill on Windows; match via CIM and force-kill. `-ne $PID` excludes THIS
      // PowerShell — its own command line contains the pattern, so without it we'd
      // recreate the very self-kill we're avoiding.
      const ps =
        "Get-CimInstance Win32_Process | Where-Object { " +
        `$_.ProcessId -ne $PID -and $_.CommandLine -like '*${frontend}*vite*' } | ` +
        "ForEach-Object { Stop-Process -Id $_.ProcessId -Force }";
      execFileSync("powershell", ["-NoProfile", "-NonInteractive", "-Command", ps], {
        stdio: "ignore",
      });
    } else {
      execFileSync("pkill", ["-f", `${frontend}.*vite`], { stdio: "ignore" });
    }
  } catch {
    // best-effort hygiene; nothing to reap.
  }
}

// The protocol mocks, as Playwright `webServer` entries. Readiness is a raw TCP
// `port`, not an HTTP `url`. The sandbox reuses the same list behind `--mocks` so an
// interactive run can drive "Test Connection" without any real infrastructure.
function mockServers() {
  const fixtures = join(repoRoot, "e2e", "fixtures");
  const p = ports();
  const sshCommandLog = join(process.env.OPSKAT_DATA_DIR || sandboxDataDir(), "ssh-mock.commands");
  return [
    {
      name: "redis-mock",
      command: `node "${join(fixtures, "redis-mock.mjs")}" ${p.redisMock}`,
      cwd: repoRoot,
      port: p.redisMock,
      env: "MOCK_REDIS_PORT",
    },
    {
      // `go run` a tiny x/crypto/ssh server (a project dep); it runs from the repo
      // root so the relative package path resolves inside the Go module.
      name: "ssh-mock",
      command: `go run ./e2e/fixtures/ssh-mock ${p.sshMock} "${sshCommandLog}"`,
      cwd: repoRoot,
      port: p.sshMock,
      env: "SSH_MOCK_PORT",
    },
    {
      name: "openai-mock",
      command: `node "${join(fixtures, "openai-mock.mjs")}" ${p.openaiMock}`,
      cwd: repoRoot,
      port: p.openaiMock,
      env: "OPENAI_MOCK_PORT",
    },
  ];
}

module.exports = {
  repoRoot,
  IsolationError,
  assertSandboxUrl,
  workspaceId,
  portBase,
  ports,
  SUITE_MASTER_KEY,
  SANDBOX_MASTER_KEY,
  suiteDataDir,
  sandboxDataDir,
  sessionFile,
  webserverLogPath,
  prepareFrontendDist,
  installExtensions,
  loadDotEnv,
  spawnBrowserHost,
  waitForCdp,
  reapOrphanVite,
  mockServers,
};
