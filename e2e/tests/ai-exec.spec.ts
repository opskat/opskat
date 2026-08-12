import { test, expect } from "@playwright/test";
import {
  ensureAIProvider,
  openApp,
  openNewChat,
  scriptModel,
  sendChat,
  seedEtcdAsset,
  seedRedisAsset,
  seedSSHAsset,
  toolResultsSeenByModel,
} from "../fixtures/ai";
import { waitForAuditLogs } from "../fixtures/db";

// The unified `exec` AI tool, end-to-end through the real app: a scripted model
// (fixtures/openai-mock.mjs, reached via a real ai_providers row) calls exec, the
// real approval dialog renders in the chat, the user clicks, and the command is
// really executed against the in-harness mock server.
//
// The invariant this file guards is "what the user approved is what ran, and is
// what was audited" — so every assertion compares the *same string* across three
// independent surfaces:
//   1. the approval dialog's rendered text (UI),
//   2. audit_logs.command read back with node:sqlite (DB oracle),
//   3. the command the mock sshd actually received, which it echoes back as
//      `mock-exec-ran: <command>` — i.e. the far end of the wire, not another of
//      the app's own copies of the string.

// 每个用例都恰好产生一条 exec 审计行；等它落库并取回。
const waitExecRow = async (asset: string) => (await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 1))[0];

test.beforeEach(async ({ page }) => {
  await openApp(page);
  await ensureAIProvider(page);
});

test("approved exec runs exactly the command the dialog showed, and audits that string", async ({ page }) => {
  const asset = `e2e-ai-exec-${Date.now()}`;
  seedSSHAsset(asset);
  const command = "uptime -p";

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command } } },
    { text: "done" },
  ]);
  await openNewChat(page);
  await sendChat(page, `run ${command} on ${asset}`);

  // The dialog shows the command — not an approximation of it.
  await expect(page.getByTestId("ai-approval-block")).toBeVisible({ timeout: 60_000 });
  const shown = (await page.getByTestId("ai-approval-command").innerText()).trim();
  expect(shown).toBe(command);

  await page.getByTestId("ai-approval-allow").click();

  const row = await waitExecRow(asset);
  expect(row.command).toBe(shown); // audited == approved
  expect(row.result).toBe(`mock-exec-ran: ${shown}\n`); // executed == approved
  expect(row.decision).toBe("allow");
  expect(row.decision_source).toBe("user_allow");
});

test("denied exec never reaches the server and the model is told it was denied", async ({ page }) => {
  const asset = `e2e-ai-deny-${Date.now()}`;
  seedSSHAsset(asset);
  const command = "whoami";

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command } } },
    { text: "stopped" },
  ]);
  await openNewChat(page);
  await sendChat(page, `run ${command} on ${asset}`);

  await expect(page.getByTestId("ai-approval-block")).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("ai-approval-deny").click();

  const row = await waitExecRow(asset);
  expect(row.decision).toBe("deny");
  expect(row.decision_source).toBe("user_deny");
  // The mock sshd echoes every command it is asked to run; nothing was echoed,
  // so nothing ran. Failed tool-block text belongs in the audit error column.
  expect(row.result).not.toContain("mock-exec-ran");
  expect(row.error).toContain(`USER DENIED`);
  expect(row.error).toContain(command);

  // …and the refusal is what the model was handed back as the tool result.
  await expect
    .poll(async () => (await toolResultsSeenByModel()).some((r) => r.includes("USER DENIED")), { timeout: 30_000 })
    .toBe(true);
});

test("the dialog shows the canonicalized command, and that is the string audited", async ({ page }) => {
  // etcd registers a CanonicalizeFunc, so the string that gets policy-checked,
  // approved and audited is the round-tripped one — never the model's raw text.
  const asset = `e2e-ai-canon-${Date.now()}`;
  seedEtcdAsset(asset);
  const raw = 'PUT /e2e/probe "hello world"';
  const canonical = "put /e2e/probe 'hello world'";

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: raw } } },
    { text: "done" },
  ]);
  await openNewChat(page);
  await sendChat(page, `put a key on ${asset}`);

  await expect(page.getByTestId("ai-approval-block")).toBeVisible({ timeout: 60_000 });
  const shown = (await page.getByTestId("ai-approval-command").innerText()).trim();
  expect(shown).toBe(canonical);
  expect(shown).not.toBe(raw);

  await page.getByTestId("ai-approval-allow").click();

  // The audit row records the approved (canonical) string, not the model's raw one.
  // The etcd endpoint itself is unreachable by design here, so this row's `error`
  // is a dial failure — irrelevant to the string identity being asserted.
  const row = await waitExecRow(asset);
  expect(row.command).toBe(shown);
});

test("stopping the turn while an approval is pending cancels it instead of executing", async ({ page }) => {
  const asset = `e2e-ai-cancel-${Date.now()}`;
  seedSSHAsset(asset);

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "sleep 1" } } },
    { text: "done" },
  ]);
  await openNewChat(page);
  await sendChat(page, `sleep on ${asset}`);

  const dialog = page.getByTestId("ai-approval-block");
  await expect(dialog).toBeVisible({ timeout: 60_000 });

  // The user hits stop while the prompt is still waiting for an answer.
  await page.getByTestId("ai-stop-button").click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });

  const row = await waitExecRow(asset);
  // An unanswered prompt resolves as a denial — never as a silent approval.
  expect(row.decision).toBe("deny");
  expect(row.decision_source).toBe("user_deny");
  expect(row.result).not.toContain("mock-exec-ran");
});

test("a Redis asset dispatches through the same exec tool", async ({ page }) => {
  // Proves dispatch keys off the asset's own type, not off a per-type tool name:
  // the identical exec call lands on the Redis executor and talks to the mock Redis.
  const asset = `e2e-ai-redis-${Date.now()}`;
  seedRedisAsset(asset);
  const command = "SET e2e:ai 1";

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command } } },
    { text: "done" },
  ]);
  await openNewChat(page);
  await sendChat(page, `set a key on ${asset}`);

  await expect(page.getByTestId("ai-approval-block")).toBeVisible({ timeout: 60_000 });
  expect((await page.getByTestId("ai-approval-command").innerText()).trim()).toBe(command);
  await page.getByTestId("ai-approval-allow").click();

  const row = await waitExecRow(asset);
  expect(row.command).toBe(command);
  expect(row.decision_source).toBe("user_allow");
  // The reply came from the mock Redis through the app's real Redis client.
  expect(row.result).toContain('"value":"OK"');
});
