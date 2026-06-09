import { defineConfig, devices } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Created once when the config is loaded (before webServer starts). Workers and
// globalTeardown read OPSKAT_DATA_DIR from the inherited process env.
const dataDir = mkdtempSync(join(tmpdir(), "opskat-e2e-"));
const MASTER_KEY = "opskat-e2e-master-key-do-not-use-in-prod";
process.env.OPSKAT_DATA_DIR = dataDir;
process.env.OPSKAT_MASTER_KEY = MASTER_KEY;
process.env.OPSKAT_E2E = "1";
process.env.OPSKAT_EXTENSIONS = "0";

// Dedicated wails dev server port for e2e (avoids the default 34115).
const DEVSERVER = "localhost:34216";
const BASE_URL = `http://${DEVSERVER}`;

export default defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: [["list"], ["html", { open: "never" }]],
  globalTeardown: "./global-teardown.ts",
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    // `wails dev -devserver` binds the IPC bridge to our dedicated port. The
    // mkdir/touch mirrors `make dev`'s prep for the //go:embed frontend/dist.
    command: `mkdir -p frontend/dist && touch frontend/dist/.keep && wails dev -devserver ${DEVSERVER}`,
    cwd: "..",
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 240_000,
    stdout: "pipe",
    stderr: "pipe",
    env: {
      OPSKAT_DATA_DIR: dataDir,
      OPSKAT_MASTER_KEY: MASTER_KEY,
      OPSKAT_E2E: "1",
      OPSKAT_EXTENSIONS: "0",
    },
  },
});
