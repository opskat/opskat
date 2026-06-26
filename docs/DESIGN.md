# OpsKat Design System

> **A reuse-oriented design reference.** It consolidates the visual language that lives in [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css) and the `@opskat/ui` component layer into one place you can copy from: **color tokens (full light/dark oklch values), the theming mechanism, the component palette, the desktop pane shell, domain surfaces, motion, state patterns, accessibility, and an end-to-end new-surface recipe.** Read this before building any new tab, pane, dialog, or block so it stays visually and behaviorally consistent with the rest of the app.

> **Stack in one line:** Wails v2 desktop · React 19 + shadcn/ui (Radix primitives, `new-york` style) + Tailwind CSS v4 + Zustand. Colors, fonts, and radius are defined as **oklch** tokens in the `:root` / `.dark` / `@theme inline` blocks of [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css). **There is no `tailwind.config.js`** (Tailwind v4); the Vite `@tailwindcss/vite` plugin compiles it; **class names have no prefix** (`bg-background`, not `tw-bg-background`). UI primitives live in the **`@opskat/ui` workspace package** ([`frontend/packages/ui/`](../frontend/packages/ui/)), not in app `src/`.

---

## 0. What this doc owns

| Owned here | Owned elsewhere |
| --- | --- |
| Color-token values (oklch), semantics, usage | The hard rules that mandate them (no hard-coded colors, hover via pseudo-classes, `cn()` / CVA / `lucide`, `notify` over `toast.success`) → [`DEVELOP.md`](./DEVELOP.md), [`AGENTS.md` → Reuse first](../AGENTS.md) |
| Theming mechanism, `dark:` usage | Commands, structure, coding style, testing, i18n, commit/PR → [`DEVELOP.md`](./DEVELOP.md) |
| Component palette, variants, shared composites, selection guidance | Process model, IPC, services, repositories, asset-type backend → [`ARCHITECTURE.md`](./ARCHITECTURE.md); end-to-end new asset type → [`adding-an-asset-type.md`](./adding-an-asset-type.md) |
| The desktop pane shell, domain surfaces (terminal / query / editor), **elevation (shadows)**, **layering (z-index)**, motion, state patterns, **accessibility**, surface recipe | — |

This doc restates the cross-cutting rules only where needed, then links back — it does not duplicate them. When editing it, follow [`DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md): token values, component names, and variant names track the current branch's `frontend/` code — **if you can't `git grep` it, don't claim it.**

---

## 1. Core Constraints (non-negotiable)

Every UI change must satisfy all of these. They are the bar for "friendly, consistent UI/UX" in this codebase.

- **Use tokens, not literal colors — one value, one place.** Never write an `oklch(...)`, a hex, or a palette class (`text-blue-500`) in a component. Always use a semantic token — `bg-background`, `text-foreground`, `border-border`, `text-primary`, `bg-primary`, `text-muted-foreground`, … (§3). All color values live in exactly one place — the token definitions in [`globals.css`](../frontend/src/styles/globals.css) — so the palette stays unified and a single edit re-skins everything. One semantic concept maps to **one** token: before adding a color, check §3 for an existing token and reuse it. Only add a new token when the concept is genuinely new — with both a `:root` and a `.dark` value — and document it in §3.
- **Both themes, always.** Light and dark are first-class. Because every color comes from a token that has a `:root` and a `.dark` value, using tokens makes a component theme-correct for free. Verify on real light *and* dark before considering anything done (§4).
- **This is a desktop app, not a responsive web page.** OpsKat runs in a fixed Wails window — there is **no mobile shell and no breakpoint system**. Design for a resizable multi-pane desktop layout (sidebar rail → tab area → main pane → side assistant), not for a phone. The window chrome is native-feeling: `html, body` are `overflow-hidden` and `user-select: none` by default — only inputs, textareas, `[contenteditable]`, and `.select-text` opt back into text selection (§7). Don't add viewport media queries to "support mobile."
- **No inline `style={{}}` for what Tailwind can express.** Compose utility classes via `cn()` (`clsx` + `tailwind-merge`, from [`@opskat/ui`](../frontend/packages/ui/src/lib/utils.ts)); build variants with `class-variance-authority` (CVA). Inline styles only for genuinely dynamic values (e.g. a computed pane `flex` ratio, an xterm-measured size).
- **Hover/focus are CSS, not state.** Express interactive visuals with pseudo-classes (`hover:bg-primary/90`, `focus-visible:ring-ring/45`). React state is for data/logic, not styling.
- **Reuse components before building new ones.** Default to the `@opskat/ui` primitives (§6) and the shared composites (`AssetSelect` / `GroupSelect` / `TreeSelect` / `ConfirmDialog` / `PasswordSourceField` / `IconPicker` …) before hand-rolling; icons come from `lucide-react` (plus the in-repo brand icons) only. When the same block appears in two or more places, extract one shared component instead of copy-pasting — keep one implementation per concept so a fix lands everywhere at once. (See [`AGENTS.md` → Reuse first](../AGENTS.md).)
- **No silent operations.** Every async flow surfaces loading / empty / error / success (and progress for long-running work — terminal connects, query runs, file transfers). The user must always know whether their action worked (§10).
- **Success toasts go through `notify`.** Use `notifyCopied` / `notifySuccess` (top-center) for success — never `toast.success` directly; errors/warnings stay on `toast.error` / `toast.warning` (bottom-right). The rationale (terminal / AI / query views refresh bottom-up, so a bottom toast occludes output) is baked into [`notify.ts`](../frontend/src/lib/notify.ts) (§6.4, #135).
- **Don't introduce new colors or fonts ad hoc.** New color → add a token in `globals.css` (with both light and dark values) and document it here. New font family → add a `--font-*` token; don't reference an unconfigured family.

---

## 2. Design Principles

The "why" behind the constraints — apply these when shaping a surface.

1. **Trust-first, clear hierarchy.** Let the most important information win the visual weight. Connection identity, asset name, and danger state read first; raw payloads (command output, query rows, file diffs) get their own scroll region rather than crowding the chrome.
2. **System state is always visible.** No silent work. Each async flow shows **trigger (disabled control + inline spinner) → process (streaming output / per-row status) → result (`notify` toast or an inline result)**. A terminal that is connecting, a query that is running, and a transfer in flight all say so.
3. **Color is semantic, never decorative.** `primary` (blue-violet) = interactive / active / selection; `success` = connected / enabled / safe; `warning` = caution / sensitive; `destructive` = danger / delete / error. Color carries meaning — and never carries it *alone* (§11).
4. **One consistent shell, swap the content.** Every workbench surface lives inside the same frame: window chrome → sidebar rail → tab area → main pane (→ optional side assistant). A terminal, a query editor, and a settings page are different *content* in the *same* shell, not different apps (§7).
5. **Panes over pages.** Because it's a desktop tool, the unit of work is a **tab** that hosts a resizable **pane tree** (split terminals, editor + result grid, diff + merge), not a scrolling document. Persist pane state so a layout survives tab switches and restarts (§7–§8).
6. **High cohesion, low coupling.** Each UI unit has a single purpose, a clear interface, and is understandable and testable on its own. Components depend on Zustand stores/hooks, not on sibling components' internals (one store per domain, [`frontend/src/stores/`](../frontend/src/stores/)). A file growing large is usually a signal to split it.

---

## 3. Color Tokens (full light / dark oklch values)

**Single source:** [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css). `:root` defines light, `.dark` overrides for dark, and `@theme inline` exposes every `--token` as a Tailwind color (`--color-*`), so `bg-<token>` / `text-<token>` / `border-<token>` all work **and switch with the theme automatically**.

**Why oklch.** The whole palette is authored in `oklch(L C H)` — perceptually-uniform lightness, so the light and dark ladders stay legible and the neutrals share a single cool hue (`H≈250`) with the brand at `H≈260`. Don't reintroduce hex/`rgb()` — keep new tokens in oklch so they sit on the same ladder.

**Usage:**
- Background `bg-<token>`, text `text-<token>`, border `border-<token>`, focus ring `ring-ring`.
- Opacity modifiers compose directly: `hover:bg-primary/90`, `bg-primary/15` (query-cell selection), `ring-ring/45`, `bg-input/30`.
- **Never hard-code a color value** — see Constraint 1. For a dark-only tweak use the `dark:` variant.

### 3.1 Base surfaces & text

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `background` | `oklch(0.96 0.01 250)` | `oklch(0.18 0.025 250)` | Window / page background |
| `foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Primary text |
| `card` | `oklch(0.99 0.003 250)` | `oklch(0.22 0.025 250)` | Card / panel surface (one step above `background`) |
| `card-foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Text on cards |
| `popover` | `oklch(0.99 0.003 250)` | `oklch(0.22 0.025 250)` | Floating layers (dropdown / tooltip / toast / select) surface |
| `popover-foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Text in floating layers |

### 3.2 Brand primary (blue-violet)

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `primary` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Brand fill **and** accent — solid button fill (`bg-primary text-primary-foreground`), active/selected state, links, indicators. Unlike some shadcn setups there is **no separate `primary-background`**; `bg-primary` *is* the solid control fill |
| `primary-foreground` | `oklch(0.985 0.003 260)` | `oklch(0.985 0.003 260)` | Text/icons on `primary` |

> Selection accents reuse `primary` at low opacity — e.g. query cells use `bg-primary/15` (selected) / `bg-primary/5` (focus), and the pane resize handle hovers to `bg-primary/50`. Keep selection emphasis as `primary/<opacity>` rather than inventing a new token.

### 3.3 Secondary / muted / accent

> Per the shadcn convention, `secondary` and `muted` share the **same value** (different semantics, one fill); `accent` is a touch darker for hover/selection.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `secondary` | `oklch(0.935 0.012 250)` | `oklch(0.26 0.025 250)` | Secondary buttons / fills |
| `secondary-foreground` | `oklch(0.22 0.02 250)` | `oklch(0.94 0.008 250)` | Text on secondary |
| `muted` | `oklch(0.935 0.012 250)` | `oklch(0.26 0.025 250)` | Muted background (group fills, sticky table headers, placeholders) |
| `muted-foreground` | `oklch(0.48 0.015 250)` | `oklch(0.62 0.015 250)` | De-emphasized / descriptive text — reserve for secondary/large text, not dense body copy (§11 contrast) |
| `accent` | `oklch(0.91 0.012 250)` | `oklch(0.30 0.025 250)` | Hover / selected background (menu items, list rows) |
| `accent-foreground` | `oklch(0.22 0.02 250)` | `oklch(0.94 0.008 250)` | Text on accent |

### 3.4 Borders, inputs, ring, dividers

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `border` | `oklch(0.87 0.012 250)` | `oklch(0.30 0.025 250)` | Global borders (the `@layer base` reset gives every element `border-border`) |
| `input` | `oklch(0.87 0.012 250)` | `oklch(0.30 0.025 250)` | Form control borders |
| `ring` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Focus ring (`focus-visible:ring-ring/45`) — equal to `primary` |
| `panel-divider` | `oklch(0 0 0 / 8%)` | `oklch(1 0 0 / 6%)` | Thin pane/section divider — a translucent line that reads on any surface (use `border-panel-divider` for split-pane gutters and inner separators) |

### 3.5 Status colors

Four semantic states. **Every status token has a light *and* dark value plus a `-foreground` pair**, so it reads correctly both as an icon/dot **and** as a text-bearing badge in either theme.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `destructive` | `oklch(0.55 0.24 27)` | `oklch(0.70 0.19 22)` | Dangerous / delete / error |
| `destructive-foreground` | `oklch(0.985 0 0)` | `oklch(0.985 0 0)` | Text on solid `destructive` |
| `success` | `oklch(0.55 0.19 155)` | `oklch(0.70 0.17 155)` | Connected / enabled / safe |
| `success-foreground` | `oklch(0.985 0 0)` | `oklch(0.985 0 0)` | Text on solid `success` |
| `warning` | `oklch(0.70 0.17 85)` | `oklch(0.78 0.15 85)` | Caution / sensitive / pending |
| `warning-foreground` | `oklch(0.22 0.03 85)` | `oklch(0.22 0.03 85)` | Text on solid `warning` (dark — `warning` is light) |
| `info` | `oklch(0.55 0.17 245)` | `oklch(0.68 0.15 245)` | Running / in-progress / informational / neutral link |
| `info-foreground` | `oklch(0.985 0.003 245)` | `oklch(0.985 0.003 245)` | Text on solid `info` |

> **The soft-chip recipe.** For a tinted status chip use **`bg-<status>/15 text-<status>`** (e.g. `bg-success/15 text-success`) — the token's `.dark` value keeps the text legible in both themes, so you **don't** add `dark:` color variants. For a *solid* status fill, pair it with its `-foreground` (`bg-warning text-warning-foreground`). For an icon or dot, plain `text-<status>` / `bg-<status>`. There is still **no `Badge` primitive** (§6.1) — chips are composed inline, but always from these tokens, never a raw `bg-amber-500`. `info` is the slot for blue/sky "running / in-progress" states (don't reuse `primary` for status).

### 3.6 Sidebar

The icon rail and any sidebar surface use their own token family so the rail can sit a step darker/cooler than the main content without re-theming every child.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `sidebar` | `oklch(0.93 0.013 250)` | `oklch(0.15 0.025 250)` | Sidebar / rail background |
| `sidebar-foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Sidebar text |
| `sidebar-primary` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Sidebar emphasis (= `primary`) |
| `sidebar-primary-foreground` | `oklch(0.985 0.003 260)` | `oklch(0.94 0.008 250)` | Text on sidebar emphasis |
| `sidebar-accent` | `oklch(0.89 0.013 250)` | `oklch(0.22 0.025 250)` | Sidebar hover / selected background |
| `sidebar-accent-foreground` | `oklch(0.22 0.02 250)` | `oklch(0.94 0.008 250)` | Text on sidebar accent |
| `sidebar-border` | `oklch(0.87 0.012 250)` | `oklch(0.30 0.025 250)` | Sidebar border |
| `sidebar-ring` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Sidebar focus ring |

### 3.7 Scrollbars & selection (global, in `@layer base`)

There are **no scrollbar tokens** — the colors are inlined in [`globals.css`](../frontend/src/styles/globals.css) `@layer base`. A custom WebKit scrollbar applies app-wide: `6px` thin, transparent track, translucent rounded thumb (`oklch(0.5 0.01 250 / 22%)` → `38%` hover; dark `oklch(0.7 0.01 250 / 18%)` → `32%`). Text selection is tinted with the brand: `::selection { background: oklch(0.63 0.20 260 / 30%) }`.

Two helper classes ride on top of the global scrollbar:

| Class | Effect | Where |
| --- | --- | --- |
| `.scroll-stable` | `scrollbar-gutter: stable` — reserves the scrollbar gutter so toggling scrollability (e.g. switching settings tabs of differing height) doesn't shift centered content horizontally (#167) | Settings page |
| `.query-table-scroll` | A faintly-tinted scrollbar track (`oklch(0.5 0.01 250 / 6%)`) so the always-present query-grid scrollbar reads during frequent scrolling | Query result grid |

### 3.8 Domain CSS (query grid & external-edit diff/merge)

Two feature areas need cell/line decorations that go beyond utility classes, so they own named classes in `globals.css` `@layer components`. These are **not general-purpose** — use them only in their feature; don't repurpose them as a generic highlight.

**Query result grid — frozen cell states** (composed over `--muted` / `--primary` / `--background` with `color-mix`):

| Class | Meaning |
| --- | --- |
| `.query-table-frozen-header-selected` | Selected column in a frozen header |
| `.query-table-frozen-cell-selected` | Selected frozen cell |
| `.query-table-frozen-cell-focus` | Focused/active frozen cell |
| `.query-table-frozen-cell-edited` | Frozen cell with an unsaved edit (amber wash; dark-tuned) |

> Non-frozen cells use plain utilities for the same states — `bg-primary/15` (selected), `bg-primary/5` (focus). The frozen variants exist only because a sticky/frozen cell needs an opaque base under the tint.

**External-edit diff / merge** — line + gutter decorations for the Monaco-based file compare/merge workbench (§8). Three families: `.external-edit-diff-*` and `.external-edit-compare-*` (insert = green, delete = red, modify/current = amber) and `.external-edit-merge-*` (local = green, remote = blue, combined = split gradient, current = dark). Applied as Monaco decoration classes from [`CodeDiffViewer.tsx`](../frontend/src/components/CodeDiffViewer.tsx); driven by [`externalEditStore`](../frontend/src/stores/externalEditStore.ts). These carry their own raw oklch values (a deliberate exception — Monaco decorations sit outside the token system); keep new diff/merge tints here, next to their siblings, not scattered in components.

> **Decoration values are raw; the React chrome is not.** Only the Monaco *decoration colors* are exempt — the `.external-edit-*` classes here plus the inline color strings in [`CodeDiffViewer.tsx`](../frontend/src/components/CodeDiffViewer.tsx) and [`merge-decorations.ts`](../frontend/src/components/terminal/external-edit/merge-decorations.ts). The **React chrome** around the editors (workbench toolbars, badges, status text, gutters-as-divs in `IdeaFrame` / `CompareWorkbench` / `MergeWorkbench` / `PendingDialog`) uses the ordinary **status tokens**: insert → `success`, delete → `destructive`, modify → `warning`, remote → `info` (§3.5).

### 3.9 Syntax tokens (value-type coloring)

For coloring a rendered value **by its JSON type** (Redis / etcd / query value cells), use the syntax family — not status colors. One token per type, with a light + dark value, so the color reads on both themes without a `dark:` variant.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `syntax-string` | `oklch(0.52 0.13 230)` | `oklch(0.72 0.12 230)` | String values |
| `syntax-number` | `oklch(0.50 0.20 300)` | `oklch(0.74 0.16 300)` | Numbers (int / float) |
| `syntax-boolean` | `oklch(0.56 0.16 65)` | `oklch(0.78 0.14 75)` | Booleans (`true` / `false`) |
| `syntax-null` | `oklch(0.55 0.02 250)` | `oklch(0.62 0.02 250)` | `null` / nil / undefined |

Use `text-syntax-string` etc. These are for **in-DOM** value rendering only; the Monaco editors carry their own theme (§8).

### 3.10 Chart tokens (categorical palette)

For an **arbitrary set of N distinct categories** that carry no inherent meaning (snippet categories, user tag colors) — never a status, never a value type — use the 5-step categorical palette. Assign by position; cycle if you have more than five.

| Token / class | Light | Dark |
| --- | --- | --- |
| `chart-1` | `oklch(0.55 0.17 250)` | `oklch(0.68 0.15 250)` |
| `chart-2` | `oklch(0.55 0.15 160)` | `oklch(0.70 0.14 160)` |
| `chart-3` | `oklch(0.58 0.19 15)` | `oklch(0.70 0.16 15)` |
| `chart-4` | `oklch(0.64 0.16 70)` | `oklch(0.76 0.14 70)` |
| `chart-5` | `oklch(0.54 0.18 300)` | `oklch(0.68 0.16 300)` |

Typical chip: `bg-chart-1/15 text-chart-1 ring-1 ring-inset ring-chart-1/25`. Don't reach for `chart-*` when the meaning is really status (→ §3.5) or a value type (→ §3.9) — those carry meaning and must stay semantic.

### 3.11 Where raw colors are allowed (the only exceptions)

Everything else uses tokens. These few places legitimately carry raw color values because they live **outside** the CSS-variable / Tailwind system, or are user-authored / brand palettes:

- **Terminal color schemes** — [`data/terminalThemes.ts`](../frontend/src/data/terminalThemes.ts), the editor defaults in [`TerminalThemeEditor.tsx`](../frontend/src/components/settings/TerminalThemeEditor.tsx), and the xterm search-highlight colors in [`TerminalSearchBar.tsx`](../frontend/src/components/terminal/TerminalSearchBar.tsx) — xterm renders to a canvas with its own palette.
- **Monaco editor** — the theme in [`monaco-setup.ts`](../frontend/src/lib/monaco-setup.ts) and the diff/merge decoration *values* in [`CodeDiffViewer.tsx`](../frontend/src/components/CodeDiffViewer.tsx) / [`merge-decorations.ts`](../frontend/src/components/terminal/external-edit/merge-decorations.ts) plus the `.external-edit-*` classes (§3.8).
- **Brand & user palettes** — the brand-icon colors and the user-facing color-picker swatches in [`IconPicker.tsx`](../frontend/src/components/asset/IconPicker.tsx) / [`brand-icons.tsx`](../frontend/src/components/asset/brand-icons.tsx), and the deterministic session-avatar palette in [`ai/sessionIconColor.ts`](../frontend/src/components/ai/sessionIconColor.ts) (already authored in oklch).

If you're not in one of these, use a token. A `text-blue-500` / `bg-[#…]` anywhere else is a bug — see Constraint 1.

---

## 4. Theming

**Mechanism:** the theme switches by adding/removing `.dark` on `document.documentElement` (`@custom-variant dark (&:is(.dark *))` in `globals.css` is what makes the `dark:` variant work). Every token is defined under both `:root` and `.dark`, so toggling the class re-skins the whole app — no per-component color changes needed.

**Provider:** [`frontend/src/components/theme-provider.tsx`](../frontend/src/components/theme-provider.tsx)

```tsx
import { useTheme, useResolvedTheme } from "@/components/theme-provider";

const { theme, setTheme } = useTheme();
// theme: "light" | "dark" | "system"  (user choice, persisted to localStorage key "theme")
setTheme("system"); // "system" follows prefers-color-scheme and updates live

const resolved = useResolvedTheme(); // "light" | "dark" — tracks the .dark class via MutationObserver
```

**Toggle UI:** [`mode-toggle.tsx`](../frontend/src/components/mode-toggle.tsx) — a dropdown over `light` / `dark` / `system`.

**Flash prevention:** an inline script in [`frontend/index.html`](../frontend/index.html) reads `localStorage["theme"]` and adds `.dark` *before* `main.tsx` loads, so a dark-mode user never sees a light frame on launch. New entry points reuse this; don't roll your own pre-mount theme logic.

**Correct usage (do / don't):**

```tsx
// ✅ Tokens — adapt to light/dark automatically
<div className="bg-card text-foreground border-border">…</div>
<button className="bg-primary text-primary-foreground hover:bg-primary/90">…</button>

// ✅ dark: variant only for a dark-specific tweak
<div className="bg-input/30 dark:bg-input/50">…</div>

// ❌ Hard-coded colors — break in dark and violate Constraint 1
<div className="bg-white text-[oklch(0.2_0.02_250)] border-[#e5e5e5]">…</div>
```

**Every UI change must hold up in both themes.** Verify on real light and dark — don't ship after checking only one.

---

## 5. Typography & Radius

### Fonts

OpsKat ships **no webfonts** — the type system is the platform's own fonts, declared as two tokens in `globals.css` and exposed via `@theme inline` (`--font-family-sans` / `--font-family-mono`). `font-sans` is applied on `body`, so everything inherits it.

| Token | Value | Use |
| --- | --- | --- |
| `font-sans` (`--font-sans`) | `system-ui, -apple-system, sans-serif` | Body / UI text — the default; you rarely write `font-sans` explicitly |
| `font-mono` (`--font-mono`) | `'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace` | Code, versions, hostnames/ports, stored values, terminal-adjacent labels (`font-mono`) |

> **No `@font-face`, no bundled webfont.** The mono families are **family *references*** — if `JetBrains Mono` / `Fira Code` aren't installed on the OS, the stack falls back to `Menlo` / `monospace`. (An unused `nunito-*.woff2` sits in [`assets/fonts/`](../frontend/src/assets/fonts/) but is not referenced — don't treat it as wired-up.) The in-pane code editor and terminal are Monaco / xterm, which carry their own font config. If a brand font ever becomes a real requirement, self-host it (woff2, local `@font-face`, never a CDN) and update this table. The `@tailwindcss/typography` plugin is loaded for prose blocks (e.g. AI markdown).

### Radius

`--radius: 0.5rem` (8px) is the base; five steps are derived via `calc` in `@theme inline`:

| class | Multiplier | Value | Typical use |
| --- | --- | --- | --- |
| `rounded-sm` | `×0.6` | ~4.8px | Small tags, compact controls |
| `rounded-md` | `×0.8` | ~6.4px | Buttons, inputs (`Button` defaults to `rounded-md`) |
| `rounded-lg` | `×1.0` | 8px | Cards, panels |
| `rounded-xl` | `×1.4` | ~11.2px | Large cards, emphasized containers |
| `rounded-2xl` | `×1.8` | ~14.4px | Dialogs / the largest containers |

### Spacing & sizing rhythm

OpsKat sizes chrome in fixed pixels (it's one window, not a fluid page):

- **TopBar** ≈ `h-10` (40px); **Sidebar** icon rail ≈ `w-14` (56px). macOS reserves traffic-light space (`pl-20`) on the TopBar; Windows draws its own `WindowControls`.
- **Panes** divide with `flex` ratios and a `1px` divider (`border-panel-divider` / `bg-border`), resized by drag (§7).
- **Block spacing:** start sections at `gap-4` (16px); card padding `p-4`–`p-6`.

---

## 6. Component palette & usage

UI primitives live in the **`@opskat/ui`** workspace package ([`frontend/packages/ui/src/components/`](../frontend/packages/ui/src/components/)) — `new-york` style, CSS-variables enabled, no class prefix. App code imports them from `@opskat/ui` (e.g. `import { Button, Dialog, cn } from "@opskat/ui"`) — enumerate the live set with `git ls-files 'frontend/packages/ui/src/components/*.tsx'`. Icons are `lucide-react` (plus the in-repo brand icons, §8); class merging is always `cn()` ([`packages/ui/src/lib/utils.ts`](../frontend/packages/ui/src/lib/utils.ts)); variants are CVA. This section is "what exists and how to choose."

> Note: `components.json` aliases `ui` to `@/components/ui` for the shadcn CLI, but the maintained primitives live in the `@opskat/ui` package — import from the package, not from app `src/`.

### 6.1 Primitives (the `@opskat/ui` set)

| File | Use |
| --- | --- |
| `button.tsx` | Buttons (variants/sizes below) |
| `card.tsx` | Card container (`Card` + `Header` / `Title` / `Description` / `Action` / `Content` / `Footer`) |
| `dialog.tsx` | General modal dialog — `size` `sm`/`md`/`lg`/`xl`/`full`, optional `showCloseButton` / `resizable`; scrim is `bg-black/50` |
| `alert-dialog.tsx` | Blocking confirmation dialog (dangerous actions); base of `ConfirmDialog` |
| `confirm-dialog.tsx` | **`ConfirmDialog`** — project confirm wrapper over AlertDialog (§6.5) |
| `popover.tsx` | Floating anchored layer |
| `dropdown-menu.tsx` | Dropdown menu |
| `context-menu.tsx` | Right-click context menu (terminals, tabs, grid cells) |
| `tooltip.tsx` | Hover tooltip (exports `TooltipProvider` — mounted once in `App.tsx`) |
| `tabs.tsx` | Tabs — `TabsList` CVA variant `default` / `line` |
| `select.tsx` | Select |
| `input.tsx` / `textarea.tsx` | Text input (input handles IME composition) |
| `checkbox.tsx` / `switch.tsx` | Checkbox / switch |
| `label.tsx` | Form label |
| `separator.tsx` | Divider |
| `scroll-area.tsx` / `scrollable-container.tsx` | Radix scroll area / a plain scrollable wrapper |
| `tree-select.tsx` | **`TreeSelect`** — generic single-select tree dropdown (pinyin search) |
| `tree-check-list.tsx` | **`TreeCheckList`** — generic tri-state checkbox tree |
| `sonner.tsx` | Global `Toaster` (theme-aware, `richColors`) |

> **There is no `badge.tsx`.** Status is shown with `success` / `warning` / `destructive` / `info`-tinted icons, dots, or small soft chips (`bg-<status>/15 text-<status>`, §3.5) composed inline — there is no `Badge` primitive to reach for. The `*-foreground` pairs now exist (§3.5), so if badges become common, add one primitive over them rather than re-deriving chips per view. There is also **no shared `Skeleton` / `EmptyState` / `LoadingState` / `StateScreen`** — see §10.

### 6.2 Button variants / sizes

Source: [`button.tsx`](../frontend/packages/ui/src/components/button.tsx).

- **variant:** `default` (solid `bg-primary`), `destructive`, `outline`, `secondary`, `ghost`, `link`
- **size:** `default` (h-9), `xs` (h-6), `sm` (h-8), `lg` (h-10), `icon` (size-9), `icon-xs` (size-6), `icon-sm` (size-8), `icon-lg` (size-10)

Base classes give every button `rounded-md`, `focus-visible:ring-1 ring-ring/45`, `disabled:opacity-50 disabled:pointer-events-none`, and auto-sized `[&_svg]:size-4`. The `default` variant is `bg-primary text-primary-foreground hover:bg-primary/90`.

```tsx
import { Button } from "@opskat/ui";
import { Plus } from "lucide-react";

<Button>Connect</Button>                                  {/* primary action */}
<Button variant="outline">Cancel</Button>                 {/* secondary action */}
<Button variant="destructive">Delete</Button>             {/* dangerous action */}
<Button variant="ghost" size="icon-sm"><Plus /></Button>  {/* icon button; svg auto-sizes */}
```

### 6.3 Shared composites (reuse before hand-rolling)

These project blocks already solve picker/credential/icon problems — `AGENTS.md` calls them out by name. Reuse them; don't re-derive expand/collapse, tri-state checkboxes, pinyin search, or credential decryption.

| Component | File | Use |
| --- | --- | --- |
| `AssetSelect` | [`components/asset/AssetSelect.tsx`](../frontend/src/components/asset/AssetSelect.tsx) | Single-select asset picker (tree, groups non-selectable); `filterType`, `excludeIds`, pinyin search |
| `AssetMultiSelect` | [`components/asset/AssetMultiSelect.tsx`](../frontend/src/components/asset/AssetMultiSelect.tsx) | Multi-select assets with tri-state group checkboxes; `activeOnly` defaults on |
| `GroupSelect` | [`components/asset/GroupSelect.tsx`](../frontend/src/components/asset/GroupSelect.tsx) | Single-select group picker with `excludeGroupId` circular-ref guard |
| `TreeSelect` / `TreeCheckList` | [`packages/ui/.../tree-select.tsx`](../frontend/packages/ui/src/components/tree-select.tsx) · [`tree-check-list.tsx`](../frontend/packages/ui/src/components/tree-check-list.tsx) | Generic tree dropdown / tri-state checkbox tree these pickers are built on |
| `ConfirmDialog` | [`packages/ui/.../confirm-dialog.tsx`](../frontend/packages/ui/src/components/confirm-dialog.tsx) | Confirm modal; `variant` `default` / `destructive` (default destructive), `confirmTestId` |
| `PasswordSourceField` | [`components/asset/PasswordSourceField.tsx`](../frontend/src/components/asset/PasswordSourceField.tsx) | Inline-password vs managed-credential selector; lazy-decrypts an existing secret via backend |
| `IconPicker` | [`components/asset/IconPicker.tsx`](../frontend/src/components/asset/IconPicker.tsx) | Icon + custom color picker; value is `"name"` or `"name#hexcolor"`; also exports `getIconComponent` / `getIconColor` (§8) |

### 6.4 Toast (`notify` + sonner)

The global `Toaster` ([`sonner.tsx`](../frontend/packages/ui/src/components/sonner.tsx)) is theme-aware with `richColors`, a neutral `popover` surface bound to the design tokens, and semantic Lucide icons; it's mounted once in [`App.tsx`](../frontend/src/App.tsx) inside `TooltipProvider`.

Business code **uses `notify` for success** ([`frontend/src/lib/notify.ts`](../frontend/src/lib/notify.ts)) — never `toast.success` directly (an `AGENTS.md` reuse rule):

```tsx
import { notifyCopied, notifySuccess } from "@/lib/notify";

notifyCopied("Copied");          // top-center, 1s flash — clipboard/copy confirms
notifySuccess("Asset saved");    // top-center, default duration — other successes
```

Errors / warnings / info stay on the **raw sonner API at the default bottom-right** (`toast.error(...)`, `toast.warning(...)`) — long error copy needs room and dwell time, which top-center can't give. Success is top-center because terminal / AI / query views refresh **bottom-up**, so a bottom toast would occlude the freshest output (#135).

### 6.5 Selection guidance

- **Confirmation:** dangerous / irreversible → `ConfirmDialog` (or `AlertDialog`). State the blast radius in the copy ("Delete asset *web-01* and its sessions? This cannot be undone.").
- **Confirm vs. act-immediately:** a modal confirm interrupts *every* time — reserve it for the genuinely irreversible or wide-blast (delete an asset, drop a table, overwrite a remote file). For easily reversible actions, prefer acting immediately. Destructive query results (UPDATE/DELETE) keep their preview/confirm step — don't go straight-to-execute.
- **Transient panels:** side detail / assistant → a dedicated panel (§7); small anchored layer → `Popover` / `DropdownMenu`; right-click → `ContextMenu`.
- **Feedback:** transient success → `notify`; errors → `toast.error`; persistent / in-pane → §10 state patterns.

---

## 7. The desktop pane shell

OpsKat is a **single resizable window**, so the layout is a fixed shell of nested panes, not a scrolling page. Source of truth: [`App.tsx`](../frontend/src/App.tsx) + [`components/layout/`](../frontend/src/components/layout/) + the layout/tab stores.

### Shell anatomy

```
┌───────────────────────────────────────────────────────────────┐
│ WindowControls (Windows only)        TopBar  (h-10, draggable) │
├───────┬───────────────────────────────────────────────┬───────┤
│       │  tab area  (TopTabBar  ·or·  LeftPanel)        │ Side  │
│ Side  │ ┌───────────────────────────────────────────┐ │ Assi- │
│ bar   │ │                                           │ │ stant │
│ rail  │ │            MainPanel                       │ │ Panel │
│ (w-14)│ │   (terminal / query / page / info tab)    │ │ (AI,  │
│       │ │                                           │ │ right)│
└───────┴─┴───────────────────────────────────────────┴─┴───────┘
```

| Region | File | Role |
| --- | --- | --- |
| `WindowControls` | [`layout/WindowControls.tsx`](../frontend/src/components/layout/WindowControls.tsx) | Windows-only min/max/close (macOS uses native traffic lights) |
| `TopBar` | [`layout/TopBar.tsx`](../frontend/src/components/layout/TopBar.tsx) | `h-10` draggable region (`--wails-draggable: drag`); command/search trigger; toggles for asset tree & AI panel |
| `Sidebar` | [`layout/Sidebar.tsx`](../frontend/src/components/layout/Sidebar.tsx) | `w-14` icon rail — top-level navigation (home, settings, keys, snippets, audit, port-forward) |
| `TopTabBar` / `LeftPanel` | [`layout/TopTabBar.tsx`](../frontend/src/components/layout/TopTabBar.tsx) · [`layout/LeftPanel.tsx`](../frontend/src/components/layout/LeftPanel.tsx) | The tab strip — **horizontal (top)** or **vertical (left)** depending on `layoutStore.tabBarLayout` |
| `MainPanel` | [`layout/MainPanel.tsx`](../frontend/src/components/layout/MainPanel.tsx) | Hosts the active tab's content |
| `SideAssistantPanel` | [`ai/SideAssistantPanel.tsx`](../frontend/src/components/ai/SideAssistantPanel.tsx) | Collapsible AI chat on the right |

The shell layout mode lives in [`layoutStore`](../frontend/src/stores/layoutStore.ts) (`tabBarLayout: "top" | "left"`, panel widths/visibility). **Swap the content, not the frame** — a new surface is a new tab type, not a new window chrome.

### Tabs

[`tabStore`](../frontend/src/stores/tabStore.ts) owns the open tabs (a discriminated union: `terminal` / `query` / `ai` / `page` / `info`), the active tab, and ordering (drag-reorder, close-others/left/right). Tabs persist to `localStorage`, so a session restores on relaunch; components register close/restore hooks for lifecycle cleanup (e.g. tearing down a terminal). Open a surface via `openTab(...)` rather than mounting it directly.

### Resizable split panes

Splitting is **custom — no resizable-panel library.** [`terminal/SplitPane.tsx`](../frontend/src/components/terminal/SplitPane.tsx) renders a recursive split tree (`vertical` / `horizontal`, a `ratio`, and `first` / `second` children) as nested flex rows/cols; a `w-1`/`h-1` divider (`bg-border`, `hover:bg-primary/50`) drags to update the ratio (`flex` transitions `150ms ease-out`, suppressed while dragging). The tree and ratios persist per tab. The asset tree, left panel, DatabasePanel sidebar, and AI panel resize the same way — drag handlers + localStorage, not a lib. **Reuse this pattern** (and the divider styling) for any new split surface.

### Visibility, not unmount

Tabs that own expensive native state (xterm buffers, WebGL) stay mounted and toggle `visibility: hidden; pointer-events: none` when inactive, so switching tabs doesn't drop a terminal's scrollback or a query's connection. Don't unmount-on-switch for stateful panes.

### Layering (z-index)

Stacking only works if everyone agrees on the order. Pick the **lowest** layer that works, from this ladder:

| Layer | Class | What lives here |
| --- | --- | --- |
| Base content | *(default)* | Normal pane flow |
| Sticky chrome | `z-10` | TopBar / tab strip / sticky table header / pinned bars — pinned, but *below* anything floating |
| Floating layers | `z-50` | `Dialog`, `DropdownMenu`, `Popover`, `Select`, `Tooltip`, `ContextMenu` — the shadcn/Radix default; **leave it**, don't bump it |
| Toast | *(owned by `sonner`)* | The global `Toaster` portals above everything |

`z-10` and `z-50` are the spine of the ladder; a handful of `z-20`/`z-30`/`z-40` exist for local stacking inside a pane — keep those scoped, and **don't escalate to magic values** (`z-[999]`). A new "always on top" need is usually a sign the element should be a real floating primitive (Dialog/Popover) that already portals correctly.

### Elevation (shadows)

Shadows signal *how high* a surface floats. There are no `--shadow-*` tokens — use the Tailwind utilities, but pick from this fixed ladder so elevation maps to meaning:

| Level | Class | Use |
| --- | --- | --- |
| **Resting** | *(none)* / `shadow-sm` / `shadow-xs` | Flat panes and rows. Prefer a `border` over a shadow at rest; add `shadow-sm`/`-xs` only for a subtle lift (a sticky bar, an `outline` button). |
| **Raised** | `shadow-md` | Anchored floating layers tied to a trigger — `DropdownMenu`, `Popover`, `Select`, hover cards. |
| **Overlay** | `shadow-lg` | Detached overlays that own the screen — `Dialog`, `AlertDialog`. |

Don't reach past `shadow-lg` (a stray `shadow-2xl` exists — treat it as drift, not a precedent). Shadows barely render on dark `card` surfaces, so depth in dark relies on the `border` + the `background → card` surface step; keep the border.

---

## 8. Domain surfaces

OpsKat's real screens are the domain panes. They share the shell (§7) and tokens (§3) but each has a canonical implementation to reuse rather than re-build.

### Terminal panes

[`terminal/Terminal.tsx`](../frontend/src/components/terminal/Terminal.tsx) renders an **xterm.js** terminal (`@xterm/xterm` + `addon-fit` / `addon-search`) inside a `SessionToolbar` (top: asset identity + connection status) / split body / `TerminalToolbar` (bottom: search, actions) frame, with an optional resizable `FileManagerPanel` (SFTP) alongside. Terminal sessions and the per-tab split tree live in [`terminalStore`](../frontend/src/stores/terminalStore.ts); xterm theming maps through [`terminalThemeStore`](../frontend/src/stores/terminalThemeStore.ts) (`toXtermTheme`). Keep new terminal chrome inside this frame.

### Query editor + result grid

[`query/`](../frontend/src/components/query/) composes a `DatabasePanel` (left DB/table tree, resizable) with inner tabs for table data vs SQL editor, over a custom **virtualized** result grid ([`QueryResultTable.tsx`](../frontend/src/components/query/QueryResultTable.tsx), `@tanstack/react-virtual`). The grid is a plain `<table>` with a `bg-muted sticky` frozen header and the `.query-table-frozen-*` cell states (§3.8); selection/focus/edited cells tint with `primary`/amber. There is **no grid library** — extend this renderer rather than introducing one. Query state lives in [`queryStore`](../frontend/src/stores/queryStore.ts). Destructive previews (UPDATE/DELETE) keep their confirm step (§6.5).

### Code editor, diff & merge

[`CodeEditor.tsx`](../frontend/src/components/CodeEditor.tsx) and [`CodeDiffViewer.tsx`](../frontend/src/components/CodeDiffViewer.tsx) wrap **Monaco** (`@monaco-editor/react`) for snippet/SQL/script editing and side-by-side diff. The external-file edit flow (editing a remote file in your local editor, then reconciling) layers the `.external-edit-diff-*` / `.external-edit-merge-*` / `.external-edit-compare-*` decorations (§3.8) over Monaco, driven by [`externalEditStore`](../frontend/src/stores/externalEditStore.ts). Use Monaco for any code surface; don't add a second editor.

### Icons & asset-type identity

An asset's icon is a string — `"name"` or `"name#hexcolor"` — resolved by `getIconComponent` / `getIconColor` in [`IconPicker.tsx`](../frontend/src/components/asset/IconPicker.tsx), backed by `lucide-react` plus the in-repo [`brand-icons.tsx`](../frontend/src/components/asset/brand-icons.tsx) (AWS, MySQL, Redis, Kubernetes, …) and the `ICON_COLORS` brand-color map in `IconPicker.tsx`. **Asset *types*** (ssh / database / redis / mongodb / kafka / k8s / etcd / serial / local) are a registry, not a switch: each [`lib/assetTypes/*.ts`](../frontend/src/lib/assetTypes/) calls `registerAssetType(...)` (default icon, label, aliases, category, connect capabilities) and [`index.ts`](../frontend/src/lib/assetTypes/index.ts) exposes `getAssetType(type)`. **Enumerate the live set from `lib/assetTypes/*.ts`** — don't hardcode the list here (it drifts; new types register themselves). Never branch on a type string in shared UI; ask the registry. (Backend side → [`adding-an-asset-type.md`](./adding-an-asset-type.md).)

### Stores (one per domain)

UI state is Zustand, **one store per domain** in [`frontend/src/stores/`](../frontend/src/stores/) — enumerate the live set with `git ls-files 'frontend/src/stores/*.ts'` (e.g. `tabStore`, `layoutStore`, `terminalStore`, `queryStore`, and `shortcutStore`, which exports `formatBinding`). The frontend structure is owned by [`ARCHITECTURE.md` §9](./ARCHITECTURE.md#9-frontend) — this doc only states the design rule: components depend on a store/hook, not on a sibling component's internals; reuse the relevant store's selectors/actions rather than threading props through panes.

---

## 9. Motion

**Sources:** `tw-animate-css` (imported in `globals.css` — provides `animate-in/out`, `fade-*`, `zoom-*`, `slide-*`) + Radix `data-state` + a few inline transitions. There is **no Framer Motion** — keep motion in CSS.

### How to add motion that stays friendly

- **Fast and light:** enter/leave in `150–250ms`, `ease-out` (the SplitPane resize uses `flex 150ms ease-out`; the theme cross-fade on `html` is `200ms ease`).
- **Hover/focus via CSS pseudo-classes, not React state** (`hover:bg-primary/90`, `focus-visible:ring-ring/45`) — Constraint 1 / §4.
- **Enter/leave via Radix `data-state`** (`data-[state=open]:animate-in data-[state=closed]:animate-out` + `fade-*` / `zoom-*`), as the Dialog/Popover/Tooltip primitives already do — don't hand-roll show/hide with `setTimeout`.
- **Prefer `transition-colors` / `transition-transform` over `transition-all`** to avoid layout thrash. Suppress transitions during active drag (SplitPane already does).
- **Spinners** are `Loader2` from `lucide-react` + `animate-spin` (§10) — that's the loading-motion vocabulary; don't invent bespoke keyframes per view. New shared animation → add it in `globals.css`, not inline in a component.

---

## 10. State patterns

Every async flow covers the states below. **OpsKat does not ship shared state components** (no `StateScreen` / `EmptyState` / `LoadingState` / `Skeleton` / `Progress` primitive) — the convention is composed inline from primitives and `notify`. Follow the convention consistently; if a state block starts repeating across panes, extract one shared component (and document it here) rather than copy-pasting a third time.

| State | Standard presentation |
| --- | --- |
| **Loading** | A `Loader2` (`lucide-react`, `animate-spin`, usually `text-primary`/`text-muted-foreground`) sized to context — inline `size-4` in a button/row, larger when a pane is still empty. Keep the pane's shape stable; show the spinner where content will land, don't collapse the layout. |
| **Empty** | A centered `muted-foreground` icon + a short title + (optional) primary CTA, composed inline. Reuse a neighboring pane's empty block for the same surface rather than re-styling. |
| **Error** | An inline message with the failure cause; for transient failures raise `toast.error(...)` (bottom-right, §6.4) with specific, actionable copy. Put raw error detail in a `font-mono` block, not the headline. |
| **Success** | `notifySuccess(...)` / `notifyCopied(...)` (top-center, §6.4) for transient confirmation; for a result that stays on screen, render it in place. |
| **In-progress** | Stream the work (terminal output, query rows, transfer progress) with per-item status, and disable the triggering control with an inline spinner while it runs. |

Practical rules:

- **Never freeze and never wait silently.** A region that is loading must show a spinner or skeleton-like placeholder — never a blank or stale frame with no signal (Constraint 7).
- **Single-action loading:** disable the control and show an inline `Loader2 size-4 animate-spin`; if the action already has an icon (e.g. a refresh `RefreshCw`), spin that icon instead.
- **One indicator per wait.** Don't stack a pane-level spinner over content that's already showing per-row progress.

### Forms

No form library (react-hook-form / zod) is used — forms are plain `useState` + controlled `@opskat/ui` inputs. Keep new forms on this pattern. Guidelines: validate on blur/submit (not while typing), keep the error message with its field (`text-destructive text-xs` under the input, `aria-invalid` on the control) and raise a `toast.error` for a form-level save failure, mark the rarer of required/optional, keep the submit button enabled and validate on click, and never clear the form on a failed save.

### Writing & microcopy

- **Sentence case** for buttons, titles, labels ("Save changes", not "Save Changes"). Product names keep their casing.
- **Buttons are verbs** naming the action ("Connect", "Run", "Delete"), not "OK"/"Submit"; the in-flight label restates it as progress ("Connecting…").
- **Errors are specific and actionable:** what failed + why + what to do, not "Something went wrong."
- **Copy is translated** ([`frontend/src/i18n/`](../frontend/src/i18n/)) — let labels wrap or `truncate` with a `title`; don't pin a control's width to its English string.

---

## 11. Accessibility

Friendly UX includes users on keyboards, screen readers, and low vision. Verify these alongside the both-themes check.

### Focus visibility

The base layer in [`globals.css`](../frontend/src/styles/globals.css) sets `button`/`[role="button"]` to `outline-none` and relies on a `focus-visible:ring-1 ring-ring/45` ring instead (so programmatic refocus after a Radix layer closes doesn't flash a native outline). The cost: **any custom interactive element you build has no visible keyboard focus unless you add the ring yourself.**

- Every custom clickable (a `div`/`span` with `onClick`, a bespoke pane action) must add `focus-visible:ring-1 focus-visible:ring-ring/45` and be reachable (real `<button>`/`<a>`, or `tabIndex={0}` + key handlers).
- Don't re-disable focus styling to "clean up" a layout — the ring is the only focus signal there is.

### Contrast & color

- **Target WCAG AA:** ≥ 4.5:1 for normal text. `foreground` passes comfortably; **`muted-foreground` is the edge case** — keep it for secondary/large/descriptive text and use `foreground` for anything dense or critical, and don't stack small `muted-foreground` on a `muted` fill.
- **Never encode meaning in color alone** (Principle 3). Pair every status color with text/icon/shape — a `success` dot also says "Connected"; a selected row also has a non-color cue (the `accent` fill plus position/weight).

### Keyboard & screen readers

- **Everything actionable is reachable and operable by keyboard.** Prefer native `<button>`/`<a>`/`<input>`; the Radix primitives (Dialog, DropdownMenu, Select, Tabs, ContextMenu…) already ship focus trap, arrow-key nav, Esc, and return-focus — a reason to reuse them over hand-rolled overlays (§6). The app also has a global shortcut system ([`shortcutStore`](../frontend/src/stores/shortcutStore.ts)) — register shortcuts there, and render their bindings with `formatBinding`.
- **Icon-only controls need an accessible name:** `aria-label` on every icon `Button` (an icon alone is invisible to a screen reader). **Decorative icons** next to a text label are `aria-hidden`.

### Accessibility checklist

- [ ] Text meets AA contrast on **both** themes; meaning never carried by color alone.
- [ ] Every custom interactive element is keyboard-reachable and shows a visible `focus-visible` ring.
- [ ] Icon-only buttons have `aria-label`; decorative icons are `aria-hidden`.
- [ ] New actions are registered as shortcuts where it makes sense, and don't collide with existing bindings.

---

## 12. New-surface recipe

When building a new tab, pane, or dialog, run this checklist to stay consistent:

- [ ] **Surface type:** is it a tab (`terminal` / `query` / `ai` / `page` / `info`)? Open it via `tabStore.openTab(...)`, not by mounting it directly (§7).
- [ ] **Shell:** live inside the existing frame (Sidebar rail → tab area → MainPanel); don't add new window chrome. Resizable regions reuse the `SplitPane` drag pattern + `border-panel-divider`/`bg-border` divider (§7).
- [ ] **Color** entirely from tokens (`bg-card` / `text-foreground` / `border-border` / `text-primary` / `bg-primary` …), no literals, verified on both themes (Constraints 1–2, §3–4).
- [ ] **Components** reuse first — `@opskat/ui` primitives + the shared composites (`AssetSelect` / `GroupSelect` / `ConfirmDialog` / `PasswordSourceField` / `IconPicker` …); variants via CVA, classes via `cn()`, icons via `lucide-react` + `brand-icons` (Constraint 6, §6, §8).
- [ ] **Asset-type logic** goes through the `assetTypes` registry (`getAssetType` / `getIconComponent`), never a `switch` on a type string (§8).
- [ ] **State:** loading / empty / error / success / in-progress all covered, never silent; spinners are `Loader2 + animate-spin`; success via `notify`, errors via `toast.error` (§6.4, §10).
- [ ] **Motion** restrained (`150–250ms`, `ease-out`), hover/focus via pseudo-classes, enter/leave via Radix `data-state` (§9).
- [ ] **Depth** uses the elevation ladder (resting/raised/overlay, §7) and the z-index ladder (`z-10` chrome / `z-50` floating) — no `shadow-2xl`, no magic `z-[…]`.
- [ ] **Accessibility:** AA contrast on both themes; meaning never color-only; custom controls keyboard-reachable with a visible focus ring; `aria-label` on icon buttons (§11).
- [ ] **Copy** defaults to sentence-case + i18n; verbs on buttons; specific errors; flexes for long locales (§10).

Pane skeleton (tokens + `@opskat/ui` primitives + the shell pattern):

```tsx
import { Button, cn } from "@opskat/ui";
import { Loader2 } from "lucide-react";

export default function ExamplePane({ loading }: { loading: boolean }) {
  return (
    <div className="flex h-full flex-col bg-background text-foreground">
      {/* pane toolbar */}
      <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
        <h2 className="text-sm font-medium">Title</h2>
        <div className="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="icon-sm" aria-label="Refresh">…</Button>
        </div>
      </header>

      {/* single scroll body */}
      <main className="min-h-0 flex-1 overflow-auto p-4">
        {loading ? (
          <div className="flex h-full items-center justify-center text-muted-foreground">
            <Loader2 className="size-5 animate-spin text-primary" />
          </div>
        ) : (
          <div className="rounded-lg border border-border bg-card p-4">…</div>
        )}
      </main>

      {/* sticky action bar */}
      <footer className="flex shrink-0 justify-end gap-2 border-t border-border px-3 py-2">
        <Button variant="outline">Cancel</Button>
        <Button>Confirm</Button>
      </footer>
    </div>
  );
}
```

---

## 13. Sources & verification

**Implementation source of truth (read/edit these when changing the design):**

- Color / radius / font tokens, scrollbar, domain CSS → [`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css)
- Theming → [`theme-provider.tsx`](../frontend/src/components/theme-provider.tsx) + flash script in [`index.html`](../frontend/index.html)
- UI primitives → [`frontend/packages/ui/src/components/`](../frontend/packages/ui/src/components/); `cn()` → [`packages/ui/src/lib/utils.ts`](../frontend/packages/ui/src/lib/utils.ts); shadcn config → [`frontend/components.json`](../frontend/components.json)
- Shell & panes → [`App.tsx`](../frontend/src/App.tsx) + [`components/layout/`](../frontend/src/components/layout/) + [`stores/`](../frontend/src/stores/)
- Toast → [`frontend/src/lib/notify.ts`](../frontend/src/lib/notify.ts) + [`packages/ui/src/components/sonner.tsx`](../frontend/packages/ui/src/components/sonner.tsx)

**Related docs:** UI hard rules and commit flow → [`DEVELOP.md`](./DEVELOP.md); cross-cutting principles (reuse, SOLID seams) → [`AGENTS.md`](../AGENTS.md); internals & subsystems → [`ARCHITECTURE.md`](./ARCHITECTURE.md); doc maintenance and fact-checking → [`DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md).

> When editing this doc, follow [`DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md): token values, component names, and variant names track the current branch's `frontend/` code (if you can't `git grep` it, don't claim it); enumerate counts and lists (asset types, stores, primitives) from their source directory rather than trusting memory.
