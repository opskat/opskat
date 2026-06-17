import { defineConfig, devices } from "@playwright/test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const MASTER_KEY = "opskat-e2e-master-key-do-not-use-in-prod";
// Deterministic data dir so every config re-eval resolves the SAME path:
// Playwright loads this config in the main runner AND in each worker process,
// so a random mkdtemp would yield a different dir per process — the db-oracle
// worker would then read a file the app (launched by the main process) never wrote.
const dataDir = join(tmpdir(), "opskat-e2e-data");

// Only the main runner (TEST_WORKER_INDEX undefined), not workers, prepares a
// fresh dir — and it runs before the webServer launches. Workers reuse the same
// path to read the db the app wrote.
if (process.env.TEST_WORKER_INDEX === undefined) {
  rmSync(dataDir, { recursive: true, force: true });
  mkdirSync(dataDir, { recursive: true });
  // `wails dev` needs frontend/dist to exist for the //go:embed (mirrors `make dev`).
  // Done here in Node — not via `mkdir -p`/`touch` in the webServer command — so that
  // command stays shell-agnostic and runs on native Windows (cmd) too.
  const distDir = join(__dirname, "..", "frontend", "dist");
  mkdirSync(distDir, { recursive: true });
  writeFileSync(join(distDir, ".keep"), "");
}

process.env.OPSKAT_DATA_DIR = dataDir;
process.env.OPSKAT_MASTER_KEY = MASTER_KEY;
process.env.OPSKAT_E2E = "1";
process.env.OPSKAT_EXTENSIONS = "0";

// Dedicated wails dev server port for e2e (avoids the default 34115).
const DEVSERVER = "localhost:34216";
const BASE_URL = `http://${DEVSERVER}`;
const WEBSERVER_LOG = join(tmpdir(), "opskat-e2e-webserver.log");

// A tiny in-harness mock Redis (pure Node, see fixtures/redis-mock.mjs) on a
// dedicated port, so the real app can actually dial a "Redis" asset and its
// "Test Connection" (a single PING) succeeds — no external Redis needed. Read
// back from the spec via process.env.MOCK_REDIS_PORT (config is re-evaluated in
// each worker, so the env carries the port into the test process).
const MOCK_REDIS_PORT = 34217;
process.env.MOCK_REDIS_PORT = String(MOCK_REDIS_PORT);
const REDIS_MOCK = join(__dirname, "fixtures", "redis-mock.mjs");

// A tiny in-harness mock SSH server (golang.org/x/crypto/ssh, NoClientAuth) on a
// dedicated port, so the real app can dial an "SSH" asset and its "Test Connection"
// (a TCP dial + SSH handshake) succeeds — no real sshd needed. Same shape as the
// Redis mock; the spec reads the port from process.env.SSH_MOCK_PORT.
const SSH_MOCK_PORT = 34218;
process.env.SSH_MOCK_PORT = String(SSH_MOCK_PORT);

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      // Mock Redis for the connect spec. Playwright waits on the TCP `port`
      // (raw socket, not HTTP) and tears the process down after the run.
      command: `node "${REDIS_MOCK}" ${MOCK_REDIS_PORT}`,
      port: MOCK_REDIS_PORT,
      reuseExistingServer: !process.env.CI,
      stdout: "ignore",
      stderr: "ignore",
    },
    {
      // Mock SSH server for the connect spec. `go run` a tiny x/crypto/ssh server
      // (a project dep); cwd is the repo root so the relative package path resolves
      // inside the Go module. Playwright waits on the raw TCP `port`.
      command: `go run ./e2e/fixtures/ssh-mock ${SSH_MOCK_PORT}`,
      cwd: "..",
      port: SSH_MOCK_PORT,
      reuseExistingServer: !process.env.CI,
      stdout: "ignore",
      stderr: "ignore",
    },
    {
      // `wails dev -devserver` binds the IPC bridge to our dedicated port. frontend/dist
      // prep happens in Node above (not in this command) so the command is just one
      // shell-agnostic line. Output is redirected to a file (not Playwright's pipe):
      // wails dev orphans its vite child on shutdown, and a piped stdout the orphan keeps
      // open would stop the Node runner from ever exiting (teardown hang). The log file
      // stays available for debugging; readiness is detected via `url` polling, not stdout.
      // `> "file" 2>&1` is valid in both POSIX sh and Windows cmd.
      command: `wails dev -devserver ${DEVSERVER} > "${WEBSERVER_LOG}" 2>&1`,
      cwd: "..",
      url: BASE_URL,
      reuseExistingServer: !process.env.CI,
      timeout: 600_000,
      stdout: "ignore",
      stderr: "ignore",
      env: {
        OPSKAT_DATA_DIR: dataDir,
        OPSKAT_MASTER_KEY: MASTER_KEY,
        OPSKAT_E2E: "1",
        OPSKAT_EXTENSIONS: "0",
      },
    },
  ],
});
