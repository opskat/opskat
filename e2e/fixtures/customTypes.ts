// UI helpers for the custom-type / generic-asset e2e spec
// (docs/specs/2026-09-28-generic-asset.md). `CustomTypeEditorDialog.tsx` and
// `CustomTypeSection.tsx` are new-enough surfaces that most of their inputs only
// carry an `aria-label` (or nothing at all) rather than the `data-testid` convention
// documented in e2e-harness-guide.md §5 — task 11 owns e2e/fixtures/ and
// e2e/tests/ only, not those components, so these helpers locate through what is
// already there (aria-label / placeholder / role / the existing `config-tab-*` /
// `generic-*` testids) instead of adding new ones.
import { expect, type Locator, type Page } from "@playwright/test";

import { findAssetByName } from "./db";

/**
 * `TemplateInput` (CustomTypeEditorDialog.tsx) swaps its `<input>` for a multi-line
 * `<textarea>` on focus (spec "自定义类型": 模板输入框聚焦时展开为多行). Confirmed by
 * hand against a running sandbox: a *first* `fill()` on a field that still holds the
 * value it loaded with races that swap and appends instead of replacing (the DOM node
 * changes under Playwright mid-action), while a second `fill()` on the now-stable
 * `<textarea>` cleanly replaces it. Filling twice always converges on `value` — for a
 * field that started empty the first fill is already correct and the second is a
 * no-op — so it is the safe way to drive every templated input below.
 */
async function fillTemplate(locator: Locator, value: string): Promise<void> {
  await locator.fill(value);
  await locator.fill(value);
  // TemplateInput swaps back from <textarea> to <input> on blur, which shrinks the
  // field (rows="3" → one line) and shifts everything below it. Left alone, the
  // *next* action's click lands after Playwright computes coordinates but before
  // that shift settles, and silently misses — reproduced against a real run, where
  // a click on "Add authentication" right after filling Base URL did nothing.
  // Blurring here, synchronously in the same step, moves the shift before the
  // caller's next click instead of leaving it to race one.
  await locator.evaluate((el) => (el as HTMLElement).blur());
}

async function openCustomTypesSettings(page: Page): Promise<void> {
  await page.getByTestId("nav-settings").click();
  await page.getByRole("tab", { name: "Custom types" }).click();
}

export interface CreateHttpCustomTypeOptions {
  name: string;
  slug: string;
  /** Non-secret field name (kept out of the header/query auth binding). */
  field: string;
  /** Secret field name, referenced by `authValueTemplate`. */
  secretField: string;
  baseUrl: string;
  authHeaderName: string;
  /** e.g. `Bearer {{token}}` — `authValueTemplate` renders against `secretField`. */
  authValueTemplate: string;
}

/** Settings → Custom types → New type, execution mode HTTP request (the default). */
export async function createHttpCustomTypeViaUI(page: Page, opts: CreateHttpCustomTypeOptions): Promise<void> {
  await openCustomTypesSettings(page);
  await page.getByRole("button", { name: "New type" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();

  // Name and Identifier are the only two textboxes with neither an aria-label nor a
  // testid; they are always the first two textboxes in the dialog regardless of exec
  // mode or which tab is active, since they render before the ConfigTabs content.
  const textboxes = dialog.getByRole("textbox");
  await textboxes.nth(0).fill(opts.name);
  await textboxes.nth(1).fill(opts.slug);

  // Fields tab is active by default; one field row exists already.
  await dialog.locator('[aria-label="Field name"]').fill(opts.field);

  await dialog.getByTestId("config-tab-request").click();
  await fillTemplate(dialog.locator('[aria-label="Base URL"]'), opts.baseUrl);
  await dialog.getByRole("button", { name: "Add authentication" }).click();
  await dialog.getByPlaceholder("Name", { exact: true }).fill(opts.authHeaderName);
  await fillTemplate(dialog.getByPlaceholder("Value template", { exact: true }), opts.authValueTemplate);

  // A second field row for the secret referenced by the auth template.
  await dialog.getByTestId("config-tab-fields").click();
  await dialog.getByRole("button", { name: "Add field" }).click();
  await dialog.locator('[aria-label="Field name"]').nth(1).fill(opts.secretField);
  await dialog.locator('[aria-label="Secret"]').nth(1).click();

  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
}

export interface CreateCommandCustomTypeOptions {
  name: string;
  slug: string;
  /** Non-secret field name; command template is left empty (spec "本地命令": 命令模板为空时
   *  走系统默认 shell — the exec args become the whole shell command). */
  field: string;
}

/** Settings → Custom types → New type, execution mode Local command, empty template. */
export async function createCommandCustomTypeViaUI(page: Page, opts: CreateCommandCustomTypeOptions): Promise<void> {
  await openCustomTypesSettings(page);
  await page.getByRole("button", { name: "New type" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();

  const textboxes = dialog.getByRole("textbox");
  await textboxes.nth(0).fill(opts.name);
  await textboxes.nth(1).fill(opts.slug);
  await dialog.getByRole("radio", { name: "Local command" }).click();
  await dialog.locator('[aria-label="Field name"]').fill(opts.field);

  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toBeHidden();
}

export interface CreateGenericAssetOptions {
  name: string;
  typeSlug: string;
  values?: Record<string, string>;
  secrets?: Record<string, string>;
}

/** Add Asset → the type picker's "Custom" group → fill fields → save. */
export async function createGenericAssetViaUI(page: Page, opts: CreateGenericAssetOptions): Promise<void> {
  await page.getByTestId("add-asset-button").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeVisible();
  await page.getByTestId("asset-type-picker").click();
  await page.getByTestId(`asset-type-option-generic:${opts.typeSlug}`).click();
  await page.getByTestId("asset-form-name-input").fill(opts.name);

  for (const [field, value] of Object.entries(opts.values ?? {})) {
    await page.getByTestId(`generic-field-${field}`).fill(value);
  }
  for (const [field, value] of Object.entries(opts.secrets ?? {})) {
    await page.locator(`[data-testid="generic-secret-${field}"] input`).fill(value);
  }

  await page.getByTestId("asset-form-submit").click();
  await expect(page.getByTestId("asset-form-dialog")).toBeHidden();
  await expect(page.getByTestId("asset-tree").getByText(opts.name, { exact: true })).toBeVisible();
  await expect.poll(() => findAssetByName(opts.name)?.type, { timeout: 10_000 }).toBe("generic");
}
