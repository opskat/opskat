import { test, expect, type Page } from "@playwright/test";
import { openAssetForm } from "../fixtures/assets";
import { findAssetByName, findAssetPersistenceByName, findAuditLogs, type AuditRow } from "../fixtures/db";

// An extension's asset type is not a special species in the frontend: it registers
// into the same registry as the built-in types, so the type picker, the form's
// ConfigSection slot, the detail card slot and the policy card all reach it through
// exactly one path. What makes it different is where the *definition* comes from —
// the guest's describe(), delivered as the manifest's configSchema / policies. This
// spec drives that seam end to end against the in-repo reference extension
// (extensions/notebook), which the harness builds and installs into the run's data
// dir before the app boots (harness/env.js → installExtensions).
//
// It caught the seam being broken outright: every extension without frontend pages
// arrives with `frontend: { pages: null }`, and reading `.pages` as an array threw
// inside the registration the extension store swallows — no type ever reached the
// picker, while vitest stayed green on hand-written manifests.

const EXT = "notebook";

// The extension system compiles the guest's wasm on a background goroutine after
// startup (main.go → initExtensionSystem), so the type appears in the picker a few
// seconds *after* the app answers on its port — longer on a cold CI machine. The
// per-test timeout has to leave room for that wait plus the test itself, or the wait
// below is silently cut short and the failure blames the picker.
const EXT_READY = 120_000;
test.describe.configure({ timeout: EXT_READY + 60_000 });

// The strings below come from the extension's own locales, resolved by the backend
// when it hands the manifest over. Forcing the language keeps that deterministic —
// otherwise the assertion depends on the machine's locale.
const CONFIG = {
  requiredField: "notebook",
  requiredLabel: "笔记本名称",
  requiredPlaceholder: "team-runbooks",
  optionalField: "maxNotes",
  optionalLabel: "笔记数量上限",
};

async function openApp(page: Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("language", "zh-CN"));
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();
}

async function pickExtensionType(page: Page): Promise<void> {
  await openAssetForm(page);
  await page.getByTestId("asset-type-picker").click();
  const option = page.getByTestId(`asset-type-option-${EXT}`);
  await expect(option).toBeVisible({ timeout: EXT_READY });
  await option.click();
}

test("an extension's asset type reaches the picker and its form is generated from configSchema", async ({
  page,
}) => {
  await openApp(page);
  await pickExtensionType(page);

  const dialog = page.getByTestId("asset-form-dialog");
  // One field per configSchema property, addressed by the property name — the schema
  // is the form. Both are here, in the declaration order describe() reported.
  const fields = dialog.locator(`#${CONFIG.requiredField}, #${CONFIG.optionalField}`);
  await expect(fields).toHaveCount(2);
  await expect(fields.first()).toHaveAttribute("id", CONFIG.requiredField);

  // title / placeholder come from the extension's locales via the manifest…
  await expect(dialog.locator(`#${CONFIG.requiredField}`)).toHaveAttribute(
    "placeholder",
    CONFIG.requiredPlaceholder
  );
  // …and `required` shows up as the marker on that field's label, and only there.
  await expect(dialog.locator(`label[for="${CONFIG.requiredField}"]`)).toHaveText(
    `${CONFIG.requiredLabel}*`
  );
  await expect(dialog.locator(`label[for="${CONFIG.optionalField}"]`)).toHaveText(CONFIG.optionalLabel);

  // A property typed `integer` is entered as a number and stored as one: the guest
  // unmarshals this config into its Go struct, so "5" would break every later tool
  // call with `cannot unmarshal string into Go struct field ... of type int`.
  await expect(dialog.locator(`#${CONFIG.optionalField}`)).toHaveAttribute("type", "number");
});

// notebook's asset type declares no test-connection handler (opskat.AssetType
// .TestConnection in the guest SDK — see docs/specs 测试连接), so the button the
// generic AssetForm shows for every *testable* type (SSH, database, …) must not
// render at all for it; it is not merely disabled.
test("an extension asset type without a test-connection handler shows no Test connection button", async ({
  page,
}) => {
  await openApp(page);
  await pickExtensionType(page);

  await expect(page.getByTestId("asset-form-dialog").getByTestId("asset-test-connection")).toHaveCount(0);
});

test("a saved extension asset persists its schema config and renders the detail card from it", async ({
  page,
}) => {
  await openApp(page);
  await pickExtensionType(page);

  const name = `e2e-ext-${Date.now()}`;
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("asset-form-dialog").locator(`#${CONFIG.requiredField}`).fill("team-runbooks");
  await page.getByTestId("asset-form-dialog").locator(`#${CONFIG.optionalField}`).fill("5");
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(name, { exact: true })).toBeVisible();

  // DB oracle: the row carries the extension-owned type, and the config JSON holds
  // the declared types — a number for the `integer` property.
  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe(EXT);
  expect(JSON.parse(findAssetPersistenceByName(name)!.config)).toEqual({
    notebook: "team-runbooks",
    maxNotes: 5,
  });

  // The detail card is generated from the same configSchema and fills the built-in
  // DetailInfoCard slot: every property that has a value, labelled by its title.
  await page.getByTestId("asset-tree").getByText(name, { exact: true }).click({ button: "right" });
  await page.getByTestId("asset-context-detail").click();
  await expect(page.getByText(CONFIG.requiredLabel)).toBeVisible();
  await expect(page.getByText("team-runbooks")).toBeVisible();
  await expect(page.getByText(CONFIG.optionalLabel)).toBeVisible();
});

test("the extension's policy card offers its ext: groups and referencing one persists", async ({ page }) => {
  await openApp(page);
  await pickExtensionType(page);

  const name = `e2e-extpol-${Date.now()}`;
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("asset-form-dialog").locator(`#${CONFIG.requiredField}`).fill("team-runbooks");
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe(EXT);

  await page.getByTestId("asset-tree").getByText(name, { exact: true }).click({ button: "right" });
  await page.getByTestId("asset-context-detail").click();

  // The groups the manifest marks as defaults are attached at creation and shown as
  // referenced; the one it does not is only offered.
  await expect(page.getByTestId(`policy-group-chip-ext:${EXT}:read`)).toBeVisible();
  await expect(page.getByTestId(`policy-group-chip-ext:${EXT}:no-delete`)).toBeVisible();
  await expect(page.getByTestId(`policy-group-chip-ext:${EXT}:write`)).toHaveCount(0);

  // Referencing the remaining one goes through the same policy card the built-in
  // types use, and reaches the asset's command_policy on disk.
  await page.getByTestId("policy-group-add").click();
  await page.getByTestId(`policy-group-option-ext:${EXT}:write`).click();
  await expect(page.getByTestId(`policy-group-chip-ext:${EXT}:write`)).toBeVisible();
  await expect
    .poll(() => JSON.parse(findAssetPersistenceByName(name)?.command_policy || "{}").groups, {
      timeout: 10_000,
    })
    .toEqual([`ext:${EXT}:read`, `ext:${EXT}:no-delete`, `ext:${EXT}:write`]);
});

// Task 8: a call the notebook page makes against its own asset (via
// window.__OPSKAT_EXT__.api.callTool in extensions/notebook/frontend/page.js)
// clears the exact same policy check / in-app approval / grant / audit gate
// opsctl's delegated exec runs through (internal/app/opsctl's handleExtToolExec →
// RunPageToolCall), not a direct dial into the plugin. notebook's own policy
// groups make the three decisions reachable without touching the AI path at all:
// "read" is granted by default (no dialog), "write" is not (needs the same
// opsctl:approval dialog opsctl/AI use), "delete" is denied outright by a default
// group (no dialog, straight to an error).
async function createNotebookAssetAndOpenPage(page: Page, name: string): Promise<void> {
  await pickExtensionType(page);
  await page.getByTestId("asset-form-name-input").fill(name);
  await page.getByTestId("asset-form-dialog").locator(`#${CONFIG.requiredField}`).fill(name);
  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect.poll(() => findAssetByName(name)?.type, { timeout: 10_000 }).toBe(EXT);

  await page.getByTestId("asset-tree").getByText(name, { exact: true }).dblclick();
  await expect(page.getByTestId("notebook-page")).toBeVisible({ timeout: 15_000 });
}

// The page also calls note_list on mount and after every note_put/note_delete to
// refresh its own list — those calls clear the same gate too (as they must: they
// are asset-scoped calls like any other) and land their own "allow" audit rows.
// Asserting on a plain row *count* for tool_name "exec" would therefore couple
// this spec to the page's own refresh behavior rather than to the one call under
// test, so audit rows are picked out by which tool they ran (the normalized
// command's leading verb) instead.
function auditRowsForTool(assetName: string, tool: string): AuditRow[] {
  return findAuditLogs({ assetName, toolName: "exec" }).filter((row) => row.command.startsWith(tool));
}

async function waitForToolAuditRowCount(
  assetName: string,
  tool: string,
  count: number,
  opts: { timeout?: number } = {}
): Promise<AuditRow[]> {
  let rows: AuditRow[] = [];
  await expect
    .poll(
      () => {
        rows = auditRowsForTool(assetName, tool);
        return rows.length;
      },
      { timeout: opts.timeout ?? 15_000 }
    )
    .toBe(count);
  return rows;
}

test("a page call needing confirmation shows the opsctl approval dialog; approving with 'remember' runs it, audits source extension_page, and the grant is honored on the next call", async ({
  page,
}) => {
  await openApp(page);
  const name = `e2e-extpage-put-${Date.now()}`;
  await createNotebookAssetAndOpenPage(page, name);

  const key = "runbook-1";
  await page.getByTestId("notebook-key-input").fill(key);
  await page.getByTestId("notebook-content-input").fill("first version");
  await page.getByTestId("notebook-put-button").click();

  // note_put is not covered by a default policy group, so it lands on NeedConfirm
  // — the same "opsctl:approval" dialog an opsctl or AI-initiated call would get.
  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "记住此操作" }).click();
  await page.getByRole("button", { name: "批准" }).click();

  await expect(page.getByTestId("notebook-status")).toHaveAttribute("data-kind", "success", { timeout: 15_000 });
  await expect(page.getByTestId(`notebook-note-${key}`)).toBeVisible();

  const [putRow] = await waitForToolAuditRowCount(name, "note_put", 1);
  expect(putRow).toMatchObject({ source: "extension_page", decision: "allow" });

  // The "always allow" grant just persisted is keyed by (action, resource) —
  // ext:notebook:write:runbook-1 — not by session or invocation id, so a second
  // call writing the same key must clear the gate without a dialog at all.
  await page.getByTestId("notebook-content-input").fill("second version");
  await page.getByTestId("notebook-put-button").click();
  await expect(page.getByTestId("opsctl-approval-dialog")).not.toBeVisible();
  await expect(page.getByTestId("notebook-status")).toHaveAttribute("data-kind", "success", { timeout: 15_000 });

  const [, secondPutRow] = await waitForToolAuditRowCount(name, "note_put", 2);
  expect(secondPutRow).toMatchObject({ source: "extension_page", decision: "allow" });
});

test("a page call denied by a default policy group returns an error to the page, with no dialog, and is audited", async ({
  page,
}) => {
  await openApp(page);
  const name = `e2e-extpage-deny-${Date.now()}`;
  await createNotebookAssetAndOpenPage(page, name);

  // A note must exist to have a Delete button to click; note_put still needs one
  // approval first (the same NeedConfirm path the other test covers).
  const key = "runbook-2";
  await page.getByTestId("notebook-key-input").fill(key);
  await page.getByTestId("notebook-content-input").fill("to be deleted");
  await page.getByTestId("notebook-put-button").click();
  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 15_000 });
  await page.getByTestId("opsctl-approval-allow").click();
  await expect(page.getByTestId(`notebook-note-${key}`)).toBeVisible({ timeout: 15_000 });

  // note_delete is refused by the "no-delete" group, which is a default — the
  // policy decision is Deny, not NeedConfirm, so no approval dialog is possible;
  // the page must get an error back, and the note must survive.
  await page.getByTestId(`notebook-delete-${key}`).click();
  await expect(page.getByTestId("opsctl-approval-dialog")).not.toBeVisible();
  await expect(page.getByTestId("notebook-status")).toHaveAttribute("data-kind", "error", { timeout: 15_000 });
  await expect(page.getByTestId(`notebook-note-${key}`)).toBeVisible();

  const [putRow] = await waitForToolAuditRowCount(name, "note_put", 1);
  expect(putRow).toMatchObject({ source: "extension_page", decision: "allow" });
  const [deleteRow] = await waitForToolAuditRowCount(name, "note_delete", 1);
  expect(deleteRow).toMatchObject({ source: "extension_page", decision: "deny" });
});
