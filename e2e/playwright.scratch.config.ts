import { defineConfig } from "@playwright/test";
import base from "./playwright.config";

// Runs throwaway functional-verification specs from ./scratch against the SAME
// live harness (webServer / env injection / isolation / teardown) as the committed
// suite — only the test directory differs. Importing the base config also runs its
// module-top-level setup (fresh temp data dir + env), exactly as a normal suite run.
// Usage / convention: docs/e2e-harness-guide.md + e2e/scratch/README.md.
export default defineConfig({ ...base, testDir: "./scratch" });
