import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import { test, expect } from "@playwright/test";

import { installExtensions } from "../harness/env.js";

// `opsctl exec` on an extension asset runs the extension inside opsctl itself: the
// desktop app does not have to be running, and on this data dir none is. The data dir
// is a fresh one the harness installed the fixture extension into (the same
// installExtensions the suite's own data dir gets), so nothing the suite's app does
// can reach this command.

const EXT = { name: "fixture-ext", dir: "pkg/extension/testdata/fixture-ext" };
const ASSET = "e2e-fixture";
// The suite's app compiles its extensions on a background goroutine after boot.
const EXT_READY = 120_000;
test.describe.configure({ timeout: EXT_READY + 120_000 });

function runOpsctl(dataDir: string, args: string[]): Promise<{ code: number | null; stdout: string; stderr: string }> {
  const child = spawn("go", ["run", "./cmd/opsctl", "--data-dir", dataDir, ...args], {
    cwd: "..",
    env: process.env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => (stdout += chunk));
  child.stderr.setEncoding("utf8").on("data", (chunk) => (stderr += chunk));
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code) => resolve({ code, stdout, stderr }));
  });
}

// opsctl registers an extension's asset types from the describe() answer the desktop
// app caches when it loads the extension — the app has loaded it once on this
// machine. The suite's app has loaded the same fixture, so its answer is the one
// copied here, keyed by the hash of the binary in the fresh data dir.
async function cachedFixtureDescriptor(): Promise<string> {
  const suiteDb = join(process.env.OPSKAT_DATA_DIR!, "opskat.db");
  let descriptor = "";
  await expect
    .poll(
      () => {
        const db = new DatabaseSync(suiteDb, { readOnly: true });
        try {
          db.exec("PRAGMA busy_timeout = 5000");
          const row = db.prepare("SELECT descriptor FROM extension_describe WHERE name = ?").get(EXT.name) as
            { descriptor: string } | undefined;
          descriptor = row?.descriptor ?? "";
          return descriptor !== "";
        } finally {
          db.close();
        }
      },
      { timeout: EXT_READY }
    )
    .toBe(true);
  return descriptor;
}

test("opsctl runs an extension tool itself when no desktop app runs on the data dir", async () => {
  const dataDir = mkdtempSync(join(tmpdir(), "opskat-e2e-opsctl-ext-"));
  try {
    installExtensions(dataDir, [EXT]);
    // The first command creates and migrates the fresh database.
    const listed = await runOpsctl(dataDir, ["ext", "list"]);
    expect(listed, listed.stderr).toMatchObject({ code: 0 });

    const descriptor = await cachedFixtureDescriptor();
    const wasmHash = createHash("sha256")
      .update(readFileSync(join(dataDir, "extensions", EXT.name, "main.wasm")))
      .digest("hex");
    const now = Math.floor(Date.now() / 1000);
    const db = new DatabaseSync(join(dataDir, "opskat.db"));
    try {
      db.prepare(
        "INSERT INTO extension_describe (name, wasm_hash, descriptor, createtime, updatetime) VALUES (?, ?, ?, ?, ?)"
      ).run(EXT.name, wasmHash, descriptor, now, now);
      db.prepare(
        "INSERT INTO assets (name, type, config, extension_name, status, createtime, updatetime) VALUES (?, ?, ?, ?, 1, ?, ?)"
      ).run(ASSET, "fixture", JSON.stringify({ endpoint: "http://127.0.0.1:1" }), EXT.name, now, now);
    } finally {
      db.close();
    }

    const result = await runOpsctl(dataDir, ["exec", ASSET, "--", "echo", "--msg", "hello"]);

    expect(result, result.stderr).toMatchObject({ code: 0 });
    // stdout is the tool's result and nothing else: a pipe can parse it as is.
    expect(JSON.parse(result.stdout)).toEqual({ tool: "echo", args: { msg: "hello" } });

    const audit = new DatabaseSync(join(dataDir, "opskat.db"), { readOnly: true });
    try {
      const rows = audit
        .prepare("SELECT source, tool_name, command, decision, success FROM audit_logs WHERE asset_name = ?")
        .all(ASSET);
      expect(rows).toEqual([
        { source: "opsctl", tool_name: "exec", command: "echo --msg=hello", decision: "allow", success: 1 },
      ]);
    } finally {
      audit.close();
    }
  } finally {
    rmSync(dataDir, { recursive: true, force: true });
  }
});

// With the desktop app running on the data dir and no terminal, the desktop is the
// approver of a call opsctl runs itself: its dialog shows the same extension item the
// app's own AI approvals show — the action and resource the extension classified the
// call as — and once approved, the tool's result is what opsctl prints.
test("the desktop app approves an extension call opsctl runs, showing its action and resource", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
  // The suite's app has loaded the fixture once it has cached its describe() answer.
  await cachedFixtureDescriptor();

  const asset = `e2e-fixture-approve-${Date.now()}`;
  const db = new DatabaseSync(join(process.env.OPSKAT_DATA_DIR!, "opskat.db"));
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    const now = Math.floor(Date.now() / 1000);
    db.prepare(
      "INSERT INTO assets (name, type, config, extension_name, status, createtime, updatetime) VALUES (?, ?, ?, ?, 1, ?, ?)"
    ).run(asset, "fixture", JSON.stringify({ endpoint: "http://127.0.0.1:1" }), EXT.name, now, now);
  } finally {
    db.close();
  }

  // The fixture's default policy allows "read" only: a write needs an approver, and
  // with stdin not a terminal that is the desktop app.
  const resultPromise = runOpsctl(process.env.OPSKAT_DATA_DIR!, ["exec", asset, "--", "reject_host", "--resource=logs"]);

  const dialog = page.getByTestId("opsctl-approval-dialog");
  await expect(dialog).toBeVisible({ timeout: 90_000 });
  await expect(dialog.getByTestId("approval-action")).toHaveText("write");
  await expect(dialog.getByTestId("approval-resource")).toHaveText(["logs"]);
  await dialog.getByTestId("opsctl-approval-allow").click();

  const result = await resultPromise;
  expect(result, result.stderr).toMatchObject({ code: 0 });
  expect(JSON.parse(result.stdout)).toEqual({ ran: true });
});
