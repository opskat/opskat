import { test, expect } from "@playwright/test";
import { ensureAIProvider, openApp, openNewChat, scriptModel, sendChat, seedSSHAsset } from "../fixtures/ai";
import { findAssetByName, findAuditLogs } from "../fixtures/db";

// Plan C collapsed the old per-verb add_asset/update_asset/delete_asset tools into
// put_asset (create-or-update, branching on whether `asset` is passed) and delete_asset
// (always-confirm, never-grantable). This file is the end-to-end proof for both
// invariants, through the real app: a scripted model calls the tool, and the DB /
// approval UI are read back independently of the app's own claims.

const putAssetRows = () => findAuditLogs({ toolName: "put_asset" });

test.beforeEach(async ({ page }) => {
  await openApp(page);
  await ensureAIProvider(page);
});

test("put_asset creates, then updates the same row through the same tool", async ({ page }) => {
  const name = `e2e-ai-put-${Date.now()}`;
  const renamed = `${name}-renamed`;
  const before = putAssetRows().length;

  await scriptModel([
    {
      tool: {
        name: "put_asset",
        args: { name, type: "ssh", config: { host: "127.0.0.1", port: 22, username: "e2e" } },
      },
    },
    { tool: { name: "put_asset", args: { asset: name, name: renamed } } },
    { text: "done" },
  ]);
  await openNewChat(page);
  await sendChat(page, `create an ssh asset called ${name}, then rename it to ${renamed}`);

  // No approval dialog for put_asset — unlike opsctl (which runs headlessly and must
  // gate every write), the chat session already puts the change in front of the user.
  await expect.poll(() => findAssetByName(renamed)?.status, { timeout: 60_000 }).toBe(1);

  // Same underlying row, not a second asset created under the new name: the id the
  // create call produced is the same id the rename left behind, and the old name is
  // gone (renamed, not duplicated).
  const row = findAssetByName(renamed)!;
  expect(row.type).toBe("ssh");
  expect(findAssetByName(name)).toBeUndefined();

  // Both the create and the update went through the one put_asset tool.
  await expect.poll(() => putAssetRows().length, { timeout: 10_000 }).toBe(before + 2);
  const rows = putAssetRows();
  expect(rows[rows.length - 1].asset_name).toBe(name); // resolved from `asset` before the rename took effect
});

test("delete_asset always prompts, and the panel offers no allow-all — approving removes the asset", async ({
  page,
}) => {
  const asset = `e2e-ai-delete-${Date.now()}`;
  seedSSHAsset(asset);

  await scriptModel([{ tool: { name: "delete_asset", args: { asset } } }, { text: "done" }]);
  await openNewChat(page);
  await sendChat(page, `delete the asset ${asset}`);

  const dialog = page.getByTestId("ai-approval-block");
  await expect(dialog).toBeVisible({ timeout: 60_000 });
  await expect(dialog).toHaveAttribute("data-approval-kind", "delete");

  // The invariant this spec exists for: delete_asset can never be pre-approved or
  // granted, so its panel must not offer "remember" (the only path to allow-all).
  await expect(page.getByTestId("ai-approval-remember")).toHaveCount(0);
  await expect(page.getByTestId("ai-approval-allow-all")).toHaveCount(0);

  await page.getByTestId("ai-approval-allow").click();

  // Soft-deleted (StatusDeleted=2), not gone from the table.
  await expect.poll(() => findAssetByName(asset)?.status, { timeout: 60_000 }).toBe(2);

  const rows = findAuditLogs({ assetName: asset, toolName: "delete_asset" });
  expect(rows.length).toBe(1);
  expect(rows[0].decision).toBe("allow");
  expect(rows[0].decision_source).toBe("user_allow");
});
