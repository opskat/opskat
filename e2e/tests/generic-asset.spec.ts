import { test, expect } from "@playwright/test";

import { createCommandCustomTypeViaUI, createGenericAssetViaUI, createHttpCustomTypeViaUI } from "../fixtures/customTypes";
import { findCustomTypeBySlug, waitForAuditLogs } from "../fixtures/db";
import { startHttpMock } from "../fixtures/http-mock";
import { runOpsctl } from "../fixtures/opsctl";

// Whole-flow coverage for docs/specs/2026-09-28-generic-asset.md: define a custom
// type in the UI, create a generic asset from it, then drive it exactly the way an
// external caller does — opsctl exec (HTTP and local command) and opsctl secret get
// — against the real app, asserting policy (allow vs. approval), the real network/
// process side effect, and that the audit trail never carries a secret value.
//
// Not covered here (docs/specs/2026-09-28-generic-asset.md "无法自动化的部分"): a
// real signed HTTP API behind an SSH tunnel — that needs a human with a throwaway
// Grafana container, per the spec's own testing-decisions table.

test("HTTP custom type: GET is allowed and injects auth, DELETE needs approval, secret get needs approval, audit omits the secret", async ({
  page,
}) => {
  test.setTimeout(120_000);
  const secret = `probe-secret-${Date.now()}`;
  const mock = await startHttpMock();
  try {
    await page.goto("/");
    await expect(page.getByTestId("app-root")).toBeVisible();

    const slug = `e2e-http-${Date.now().toString(36)}`;
    const asset = `e2e-http-asset-${Date.now()}`;
    await createHttpCustomTypeViaUI(page, {
      name: `E2E HTTP ${Date.now()}`,
      slug,
      field: "region",
      secretField: "apiKey",
      baseUrl: mock.baseUrl,
      authHeaderName: "Authorization",
      authValueTemplate: "Bearer {{apiKey}}",
    });
    await createGenericAssetViaUI(page, {
      name: asset,
      typeSlug: slug,
      values: { region: "ap-east-1" },
      secrets: { apiKey: secret },
    });

    // GET matches the editor's prefilled HTTP default allow rule (GET / HEAD / OPTIONS,
    // design decision 9), copied onto the asset: no approval dialog, and the rendered
    // Authorization header actually reaches the wire.
    const getResult = await runOpsctl([
      "--data-dir",
      process.env.OPSKAT_DATA_DIR!,
      "exec",
      asset,
      "--type",
      slug,
      "--",
      "GET",
      "/items",
    ]);
    expect(getResult, getResult.stderr).toMatchObject({ code: 0 });
    expect(mock.requests).toHaveLength(1);
    expect(mock.requests[0]).toMatchObject({ method: "GET", path: "/items" });
    expect(mock.requests[0].headers.authorization).toBe(`Bearer ${secret}`);

    // DELETE is not covered by the allow rule: the desktop approval dialog appears
    // (opsctl has no terminal to prompt in — it is spawned with stdio "ignore" —
    // so it falls back to the desktop dialog exactly like opsctl-approval.spec.ts).
    const deleteResultPromise = runOpsctl([
      "--data-dir",
      process.env.OPSKAT_DATA_DIR!,
      "exec",
      asset,
      "--type",
      slug,
      "--",
      "DELETE",
      "/items/1",
    ]);
    await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId("opsctl-approval-dialog")).not.toContainText(secret);
    await page.getByTestId("opsctl-approval-allow").click();
    const deleteResult = await deleteResultPromise;
    expect(deleteResult, deleteResult.stderr).toMatchObject({ code: 0 });
    expect(mock.requests).toHaveLength(2);
    expect(mock.requests[1]).toMatchObject({ method: "DELETE", path: "/items/1" });

    // Secret get: the non-secret field would return immediately with no approval
    // (docs/specs/2026-09-28-generic-asset.md "取值"), but a secret field always
    // needs one — the dialog names the field, not the value, and stdout is the
    // literal plaintext.
    const secretResultPromise = runOpsctl([
      "--data-dir",
      process.env.OPSKAT_DATA_DIR!,
      "secret",
      "get",
      asset,
      "apiKey",
    ]);
    await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId("opsctl-approval-dialog")).toContainText("secret:apiKey");
    await expect(page.getByTestId("opsctl-approval-dialog")).not.toContainText(secret);
    await page.getByTestId("opsctl-approval-allow").click();
    const secretResult = await secretResultPromise;
    expect(secretResult, secretResult.stderr).toMatchObject({ code: 0 });
    expect(secretResult.stdout.trim()).toBe(secret);

    // Audit: one row per call, none of them carrying the secret anywhere.
    const rows = await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 2);
    expect(rows.map((r) => r.command).sort()).toEqual(["DELETE /items/1", "GET /items"]);
    for (const row of rows) {
      expect(row.command + row.result + row.request).not.toContain(secret);
    }
    const secretRows = await waitForAuditLogs({ assetName: asset, toolName: "get_asset_secret" }, 1);
    expect(secretRows[0].result + secretRows[0].command + secretRows[0].request).not.toContain(secret);
  } finally {
    await mock.close();
  }
});

test("command custom type: a harmless command needs approval and runs through the system shell", async ({ page }) => {
  test.setTimeout(90_000);
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const slug = `e2e-cmd-${Date.now().toString(36)}`;
  const asset = `e2e-cmd-asset-${Date.now()}`;
  await createCommandCustomTypeViaUI(page, { name: `E2E Command ${Date.now()}`, slug, field: "note" });
  await createGenericAssetViaUI(page, { name: asset, typeSlug: slug });

  // The command mode never gets a default allow rule (the editor prefills none when the
  // execution mode switches to command, matching custom_type_entity.DefaultPolicyFor), so
  // every command needs approval — including this harmless, cross-platform one
  // ("echo" exists as a shell builtin on both POSIX shells and cmd.exe).
  const resultPromise = runOpsctl([
    "--data-dir",
    process.env.OPSKAT_DATA_DIR!,
    "exec",
    asset,
    "--type",
    slug,
    "--",
    "echo",
    "generic-cmd-ok",
  ]);
  await expect(page.getByTestId("opsctl-approval-dialog")).toBeVisible({ timeout: 60_000 });
  await page.getByTestId("opsctl-approval-allow").click();
  const result = await resultPromise;
  expect(result, result.stderr).toMatchObject({ code: 0 });
  expect(result.stdout.trim()).toBe("generic-cmd-ok");

  const rows = await waitForAuditLogs({ assetName: asset, toolName: "exec" }, 1);
  expect(rows[0]).toMatchObject({ command: "echo generic-cmd-ok", decision: "allow" });
});

test("export and import round-trip a custom type through the binder the settings UI already uses", async ({ page }) => {
  // ExportCustomType / SelectImportTypeFile (internal/app/customtype/export_import.go)
  // go straight to Wails' native OS save/open dialogs — there is no web-page element
  // for Playwright/CDP to drive, and headlessly invoking them would hang waiting for
  // an OS picker that never appears. custom_type_svc.ExportType / ParseImportFile are
  // the pure functions behind those dialogs, but they take a file path, not a
  // Wails-reachable argument, so the boundary this spec *can* drive end-to-end is one
  // layer up: the same GetCustomType / SaveCustomType binder calls the settings UI
  // itself makes (CustomTypeSection "Edit" reads via GetCustomType;
  // ImportCustomTypeDialog's confirm button writes via SaveCustomType, both already
  // used by e2e/fixtures/ai.ts's ensureAIProvider for the AI-provider binder). A
  // GetCustomType response *is* what a real export file's JSON is built from
  // (custom_type_svc.ExportType(ct) marshals exactly this struct plus a format
  // version) — round-tripping it through SaveCustomType under a new slug exercises
  // the same "identifier already exists → must rename, never overwrite" contract
  // (design decision 16) that ImportCustomTypeDialog enforces in the UI.
  await page.goto("/");
  await expect(page.getByTestId("app-root")).toBeVisible();

  const slug = `e2e-export-${Date.now().toString(36)}`;
  await createHttpCustomTypeViaUI(page, {
    name: `E2E Export ${Date.now()}`,
    slug,
    field: "region",
    secretField: "apiKey",
    baseUrl: "http://127.0.0.1:1",
    authHeaderName: "X-Api-Key",
    authValueTemplate: "{{apiKey}}",
  });

  const exported = await page.evaluate(async (typeSlug) => {
    const api = (
      window as unknown as { go: { customtype: { CustomType: Record<string, (...a: unknown[]) => Promise<unknown>> } } }
    ).go.customtype.CustomType;
    const list = (await api.ListCustomTypes()) as Array<{ id: number; slug: string }>;
    const summary = list.find((t) => t.slug === typeSlug);
    if (!summary) throw new Error(`custom type ${typeSlug} not found in ListCustomTypes`);
    return api.GetCustomType(summary.id);
  }, slug);

  // The exported shape carries the field/binding structure but no value: a secret
  // field is rejected from ever having a default at save time (spec "自定义类型"
  // validation: 密钥字段没有默认值), which is why a type export contains no values
  // at all — there is nothing per-field to strip.
  const exportedFields = exported as { fields: Array<{ name: string; secret: boolean; default?: string }> };
  const secretField = exportedFields.fields.find((f) => f.name === "apiKey");
  expect(secretField?.secret).toBe(true);
  expect(secretField?.default ?? "").toBe("");

  // Re-"import" it: SaveCustomType with id reset to 0 under a new slug — exactly
  // what ImportCustomTypeDialog's confirm button does with the file's parsed type
  // (custom_type_entity.CustomType(...ct, id: 0, slug, name)).
  const importedSlug = `${slug}-copy`;
  const saveResult = await page.evaluate(
    async ({ ct, importedSlug: newSlug }) => {
      const api = (
        window as unknown as { go: { customtype: { CustomType: Record<string, (...a: unknown[]) => Promise<unknown>> } } }
      ).go.customtype.CustomType;
      return api.SaveCustomType({ ...(ct as Record<string, unknown>), id: 0, slug: newSlug });
    },
    { ct: exported, importedSlug }
  );

  const saved = saveResult as { issues?: unknown[]; type?: { slug: string; fields: unknown } };
  expect(saved.issues ?? []).toEqual([]);
  expect(saved.type?.slug).toBe(importedSlug);
  expect(saved.type?.fields).toEqual(exportedFields.fields);

  // Independent oracle: the new row is really on disk, not just echoed back by the
  // same call that wrote it.
  await expect.poll(() => findCustomTypeBySlug(importedSlug)?.slug, { timeout: 10_000 }).toBe(importedSlug);
  const original = findCustomTypeBySlug(slug);
  const copy = findCustomTypeBySlug(importedSlug);
  expect(copy?.fields).toBe(original?.fields);
  expect(copy?.exec_mode).toBe(original?.exec_mode);

  // Re-importing the same file again must not overwrite the original or the copy —
  // the identifier is taken, so it comes back as a validation issue (design decision
  // 16: "标识冲突时必须填新标识，不能覆盖已有类型").
  const rejected = await page.evaluate(
    async ({ ct, takenSlug }) => {
      const api = (
        window as unknown as { go: { customtype: { CustomType: Record<string, (...a: unknown[]) => Promise<unknown>> } } }
      ).go.customtype.CustomType;
      return api.SaveCustomType({ ...(ct as Record<string, unknown>), id: 0, slug: takenSlug });
    },
    { ct: exported, takenSlug: slug }
  );
  const rejectedResult = rejected as { issues?: Array<{ path: string }>; type?: unknown };
  expect(rejectedResult.type).toBeFalsy();
  expect(rejectedResult.issues?.some((i) => i.path === "slug")).toBe(true);
});
