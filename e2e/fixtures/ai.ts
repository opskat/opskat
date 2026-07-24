import { expect, type Page } from "@playwright/test";
import { DatabaseSync } from "node:sqlite";
import { dbPath, findAIProviderByName } from "./db";

// Shared plumbing for the AI exec specs: the scripted model (fixtures/openai-mock.mjs),
// the AI provider row that points the real app at it, direct asset seeding, and the
// two UI gestures every AI spec starts with (open a fresh chat, send a message).

export const MOCK_BASE = `http://127.0.0.1:${process.env.OPENAI_MOCK_PORT ?? "34219"}`;
export const AI_PROVIDER_NAME = "e2e-mock-openai";
// Not a real key — but a *non-empty* one on purpose: it forces the app to encrypt
// it on write (credential_svc) and decrypt it on activate, so the specs run through
// the same encrypted-at-rest path a user's key does.
export const AI_PROVIDER_KEY = "e2e-fake-key-0123456789";

/** One scripted model turn. `tool`/`tools` produce tool calls, `text` a plain reply. */
export interface ModelStep {
  text?: string;
  tool?: { name: string; args: Record<string, unknown> };
  tools?: Array<{ name: string; args: Record<string, unknown> }>;
}

/** Installs the model script for the next AI turn(s) and clears recorded requests. */
export async function scriptModel(steps: ModelStep[], exhaustedText?: string): Promise<void> {
  const res = await fetch(`${MOCK_BASE}/__mock/script`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ steps, exhaustedText }),
  });
  if (!res.ok) throw new Error(`openai-mock script failed: ${res.status} ${await res.text()}`);
}

export interface MockRequest {
  model?: string;
  messages?: Array<{ role: string; content?: string; tool_call_id?: string; name?: string }>;
  tools?: Array<{ function?: { name?: string } }>;
}

/** The raw chat-completions bodies the app sent — i.e. what the model was shown. */
export async function mockRequests(): Promise<MockRequest[]> {
  const res = await fetch(`${MOCK_BASE}/__mock/requests`);
  if (!res.ok) throw new Error(`openai-mock requests failed: ${res.status}`);
  return ((await res.json()) as { requests: MockRequest[] }).requests;
}

/** Concatenated `tool` role message contents across every recorded request — the
 *  tool results the model actually received. */
export async function toolResultsSeenByModel(): Promise<string[]> {
  const reqs = await mockRequests();
  const out: string[] = [];
  for (const r of reqs) {
    for (const m of r.messages ?? []) {
      if (m.role === "tool" && typeof m.content === "string") out.push(m.content);
    }
  }
  return out;
}

/**
 * Creates (once per run) an AI provider pointing at the mock and activates it,
 * through the *real* Wails bindings — so the API key goes through the app's
 * encryption on write and decryption on activate. Reloads the page afterwards so
 * the store re-runs checkConfigured() and the chat UI leaves the setup wizard.
 */
export async function ensureAIProvider(page: Page): Promise<void> {
  const existing = findAIProviderByName(AI_PROVIDER_NAME);
  const id =
    existing?.id ??
    (await page.evaluate(
      async ([name, apiBase, apiKey]) => {
        const ai = (window as unknown as { go: { ai: { AI: Record<string, (...a: unknown[]) => Promise<unknown>> } } })
          .go.ai.AI;
        const created = (await ai.CreateAIProvider(name, "openai", apiBase, apiKey, "mock-model", 4096, 32000, false, "")) as {
          id: number;
        };
        return created.id;
      },
      [AI_PROVIDER_NAME, `${MOCK_BASE}/v1`, AI_PROVIDER_KEY]
    ));

  await page.evaluate(async (providerID) => {
    const ai = (window as unknown as { go: { ai: { AI: Record<string, (...a: unknown[]) => Promise<unknown>> } } }).go.ai
      .AI;
    await ai.SetActiveAIProvider(providerID);
  }, id);

  await page.reload();
}

export interface SeedAssetOptions {
  name: string;
  type: string;
  config: Record<string, unknown>;
  /** JSON for the `command_policy` column (policy.CommandPolicy / RedisPolicy / …). */
  commandPolicy?: Record<string, unknown>;
}

/**
 * Inserts an asset row straight into the temp DB (the writable counterpart of the
 * db.ts oracle). The app re-reads assets on each fetch, so no restart is needed.
 * Used for asset types whose credential is empty — the mock SSH server is
 * NoClientAuth and the mock Redis needs no password, so nothing needs encrypting.
 */
export function seedAsset(opts: SeedAssetOptions): number {
  const db = new DatabaseSync(dbPath());
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    const now = Math.floor(Date.now() / 1000);
    db.prepare(
      "INSERT INTO assets (name, type, group_id, config, command_policy, status, ssh_tunnel_id, extension_name, createtime, updatetime)" +
        " VALUES (?,?,?,?,?,?,?,?,?,?)"
    ).run(
      opts.name,
      opts.type,
      0,
      JSON.stringify(opts.config),
      opts.commandPolicy ? JSON.stringify(opts.commandPolicy) : "",
      1,
      0,
      "",
      now,
      now
    );
    const row = db.prepare("SELECT id FROM assets WHERE name = ? ORDER BY id DESC LIMIT 1").get(opts.name) as {
      id: number;
    };
    return row.id;
  } finally {
    db.close();
  }
}

/** An SSH asset pointing at the in-harness mock sshd (NoClientAuth → no credential). */
export function seedSSHAsset(name: string, commandPolicy?: Record<string, unknown>): number {
  return seedAsset({
    name,
    type: "ssh",
    config: {
      host: "127.0.0.1",
      port: Number(process.env.SSH_MOCK_PORT ?? 34218),
      username: "e2e",
      auth_type: "password",
    },
    commandPolicy,
  });
}

/** A Redis asset pointing at the in-harness mock Redis (no password). */
export function seedRedisAsset(name: string, commandPolicy?: Record<string, unknown>): number {
  return seedAsset({
    name,
    type: "redis",
    config: { host: "127.0.0.1", port: Number(process.env.MOCK_REDIS_PORT ?? 34217), database: 0 },
    commandPolicy,
  });
}

/**
 * An etcd asset. There is no etcd mock — and none is needed for the specs that use
 * it: etcd is the type that registers a CanonicalizeFunc, and canonicalization
 * (plus the approval + audit that hang off it) all happen *before* anything dials
 * the endpoint. The dial itself then fails, which is exactly what the
 * "unexecutable commands must not reach the approval dialog" ordering specs want.
 */
export function seedEtcdAsset(name: string, commandPolicy?: Record<string, unknown>): number {
  return seedAsset({
    name,
    type: "etcd",
    // Port 1 is reserved/unused — the dial fails fast instead of hanging.
    config: { endpoints: ["127.0.0.1:1"], dial_timeout_seconds: 1, command_timeout_seconds: 1 },
    commandPolicy,
  });
}

/**
 * Navigates to the app and waits until it has actually mounted.
 *
 * Playwright's webServer readiness only proves *something* answers on the dev
 * port; `wails dev` binds it around the time it (re)launches the app binary, so
 * on a loaded machine the first navigations can land on a not-yet-mounted app or
 * even hit ECONNREFUSED during the app's initial restart. These specs sort first
 * in the suite, so they are the ones that meet that window. Retrying the whole
 * navigate+mount step is the only reliable way through it — a longer single
 * timeout does not help, because a refused connection fails immediately.
 */
export async function openApp(page: Page): Promise<void> {
  const deadline = Date.now() + 180_000;
  for (let attempt = 1; ; attempt++) {
    try {
      await page.goto("/", { timeout: 30_000 });
      await expect(page.getByTestId("app-root")).toBeVisible({ timeout: 30_000 });
      return;
    } catch (err) {
      if (Date.now() > deadline) throw err;
      await page.waitForTimeout(2_000); // back off before re-navigating, not a fixed wait for a condition
    }
  }
}

/** Opens a brand-new sidebar chat (⇒ a fresh conversation, i.e. a fresh doc gate). */
export async function openNewChat(page: Page): Promise<void> {
  await page.getByTestId("ai-new-chat").click();
  await page.getByTestId("ai-chat-input").waitFor();
}

/**
 * Races the two possible outcomes of an exec: an approval dialog shows up, or the
 * call settles (`settled()` turns true — typically "its audit row landed").
 *
 * This is how the ordering specs assert "the dialog never appeared" *without* a
 * sleep and without a false green: an approval dialog blocks the tool call
 * forever, so simply waiting for the audit row would time out on the *waiting*
 * assertion rather than on the one the spec is about. Returning which one won
 * lets the spec assert `toBe("settled")`, which fails loudly with
 * `Received: "approval"` the moment the permission check moves ahead of a
 * no-side-effect check.
 */
export async function execOutcome(
  page: Page,
  settled: () => boolean,
  opts: { timeout?: number; exceptConfirmId?: string } = {}
): Promise<"approval" | "settled"> {
  const prompt = pendingApproval(page, opts.exceptConfirmId);
  let seen: "approval" | "settled" | "pending" = "pending";
  await expect
    .poll(
      async () => {
        if (await prompt.isVisible()) return (seen = "approval");
        if (settled()) return (seen = "settled");
        return "pending";
      },
      { timeout: opts.timeout ?? 60_000 }
    )
    .not.toBe("pending");
  return seen as "approval" | "settled";
}

/**
 * An approval prompt, optionally excluding one already answered.
 *
 * Answering a prompt and the *next* prompt appearing are two independent
 * events, and the second can land before Playwright observes the first one
 * disappear — so "the same block is gone" is a racy way to detect "a new one
 * showed up". Each prompt carries its own `confirmId`, so selecting by
 * "any prompt that is not that one" is race-free.
 */
export function pendingApproval(page: Page, exceptConfirmId?: string) {
  const base = '[data-testid="ai-approval-block"]';
  return page.locator(exceptConfirmId ? `${base}:not([data-confirm-id="${exceptConfirmId}"])` : base).first();
}

/** The confirmId of the currently shown approval prompt. */
export async function approvalConfirmId(page: Page): Promise<string> {
  const id = await pendingApproval(page).getAttribute("data-confirm-id");
  if (!id) throw new Error("approval block has no data-confirm-id");
  return id;
}

/**
 * Types a user message into the AI composer and sends it.
 *
 * Keep `text` free of `/` and `@`: they are the composer's snippet / asset-mention
 * triggers, and the suggestion popover they open covers the send button.
 */
export async function sendChat(page: Page, text: string): Promise<void> {
  const input = page.getByTestId("ai-chat-input");
  const send = page.getByTestId("ai-send-button");
  await input.click();
  await input.fill(text);
  try {
    // The send button is disabled while the composer is empty, so "enabled"
    // is the signal that the TipTap editor really took the text.
    await expect(send).toBeEnabled({ timeout: 5_000 });
  } catch {
    // A fill that lands before the editor finished mounting is silently
    // dropped; one re-fill is enough (the editor exists by now).
    await input.click();
    await input.fill(text);
    await expect(send).toBeEnabled({ timeout: 15_000 });
  }
  // The composer advertises Enter as its primary send action. Use that keyboard
  // path so an unrelated bottom-right toast (for example an update notice) cannot
  // cover the button and turn an AI-flow assertion into a layout timeout.
  await input.press("Enter");
}
