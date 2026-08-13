// Drive the running verification sandbox, one command at a time.
//
// Each invocation attaches to the sandbox's Chromium over CDP, does one thing, and
// exits — the browser, the page and all its state stay alive in between. That is the
// point: verifying a feature is a conversation with a live app, not a spec file you
// write, run once, and delete.
//
//   make dev-sandbox                              # once, in another terminal
//   node e2e/drive.mjs snapshot                   # what's on screen right now
//   node e2e/drive.mjs click add-asset-button
//   node e2e/drive.mjs fill asset-form-name-input my-server
//   node e2e/drive.mjs click asset-form-submit
//   node e2e/drive.mjs shot after-create          # → e2e/scratch/<scenario>/
//   node e2e/oracle.mjs assets my-server          # …and confirm it really persisted
//
// Selectors: a bare word is this repo's `data-testid` convention, so
// `click add-asset-button` means `getByTestId("add-asset-button")`. Otherwise use an
// explicit kind — `text=Save`, `role=button[name="Save"]`, `label=名称`,
// `placeholder=…`, `css=.tree > li:first-child`. Prefer testids: visible text is
// i18n'd and moves.
//
// Every call appends a line to the scenario's `drive.log`, so the sequence that
// produced a screenshot is recorded as you go rather than reconstructed afterwards.
//
// Usage / workflow: docs/VERIFICATION.md
import { appendFileSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import {
  IsolationError,
  assertSandboxUrl,
  ports,
  repoRoot,
  sessionFile,
  spawnBrowserHost,
  waitForCdp,
} from "./harness/env.js";

const PORTS = ports();

const [command, ...rest] = process.argv.slice(2);
const args = rest.filter((a) => !a.startsWith("--"));
const flag = (name) => rest.includes(`--${name}`);
const option = (name, fallback) => {
  const hit = rest.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
};

const USAGE = `Usage: node e2e/drive.mjs <command> [args] [--flags]

  Look
    snapshot [sel]        visible structure: testids, roles, text, state
    testids               every data-testid currently on the page
    text <sel>            innerText of the first match
    html <sel>            outerHTML of the first match
    url                   current page URL and title
    console               console messages and page errors since the last load

  Act
    open [url]            navigate (default: the sandbox app; non-sandbox URLs refused)
    reload
    click <sel>
    dblclick <sel>        e.g. an asset row, to open its tab
    rightclick <sel>      e.g. an asset row, to open its context menu
    fill <sel> <value>
    press <key> [sel]     e.g. Enter, Escape, Control+A
    check <sel> / uncheck <sel>
    select <sel> <value>
    hover <sel>
    wait <sel>            block until it appears (--hidden waits for it to go)
    eval <js>             escape hatch, evaluated in the page

  Record
    shot [name]           screenshot → e2e/scratch/<scenario>/<name>.png
                          (every command is also appended to that dir's drive.log)

  Selectors
    add-asset-button      a bare word is a data-testid — the convention here
    text=Save             also: role=button[name="Save"], label=, placeholder=,
    css=.tree > li        title=, testid=, and css= for a raw CSS selector

  Flags
    --scenario=NAME       evidence subdirectory (default: $OPSKAT_SCENARIO or "session")
    --timeout=MS          per-command timeout (default 15000)
    --hidden              for \`wait\`: wait for the selector to disappear
    --all                 for \`snapshot\`: don't collapse repeated siblings
`;

if (!command || process.argv.includes("--help")) {
  process.stdout.write(USAGE);
  process.exit(command ? 0 : 1);
}

const timeout = Number(option("timeout", 15_000));
const scenario = option("scenario", process.env.OPSKAT_SCENARIO || "session");

const { page, appUrl } = await attach();

try {
  await run();
  record("ok");
} catch (error) {
  // A failed command is evidence too — record it and report what actually happened
  // rather than a stack trace, and never swallow it into a zero exit.
  const reason = error.message.split("\n")[0];
  record(error instanceof IsolationError ? `REFUSED ${reason}` : `FAILED ${reason}`);
  process.stderr.write(`✗ ${command}: ${reason}\n`);
  process.exit(1);
}
process.exit(0);

// One line per invocation, appended as it happens. Reconstructing "what did I actually
// click" from shell history after the fact is how a report ends up describing a run
// that never happened.
function record(outcome) {
  try {
    const dir = scenarioDir();
    mkdirSync(dir, { recursive: true });
    const shown = [command, ...args.map((a) => (/\s/.test(a) ? JSON.stringify(a) : a))].join(" ");
    appendFileSync(join(dir, "drive.log"), `${new Date().toISOString()}  ${shown}  → ${outcome}\n`);
  } catch {
    // evidence logging must never be the thing that fails a verification step
  }
}

function scenarioDir() {
  return join(repoRoot, "e2e", "scratch", scenario);
}

async function run() {
  switch (command) {
    case "open": {
      // Guarded, not trusted: `open http://localhost:34115` would be your real app.
      const target = assertSandboxUrl(args[0] ?? appUrl);
      await page.goto(target, { timeout, waitUntil: "domcontentloaded" });
      await installCollector();
      return out(`opened ${page.url()}`);
    }
    case "reload":
      await page.reload({ timeout, waitUntil: "domcontentloaded" });
      await installCollector();
      return out(`reloaded ${page.url()}`);

    case "url":
      return out(`${page.url()}\ntitle: ${await page.title()}`);

    case "snapshot":
      return out(await snapshot(args[0]));

    case "testids": {
      const ids = await page.evaluate(() =>
        [...document.querySelectorAll("[data-testid]")]
          .filter((el) => el.getClientRects().length > 0)
          .map((el) => el.getAttribute("data-testid")),
      );
      return out(ids.length ? [...new Set(ids)].sort().join("\n") : "(no visible data-testid on the page)");
    }

    case "text":
      return out(await locate(need(0, "text <sel>")).innerText({ timeout }));

    case "html":
      return out(await locate(need(0, "html <sel>")).evaluate((el) => el.outerHTML, { timeout }));

    case "click":
      await locate(need(0, "click <sel>")).click({ timeout });
      return out(`clicked ${args[0]}`);

    case "dblclick":
      await locate(need(0, "dblclick <sel>")).dblclick({ timeout });
      return out(`double-clicked ${args[0]}`);

    case "rightclick":
      await locate(need(0, "rightclick <sel>")).click({ button: "right", timeout });
      return out(`right-clicked ${args[0]}`);

    case "hover":
      await locate(need(0, "hover <sel>")).hover({ timeout });
      return out(`hovered ${args[0]}`);

    case "fill":
      await locate(need(0, "fill <sel> <value>")).fill(need(1, "fill <sel> <value>"), { timeout });
      return out(`filled ${args[0]}`);

    case "select":
      await locate(need(0, "select <sel> <value>")).selectOption(need(1, "select <sel> <value>"), { timeout });
      return out(`selected ${args[1]} in ${args[0]}`);

    case "check":
      await locate(need(0, "check <sel>")).check({ timeout });
      return out(`checked ${args[0]}`);

    case "uncheck":
      await locate(need(0, "uncheck <sel>")).uncheck({ timeout });
      return out(`unchecked ${args[0]}`);

    case "press": {
      const key = need(0, "press <key> [sel]");
      if (args[1]) await locate(args[1]).press(key, { timeout });
      else await page.keyboard.press(key);
      return out(`pressed ${key}`);
    }

    case "wait": {
      const target = need(0, "wait <sel>");
      await locate(target).waitFor({ state: flag("hidden") ? "hidden" : "visible", timeout });
      return out(`${target} is ${flag("hidden") ? "hidden" : "visible"}`);
    }

    case "eval":
      return out(format(await page.evaluate(need(0, "eval <js>"))));

    case "console": {
      const entries = await page.evaluate(() => globalThis.__drive?.log ?? []);
      return out(entries.length ? entries.join("\n") : "(nothing logged since the last load)");
    }

    case "shot": {
      const dir = scenarioDir();
      mkdirSync(dir, { recursive: true });
      const file = join(dir, `${args[0] ?? "shot"}.png`);
      await page.screenshot({ path: file, fullPage: flag("full") });
      return out(file);
    }

    default:
      process.stderr.write(`unknown command "${command}"\n\n${USAGE}`);
      process.exit(1);
  }
}

// A bare word is a data-testid (§5 of the harness guide makes those the locator
// convention); otherwise the kind is explicit. A bare `button` meaning "the testid
// `button`" rather than "every <button>" is exactly why raw CSS needs `css=`.
function locate(selector) {
  const eq = selector.indexOf("=");
  const kind = eq > 0 ? selector.slice(0, eq) : "";
  const value = eq > 0 ? selector.slice(eq + 1) : selector;
  switch (kind) {
    case "testid":
      return page.getByTestId(value);
    case "text":
      return page.getByText(value);
    case "label":
      return page.getByLabel(value);
    case "placeholder":
      return page.getByPlaceholder(value);
    case "title":
      return page.getByTitle(value);
    case "css":
      return page.locator(value);
    case "role": {
      const match = /^([a-z]+)(?:\[name=(?:"([^"]*)"|'([^']*)')\])?$/.exec(value);
      if (!match) throw new Error(`bad role locator: ${selector} (expected role=button[name="Save"])`);
      const name = match[2] ?? match[3];
      return page.getByRole(match[1], name === undefined ? {} : { name });
    }
    default:
      if (/^[A-Za-z][\w-]*$/.test(selector)) return page.getByTestId(selector);
      throw new Error(
        `unrecognised selector "${selector}" — use a bare data-testid, or ` +
          "testid= / text= / role= / label= / placeholder= / title= / css=",
      );
  }
}

function need(index, form) {
  if (args[index] === undefined) throw new Error(`missing argument — ${form}`);
  return args[index];
}

// A compact, readable view of what is actually on screen: visible elements that a
// person could interact with or read, with the testid to address them by. This is
// what removes the guesswork that makes blind selector-driving unreliable.
async function snapshot(root) {
  const selector = root ? locate(root) : page.locator("body");
  await selector.first().waitFor({ state: "visible", timeout }).catch(() => {});
  const lines = await selector.first().evaluate((rootEl, collapse) => {
    const INTERACTIVE = new Set(["A", "BUTTON", "INPUT", "SELECT", "TEXTAREA", "SUMMARY"]);
    const out = [];
    // Two different questions. `hidden` prunes a whole subtree; `visible` decides
    // whether to print one element. They must stay separate: a portal wrapper (every
    // Radix dialog has one) has no layout box of its own while everything inside it is
    // on screen, so pruning on "no client rects" would hide every modal — the one
    // thing you most need to see.
    const hidden = (el) => {
      const style = getComputedStyle(el);
      return style.display === "none" || style.visibility === "hidden" || el.getAttribute("aria-hidden") === "true";
    };
    const visible = (el) => el.getClientRects().length > 0 && getComputedStyle(el).opacity !== "0";
    const label = (el) => {
      const own = [...el.childNodes]
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent.trim())
        .join(" ")
        .replace(/\s+/g, " ")
        .trim();
      return own || el.getAttribute("aria-label") || el.getAttribute("placeholder") || "";
    };
    const walk = (el, depth) => {
      if (depth > 24 || out.length > 400) return;
      const testid = el.getAttribute("data-testid");
      const role = el.getAttribute("role");
      const interesting =
        testid || role || INTERACTIVE.has(el.tagName) || (label(el) && el.children.length === 0);
      if (interesting && visible(el)) {
        const parts = [el.tagName.toLowerCase()];
        if (testid) parts.push(`#${testid}`);
        if (role) parts.push(`role=${role}`);
        if (el.disabled) parts.push("disabled");
        if (el.checked) parts.push("checked");
        if (el.value) parts.push(`value=${JSON.stringify(String(el.value).slice(0, 60))}`);
        const text = label(el).slice(0, 80);
        if (text) parts.push(JSON.stringify(text));
        out.push("  ".repeat(Math.min(depth, 12)) + parts.join(" "));
      }
      const nextDepth = interesting ? depth + 1 : depth;
      let repeats = 0;
      let previous = null;
      for (const child of el.children) {
        if (hidden(child)) continue;
        const shape = child.tagName + (child.getAttribute("data-testid") ? "" : child.className);
        if (collapse && shape === previous && ++repeats > 4) continue;
        if (shape !== previous) repeats = 0;
        previous = shape;
        walk(child, nextDepth);
      }
    };
    if (!hidden(rootEl)) walk(rootEl, 0);
    return out;
  }, !flagAll());
  return lines.length ? lines.join("\n") : "(nothing visible)";
}

function flagAll() {
  return rest.includes("--all");
}

// Console output and uncaught errors are buffered in the page itself rather than by a
// listener in this process: every command is a separate short-lived process, so a
// listener here would only ever see its own few milliseconds.
async function installCollector() {
  await page.evaluate(() => {
    if (globalThis.__drive) return;
    const log = [];
    globalThis.__drive = { log };
    for (const level of ["log", "info", "warn", "error"]) {
      const original = console[level].bind(console);
      console[level] = (...a) => {
        log.push(`${level}: ${a.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(" ")}`);
        original(...a);
      };
    }
    addEventListener("error", (e) => log.push(`pageerror: ${e.message}`));
    addEventListener("unhandledrejection", (e) => log.push(`unhandledrejection: ${e.reason}`));
  });
}

// Attaches to the sandbox's browser. If none is running, starts a detached Chromium —
// so `drive.mjs` still works against an app you launched some other way — but the app
// URL then has to come from the session file or --url.
async function attach() {
  const { chromium } = await import("@playwright/test");
  const session = readSession();
  const endpoint = session?.cdpEndpoint ?? `http://127.0.0.1:${PORTS.sandboxCdp}`;
  const url = assertSandboxUrl(option("url", session?.url ?? `http://localhost:${PORTS.sandboxApp}`));

  if (!(await cdpAlive(endpoint))) await launchDetachedBrowser(url);

  const browser = await chromium.connectOverCDP(endpoint, { timeout: 30_000 }).catch(() => {
    throw new Error(
      `no browser at ${endpoint}. Start the sandbox first: make dev-sandbox`,
    );
  });
  const context = browser.contexts()[0] ?? (await browser.newContext());
  const pages = context.pages();
  const target = pages.find((p) => p.url().startsWith(url)) ?? pages[0] ?? (await context.newPage());
  if (target.url() === "about:blank") await target.goto(url).catch(() => {});
  return { page: target, appUrl: url };
}

function readSession() {
  const file = sessionFile();
  if (!existsSync(file)) return null;
  try {
    return JSON.parse(readFileSync(file, "utf8"));
  } catch {
    return null;
  }
}

async function cdpAlive(endpoint) {
  try {
    const response = await fetch(`${endpoint}/json/version`, { signal: AbortSignal.timeout(2000) });
    return response.ok;
  } catch {
    return false;
  }
}

async function launchDetachedBrowser(url) {
  spawnBrowserHost({
    cdpPort: PORTS.sandboxCdp,
    profile: join(repoRoot, "e2e", ".drive-profile"),
    url,
    headed: process.argv.includes("--headed"),
  });
  if (!(await waitForCdp(PORTS.sandboxCdp))) {
    throw new Error("launched Chromium but its DevTools endpoint never came up");
  }
}

function format(value) {
  return typeof value === "string" ? value : JSON.stringify(value, null, 2);
}

function out(text) {
  process.stdout.write(`${text}\n`);
}
