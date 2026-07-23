import { test, expect } from "@playwright/test";
import {
  ensureAIProvider,
  execOutcome,
  openApp,
  openNewChat,
  scriptModel,
  sendChat,
  seedEtcdAsset,
  seedSSHAsset,
} from "../fixtures/ai";
import { findAuditLogs } from "../fixtures/db";

// Ordering invariant: a command that *cannot* execute must never reach the
// approval dialog. The approval prompt has a user-visible side effect (it blocks
// the turn waiting for a click), so every no-side-effect check — asset
// resolution, empty command, the help/doc gate, canonicalization — has to run
// first. This has regressed more than once, hence a permanent guard.
//
// Each case asserts via execOutcome(): it races "an approval dialog appeared"
// against "the call settled", so a regression fails with Received: "approval"
// instead of hanging on a wait.

const execRows = (asset: string) => findAuditLogs({ assetName: asset, toolName: "exec" });
const allExecRows = () => findAuditLogs({ toolName: "exec" });

test.beforeEach(async ({ page }) => {
  await openApp(page);
  await ensureAIProvider(page);
});

test("exec before help is gated with guidance, without prompting the user", async ({ page }) => {
  const asset = `e2e-ai-gate-${Date.now()}`;
  seedSSHAsset(asset);

  // No help() step: the doc gate must short-circuit exec.
  await scriptModel([{ tool: { name: "exec", args: { asset, command: "uptime" } } }, { text: "ok" }]);
  await openNewChat(page);
  await sendChat(page, `uptime on ${asset}`);

  expect(await execOutcome(page, () => execRows(asset).length > 0)).toBe("settled");

  const row = execRows(asset)[0];
  // The failed tool block is stored in the audit error column, while its text is
  // still returned to the model so it can self-correct inside the same turn.
  expect(row.error).toContain(`call help(asset="${asset}")`);
  expect(row.result).not.toContain("mock-exec-ran");
  // Blocked-before-execution is recorded as such, so audit queries can't mistake
  // it for a command that ran.
  expect(row.decision).toBe("deny");
  expect(row.decision_source).toBe("exec_gate_blocked");
});

test("an empty command fails before any approval prompt", async ({ page }) => {
  const asset = `e2e-ai-empty-${Date.now()}`;
  seedSSHAsset(asset);

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "   " } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `run nothing on ${asset}`);

  expect(await execOutcome(page, () => execRows(asset).length > 0)).toBe("settled");
  expect(execRows(asset)[0].error).toBe("missing required parameter: command");
});

test("an unknown asset fails before any approval prompt", async ({ page }) => {
  const missing = `e2e-ai-missing-${Date.now()}`;
  const before = allExecRows().length;

  await scriptModel([{ tool: { name: "exec", args: { asset: missing, command: "uptime" } } }, { text: "ok" }]);
  await openNewChat(page);
  await sendChat(page, `run uptime on ${missing}`);

  expect(await execOutcome(page, () => allExecRows().length > before)).toBe("settled");
  expect(allExecRows().at(-1)!.error).toBe(`asset not found: ${missing}`);
});

test("an ambiguous asset name fails before any approval prompt", async ({ page }) => {
  // Two assets share a name: picking one silently could run the command on the
  // wrong machine, so exec must refuse — and refuse before prompting.
  const asset = `e2e-ai-dup-${Date.now()}`;
  const first = seedSSHAsset(asset);
  const second = seedSSHAsset(asset);
  const before = allExecRows().length;

  await scriptModel([{ tool: { name: "exec", args: { asset, command: "uptime" } } }, { text: "ok" }]);
  await openNewChat(page);
  await sendChat(page, `run uptime on ${asset}`);

  expect(await execOutcome(page, () => allExecRows().length > before)).toBe("settled");
  const row = allExecRows().at(-1)!;
  expect(row.error).toContain("is ambiguous");
  expect(row.error).toContain(`[${first}, ${second}]`);
});

test("a command that fails canonicalization is rejected before any approval prompt", async ({ page }) => {
  // etcd canonicalization is a pure parse — a command that cannot be parsed can
  // never execute, so approving it could only ever end in failure.
  const asset = `e2e-ai-badsyntax-${Date.now()}`;
  seedEtcdAsset(asset);

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "frobnicate /x" } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `run a bogus etcd command on ${asset}`);

  expect(await execOutcome(page, () => execRows(asset).length > 0)).toBe("settled");
  const row = execRows(asset)[0];
  expect(row.error).toBe("unsupported op: frobnicate");
  expect(row.decision).toBe("deny");
  expect(row.decision_source).toBe("exec_canonicalize_error");
});
