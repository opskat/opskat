// Cross-platform e2e runner: runs `playwright test` (forwarding any extra args),
// then cleans up AFTER Playwright has fully exited.
//
// Why a Node wrapper instead of doing cleanup elsewhere:
//   - Not in Playwright's globalTeardown: that runs while `wails dev` is still the
//     *managed* webServer, so killing there SIGTERMs the live server (exit 143), and
//     on Windows the still-open opskat.db can't be deleted (EPERM). See harness guide §7.
//   - Not in a Makefile `pkill`: that's Unix-only and self-matches the recipe shell's
//     own command line on Linux (procps reads /proc/<pid>/cmdline), SIGTERMing make.
// Running here means cleanup happens once Playwright has torn down the webServer —
// the app is gone (db closed), vite is orphaned — and it works the same on
// Windows / macOS / Linux, so `make test-e2e` and a bare `pnpm test` both behave.
import { execFileSync, spawn } from "node:child_process";
import { createRequire } from "node:module";
import { rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url)); // e2e/
const repoRoot = join(here, "..");
// These must match the paths in playwright.config.ts.
const dataDir = join(tmpdir(), "opskat-e2e-data");
const webserverLog = join(tmpdir(), "opskat-e2e-webserver.log");

const require = createRequire(import.meta.url);
const playwrightCli = require.resolve("@playwright/test/cli");

const child = spawn(
  process.execPath,
  [playwrightCli, "test", ...process.argv.slice(2)],
  { cwd: here, stdio: "inherit" },
);

child.on("exit", (code) => {
  cleanup({ preserveWebserverLog: code !== 0 });
  // Mirror the child's outcome; a signal-killed run (code === null) counts as failure.
  process.exit(code ?? 1);
});

function cleanup({ preserveWebserverLog }) {
  reapOrphanVite();
  rmSync(dataDir, { recursive: true, force: true });
  if (!preserveWebserverLog) {
    rmSync(webserverLog, { force: true });
  }
}

// `wails dev` orphans its vite child on shutdown (a separate process group on Unix),
// which Playwright's group-kill misses. Reap it by command line, scoped to THIS repo's
// frontend so a sibling checkout's vite (e.g. agentre) is never touched. Best-effort:
// no match → non-zero exit → ignored.
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
