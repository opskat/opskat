// Holds the sandbox's browser open. Started detached by `sandbox.mjs up`, it launches
// Chromium through Playwright and then does nothing but stay alive.
//
// Why a host process instead of spawning the Chromium binary directly: a bare
// `spawn(chromium.executablePath(), […])` headless browser never produces frames.
// `requestAnimationFrame` never fires in it, so every Playwright action that waits for
// an element to be *stable* — click, hover, screenshot — hangs until timeout, while
// `evaluate` still works. That failure reads as "the button is right there and
// enabled, but click times out", which is a miserable thing to debug. Playwright's own
// launcher sets the browser up so frames flow; reproducing that by hand-copying flags
// did not work. So Playwright launches it, and this process exists only to be the
// owner Playwright needs.
//
// It is deliberately dumb: the page is driven over CDP by `drive.mjs`, not from here.
// Killing this process (`sandbox.mjs down`) takes the browser with it.
import { chromium } from "@playwright/test";

const [cdpPort, profile, url, headed] = process.argv.slice(2);

const context = await chromium.launchPersistentContext(profile, {
  headless: headed !== "headed",
  viewport: { width: 1600, height: 1000 },
  args: [`--remote-debugging-port=${cdpPort}`],
});

const page = context.pages()[0] ?? (await context.newPage());
await page.goto(url).catch(() => {
  // The app may still be settling; drive.mjs navigates again when it attaches.
});

// If the browser dies (crash, or someone closed it), don't linger as a host with
// nothing to host — `sandbox.mjs status` should show this pid gone, not running.
context.on("close", () => process.exit(0));

setInterval(() => {}, 1 << 30);
