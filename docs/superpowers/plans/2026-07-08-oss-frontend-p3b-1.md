# OSS 对象浏览器 P3b-1 核心浏览+删除 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the P3b-1 slice of the OSS object browser — a query-model tab that browses Buckets + a lazy server-side prefix tree, a breadcrumb, a paginated object list, refresh, and single/multi delete with confirm — reusing the `EtcdPanel` shell + `@opskat/ui useResizeHandle`, a per-tab Zustand store over the P3a `OSS*` bindings, and empty/loading/error states. Scope = spec `docs/superpowers/specs/2026-07-08-oss-frontend-p3b-1.md` P3b-1 only.

**Architecture:** Bottom-up, presentational-leaf + container split. Pure lazy-tree model (`ossPrefixTree.ts`) → per-tab store (`ossBrowserStore.ts`) over the mocked `oss` binder → presentational leaf components (`OSSBreadcrumb` / `OSSObjectList` / `OSSBucketTree`, props-in/callbacks-out like `EtcdKeyDetail`'s `onRequestDelete` contract) → container (`OSSBrowserPanel`, mirrors `EtcdPanel`, owns store subscription + `ConfirmDialog` + toasts) → tab wiring last so nothing references a not-yet-created file.

**Tech Stack:** Wails v2, React 19 + TypeScript, Zustand, `@opskat/ui` (Radix), `react-i18next`, `sonner`, vitest + `@testing-library/react` (happy-dom), `lucide-react`.

## Global Constraints

- **⚠️ P0 PREREQUISITE — regenerate Wails bindings before Task 3.** The generated, gitignored binders are STALE: `frontend/wailsjs/go/oss/OSS.d.ts` has NO `OSSRemoveObjects`, and `frontend/wailsjs/go/models.ts` `oss_svc` is missing `ListObjectsRequest.maxKeys`/`.continuationToken`, `ListObjectsResult.nextContinuationToken`/`.isTruncated`, and the whole `RemoveObjectsRequest` class. Regenerate from repo root (`make dev` — wait for bindings to be written, then stop; or `wails build`) so the Go `internal/app/oss/oss_ops.go` + `internal/service/oss_svc/types.go` surface lands in TS. VERIFY: `grep OSSRemoveObjects frontend/wailsjs/go/oss/OSS.d.ts` and `grep nextContinuationToken frontend/wailsjs/go/models.ts` both hit. Without this, `npx tsc -b` fails at Task 3 (`OSSRemoveObjects` not exported; `nextContinuationToken` not on `ListObjectsResult`). NEVER commit `frontend/wailsjs/**` — it is generated/gitignored.
- **⚠️ pnpm-lockfile ENV TRAP (critical).** In this environment `pnpm test` / `pnpm lint` REWRITE `frontend/pnpm-lock.yaml` (drop a dompurify override) and can inject an invalid `allowBuilds` line into `frontend/pnpm-workspace.yaml`. After running any gate, implementers MUST `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml` and stage ONLY their task files with explicit `git add <files>` (never `git add -A`); before every commit verify `git status --short` shows no `pnpm-lock.yaml` / `pnpm-workspace.yaml`.
- **Gate commands (run from `frontend/`):** `pnpm test <path>` (vitest filter for the task's own spec), `pnpm test` (full suite, includes `src/__tests__/i18n.test.ts`), `npx tsc -b` (typecheck), `pnpm lint` (eslint). The filtered `pnpm test <path>` does NOT run `i18n.test.ts`; the full `pnpm test` does.
- **Module alias `@` → `frontend/src`** (tsconfig `paths` + vite alias + vitest.config). `wailsjs/` is OUTSIDE `src` → relative imports (`../../wailsjs/...` from `src/stores`, `../../../wailsjs/...` from `src/components/oss`, `../../../../wailsjs/...` from a test under `src/components/oss/__tests__`).
- **vitest env = happy-dom.** `src/__tests__/setup.ts` mocks all wailsjs binders (one `mockBinderModule` per package — the `oss` binder is NOT yet listed; Task 7 adds it) and mocks `react-i18next` so `t(key)` returns the key verbatim (missing i18n keys never fail component tests — only `i18n.test.ts` guards keys). happy-dom does no layout: `getBoundingClientRect`/scroll metrics are stubbed constants — real scroll-driven pagination is NOT observable in tests (test the extracted predicate instead).
- **Success toasts go through `frontend/src/lib/notify.ts` `notifySuccess` (top-center)** — NOT `toast.success`. Errors/warnings use `toast.error` / `toast.warning` (bottom-right). Copy uses `notifyCopied`.
- **No global loading overlay** — per-pane skeleton/spinner only (left tree skeleton, right list skeleton, page-bottom spinner).
- **i18n `en` / `zh-CN` key-locked.** `i18n.test.ts` enforces (a) en⇔zh key parity and (b) every `t("literal")` in `src/**` exists in BOTH locales. Idiomatic per language, NOT literal transliteration. All P3b-1 keys live under `oss.browser.*`; `nav.oss` / `asset.typeOSS` already exist. Keys are added up-front in Task 2 so the full suite stays green at every later commit.
- **P3a binding surface (camelCase DTOs), consumed as-is:** `OSSListBuckets(assetId: number) → BucketItem[]`; `OSSListObjects(req: {assetId,bucket,prefix,maxKeys,continuationToken}) → {prefixes: string[], objects: ObjectItem[], nextContinuationToken: string, isTruncated: boolean}`; `OSSRemoveObject(req: {assetId,bucket,key})`; `OSSRemoveObjects(req: {assetId,bucket,keys})`. DTO source: `internal/service/oss_svc/types.go`. `BucketItem = {name,creationDate}`, `ObjectItem = {key,size,lastModified,etag,storageClass,contentType,isPrefix}`. `lastModified` / `creationDate` are unix **seconds** (Go `.Unix()`, `client_minio.go:142`).
- **Prefix tree is LAZY server-side per level** — do NOT use `redisKeyTree.buildKeyTree` (one-shot full client tree). Each node's children come from that level's `OSSListObjects(prefix).prefixes` on expand; only the flatten/`expandedSet` RENDER shape is borrowed from `redisKeyTree.flattenTree`.
- **Store surfaces errors, never swallows** (AGENTS.md): every async action sets the tab's `error` field AND rethrows; the container `.catch(...)`es to `toast.error`. No `catch { return }`.
- **Commit style:** gitmoji + Chinese subject, one commit per task, NO issue/review numbers.
- **Scope guardrails (spec §1, do NOT build):** upload/download/transfer dock, new-folder, drag-drop, detail drawer, presigned dialog, grid/thumbnails, rename/move/copy, recursive folder (prefix) delete. `OSSPresign*` / `OSSCopyObject` / `OSSMoveObject` / `OSSCreateFolder` bindings exist but are OUT of scope here.

---

## Task boundary rationale (why 8 tasks, each independently reject-able)

- **T1 pure model** and **T2 i18n** have zero code deps and are reviewable in isolation (tree math; locale JSON parity).
- **T3 store** owns ALL binder calls + the tab-meta `"oss"` union additions (it registers a `tabCloseHook` that reads `meta.assetType === "oss"`, so the union add is exercised by a real test, not an assert-nothing change).
- **T4/T5/T6 leaf components** are presentational (props-in / callbacks-out, mirroring `EtcdKeyDetail`'s `onRequestDelete`/`onDeleted` callback contract) so each unit-tests with spies and zero store/binder setup — a reviewer can reject the object list while approving the breadcrumb.
- **T7 container** is the only place that subscribes to the store, drives `useResizeHandle`, and wires `ConfirmDialog` + `notifySuccess`; it adds the `oss` binder to `setup.ts`.
- **T8 tab wiring** lands LAST because `MainPanel`'s new branch imports `OSSBrowserPanel` (created in T7). Ordering guarantees no task references a not-yet-created file.

---

## Task 1 — `ossPrefixTree.ts` lazy-tree pure model

**Files**
- Create `frontend/src/lib/ossPrefixTree.ts`
- Create `frontend/src/lib/__tests__/ossPrefixTree.test.ts`

**Interfaces**
- Produces:
  ```ts
  export interface OssPrefixNode { childPrefixes: string[]; loaded: boolean; cursor: string; truncated: boolean }
  export interface OssPrefixRow { depth: number; name: string; prefix: string; isExpanded: boolean; loaded: boolean }
  export function prefixLeafName(prefix: string): string
  export function flattenPrefixTree(tree: Record<string, OssPrefixNode>, expanded: Set<string>, rootPrefix?: string): OssPrefixRow[]
  ```
- Consumes: nothing.
- Borrowed shape: `RedisFlatTreeRow` / `flattenTree` from `frontend/src/lib/redisKeyTree.ts` (depth-walk + `expandedSet.has(nodeId)` recursion) — but children are read from `tree[prefix].childPrefixes` (lazy, per-level), NOT from a one-shot `buildKeyTree`.

### Steps

- [ ] **Write failing test** — `frontend/src/lib/__tests__/ossPrefixTree.test.ts`:
  ```ts
  import { describe, it, expect } from "vitest";
  import { prefixLeafName, flattenPrefixTree, type OssPrefixNode } from "../ossPrefixTree";

  describe("prefixLeafName", () => {
    it("returns the last path segment of a trailing-slash prefix", () => {
      expect(prefixLeafName("a/b/c/")).toBe("c");
    });
    it("handles a single top-level prefix", () => {
      expect(prefixLeafName("a/")).toBe("a");
    });
    it("returns empty string for the root", () => {
      expect(prefixLeafName("")).toBe("");
    });
  });

  describe("flattenPrefixTree", () => {
    const tree: Record<string, OssPrefixNode> = {
      "": { childPrefixes: ["a/", "b/"], loaded: true, cursor: "", truncated: false },
      "a/": { childPrefixes: ["a/x/"], loaded: true, cursor: "", truncated: false },
    };

    it("lists only root children when nothing is expanded", () => {
      const rows = flattenPrefixTree(tree, new Set(), "");
      expect(rows).toEqual([
        { depth: 0, name: "a", prefix: "a/", isExpanded: false, loaded: true },
        { depth: 0, name: "b", prefix: "b/", isExpanded: false, loaded: false },
      ]);
    });

    it("recurses into an expanded node using that node's lazily-loaded children", () => {
      const rows = flattenPrefixTree(tree, new Set(["a/"]), "");
      expect(rows.map((r) => `${r.depth}:${r.prefix}`)).toEqual(["0:a/", "1:a/x/", "0:b/"]);
      expect(rows[0].isExpanded).toBe(true);
    });

    it("does not recurse into an expanded node whose children are not loaded yet", () => {
      const partial: Record<string, OssPrefixNode> = {
        "": { childPrefixes: ["a/"], loaded: true, cursor: "", truncated: false },
      };
      const rows = flattenPrefixTree(partial, new Set(["a/"]), "");
      expect(rows).toEqual([{ depth: 0, name: "a", prefix: "a/", isExpanded: true, loaded: false }]);
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/lib/__tests__/ossPrefixTree.test.ts`. Expected: `Failed to resolve import "../ossPrefixTree"` (module does not exist).

- [ ] **Minimal impl** — `frontend/src/lib/ossPrefixTree.ts`:
  ```ts
  // 懒前缀树纯模型：节点子前缀按层从 OSSListObjects(prefix).prefixes 填入，
  // flatten 只负责把 tree + expanded 展平成带缩进的渲染行（借 redisKeyTree.flattenTree 的
  // depth-walk / expandedSet 范式，但绝不一次性 buildKeyTree 全量建树）。

  export interface OssPrefixNode {
    childPrefixes: string[];
    loaded: boolean;
    cursor: string;
    truncated: boolean;
  }

  export interface OssPrefixRow {
    depth: number;
    name: string;
    prefix: string;
    isExpanded: boolean;
    loaded: boolean;
  }

  /** "a/b/c/" -> "c"；"a/" -> "a"；"" -> ""。 */
  export function prefixLeafName(prefix: string): string {
    const trimmed = prefix.endsWith("/") ? prefix.slice(0, -1) : prefix;
    const idx = trimmed.lastIndexOf("/");
    return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
  }

  export function flattenPrefixTree(
    tree: Record<string, OssPrefixNode>,
    expanded: Set<string>,
    rootPrefix = ""
  ): OssPrefixRow[] {
    const rows: OssPrefixRow[] = [];
    const walk = (parentPrefix: string, depth: number) => {
      const node = tree[parentPrefix];
      if (!node) return;
      for (const childPrefix of node.childPrefixes) {
        const isExpanded = expanded.has(childPrefix);
        const childNode = tree[childPrefix];
        rows.push({
          depth,
          name: prefixLeafName(childPrefix),
          prefix: childPrefix,
          isExpanded,
          loaded: childNode?.loaded ?? false,
        });
        // 只有 expanded 且子节点已懒填才继续下钻；expanded 但未 loaded 的节点先只画自己。
        if (isExpanded && childNode?.loaded) walk(childPrefix, depth + 1);
      }
    };
    walk(rootPrefix, 0);
    return rows;
  }
  ```

- [ ] **Run — passes** — `pnpm test src/lib/__tests__/ossPrefixTree.test.ts` (green), then `npx tsc -b`, then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/lib/ossPrefixTree.ts frontend/src/lib/__tests__/ossPrefixTree.test.ts
  git status --short   # must NOT list any pnpm-lock.yaml / pnpm-workspace.yaml
  git commit -m "✨ OSS 懒前缀树纯模型 flatten/leaf 名"
  ```

---

## Task 2 — i18n `oss.browser.*` (en / zh-CN lockstep)

**Files**
- Modify `frontend/src/i18n/locales/en/common.json`
- Modify `frontend/src/i18n/locales/zh-CN/common.json`

**Interfaces**
- Produces the exact key set consumed by T4–T7 (added up-front so the full `pnpm test` stays green at every later commit). Consumes nothing.
- The existing `oss` block already has `oss.form.*` + `oss.error.*` (en/zh ~line 2504); add a sibling `oss.browser` object. `action.cancel` / `action.confirm` already exist and are reused by the panel.

**Rationale for early placement:** `i18n.test.ts` "covers static common translation calls" scans every `t("literal")` in `src/**` and fails if the key is absent in either locale. Defining the whole `oss.browser.*` set before any component references it keeps the full suite green at each subsequent commit.

### Steps

- [ ] **Write failing test** — no new test file; the guard is the existing `frontend/src/__tests__/i18n.test.ts`. To make failure concrete first, add a TEMP probe at the top of `describe("i18n resources")` proving the keys are missing:
  ```ts
  it("TEMP: oss.browser keys present in both locales", () => {
    const enKeys = new Set(flattenKeys(enCommon));
    const zhKeys = new Set(flattenKeys(zhCommon));
    for (const k of ["oss.browser.refresh", "oss.browser.emptyDir", "oss.browser.deleteConfirmMany"]) {
      expect(enKeys.has(k)).toBe(true);
      expect(zhKeys.has(k)).toBe(true);
    }
  });
  ```

- [ ] **Run — fails** — `pnpm test src/__tests__/i18n.test.ts`. Expected: the TEMP test fails (`expected false to be true`) because `oss.browser.*` does not exist yet.

- [ ] **Minimal impl** — insert a `"browser"` object inside the existing `"oss"` block (right after the `"error"` object) in BOTH files. `en/common.json`:
  ```json
  "browser": {
    "refresh": "Refresh",
    "loading": "Loading…",
    "loadingMore": "Loading more…",
    "emptyDir": "This folder is empty",
    "noBuckets": "No buckets",
    "selectBucket": "Select a bucket to start browsing",
    "colName": "Name",
    "colSize": "Size",
    "colStorageClass": "Storage class",
    "colModified": "Modified",
    "missingAsset": "Missing asset",
    "loadFailed": "Load failed",
    "selectedCount": "{{count}} selected",
    "deleteSelected": "Delete",
    "deleteConfirmTitle": "Confirm delete",
    "deleteConfirmOne": "Delete object {{key}}?",
    "deleteConfirmMany": "Delete {{count}} objects?",
    "deleteSuccess": "Deleted",
    "deleteFailed": "Delete failed"
  }
  ```
  `zh-CN/common.json` (idiomatic, not literal):
  ```json
  "browser": {
    "refresh": "刷新",
    "loading": "加载中…",
    "loadingMore": "加载更多…",
    "emptyDir": "此目录为空",
    "noBuckets": "暂无存储桶",
    "selectBucket": "选择一个存储桶开始浏览",
    "colName": "名称",
    "colSize": "大小",
    "colStorageClass": "存储类型",
    "colModified": "修改时间",
    "missingAsset": "缺少资产",
    "loadFailed": "加载失败",
    "selectedCount": "已选 {{count}} 项",
    "deleteSelected": "删除",
    "deleteConfirmTitle": "确认删除",
    "deleteConfirmOne": "删除对象 {{key}}?",
    "deleteConfirmMany": "删除 {{count}} 个对象?",
    "deleteSuccess": "已删除",
    "deleteFailed": "删除失败"
  }
  ```
  (The preceding `"error": { ... }` object needs a trailing comma before the new `"browser"` key.)

- [ ] **Run — passes** — `pnpm test src/__tests__/i18n.test.ts` (green). Then DELETE the TEMP probe test (it only proved red→green; the file's real parity + coverage assertions are the lasting guard). Re-run `pnpm test src/__tests__/i18n.test.ts` to confirm still green, then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/i18n/locales/en/common.json frontend/src/i18n/locales/zh-CN/common.json
  git status --short
  git commit -m "🌐 新增 OSS 对象浏览器 en/zh 文案 oss.browser.*"
  ```

---

## Task 3 — `ossBrowserStore.ts` (per-tab state + actions) + `"oss"` tab-meta unions

**Files**
- Create `frontend/src/stores/ossBrowserStore.ts`
- Create `frontend/src/stores/ossBrowserStore.test.ts`
- Modify `frontend/src/stores/tabStore.ts` (add `"oss"` to `QueryTabMeta.assetType`, line ~29)
- Modify `frontend/src/stores/queryStore.ts` (add `"oss"` to `QueryTab.assetType` line ~16 AND to the `openQueryTab` `asset.Type as ...` cast, line ~447)

**Interfaces**
- Consumes: `OSSListBuckets`, `OSSListObjects`, `OSSRemoveObject`, `OSSRemoveObjects` from `../../wailsjs/go/oss/OSS`; `oss_svc.BucketItem` / `oss_svc.ObjectItem` / `oss_svc.ListObjectsResult` from `../../wailsjs/go/models`; `OssPrefixNode` from `@/lib/ossPrefixTree`; `registerTabCloseHook`, `QueryTabMeta` from `./tabStore`.
- Produces:
  ```ts
  export interface OssListing { objects: oss_svc.ObjectItem[]; prefixes: string[]; cursor: string; truncated: boolean }
  export interface OssBrowserTabState {
    assetId: number;
    buckets: oss_svc.BucketItem[] | null;   // null = 未加载
    currentBucket: string;
    currentPrefix: string;                   // "" 或以 "/" 结尾
    tree: Record<string, OssPrefixNode>;
    expanded: Set<string>;
    listing: OssListing | null;
    selection: Set<string>;
    loading: { buckets: boolean; listing: boolean; page: boolean };
    error: string | null;
  }
  export const useOssBrowserStore; // actions:
  //   loadBuckets(tabId,assetId) selectBucket(tabId,bucket) navigateToPrefix(tabId,prefix)
  //   expandNode(tabId,prefix) loadNextPage(tabId) toggleSelect(tabId,key) clearSelection(tabId)
  //   deleteSelected(tabId) refresh(tabId)   — all async ones set error + rethrow on failure
  ```

**Scope note (spec §5 `deleteOne` folded in):** the spec lists both `deleteSelected` and `deleteOne`. `deleteSelected` already dispatches `OSSRemoveObject` when `selection.size === 1` and `OSSRemoveObjects` otherwise (spec §6). The P3b-1 UI deletes only via multi-select + one delete button, so a separate per-row `deleteOne` would be dead code (AGENTS.md: no dead code). `deleteOne` is intentionally NOT implemented; single delete = select one → `deleteSelected`.

### Steps

- [ ] **Add `"oss"` to the tab-meta unions first (unblocks store + test typing).**
  - `frontend/src/stores/tabStore.ts` line ~29:
    ```ts
    assetType: "database" | "redis" | "mongodb" | "kafka" | "k8s" | "etcd" | "oss";
    ```
  - `frontend/src/stores/queryStore.ts` line ~16 (`QueryTab.assetType`) same union; and line ~447 cast in `openQueryTab`:
    ```ts
    assetType: asset.Type as "database" | "redis" | "mongodb" | "kafka" | "k8s" | "etcd" | "oss",
    ```
  (No new `openQueryTab` branch is needed: OSS keeps its browse state in `ossBrowserStore`, so `openQueryTab` just creates the tab and returns — the existing `if/else if` chain already no-ops for `oss`.)

- [ ] **Write failing test** — `frontend/src/stores/ossBrowserStore.test.ts` (inline binder mock, mirroring `etcdStore.test.ts`):
  ```ts
  /* eslint-disable @typescript-eslint/no-explicit-any */
  import { describe, it, expect, vi, beforeEach } from "vitest";

  vi.mock("../../wailsjs/go/oss/OSS", () => ({
    OSSListBuckets: vi.fn(),
    OSSListObjects: vi.fn(),
    OSSRemoveObject: vi.fn(),
    OSSRemoveObjects: vi.fn(),
  }));

  import { OSSListBuckets, OSSListObjects, OSSRemoveObject, OSSRemoveObjects } from "../../wailsjs/go/oss/OSS";
  import { useOssBrowserStore } from "./ossBrowserStore";
  import { useTabStore } from "./tabStore";

  const TAB = "query-7";

  function obj(key: string, size = 1): any {
    return { key, size, lastModified: 0, etag: "", storageClass: "STANDARD", contentType: "", isPrefix: false };
  }

  // 已选桶的基础 tab 态（selection/delete/close-hook 用例直接 setState 灌入）。
  function baseState(over: Partial<any> = {}): any {
    return {
      assetId: 7,
      buckets: [{ name: "b1", creationDate: 0 }],
      currentBucket: "b1",
      currentPrefix: "",
      tree: { "": { childPrefixes: [], loaded: true, cursor: "", truncated: false } },
      expanded: new Set(),
      listing: { objects: [], prefixes: [], cursor: "", truncated: false },
      selection: new Set(),
      loading: { buckets: false, listing: false, page: false },
      error: null,
      ...over,
    };
  }

  beforeEach(() => {
    vi.mocked(OSSListBuckets).mockReset();
    vi.mocked(OSSListObjects).mockReset();
    vi.mocked(OSSRemoveObject).mockReset();
    vi.mocked(OSSRemoveObjects).mockReset();
    useOssBrowserStore.setState({ tabs: {} });
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  describe("loadBuckets", () => {
    it("stores buckets and clears the loading flag", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
      await useOssBrowserStore.getState().loadBuckets(TAB, 7);
      const s = useOssBrowserStore.getState().tabs[TAB];
      expect(OSSListBuckets).toHaveBeenCalledWith(7);
      expect(s.buckets).toEqual([{ name: "b1", creationDate: 0 }]);
      expect(s.loading.buckets).toBe(false);
    });

    it("records error and rethrows on binder failure", async () => {
      vi.mocked(OSSListBuckets).mockRejectedValue(new Error("boom"));
      await expect(useOssBrowserStore.getState().loadBuckets(TAB, 7)).rejects.toThrow("boom");
      const s = useOssBrowserStore.getState().tabs[TAB];
      expect(s.error).toContain("boom");
      expect(s.loading.buckets).toBe(false);
    });
  });

  describe("selectBucket + navigateToPrefix", () => {
    it("selects a bucket, lists its root, fills tree + listing", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
      vi.mocked(OSSListObjects).mockResolvedValue({
        prefixes: ["docs/"],
        objects: [obj("readme.txt")],
        nextContinuationToken: "",
        isTruncated: false,
      } as any);
      const st = useOssBrowserStore.getState();
      await st.loadBuckets(TAB, 7);
      await st.selectBucket(TAB, "b1");
      const s = useOssBrowserStore.getState().tabs[TAB];
      expect(OSSListObjects).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", prefix: "", maxKeys: 200, continuationToken: "" });
      expect(s.currentBucket).toBe("b1");
      expect(s.currentPrefix).toBe("");
      expect(s.listing?.objects.map((o: any) => o.key)).toEqual(["readme.txt"]);
      expect(s.tree[""].childPrefixes).toEqual(["docs/"]);
    });
  });

  describe("loadNextPage cursor pagination", () => {
    it("appends objects (does not overwrite) and advances the cursor", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
      vi.mocked(OSSListObjects)
        .mockResolvedValueOnce({ prefixes: [], objects: [obj("a")], nextContinuationToken: "C1", isTruncated: true } as any)
        .mockResolvedValueOnce({ prefixes: [], objects: [obj("b")], nextContinuationToken: "", isTruncated: false } as any);
      const st = useOssBrowserStore.getState();
      await st.loadBuckets(TAB, 7);
      await st.selectBucket(TAB, "b1");
      await st.loadNextPage(TAB);
      const s = useOssBrowserStore.getState().tabs[TAB];
      expect(OSSListObjects).toHaveBeenLastCalledWith({ assetId: 7, bucket: "b1", prefix: "", maxKeys: 200, continuationToken: "C1" });
      expect(s.listing?.objects.map((o: any) => o.key)).toEqual(["a", "b"]);
      expect(s.listing?.truncated).toBe(false);
      expect(s.listing?.cursor).toBe("");
    });

    it("no-ops when the current listing is not truncated", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
      vi.mocked(OSSListObjects).mockResolvedValue({ prefixes: [], objects: [obj("a")], nextContinuationToken: "", isTruncated: false } as any);
      const st = useOssBrowserStore.getState();
      await st.loadBuckets(TAB, 7);
      await st.selectBucket(TAB, "b1");
      vi.mocked(OSSListObjects).mockClear();
      await st.loadNextPage(TAB);
      expect(OSSListObjects).not.toHaveBeenCalled();
    });
  });

  describe("expandNode", () => {
    it("lazily loads a node's child prefixes once, and collapses without refetch", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as any);
      vi.mocked(OSSListObjects)
        .mockResolvedValueOnce({ prefixes: ["docs/"], objects: [], nextContinuationToken: "", isTruncated: false } as any)
        .mockResolvedValueOnce({ prefixes: ["docs/sub/"], objects: [], nextContinuationToken: "", isTruncated: false } as any);
      const st = useOssBrowserStore.getState();
      await st.loadBuckets(TAB, 7);
      await st.selectBucket(TAB, "b1");   // fills tree[""]
      await st.expandNode(TAB, "docs/");  // loads tree["docs/"]
      let s = useOssBrowserStore.getState().tabs[TAB];
      expect(s.expanded.has("docs/")).toBe(true);
      expect(s.tree["docs/"].childPrefixes).toEqual(["docs/sub/"]);
      const callsAfterLoad = vi.mocked(OSSListObjects).mock.calls.length;
      await st.expandNode(TAB, "docs/");  // collapse — no fetch
      s = useOssBrowserStore.getState().tabs[TAB];
      expect(s.expanded.has("docs/")).toBe(false);
      expect(vi.mocked(OSSListObjects).mock.calls.length).toBe(callsAfterLoad);
    });
  });

  describe("selection + delete", () => {
    it("toggleSelect adds then removes a key", () => {
      useOssBrowserStore.setState({ tabs: { [TAB]: baseState() } });
      const st = useOssBrowserStore.getState();
      st.toggleSelect(TAB, "a");
      expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(true);
      st.toggleSelect(TAB, "a");
      expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(false);
    });

    it("deleteSelected with one key calls OSSRemoveObject, reloads, clears selection", async () => {
      useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["docs/a"]) }) } });
      vi.mocked(OSSListObjects).mockResolvedValue({ prefixes: [], objects: [], nextContinuationToken: "", isTruncated: false } as any);
      await useOssBrowserStore.getState().deleteSelected(TAB);
      expect(OSSRemoveObject).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", key: "docs/a" });
      expect(OSSRemoveObjects).not.toHaveBeenCalled();
      expect(OSSListObjects).toHaveBeenCalled(); // refresh re-listed current prefix
      expect(useOssBrowserStore.getState().tabs[TAB].selection.size).toBe(0);
    });

    it("deleteSelected with many keys calls OSSRemoveObjects", async () => {
      useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["a", "b"]) }) } });
      vi.mocked(OSSListObjects).mockResolvedValue({ prefixes: [], objects: [], nextContinuationToken: "", isTruncated: false } as any);
      await useOssBrowserStore.getState().deleteSelected(TAB);
      expect(OSSRemoveObjects).toHaveBeenCalledWith({ assetId: 7, bucket: "b1", keys: ["a", "b"] });
    });

    it("deleteSelected rethrows and preserves selection on binder failure", async () => {
      useOssBrowserStore.setState({ tabs: { [TAB]: baseState({ selection: new Set(["a"]) }) } });
      vi.mocked(OSSRemoveObject).mockRejectedValue(new Error("denied"));
      await expect(useOssBrowserStore.getState().deleteSelected(TAB)).rejects.toThrow("denied");
      expect(useOssBrowserStore.getState().tabs[TAB].selection.has("a")).toBe(true);
    });
  });

  describe("tab close hook", () => {
    it("drops the tab slice when an oss query tab is closed", () => {
      useOssBrowserStore.setState({ tabs: { [TAB]: baseState() } });
      useTabStore.setState({
        tabs: [{ id: TAB, type: "query", label: "b1", meta: { type: "query", assetId: 7, assetName: "b1", assetIcon: "", assetType: "oss" } }],
        activeTabId: TAB,
      });
      useTabStore.getState().closeTab(TAB);
      expect(useOssBrowserStore.getState().tabs[TAB]).toBeUndefined();
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/stores/ossBrowserStore.test.ts`. Expected: `Failed to resolve import "./ossBrowserStore"`.

- [ ] **Minimal impl** — `frontend/src/stores/ossBrowserStore.ts`:
  ```ts
  import { create } from "zustand";
  import { OSSListBuckets, OSSListObjects, OSSRemoveObject, OSSRemoveObjects } from "../../wailsjs/go/oss/OSS";
  import { oss_svc } from "../../wailsjs/go/models";
  import { registerTabCloseHook, type QueryTabMeta } from "./tabStore";
  import type { OssPrefixNode } from "@/lib/ossPrefixTree";

  const OSS_PAGE_SIZE = 200;

  export interface OssListing {
    objects: oss_svc.ObjectItem[];
    prefixes: string[];
    cursor: string;
    truncated: boolean;
  }

  export interface OssBrowserTabState {
    assetId: number;
    buckets: oss_svc.BucketItem[] | null;
    currentBucket: string;
    currentPrefix: string;
    tree: Record<string, OssPrefixNode>;
    expanded: Set<string>;
    listing: OssListing | null;
    selection: Set<string>;
    loading: { buckets: boolean; listing: boolean; page: boolean };
    error: string | null;
  }

  interface OssBrowserState {
    tabs: Record<string, OssBrowserTabState>;
    loadBuckets: (tabId: string, assetId: number) => Promise<void>;
    selectBucket: (tabId: string, bucket: string) => Promise<void>;
    navigateToPrefix: (tabId: string, prefix: string) => Promise<void>;
    expandNode: (tabId: string, prefix: string) => Promise<void>;
    loadNextPage: (tabId: string) => Promise<void>;
    toggleSelect: (tabId: string, key: string) => void;
    clearSelection: (tabId: string) => void;
    deleteSelected: (tabId: string) => Promise<void>;
    refresh: (tabId: string) => Promise<void>;
  }

  function emptyTabState(assetId: number): OssBrowserTabState {
    return {
      assetId,
      buckets: null,
      currentBucket: "",
      currentPrefix: "",
      tree: {},
      expanded: new Set(),
      listing: null,
      selection: new Set(),
      loading: { buckets: false, listing: false, page: false },
      error: null,
    };
  }

  export const useOssBrowserStore = create<OssBrowserState>((set, get) => {
    // 只在同一 tab 存在时打补丁；不存在则整体不变（避免为已关闭 tab 重建 slice）。
    const patch = (tabId: string, fn: (t: OssBrowserTabState) => OssBrowserTabState) =>
      set((s) => (s.tabs[tabId] ? { tabs: { ...s.tabs, [tabId]: fn(s.tabs[tabId]) } } : { tabs: s.tabs }));

    const listInto = (tabId: string, prefix: string, continuationToken: string): Promise<oss_svc.ListObjectsResult> => {
      const t = get().tabs[tabId];
      return OSSListObjects({
        assetId: t.assetId,
        bucket: t.currentBucket,
        prefix,
        maxKeys: OSS_PAGE_SIZE,
        continuationToken,
      });
    };

    return {
      tabs: {},

      loadBuckets: async (tabId, assetId) => {
        set((s) => ({
          tabs: {
            ...s.tabs,
            [tabId]: {
              ...(s.tabs[tabId] ?? emptyTabState(assetId)),
              assetId,
              loading: { ...(s.tabs[tabId]?.loading ?? emptyTabState(assetId).loading), buckets: true },
              error: null,
            },
          },
        }));
        try {
          const buckets = await OSSListBuckets(assetId);
          patch(tabId, (t) => ({ ...t, buckets: buckets ?? [], loading: { ...t.loading, buckets: false } }));
        } catch (err) {
          patch(tabId, (t) => ({ ...t, loading: { ...t.loading, buckets: false }, error: String(err) }));
          throw err;
        }
      },

      selectBucket: async (tabId, bucket) => {
        patch(tabId, (t) => ({
          ...t,
          currentBucket: bucket,
          currentPrefix: "",
          tree: {},
          expanded: new Set(),
          listing: null,
          selection: new Set(),
        }));
        await get().navigateToPrefix(tabId, "");
      },

      navigateToPrefix: async (tabId, prefix) => {
        if (!get().tabs[tabId]) return;
        patch(tabId, (t) => ({ ...t, currentPrefix: prefix, selection: new Set(), loading: { ...t.loading, listing: true }, error: null }));
        try {
          const res = await listInto(tabId, prefix, "");
          patch(tabId, (t) => ({
            ...t,
            listing: {
              objects: res.objects ?? [],
              prefixes: res.prefixes ?? [],
              cursor: res.nextContinuationToken ?? "",
              truncated: !!res.isTruncated,
            },
            tree: {
              ...t.tree,
              [prefix]: { childPrefixes: res.prefixes ?? [], loaded: true, cursor: res.nextContinuationToken ?? "", truncated: !!res.isTruncated },
            },
            loading: { ...t.loading, listing: false },
          }));
        } catch (err) {
          patch(tabId, (t) => ({ ...t, loading: { ...t.loading, listing: false }, error: String(err) }));
          throw err;
        }
      },

      expandNode: async (tabId, prefix) => {
        const t0 = get().tabs[tabId];
        if (!t0) return;
        const wasExpanded = t0.expanded.has(prefix);
        patch(tabId, (t) => {
          const expanded = new Set(t.expanded);
          if (wasExpanded) expanded.delete(prefix);
          else expanded.add(prefix);
          return { ...t, expanded };
        });
        if (wasExpanded || t0.tree[prefix]?.loaded) return; // collapse, or already lazily loaded
        try {
          const res = await listInto(tabId, prefix, "");
          patch(tabId, (t) => ({
            ...t,
            tree: {
              ...t.tree,
              [prefix]: { childPrefixes: res.prefixes ?? [], loaded: true, cursor: res.nextContinuationToken ?? "", truncated: !!res.isTruncated },
            },
          }));
        } catch (err) {
          patch(tabId, (t) => ({ ...t, error: String(err) }));
          throw err;
        }
      },

      loadNextPage: async (tabId) => {
        const t0 = get().tabs[tabId];
        if (!t0 || !t0.listing || !t0.listing.truncated || t0.loading.page) return;
        const cursor = t0.listing.cursor;
        const prefix = t0.currentPrefix;
        patch(tabId, (t) => ({ ...t, loading: { ...t.loading, page: true } }));
        try {
          const res = await listInto(tabId, prefix, cursor);
          patch(tabId, (t) => ({
            ...t,
            listing: t.listing
              ? {
                  objects: [...t.listing.objects, ...(res.objects ?? [])],
                  prefixes: [...t.listing.prefixes, ...(res.prefixes ?? [])],
                  cursor: res.nextContinuationToken ?? "",
                  truncated: !!res.isTruncated,
                }
              : t.listing,
            loading: { ...t.loading, page: false },
          }));
        } catch (err) {
          patch(tabId, (t) => ({ ...t, loading: { ...t.loading, page: false }, error: String(err) }));
          throw err;
        }
      },

      toggleSelect: (tabId, key) => {
        patch(tabId, (t) => {
          const selection = new Set(t.selection);
          if (selection.has(key)) selection.delete(key);
          else selection.add(key);
          return { ...t, selection };
        });
      },

      clearSelection: (tabId) => patch(tabId, (t) => ({ ...t, selection: new Set() })),

      deleteSelected: async (tabId) => {
        const t0 = get().tabs[tabId];
        if (!t0 || t0.selection.size === 0) return;
        const keys = Array.from(t0.selection);
        try {
          if (keys.length === 1) {
            await OSSRemoveObject({ assetId: t0.assetId, bucket: t0.currentBucket, key: keys[0] });
          } else {
            await OSSRemoveObjects({ assetId: t0.assetId, bucket: t0.currentBucket, keys });
          }
        } catch (err) {
          patch(tabId, (t) => ({ ...t, error: String(err) }));
          throw err;
        }
        get().clearSelection(tabId);
        await get().refresh(tabId);
      },

      refresh: async (tabId) => {
        const t0 = get().tabs[tabId];
        if (!t0) return;
        patch(tabId, (t) => {
          const tree = { ...t.tree };
          delete tree[t.currentPrefix];
          return { ...t, tree };
        });
        await get().navigateToPrefix(tabId, get().tabs[tabId]!.currentPrefix);
      },
    };
  });

  // tab 关闭时清理该 OSS query tab 的浏览态（仿 queryStore / sftpStore 的 close hook）。
  registerTabCloseHook((tab) => {
    if (tab.type !== "query") return;
    if ((tab.meta as QueryTabMeta).assetType !== "oss") return;
    useOssBrowserStore.setState((s) => {
      const next = { ...s.tabs };
      delete next[tab.id];
      return { tabs: next };
    });
  });
  ```

- [ ] **Run — passes** — `pnpm test src/stores/ossBrowserStore.test.ts` (green), then `npx tsc -b` (this is the gate that REQUIRES the P0 binding regen — `OSSRemoveObjects` + `nextContinuationToken` must resolve), then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/stores/ossBrowserStore.ts frontend/src/stores/ossBrowserStore.test.ts frontend/src/stores/tabStore.ts frontend/src/stores/queryStore.ts
  git status --short
  git commit -m "✨ OSS 浏览态 store 与 tab meta 联合类型"
  ```

---

## Task 4 — `OSSBreadcrumb.tsx` (+ `crumbSegments` pure fn)

**Files**
- Create `frontend/src/components/oss/OSSBreadcrumb.tsx`
- Create `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx`

**Interfaces**
- Produces:
  ```ts
  export interface OssCrumb { label: string; prefix: string; isCurrent: boolean }
  export function crumbSegments(bucket: string, prefix: string): OssCrumb[]
  export interface OSSBreadcrumbProps { bucket: string; prefix: string; onNavigate: (prefix: string) => void; onRefresh: () => void }
  export function OSSBreadcrumb(props: OSSBreadcrumbProps): JSX.Element
  ```
- Consumes: `Button` from `@opskat/ui`; `RefreshCw` from `lucide-react`; `useTranslation`. Presentational — no store, no binder. (Path-split modeled on `EtcdKeyDetail.breadcrumbSegments`.)

### Steps

- [ ] **Write failing test** — `frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx`:
  ```tsx
  import { describe, it, expect, vi } from "vitest";
  import { render, screen, fireEvent } from "@testing-library/react";
  import { OSSBreadcrumb, crumbSegments } from "../OSSBreadcrumb";

  describe("crumbSegments", () => {
    it("returns only the bucket crumb at the root", () => {
      expect(crumbSegments("mb", "")).toEqual([{ label: "mb", prefix: "", isCurrent: true }]);
    });
    it("splits a trailing-slash prefix into cumulative crumbs, last is current", () => {
      expect(crumbSegments("mb", "a/b/")).toEqual([
        { label: "mb", prefix: "", isCurrent: false },
        { label: "a", prefix: "a/", isCurrent: false },
        { label: "b", prefix: "a/b/", isCurrent: true },
      ]);
    });
  });

  describe("OSSBreadcrumb", () => {
    it("renders each crumb and navigates on click; refresh fires onRefresh", () => {
      const onNavigate = vi.fn();
      const onRefresh = vi.fn();
      render(<OSSBreadcrumb bucket="mb" prefix="a/b/" onNavigate={onNavigate} onRefresh={onRefresh} />);
      expect(screen.getByText("mb")).toBeInTheDocument();
      expect(screen.getByText("a")).toBeInTheDocument();
      expect(screen.getByText("b")).toBeInTheDocument();

      fireEvent.click(screen.getByTestId("oss-crumb-1")); // "a"
      expect(onNavigate).toHaveBeenCalledWith("a/");
      fireEvent.click(screen.getByTestId("oss-crumb-0")); // bucket root
      expect(onNavigate).toHaveBeenCalledWith("");
      fireEvent.click(screen.getByTestId("oss-refresh"));
      expect(onRefresh).toHaveBeenCalledTimes(1);
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/components/oss/__tests__/OSSBreadcrumb.test.tsx`. Expected: `Failed to resolve import "../OSSBreadcrumb"`.

- [ ] **Minimal impl** — `frontend/src/components/oss/OSSBreadcrumb.tsx`:
  ```tsx
  import { useTranslation } from "react-i18next";
  import { Button } from "@opskat/ui";
  import { RefreshCw } from "lucide-react";

  export interface OssCrumb {
    label: string;
    prefix: string;
    isCurrent: boolean;
  }

  /** bucket + "a/b/" -> [bucket(""), a("a/"), b("a/b/")]，最后一段为当前。 */
  export function crumbSegments(bucket: string, prefix: string): OssCrumb[] {
    const parts = prefix.split("/").filter(Boolean);
    const crumbs: OssCrumb[] = [{ label: bucket, prefix: "", isCurrent: parts.length === 0 }];
    let acc = "";
    parts.forEach((part, i) => {
      acc += `${part}/`;
      crumbs.push({ label: part, prefix: acc, isCurrent: i === parts.length - 1 });
    });
    return crumbs;
  }

  export interface OSSBreadcrumbProps {
    bucket: string;
    prefix: string;
    onNavigate: (prefix: string) => void;
    onRefresh: () => void;
  }

  export function OSSBreadcrumb({ bucket, prefix, onNavigate, onRefresh }: OSSBreadcrumbProps) {
    const { t } = useTranslation();
    const crumbs = crumbSegments(bucket, prefix);
    return (
      <div className="flex items-center gap-1 border-b px-3 py-1.5 text-xs" data-testid="oss-breadcrumb">
        <div className="flex min-w-0 flex-1 items-center gap-0.5 font-mono">
          {crumbs.map((c, i) => (
            <span key={c.prefix} className="flex items-center gap-0.5">
              {i > 0 && <span className="text-muted-foreground/40">/</span>}
              <button
                type="button"
                className={c.isCurrent ? "font-semibold text-foreground" : "text-muted-foreground hover:text-foreground"}
                onClick={() => onNavigate(c.prefix)}
                data-testid={`oss-crumb-${i}`}
              >
                {c.label}
              </button>
            </span>
          ))}
        </div>
        <Button size="sm" variant="outline" className="shrink-0" onClick={onRefresh} data-testid="oss-refresh">
          <RefreshCw className="size-3" /> {t("oss.browser.refresh")}
        </Button>
      </div>
    );
  }
  ```

- [ ] **Run — passes** — `pnpm test src/components/oss/__tests__/OSSBreadcrumb.test.tsx`, then `npx tsc -b`, then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/components/oss/OSSBreadcrumb.tsx frontend/src/components/oss/__tests__/OSSBreadcrumb.test.tsx
  git status --short
  git commit -m "✨ OSS 面包屑组件与路径分段"
  ```

---

## Task 5 — `OSSObjectList.tsx` (rows, folder nav, multi-select, page predicate)

**Files**
- Create `frontend/src/components/oss/OSSObjectList.tsx`
- Create `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx`

**Interfaces**
- Produces:
  ```ts
  export function formatBytes(size: number): string
  export function shouldLoadNextPage(scrollTop: number, clientHeight: number, scrollHeight: number, truncated: boolean, loadingPage: boolean, threshold?: number): boolean
  export interface OSSObjectListProps {
    prefixes: string[];
    objects: oss_svc.ObjectItem[];
    selection: Set<string>;
    loading: boolean;
    loadingPage: boolean;
    truncated: boolean;
    onNavigatePrefix: (prefix: string) => void;
    onToggleSelect: (key: string) => void;
    onScrollNearBottom: () => void;
  }
  export function OSSObjectList(props: OSSObjectListProps): JSX.Element
  ```
- Consumes: `Checkbox` from `@opskat/ui`; `Folder`, `FileText` from `lucide-react`; `prefixLeafName` from `@/lib/ossPrefixTree`; `oss_svc.ObjectItem` from `../../../wailsjs/go/models`; `useTranslation`. Presentational.
- **happy-dom honesty:** scroll metrics are stubbed constants, so the scroll-to-bottom → `onScrollNearBottom` wiring is NOT reliably observable. The decision is extracted into `shouldLoadNextPage` and unit-tested; the actual `onScroll` binding is verified manually (spec §9 observational: scroll a truncated MinIO listing → more rows load).

### Steps

- [ ] **Write failing test** — `frontend/src/components/oss/__tests__/OSSObjectList.test.tsx`:
  ```tsx
  import { describe, it, expect, vi } from "vitest";
  import { render, screen, fireEvent } from "@testing-library/react";
  import { OSSObjectList, formatBytes, shouldLoadNextPage } from "../OSSObjectList";
  import type { oss_svc } from "../../../../wailsjs/go/models";

  function obj(key: string, size: number): oss_svc.ObjectItem {
    return { key, size, lastModified: 1751811127, etag: "", storageClass: "STANDARD", contentType: "", isPrefix: false } as oss_svc.ObjectItem;
  }

  describe("formatBytes", () => {
    it("formats bytes / KB / MB", () => {
      expect(formatBytes(0)).toBe("0 B");
      expect(formatBytes(512)).toBe("512 B");
      expect(formatBytes(1024)).toBe("1.0 KB");
      expect(formatBytes(1536)).toBe("1.5 KB");
      expect(formatBytes(1048576)).toBe("1.0 MB");
    });
  });

  describe("shouldLoadNextPage", () => {
    it("is false when not truncated or already loading a page", () => {
      expect(shouldLoadNextPage(900, 100, 1000, false, false)).toBe(false);
      expect(shouldLoadNextPage(900, 100, 1000, true, true)).toBe(false);
    });
    it("is true only near the bottom of a truncated list", () => {
      expect(shouldLoadNextPage(900, 100, 1000, true, false)).toBe(true);
      expect(shouldLoadNextPage(100, 100, 1000, true, false)).toBe(false);
    });
  });

  describe("OSSObjectList", () => {
    const base = {
      selection: new Set<string>(),
      loading: false,
      loadingPage: false,
      truncated: false,
      onNavigatePrefix: vi.fn(),
      onToggleSelect: vi.fn(),
      onScrollNearBottom: vi.fn(),
    };

    it("renders folder rows and object rows with leaf names + size", () => {
      render(<OSSObjectList {...base} prefixes={["docs/sub/"]} objects={[obj("docs/readme.txt", 1536)]} />);
      expect(screen.getByText("sub")).toBeInTheDocument();
      expect(screen.getByText("readme.txt")).toBeInTheDocument();
      expect(screen.getByText("1.5 KB")).toBeInTheDocument();
      expect(screen.getByText("STANDARD")).toBeInTheDocument();
    });

    it("double-clicking a folder navigates into it", () => {
      const onNavigatePrefix = vi.fn();
      render(<OSSObjectList {...base} onNavigatePrefix={onNavigatePrefix} prefixes={["docs/sub/"]} objects={[]} />);
      fireEvent.doubleClick(screen.getByTestId("oss-folder-docs/sub/"));
      expect(onNavigatePrefix).toHaveBeenCalledWith("docs/sub/");
    });

    it("clicking an object checkbox toggles its selection", () => {
      const onToggleSelect = vi.fn();
      render(<OSSObjectList {...base} onToggleSelect={onToggleSelect} prefixes={[]} objects={[obj("docs/a.txt", 1)]} />);
      fireEvent.click(screen.getByTestId("oss-select-docs/a.txt"));
      expect(onToggleSelect).toHaveBeenCalledWith("docs/a.txt");
    });

    it("shows an empty-folder placeholder when there are no prefixes and no objects", () => {
      render(<OSSObjectList {...base} prefixes={[]} objects={[]} />);
      expect(screen.getByTestId("oss-list-empty")).toHaveTextContent("oss.browser.emptyDir");
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx`. Expected: `Failed to resolve import "../OSSObjectList"`.

- [ ] **Minimal impl** — `frontend/src/components/oss/OSSObjectList.tsx`:
  ```tsx
  import type React from "react";
  import { useTranslation } from "react-i18next";
  import { Checkbox } from "@opskat/ui";
  import { Folder, FileText } from "lucide-react";
  import type { oss_svc } from "../../../wailsjs/go/models";
  import { prefixLeafName } from "@/lib/ossPrefixTree";

  export function formatBytes(size: number): string {
    if (size < 1024) return `${size} B`;
    const units = ["KB", "MB", "GB", "TB"];
    let val = size / 1024;
    let i = 0;
    while (val >= 1024 && i < units.length - 1) {
      val /= 1024;
      i++;
    }
    return `${val.toFixed(1)} ${units[i]}`;
  }

  /** 抽出滚动到底判定，happy-dom 无布局无法驱动真实 scroll —— 单测这个纯函数，滚动绑定人工验证。 */
  export function shouldLoadNextPage(
    scrollTop: number,
    clientHeight: number,
    scrollHeight: number,
    truncated: boolean,
    loadingPage: boolean,
    threshold = 48
  ): boolean {
    if (!truncated || loadingPage) return false;
    return scrollTop + clientHeight >= scrollHeight - threshold;
  }

  export interface OSSObjectListProps {
    prefixes: string[];
    objects: oss_svc.ObjectItem[];
    selection: Set<string>;
    loading: boolean;
    loadingPage: boolean;
    truncated: boolean;
    onNavigatePrefix: (prefix: string) => void;
    onToggleSelect: (key: string) => void;
    onScrollNearBottom: () => void;
  }

  export function OSSObjectList({
    prefixes,
    objects,
    selection,
    loading,
    loadingPage,
    truncated,
    onNavigatePrefix,
    onToggleSelect,
    onScrollNearBottom,
  }: OSSObjectListProps) {
    const { t } = useTranslation();

    const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
      const el = e.currentTarget;
      if (shouldLoadNextPage(el.scrollTop, el.clientHeight, el.scrollHeight, truncated, loadingPage)) {
        onScrollNearBottom();
      }
    };

    if (loading) {
      return (
        <div className="p-3 text-xs text-muted-foreground" data-testid="oss-list-loading">
          {t("oss.browser.loading")}
        </div>
      );
    }
    if (prefixes.length === 0 && objects.length === 0) {
      return (
        <div className="p-6 text-center text-xs text-muted-foreground" data-testid="oss-list-empty">
          {t("oss.browser.emptyDir")}
        </div>
      );
    }

    return (
      <div className="min-h-0 flex-1 overflow-auto" onScroll={handleScroll} data-testid="oss-object-list">
        <table className="w-full text-xs">
          <thead className="sticky top-0 bg-muted/30 text-left text-muted-foreground">
            <tr>
              <th className="w-6 px-2 py-1" />
              <th className="px-2 py-1">{t("oss.browser.colName")}</th>
              <th className="px-2 py-1">{t("oss.browser.colSize")}</th>
              <th className="px-2 py-1">{t("oss.browser.colStorageClass")}</th>
              <th className="px-2 py-1">{t("oss.browser.colModified")}</th>
            </tr>
          </thead>
          <tbody>
            {prefixes.map((p) => (
              <tr
                key={p}
                className="cursor-pointer hover:bg-accent/50"
                onDoubleClick={() => onNavigatePrefix(p)}
                data-testid={`oss-folder-${p}`}
              >
                <td className="px-2 py-1" />
                <td className="px-2 py-1">
                  <span className="flex items-center gap-1">
                    <Folder className="size-3 text-warning" />
                    {prefixLeafName(p)}
                  </span>
                </td>
                <td className="px-2 py-1 text-muted-foreground">—</td>
                <td className="px-2 py-1 text-muted-foreground">—</td>
                <td className="px-2 py-1 text-muted-foreground">—</td>
              </tr>
            ))}
            {objects.map((o) => (
              <tr key={o.key} className="hover:bg-accent/50" data-testid={`oss-object-${o.key}`}>
                <td className="px-2 py-1">
                  <Checkbox
                    checked={selection.has(o.key)}
                    onCheckedChange={() => onToggleSelect(o.key)}
                    data-testid={`oss-select-${o.key}`}
                  />
                </td>
                <td className="px-2 py-1">
                  <span className="flex items-center gap-1">
                    <FileText className="size-3 text-muted-foreground" />
                    {prefixLeafName(o.key)}
                  </span>
                </td>
                <td className="px-2 py-1 text-muted-foreground">{formatBytes(o.size)}</td>
                <td className="px-2 py-1 text-muted-foreground">{o.storageClass || "—"}</td>
                <td className="px-2 py-1 text-muted-foreground">
                  {o.lastModified ? new Date(o.lastModified * 1000).toLocaleString() : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {loadingPage && (
          <div className="p-2 text-center text-xs text-muted-foreground" data-testid="oss-list-page-spinner">
            {t("oss.browser.loadingMore")}
          </div>
        )}
      </div>
    );
  }
  ```

- [ ] **Run — passes** — `pnpm test src/components/oss/__tests__/OSSObjectList.test.tsx`, then `npx tsc -b`, then `pnpm lint`. (Radix `Checkbox` fires `onCheckedChange` on its underlying button click, so the toggle assertion is real, not assert-nothing — do NOT weaken it if it's flaky; fix the query/fireEvent target instead.)

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/components/oss/OSSObjectList.tsx frontend/src/components/oss/__tests__/OSSObjectList.test.tsx
  git status --short
  git commit -m "✨ OSS 对象列表 行渲染/文件夹导航/多选/翻页判定"
  ```

---

## Task 6 — `OSSBucketTree.tsx` (bucket list + lazy prefix rows)

**Files**
- Create `frontend/src/components/oss/OSSBucketTree.tsx`
- Create `frontend/src/components/oss/__tests__/OSSBucketTree.test.tsx`

**Interfaces**
- Produces:
  ```ts
  export interface OSSBucketTreeProps {
    buckets: oss_svc.BucketItem[] | null;
    currentBucket: string;
    rows: OssPrefixRow[];              // from flattenPrefixTree(tree, expanded) — supplied by the panel
    loadingBuckets: boolean;
    onSelectBucket: (bucket: string) => void;
    onToggleExpand: (prefix: string) => void;
    onNavigatePrefix: (prefix: string) => void;
  }
  export function OSSBucketTree(props: OSSBucketTreeProps): JSX.Element
  ```
- Consumes: `ChevronRight`, `ChevronDown`, `Folder`, `Database`, `Loader2` from `lucide-react`; `OssPrefixRow` (type) from `@/lib/ossPrefixTree` (T1); `oss_svc.BucketItem` from `../../../wailsjs/go/models`; `useTranslation`. Presentational — the panel (T7) computes `rows` via `flattenPrefixTree`.

### Steps

- [ ] **Write failing test** — `frontend/src/components/oss/__tests__/OSSBucketTree.test.tsx`:
  ```tsx
  import { describe, it, expect, vi } from "vitest";
  import { render, screen, fireEvent } from "@testing-library/react";
  import { OSSBucketTree } from "../OSSBucketTree";
  import type { OssPrefixRow } from "@/lib/ossPrefixTree";
  import type { oss_svc } from "../../../../wailsjs/go/models";

  const buckets: oss_svc.BucketItem[] = [
    { name: "b1", creationDate: 0 } as oss_svc.BucketItem,
    { name: "b2", creationDate: 0 } as oss_svc.BucketItem,
  ];
  const rows: OssPrefixRow[] = [{ depth: 0, name: "docs", prefix: "docs/", isExpanded: false, loaded: false }];
  const base = {
    loadingBuckets: false,
    onSelectBucket: vi.fn(),
    onToggleExpand: vi.fn(),
    onNavigatePrefix: vi.fn(),
  };

  describe("OSSBucketTree", () => {
    it("renders buckets and the selected bucket's prefix rows", () => {
      render(<OSSBucketTree {...base} buckets={buckets} currentBucket="b1" rows={rows} />);
      expect(screen.getByText("b1")).toBeInTheDocument();
      expect(screen.getByText("b2")).toBeInTheDocument();
      expect(screen.getByText("docs")).toBeInTheDocument();
    });

    it("wires select / expand / navigate callbacks", () => {
      const onSelectBucket = vi.fn();
      const onToggleExpand = vi.fn();
      const onNavigatePrefix = vi.fn();
      render(
        <OSSBucketTree
          {...base}
          buckets={buckets}
          currentBucket="b1"
          rows={rows}
          onSelectBucket={onSelectBucket}
          onToggleExpand={onToggleExpand}
          onNavigatePrefix={onNavigatePrefix}
        />
      );
      fireEvent.click(screen.getByTestId("oss-bucket-b2"));
      expect(onSelectBucket).toHaveBeenCalledWith("b2");
      fireEvent.click(screen.getByTestId("oss-tree-toggle-docs/"));
      expect(onToggleExpand).toHaveBeenCalledWith("docs/");
      fireEvent.click(screen.getByTestId("oss-tree-nav-docs/"));
      expect(onNavigatePrefix).toHaveBeenCalledWith("docs/");
    });

    it("shows the no-buckets placeholder for an empty account", () => {
      render(<OSSBucketTree {...base} buckets={[]} currentBucket="" rows={[]} />);
      expect(screen.getByTestId("oss-buckets-empty")).toHaveTextContent("oss.browser.noBuckets");
    });

    it("shows a skeleton while buckets are loading and none are known yet", () => {
      render(<OSSBucketTree {...base} buckets={null} currentBucket="" rows={[]} loadingBuckets />);
      expect(screen.getByTestId("oss-buckets-loading")).toBeInTheDocument();
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/components/oss/__tests__/OSSBucketTree.test.tsx`. Expected: `Failed to resolve import "../OSSBucketTree"`.

- [ ] **Minimal impl** — `frontend/src/components/oss/OSSBucketTree.tsx`:
  ```tsx
  import { useTranslation } from "react-i18next";
  import { ChevronRight, ChevronDown, Folder, Database, Loader2 } from "lucide-react";
  import type { oss_svc } from "../../../wailsjs/go/models";
  import type { OssPrefixRow } from "@/lib/ossPrefixTree";

  export interface OSSBucketTreeProps {
    buckets: oss_svc.BucketItem[] | null;
    currentBucket: string;
    rows: OssPrefixRow[];
    loadingBuckets: boolean;
    onSelectBucket: (bucket: string) => void;
    onToggleExpand: (prefix: string) => void;
    onNavigatePrefix: (prefix: string) => void;
  }

  export function OSSBucketTree({
    buckets,
    currentBucket,
    rows,
    loadingBuckets,
    onSelectBucket,
    onToggleExpand,
    onNavigatePrefix,
  }: OSSBucketTreeProps) {
    const { t } = useTranslation();

    if (loadingBuckets && buckets === null) {
      return (
        <div className="flex items-center gap-1 p-3 text-xs text-muted-foreground" data-testid="oss-buckets-loading">
          <Loader2 className="size-3 animate-spin" /> {t("oss.browser.loading")}
        </div>
      );
    }
    if (buckets !== null && buckets.length === 0) {
      return (
        <div className="p-3 text-xs text-muted-foreground" data-testid="oss-buckets-empty">
          {t("oss.browser.noBuckets")}
        </div>
      );
    }

    return (
      <div className="flex h-full flex-col overflow-auto text-xs" data-testid="oss-bucket-tree">
        {(buckets ?? []).map((b) => {
          const selected = b.name === currentBucket;
          return (
            <div key={b.name}>
              <button
                type="button"
                className={`flex w-full items-center gap-1 px-2 py-1 text-left hover:bg-accent/50 ${selected ? "bg-accent font-medium" : ""}`}
                onClick={() => onSelectBucket(b.name)}
                data-testid={`oss-bucket-${b.name}`}
              >
                <Database className="size-3 text-muted-foreground" /> {b.name}
              </button>
              {selected &&
                rows.map((row) => (
                  <div
                    key={row.prefix}
                    className="flex items-center gap-1 py-0.5 hover:bg-accent/40"
                    style={{ paddingLeft: 12 + row.depth * 12 }}
                    data-testid={`oss-tree-row-${row.prefix}`}
                  >
                    <button
                      type="button"
                      className="shrink-0"
                      onClick={() => onToggleExpand(row.prefix)}
                      data-testid={`oss-tree-toggle-${row.prefix}`}
                    >
                      {row.isExpanded ? <ChevronDown className="size-3" /> : <ChevronRight className="size-3" />}
                    </button>
                    <button
                      type="button"
                      className="flex min-w-0 flex-1 items-center gap-1 text-left"
                      onClick={() => onNavigatePrefix(row.prefix)}
                      data-testid={`oss-tree-nav-${row.prefix}`}
                    >
                      <Folder className="size-3 text-warning" />
                      <span className="truncate">{row.name}</span>
                    </button>
                  </div>
                ))}
            </div>
          );
        })}
      </div>
    );
  }
  ```

- [ ] **Run — passes** — `pnpm test src/components/oss/__tests__/OSSBucketTree.test.tsx`, then `npx tsc -b`, then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/components/oss/OSSBucketTree.tsx frontend/src/components/oss/__tests__/OSSBucketTree.test.tsx
  git status --short
  git commit -m "✨ OSS Bucket 列表与懒前缀树行"
  ```

---

## Task 7 — `OSSBrowserPanel.tsx` (container shell) + `setup.ts` oss binder mock

**Files**
- Create `frontend/src/components/query/OSSBrowserPanel.tsx`
- Create `frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx`
- Modify `frontend/src/__tests__/setup.ts` (register the `oss` binder mock so component tests that render the real store don't hit `window.go`)

**Interfaces**
- Produces: `export interface OSSBrowserPanelProps { tabId: string }` / `export function OSSBrowserPanel(props): JSX.Element`.
- Consumes: `useResizeHandle`, `ConfirmDialog`, `Button` from `@opskat/ui`; `useTabStore` + `QueryTabMeta` from `@/stores/tabStore`; `useOssBrowserStore` from `@/stores/ossBrowserStore`; `flattenPrefixTree` from `@/lib/ossPrefixTree`; `notifySuccess` from `@/lib/notify`; `toast` from `sonner`; `OSSBucketTree` / `OSSBreadcrumb` / `OSSObjectList` from `@/components/oss/*`; `Trash2` from `lucide-react`. Mirrors `EtcdPanel`'s shell (reads `meta.assetId` from `tabStore`; `useResizeHandle({defaultSize:260,minSize:180,maxSize:480,targetRef})` → `{size, handleMouseDown}`; `<div className="w-1 ... cursor-col-resize" onMouseDown={handleMouseDown} />`).
- **happy-dom honesty:** the resize drag, Radix `ConfirmDialog` portal + `notifySuccess` (sonner) toast, and scroll pagination are NOT reliably observable under happy-dom. The panel test asserts only the observable, deterministic wiring (mount → `loadBuckets(assetId)`; buckets render; missing-asset guard). The delete-confirm + toast flow and drag are verified manually per spec §9 (run app → MinIO → select → delete → read `logs/opskat.log` / `audit_logs`).

### Steps

- [ ] **Register the oss binder mock in `frontend/src/__tests__/setup.ts`** (add alongside the other `vi.mock(... mockBinderModule(...))` lines, right after the `etcd` line):
  ```ts
  vi.mock("../../wailsjs/go/oss/OSS", () => mockBinderModule("../../wailsjs/go/oss/OSS"));
  ```
  (After the P0 regen, `OSS.js` exports all `OSS*` methods, so `mockBinderModule`'s `importActual` yields a complete default-`undefined` mock; the panel test overrides `OSSListBuckets` via `vi.mocked`.)

- [ ] **Write failing test** — `frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx`:
  ```tsx
  import { describe, it, expect, vi, beforeEach } from "vitest";
  import { render, screen, waitFor } from "@testing-library/react";
  import { OSSListBuckets } from "../../../../wailsjs/go/oss/OSS";
  import { OSSBrowserPanel } from "../OSSBrowserPanel";
  import { useTabStore } from "@/stores/tabStore";
  import { useOssBrowserStore } from "@/stores/ossBrowserStore";

  const TAB = "query-7";

  beforeEach(() => {
    vi.mocked(OSSListBuckets).mockReset();
    useOssBrowserStore.setState({ tabs: {} });
    useTabStore.setState({
      tabs: [{ id: TAB, type: "query", label: "b", meta: { type: "query", assetId: 7, assetName: "b", assetIcon: "", assetType: "oss" } }],
      activeTabId: TAB,
    });
  });

  describe("OSSBrowserPanel", () => {
    it("loads buckets on mount and renders them in the tree", async () => {
      vi.mocked(OSSListBuckets).mockResolvedValue([{ name: "b1", creationDate: 0 }] as never);
      render(<OSSBrowserPanel tabId={TAB} />);
      await waitFor(() => expect(OSSListBuckets).toHaveBeenCalledWith(7));
      expect(await screen.findByTestId("oss-bucket-b1")).toBeInTheDocument();
      expect(screen.getByTestId("oss-browser-panel")).toBeInTheDocument();
    });

    it("guards against a missing asset id", () => {
      useTabStore.setState({ tabs: [], activeTabId: null });
      render(<OSSBrowserPanel tabId="nope" />);
      expect(screen.getByText("oss.browser.missingAsset")).toBeInTheDocument();
    });
  });
  ```

- [ ] **Run — fails** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`. Expected: `Failed to resolve import "../OSSBrowserPanel"`.

- [ ] **Minimal impl** — `frontend/src/components/query/OSSBrowserPanel.tsx`:
  ```tsx
  import { useCallback, useEffect, useMemo, useRef, useState } from "react";
  import { useTranslation } from "react-i18next";
  import { toast } from "sonner";
  import { Trash2 } from "lucide-react";
  import { useResizeHandle, ConfirmDialog, Button } from "@opskat/ui";
  import { useTabStore, type QueryTabMeta } from "@/stores/tabStore";
  import { useOssBrowserStore } from "@/stores/ossBrowserStore";
  import { flattenPrefixTree } from "@/lib/ossPrefixTree";
  import { notifySuccess } from "@/lib/notify";
  import { OSSBucketTree } from "@/components/oss/OSSBucketTree";
  import { OSSBreadcrumb } from "@/components/oss/OSSBreadcrumb";
  import { OSSObjectList } from "@/components/oss/OSSObjectList";

  export interface OSSBrowserPanelProps {
    tabId: string;
  }

  export function OSSBrowserPanel({ tabId }: OSSBrowserPanelProps) {
    const { t } = useTranslation();
    const tab = useTabStore((s) => s.tabs.find((tt) => tt.id === tabId));
    const meta = tab?.meta as QueryTabMeta | undefined;
    const assetId = meta?.assetId;

    const state = useOssBrowserStore((s) => s.tabs[tabId]);
    const loadBuckets = useOssBrowserStore((s) => s.loadBuckets);
    const selectBucket = useOssBrowserStore((s) => s.selectBucket);
    const navigateToPrefix = useOssBrowserStore((s) => s.navigateToPrefix);
    const expandNode = useOssBrowserStore((s) => s.expandNode);
    const loadNextPage = useOssBrowserStore((s) => s.loadNextPage);
    const toggleSelect = useOssBrowserStore((s) => s.toggleSelect);
    const deleteSelected = useOssBrowserStore((s) => s.deleteSelected);
    const refresh = useOssBrowserStore((s) => s.refresh);

    const [confirmOpen, setConfirmOpen] = useState(false);

    const fail = useCallback((e: unknown) => toast.error(`${t("oss.browser.loadFailed")}: ${String(e)}`), [t]);

    useEffect(() => {
      if (assetId) void loadBuckets(tabId, assetId).catch(fail);
    }, [assetId, tabId, loadBuckets, fail]);

    const rows = useMemo(() => (state ? flattenPrefixTree(state.tree, state.expanded, "") : []), [state]);

    const sidebarRef = useRef<HTMLDivElement>(null);
    const { size: sidebarWidth, handleMouseDown } = useResizeHandle({
      defaultSize: 260,
      minSize: 180,
      maxSize: 480,
      targetRef: sidebarRef,
    });

    const onNavigate = useCallback((prefix: string) => void navigateToPrefix(tabId, prefix).catch(fail), [navigateToPrefix, tabId, fail]);
    const onExpand = useCallback((prefix: string) => void expandNode(tabId, prefix).catch(fail), [expandNode, tabId, fail]);
    const onSelectBucket = useCallback((bucket: string) => void selectBucket(tabId, bucket).catch(fail), [selectBucket, tabId, fail]);

    const confirmDelete = async () => {
      setConfirmOpen(false);
      try {
        await deleteSelected(tabId);
        notifySuccess(t("oss.browser.deleteSuccess"));
      } catch (e) {
        toast.error(`${t("oss.browser.deleteFailed")}: ${String(e)}`);
      }
    };

    if (!assetId) {
      return <div className="p-3 text-xs text-destructive">{t("oss.browser.missingAsset")}</div>;
    }

    const selectionCount = state?.selection.size ?? 0;
    const confirmBody =
      selectionCount === 1
        ? t("oss.browser.deleteConfirmOne", { key: Array.from(state?.selection ?? [])[0] })
        : t("oss.browser.deleteConfirmMany", { count: selectionCount });

    return (
      <div className="flex h-full w-full flex-col" data-testid="oss-browser-panel">
        <div className="flex min-h-0 flex-1">
          {/* Left: bucket list + lazy prefix tree */}
          <div ref={sidebarRef} className="shrink-0 border-r" style={{ width: sidebarWidth }}>
            <OSSBucketTree
              buckets={state?.buckets ?? null}
              currentBucket={state?.currentBucket ?? ""}
              rows={rows}
              loadingBuckets={state?.loading.buckets ?? false}
              onSelectBucket={onSelectBucket}
              onToggleExpand={onExpand}
              onNavigatePrefix={onNavigate}
            />
          </div>

          {/* Resize handle */}
          <div
            className="w-1 shrink-0 cursor-col-resize hover:bg-accent active:bg-accent"
            onMouseDown={handleMouseDown}
          />

          {/* Right: breadcrumb + (selection bar) + object list */}
          <div className="flex min-w-0 flex-1 flex-col">
            {state?.currentBucket ? (
              <>
                <OSSBreadcrumb
                  bucket={state.currentBucket}
                  prefix={state.currentPrefix}
                  onNavigate={onNavigate}
                  onRefresh={() => void refresh(tabId).catch(fail)}
                />
                {selectionCount > 0 && (
                  <div className="flex items-center gap-2 border-b bg-muted/20 px-3 py-1 text-xs" data-testid="oss-selection-bar">
                    <span>{t("oss.browser.selectedCount", { count: selectionCount })}</span>
                    <Button size="sm" variant="destructive" onClick={() => setConfirmOpen(true)} data-testid="oss-delete-selected">
                      <Trash2 className="size-3" /> {t("oss.browser.deleteSelected")}
                    </Button>
                  </div>
                )}
                <OSSObjectList
                  prefixes={state.listing?.prefixes ?? []}
                  objects={state.listing?.objects ?? []}
                  selection={state.selection}
                  loading={state.loading.listing}
                  loadingPage={state.loading.page}
                  truncated={state.listing?.truncated ?? false}
                  onNavigatePrefix={onNavigate}
                  onToggleSelect={(key) => toggleSelect(tabId, key)}
                  onScrollNearBottom={() => void loadNextPage(tabId).catch(fail)}
                />
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center text-xs text-muted-foreground" data-testid="oss-no-bucket">
                {t("oss.browser.selectBucket")}
              </div>
            )}
          </div>
        </div>

        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t("oss.browser.deleteConfirmTitle")}
          description={confirmBody}
          cancelText={t("action.cancel")}
          confirmText={t("action.confirm")}
          onConfirm={() => void confirmDelete()}
        />
      </div>
    );
  }
  ```

- [ ] **Run — passes** — `pnpm test src/components/query/__tests__/OSSBrowserPanel.test.tsx`, then `npx tsc -b`, then `pnpm lint`.

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/components/query/OSSBrowserPanel.tsx frontend/src/components/query/__tests__/OSSBrowserPanel.test.tsx frontend/src/__tests__/setup.ts
  git status --short
  git commit -m "✨ OSS 对象浏览器面板壳 装配树/面包屑/列表与删除确认"
  ```

---

## Task 8 — Tab wiring (flip `oss.ts`, `MainPanel` branch, update `registry.test.ts`)

**Files**
- Modify `frontend/src/lib/assetTypes/oss.ts` (flip `canConnect` → `true`; see the decision note on `canConnectInNewTab`)
- Modify `frontend/src/components/layout/MainPanel.tsx` (lazy-import `OSSBrowserPanel` + add the `oss` branch in the query ternary)
- Modify `frontend/src/lib/assetTypes/__tests__/registry.test.ts` (update the oss "本期仅新建/编辑/测试" assertions)
- (No change to `frontend/src/App.tsx` — the generic `connectAction==="query"` path at `:287-289` already routes oss. No change to `frontend/src/__tests__/assetTypeOptions.test.ts` — it asserts ordering/category only, not `canConnect`.)

**⚠️ Decision — `canConnectInNewTab` (spec §3 says flip to `true`, codebase says keep `false`).** `App.tsx handleConnectAssetInNewTab` (`:321-328`) gates ONLY on `canConnectInNewTab` and then calls `connect(asset, "", true)` — the **terminal** connect path, which is NOT `connectAction`-aware. Every other query-type asset (`database`/`redis`/`mongodb`/`kafka`/`k8s`/`etcd`) keeps `canConnectInNewTab: false` for exactly this reason. Flipping oss to `true` would let an "open in new tab" action mis-route a query asset into a terminal session. **This plan flips `canConnect: true` and keeps `canConnectInNewTab: false`** (matching the query siblings; avoids a known mis-route), and updates `registry.test.ts` to `canConnect === true`, `canConnectInNewTab === false`. Flagged for the controller to reconcile with the spec author; if new-tab OSS is truly wanted, `handleConnectAssetInNewTab` must first become `connectAction`-aware (out of P3b-1 scope).

**Interfaces**
- Consumes `OSSBrowserPanel` from `@/components/query/OSSBrowserPanel` (T7).

### Steps

- [ ] **Write failing test** — update the oss block in `frontend/src/lib/assetTypes/__tests__/registry.test.ts` (lines 90-94) to the new contract and sharpen the description:
  ```ts
  it("oss 支持连接（对象浏览器已落地），单例 query tab 不支持新标签", () => {
    expect(getAssetType("oss")!.canConnect).toBe(true);
    expect(getAssetType("oss")!.canConnectInNewTab).toBe(false);
    expect(getAssetType("oss")!.testable).toBe(true);
  });
  ```

- [ ] **Run — fails** — `pnpm test src/lib/assetTypes/__tests__/registry.test.ts`. Expected: `expected false to be true` on the `canConnect` assertion (oss is still `canConnect:false`).

- [ ] **Minimal impl — flip the registration** in `frontend/src/lib/assetTypes/oss.ts`:
  ```ts
  registerAssetType({
    type: "oss",
    icon: S3Icon,
    aliases: ["oss"],
    label: "nav.oss",
    category: "databases",
    // P3b-1 对象浏览器已落地：canConnect 开启双击/右键「连接」→ 通用 query 路径（App.tsx :287）。
    // canConnectInNewTab 保持 false —— 与其它 query 资产一致；新标签路径 handleConnectAssetInNewTab
    // 仅走 terminal connect()，对 query 资产会误路由（见 plan T8 决策，需要新标签需先改造该 handler）。
    canConnect: true,
    canConnectInNewTab: false,
    connectAction: "query",
    DetailInfoCard: OSSDetailInfoCard,
    ConfigSection: OSSConfigSection,
    testable: true,
    // 后端 PolicyKind()=="" / DefaultPolicy()==nil —— OSS 无 policy。
    policy: undefined,
  });
  ```

- [ ] **Minimal impl — add the render branch** in `frontend/src/components/layout/MainPanel.tsx`. Add the lazy import next to the other panels (after the `EtcdPanel` lazy line ~43):
  ```ts
  const OSSBrowserPanel = lazy(() =>
    import("@/components/query/OSSBrowserPanel").then((m) => ({ default: m.OSSBrowserPanel }))
  );
  ```
  Extend the query ternary (currently `... : meta.assetType === "etcd" ? (<EtcdPanel .../>) : (<MongoDBPanel .../>)`, lines ~307-311) to add the oss arm before the `MongoDBPanel` fallback:
  ```tsx
  ) : meta.assetType === "etcd" ? (
    <EtcdPanel tabId={tab.id} />
  ) : meta.assetType === "oss" ? (
    <OSSBrowserPanel tabId={tab.id} />
  ) : (
    <MongoDBPanel tabId={tab.id} />
  )}
  ```

- [ ] **Run — passes** — `pnpm test src/lib/assetTypes/__tests__/registry.test.ts` (green). Then the FULL gate set:
  - `pnpm test` (full suite — confirms `i18n.test.ts` parity/coverage green with all `oss.browser.*` literals now referenced, and every prior spec still passes).
  - `npx tsc -b`
  - `pnpm lint`

- [ ] **Commit**
  ```bash
  git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml
  git add frontend/src/lib/assetTypes/oss.ts frontend/src/components/layout/MainPanel.tsx frontend/src/lib/assetTypes/__tests__/registry.test.ts
  git status --short
  git commit -m "🔌 接线 OSS 资产连接到对象浏览器面板"
  ```

---

## Final verification (after Task 8)

- [ ] From `frontend/`: `pnpm test` (full, all green) · `npx tsc -b` · `pnpm lint`, then `git checkout -- frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml` and confirm `git status --short` is clean of generated/lockfile churn.
- [ ] **Observational verification (spec §9 — observe, don't assert):** run the app (`make dev`), double-click an OSS asset to connect → the browser tab opens; browse Buckets + a lazy prefix, scroll a truncated listing to page, select one and several objects → delete → confirm; read `logs/opskat.log` (`oss remove objects` key-flow line) and `opskat.db` `audit_logs` to confirm the side effects. Requires a local MinIO OSS asset.

## Spec §3–§9 → task coverage map

- §3 tab wiring: oss.ts flip **T8**; tabStore/queryStore unions **T3**; MainPanel branch **T8**; App.tsx unchanged **T8 note**; i18n `oss.browser.*` **T2**.
- §4 components: `OSSBrowserPanel` **T7**, `OSSBucketTree` **T6**, `OSSObjectList` **T5**, `OSSBreadcrumb` **T4**, `ossPrefixTree` **T1**, `ossBrowserStore` **T3**.
- §5 state model + actions: **T3** (`deleteOne` folded into `deleteSelected` — see T3 scope note).
- §6 data flows (mount/select/navigate/paginate/refresh/delete): store **T3**, container wiring **T7**.
- §7 error/empty/loading: store `error`+rethrow **T3**; empty/skeleton placeholders **T5/T6**; toast.error + ConfirmDialog + notifySuccess **T7**.
- §8 reuse map: `EtcdPanel` shell + `useResizeHandle` **T7**; `redisKeyTree` flatten shape **T1**; `EtcdKeyDetail.breadcrumbSegments` **T4**; bindings **T3**; notify **T7**.
- §9 test strategy: pure logic **T1/T4/T5**; store **T3**; components **T4/T5/T6/T7**; i18n lockstep **T2** (+ full-suite gate **T8**); observational **Final verification**.
