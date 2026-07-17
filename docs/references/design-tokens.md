# Design Tokens — full light / dark oklch values

> Reference tables split out of [DESIGN.md §3](../DESIGN.md) — heavy detail, load on demand: every color-token family with its exact light/dark oklch values and per-token usage. Open this when choosing an exact token, adding a new one, or verifying a value; day-to-day usage rules (`bg-<token>` composition, opacity modifiers) and the raw-color exceptions stay in [DESIGN.md §3](../DESIGN.md).
>
> **Single source of the values:** [`frontend/src/styles/globals.css`](../../frontend/src/styles/globals.css) — `:root` defines light, `.dark` overrides for dark, `@theme inline` exposes every `--token` as a Tailwind color. When editing this doc, follow [DOC-MAINTENANCE.md](../DOC-MAINTENANCE.md): the tables track the current branch's `globals.css`.

**Why oklch.** The whole palette is authored in `oklch(L C H)` — perceptually-uniform lightness, so the light and dark ladders stay legible and the neutrals share a single cool hue (`H≈250`) with the brand at `H≈260`. Don't reintroduce hex/`rgb()` — keep new tokens in oklch so they sit on the same ladder.

## 1. Base surfaces & text

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `background` | `oklch(0.96 0.01 250)` | `oklch(0.18 0.025 250)` | Window / page background |
| `foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Primary text |
| `card` | `oklch(0.99 0.003 250)` | `oklch(0.22 0.025 250)` | Card / panel surface (one step above `background`) |
| `card-foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Text on cards |
| `popover` | `oklch(0.99 0.003 250)` | `oklch(0.22 0.025 250)` | Floating layers (dropdown / tooltip / toast / select) surface |
| `popover-foreground` | `oklch(0.20 0.025 250)` | `oklch(0.94 0.008 250)` | Text in floating layers |

## 2. Brand primary (blue-violet)

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `primary` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Brand fill **and** accent — solid button fill (`bg-primary text-primary-foreground`), active/selected state, links, indicators. Unlike some shadcn setups there is **no separate `primary-background`**; `bg-primary` *is* the solid control fill |
| `primary-foreground` | `oklch(0.985 0.003 260)` | `oklch(0.985 0.003 260)` | Text/icons on `primary` |

> Selection accents reuse `primary` at low opacity — e.g. query cells use `bg-primary/15` (selected) / `bg-primary/5` (focus), and the pane resize handle hovers to `bg-primary/50`. Keep selection emphasis as `primary/<opacity>` rather than inventing a new token.

## 3. Secondary / muted / accent

> Per the shadcn convention, `secondary` and `muted` share the **same value** (different semantics, one fill); `accent` is a touch darker for hover/selection.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `secondary` | `oklch(0.935 0.012 250)` | `oklch(0.26 0.025 250)` | Secondary buttons / fills |
| `secondary-foreground` | `oklch(0.22 0.02 250)` | `oklch(0.94 0.008 250)` | Text on secondary |
| `muted` | `oklch(0.935 0.012 250)` | `oklch(0.26 0.025 250)` | Muted background (group fills, sticky table headers, placeholders) |
| `muted-foreground` | `oklch(0.48 0.015 250)` | `oklch(0.62 0.015 250)` | De-emphasized / descriptive text — reserve for secondary/large text, not dense body copy (DESIGN.md §11 contrast) |
| `accent` | `oklch(0.91 0.012 250)` | `oklch(0.30 0.025 250)` | Hover / selected background (menu items, list rows) |
| `accent-foreground` | `oklch(0.22 0.02 250)` | `oklch(0.94 0.008 250)` | Text on accent |

## 4. Borders, inputs, ring, dividers

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `border` | `oklch(0.87 0.012 250)` | `oklch(0.30 0.025 250)` | Global borders (the `@layer base` reset gives every element `border-border`) |
| `input` | `oklch(0.87 0.012 250)` | `oklch(0.30 0.025 250)` | Form control borders |
| `ring` | `oklch(0.55 0.22 260)` | `oklch(0.63 0.20 260)` | Focus ring (`focus-visible:ring-ring/45`) — equal to `primary` |
| `panel-divider` | `oklch(0 0 0 / 8%)` | `oklch(1 0 0 / 6%)` | Thin pane/section divider — a translucent line that reads on any surface (use `border-panel-divider` for split-pane gutters and inner separators) |

## 5. Status colors

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

> **The soft-chip recipe.** For a tinted status chip use **`bg-<status>/15 text-<status>`** (e.g. `bg-success/15 text-success`) — the token's `.dark` value keeps the text legible in both themes, so you **don't** add `dark:` color variants. For a *solid* status fill, pair it with its `-foreground` (`bg-warning text-warning-foreground`). For an icon or dot, plain `text-<status>` / `bg-<status>`. There is still **no `Badge` primitive** (DESIGN.md §6.1) — chips are composed inline, but always from these tokens, never a raw `bg-amber-500`. `info` is the slot for blue/sky "running / in-progress" states (don't reuse `primary` for status).

## 6. Sidebar

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

## 7. Scrollbars & selection (global, in `@layer base`)

There are **no scrollbar tokens** — the colors are inlined in [`globals.css`](../../frontend/src/styles/globals.css) `@layer base`. A custom WebKit scrollbar applies app-wide: `6px` thin, transparent track, translucent rounded thumb (`oklch(0.5 0.01 250 / 22%)` → `38%` hover; dark `oklch(0.7 0.01 250 / 18%)` → `32%`). Text selection is tinted with the brand: `::selection { background: oklch(0.63 0.20 260 / 30%) }`.

Two helper classes ride on top of the global scrollbar:

| Class | Effect | Where |
| --- | --- | --- |
| `.scroll-stable` | `scrollbar-gutter: stable` — reserves the scrollbar gutter so toggling scrollability (e.g. switching settings tabs of differing height) doesn't shift centered content horizontally (#167) | Settings page |
| `.query-table-scroll` | A faintly-tinted scrollbar track (`oklch(0.5 0.01 250 / 6%)`) so the always-present query-grid scrollbar reads during frequent scrolling | Query result grid |

## 8. Domain CSS (query grid & external-edit diff/merge)

Two feature areas need cell/line decorations that go beyond utility classes, so they own named classes in `globals.css` `@layer components`. These are **not general-purpose** — use them only in their feature; don't repurpose them as a generic highlight.

**Query result grid — frozen cell states** (composed over `--muted` / `--primary` / `--background` with `color-mix`):

| Class | Meaning |
| --- | --- |
| `.query-table-frozen-header-selected` | Selected column in a frozen header |
| `.query-table-frozen-cell-selected` | Selected frozen cell |
| `.query-table-frozen-cell-focus` | Focused/active frozen cell |
| `.query-table-frozen-cell-edited` | Frozen cell with an unsaved edit (amber wash; dark-tuned) |

> Non-frozen cells use plain utilities for the same states — `bg-primary/15` (selected), `bg-primary/5` (focus). The frozen variants exist only because a sticky/frozen cell needs an opaque base under the tint.

**External-edit diff / merge** — line + gutter decorations for the Monaco-based file compare/merge workbench (DESIGN.md §8). Three families: `.external-edit-diff-*` and `.external-edit-compare-*` (insert = green, delete = red, modify/current = amber) and `.external-edit-merge-*` (local = green, remote = blue, combined = split gradient, current = dark). Applied as Monaco decoration classes from [`CodeDiffViewer.tsx`](../../frontend/src/components/CodeDiffViewer.tsx); driven by [`externalEditStore`](../../frontend/src/stores/externalEditStore.ts). These carry their own raw oklch values (a deliberate exception — Monaco decorations sit outside the token system); keep new diff/merge tints here, next to their siblings, not scattered in components.

> **Decoration values are raw; the React chrome is not.** Only the Monaco *decoration colors* are exempt — the `.external-edit-*` classes here plus the inline color strings in [`CodeDiffViewer.tsx`](../../frontend/src/components/CodeDiffViewer.tsx) and [`merge-decorations.ts`](../../frontend/src/components/terminal/external-edit/merge-decorations.ts). The **React chrome** around the editors (workbench toolbars, badges, status text, gutters-as-divs in `IdeaFrame` / `CompareWorkbench` / `MergeWorkbench` / `PendingDialog`) uses the ordinary **status tokens**: insert → `success`, delete → `destructive`, modify → `warning`, remote → `info` (§5).

## 9. Syntax tokens (value-type coloring)

For coloring a rendered value **by its JSON type** (Redis / etcd / query value cells), use the syntax family — not status colors. One token per type, with a light + dark value, so the color reads on both themes without a `dark:` variant.

| Token / class | Light | Dark | Use |
| --- | --- | --- | --- |
| `syntax-string` | `oklch(0.52 0.13 230)` | `oklch(0.72 0.12 230)` | String values |
| `syntax-number` | `oklch(0.50 0.20 300)` | `oklch(0.74 0.16 300)` | Numbers (int / float) |
| `syntax-boolean` | `oklch(0.56 0.16 65)` | `oklch(0.78 0.14 75)` | Booleans (`true` / `false`) |
| `syntax-null` | `oklch(0.55 0.02 250)` | `oklch(0.62 0.02 250)` | `null` / nil / undefined |

Use `text-syntax-string` etc. These are for **in-DOM** value rendering only; the Monaco editors carry their own theme (DESIGN.md §8).

## 10. Chart tokens (categorical palette)

For an **arbitrary set of N distinct categories** that carry no inherent meaning (snippet categories, user tag colors) — never a status, never a value type — use the 5-step categorical palette. Assign by position; cycle if you have more than five.

| Token / class | Light | Dark |
| --- | --- | --- |
| `chart-1` | `oklch(0.55 0.17 250)` | `oklch(0.68 0.15 250)` |
| `chart-2` | `oklch(0.55 0.15 160)` | `oklch(0.70 0.14 160)` |
| `chart-3` | `oklch(0.58 0.19 15)` | `oklch(0.70 0.16 15)` |
| `chart-4` | `oklch(0.64 0.16 70)` | `oklch(0.76 0.14 70)` |
| `chart-5` | `oklch(0.54 0.18 300)` | `oklch(0.68 0.16 300)` |

Typical chip: `bg-chart-1/15 text-chart-1 ring-1 ring-inset ring-chart-1/25`. Don't reach for `chart-*` when the meaning is really status (→ §5) or a value type (→ §9) — those carry meaning and must stay semantic.

