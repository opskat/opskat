import { defineConfig, devices } from "@playwright/test";
import { rmSync, mkdirSync } from "node:fs";
import {
  SUITE_MASTER_KEY,
  installExtensions,
  loadDotEnv,
  mockServers,
  ports,
  prepareFrontendDist,
  suiteDataDir,
  webserverLogPath,
} from "./harness/env.js";

// Ports, data dirs, `.env` loading and `frontend/dist` prep are owned by
// ./harness/env.js, shared with the interactive sandbox (`make dev-sandbox`)
// so the two can never drift apart. All of them are scoped to this checkout, so
// another worktree can run its own suite at the same time. This file only wires
// them into Playwright.
const dataDir = suiteDataDir();
const PORTS = ports();

// Only the main runner (TEST_WORKER_INDEX undefined), not workers, prepares a
// fresh dir — and it runs before the webServer launches. Playwright re-evaluates
// this config in each worker; they reuse the same path to read the db the app wrote.
if (process.env.TEST_WORKER_INDEX === undefined) {
  rmSync(dataDir, { recursive: true, force: true });
  mkdirSync(dataDir, { recursive: true });
  prepareFrontendDist();
  // The reference extension is laid into the fresh data dir before the app boots, so
  // the app's own boot-time scan is what loads it — the same path a user's install
  // ends on. Building it is part of the harness (see harness/env.js), so no separate
  // `make build-ext` step has to be arranged locally or on CI.
  installExtensions(dataDir);
}

process.env.OPSKAT_DATA_DIR = dataDir;
process.env.OPSKAT_MASTER_KEY = SUITE_MASTER_KEY;
process.env.OPSKAT_E2E = "1";
// The extension system runs for the whole suite: the extension specs need it, and the
// app has one webServer, so it cannot be a per-spec choice. It costs one wasm compile
// on a background goroutine at boot — it never blocks startup, and no other spec waits
// on it.
process.env.OPSKAT_EXTENSIONS = "1";

// Real verification targets (E2E_SSH_*, …) reach specs through process.env exactly
// like OPSKAT_DATA_DIR. Convention & usage: docs/references/e2e-harness-guide.md §6.
loadDotEnv();

const DEVSERVER = `localhost:${PORTS.app}`;
const BASE_URL = `http://${DEVSERVER}`;
const WEBSERVER_LOG = webserverLogPath();

// The in-harness protocol mocks, so the real app can actually dial a "Redis" / "SSH"
// asset and drive its Test Connection, and the real AI stack can talk to a *scripted*
// model — no external Redis, sshd or OpenAI needed. Each spec reads its port back from
// the env var below (the config is re-evaluated in each worker, so it carries through).
const mocks = mockServers();
for (const mock of mocks) process.env[mock.env] = String(mock.port);

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
    // Playwright waits on each mock's raw TCP `port` (not HTTP) and tears the
    // process down after the run.
    ...mocks.map((mock) => ({
      command: mock.command,
      cwd: mock.cwd,
      port: mock.port,
      // Protocol fixtures are executable test code and may change with a spec.
      // Never adopt an older process left on the port: that can make a local run
      // exercise stale mock behaviour while reporting against the current tree.
      reuseExistingServer: false,
      stdout: "ignore" as const,
      stderr: "ignore" as const,
    })),
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
        OPSKAT_MASTER_KEY: SUITE_MASTER_KEY,
        OPSKAT_E2E: "1",
        OPSKAT_EXTENSIONS: "1",
      },
    },
  ],
});
