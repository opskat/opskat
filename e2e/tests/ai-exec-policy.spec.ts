import { test, expect } from "@playwright/test";
import {
  approvalConfirmId,
  ensureAIProvider,
  execOutcome,
  openApp,
  openNewChat,
  pendingApproval,
  scriptModel,
  sendChat,
  seedSSHAsset,
} from "../fixtures/ai";
import { findApprovedGrantItems, findAuditLogs, waitForAuditLogs } from "../fixtures/db";

// The two decision paths that must NOT prompt (policy allow / policy deny), and
// the authorization-persistence rule: "remember + allow" persists a grant that
// auto-approves the next matching command, plain "allow" (this once) does not.
// The allow-once half regressed once before, when a second check re-used the
// first one's decision.

const execRows = (asset: string) => findAuditLogs({ assetName: asset, toolName: "exec" });

test.beforeEach(async ({ page }) => {
  await openApp(page);
  await ensureAIProvider(page);
});

test("a policy-allowed command executes without prompting", async ({ page }) => {
  const asset = `e2e-ai-allow-${Date.now()}`;
  seedSSHAsset(asset, { allow_list: ["uptime"], deny_list: [] });

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "uptime" } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `uptime on ${asset}`);

  expect(await execOutcome(page, () => execRows(asset).length > 0)).toBe("settled");
  const row = execRows(asset)[0];
  expect(row.decision).toBe("allow");
  expect(row.decision_source).toBe("policy_allow");
  expect(row.matched_pattern).toBe("uptime");
  expect(row.result).toBe("mock-exec-ran: uptime\n"); // it really ran
});

test("a policy-denied command is refused without prompting", async ({ page }) => {
  // deny beats confirm: the user is never asked about a command policy forbids.
  const asset = `e2e-ai-denied-${Date.now()}`;
  seedSSHAsset(asset, { allow_list: [], deny_list: ["rm *"] });

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "rm -rf /tmp/x" } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `remove the temp file on ${asset}`);

  expect(await execOutcome(page, () => execRows(asset).length > 0)).toBe("settled");
  const row = execRows(asset)[0];
  expect(row.decision).toBe("deny");
  expect(row.decision_source).toBe("policy_deny");
  expect(row.matched_pattern).toBe("rm *");
  expect(row.result).not.toContain("mock-exec-ran"); // never reached the server
});

test("remember-and-allow persists a grant that auto-approves the next command", async ({ page }) => {
  const asset = `e2e-ai-grant-${Date.now()}`;
  seedSSHAsset(asset);

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "uptime" } } },
    { tool: { name: "exec", args: { asset, command: "uptime" } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `uptime twice on ${asset}`);

  await expect(pendingApproval(page)).toBeVisible({ timeout: 60_000 });
  const answered = await approvalConfirmId(page);
  await page.getByTestId("ai-approval-remember").click();
  await page.getByTestId("ai-approval-allow-all").click();

  // The second, identical command settles without prompting *again* — "again"
  // meaning any prompt other than the one just answered.
  expect(await execOutcome(page, () => execRows(asset).length > 1, { exceptConfirmId: answered })).toBe("settled");
  const rows = execRows(asset);
  expect(rows[0].decision_source).toBe("user_allow");
  expect(rows[1].decision_source).toBe("grant_allow"); // …because the grant matched it
  expect(rows[1].matched_pattern).toBe("uptime");
  // …and the grant is on disk, not just in memory.
  expect(findApprovedGrantItems(asset).map((g) => g.command)).toEqual(["uptime"]);
});

test("allow-this-once persists nothing and the next command prompts again", async ({ page }) => {
  const asset = `e2e-ai-once-${Date.now()}`;
  seedSSHAsset(asset);

  await scriptModel([
    { tool: { name: "help", args: { asset } } },
    { tool: { name: "exec", args: { asset, command: "uptime" } } },
    { tool: { name: "exec", args: { asset, command: "uptime" } } },
    { text: "ok" },
  ]);
  await openNewChat(page);
  await sendChat(page, `uptime twice on ${asset}`);

  await expect(pendingApproval(page)).toBeVisible({ timeout: 60_000 });
  const answered = await approvalConfirmId(page);
  await page.getByTestId("ai-approval-allow").click();

  // The very same command asks again — a one-off approval authorizes one call.
  // A *distinct* prompt (different confirmId) is the race-free proof of that.
  await expect(pendingApproval(page, answered)).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("ai-approval-allow").click();

  const rows = await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 2);
  expect(rows.map((r) => r.decision_source)).toEqual(["user_allow", "user_allow"]);
  expect(findApprovedGrantItems(asset)).toEqual([]);
});
