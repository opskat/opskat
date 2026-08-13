import { defineConfig, devices } from "@playwright/test";
import { rmSync, mkdirSync } from "node:fs";
import {
  SUITE_MASTER_KEY,
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
}

process.env.OPSKAT_DATA_DIR = dataDir;
process.env.OPSKAT_MASTER_KEY = SUITE_MASTER_KEY;
process.env.OPSKAT_E2E = "1";
process.env.OPSKAT_EXTENSIONS = "0";

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
      reuseExistingServer: !process.env.CI,
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
        OPSKAT_EXTENSIONS: "0",
      },
    },
  ],
});
