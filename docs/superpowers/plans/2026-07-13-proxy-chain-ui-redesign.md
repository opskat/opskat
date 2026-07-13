# Proxy Chain UI/UX Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the proxy-chain editor as a visualized, type-colored, drag-reorderable connected path (本机 → hops → 目标) with inline-expand editing and a unified add menu, and add a read-only chain visualization to the asset detail cards (currently blank).

**Architecture:** All chain *logic* (validation, reorder, type metadata) is pure and lives in `proxyConfig.ts` (unit-tested). The editor `ConnectionMethodFields.tsx` is rewritten as a `@dnd-kit/sortable` list rendered as a vertical path with endpoint rows + a rail spine. A new `ProxyChainDetailSection` in `detail/InfoItem.tsx` renders the read-only path and is wired into the 9 existing detail cards. No backend or wire-format changes — the persisted `ProxyChainJSON` is unchanged.

**Tech Stack:** React 19 + TypeScript, `@opskat/ui` (shadcn-style: `Button`, `Select`, `DropdownMenu*`, `Input`, `cn`), `@dnd-kit/core` + `@dnd-kit/sortable` + `@dnd-kit/utilities` (already deps), `lucide-react`, `react-i18next`, Vitest + `@testing-library/react`.

**Design source of truth:** `/Users/codfrm/Code/设计稿/opskat.pen` — frames `代理链 · 重构 · 编辑器` (editor), `代理链 · 只读详情(资产卡片)` (read-only), `代理链 · 交互状态` (empty/direct, add-dropdown, drag, validation-error).

## Global Constraints

- **No wire-format change.** `ProxyChainJSON` / `ProxyChainLayerJSON` in `proxyConfig.ts` stay byte-compatible; `buildProxyChainJSON` / `parseProxyChain` output/shape unchanged.
- **Type colors (semantic tokens already in `globals.css`, no new tokens):** SSH = `primary` (blue), SOCKS5 = `success` (green), HTTP tunnel = `warning` (amber), errors = `destructive` (red). Soft fills use `/10`–`/15` (e.g. `bg-success/15`), rings use `/40`–`/50`.
- **Reuse-first:** reuse `Field` + `Segmented` (`components/asset/fields.tsx`), `AssetSelect`, `@opskat/ui` primitives, `parseDetailConfig` (`detail/utils.ts`). Do not add new dependencies.
- **i18n:** every user-facing string goes through `t(...)`; add matching keys to BOTH `src/i18n/locales/zh-CN/common.json` and `src/i18n/locales/en/common.json` under the `asset` object. Do not literal-translate — use idiomatic phrasing per language.
- **Toasts** (if any success feedback is added) go through `lib/notify.ts`, never `toast.success`.
- **Lint is all-error** (react-x rules). No `any`, no unused imports, no set-state-in-effect.
- **Commit style:** gitmoji subject, no PR/review number. Only include `#<issue>` if deliberately linking an issue.
- **Verify commands** (run from `frontend/`): `pnpm vitest run <file>` (single), `pnpm lint`, `pnpm exec tsc -b --noEmit` (typecheck). Do not gate on `pnpm build`.

---

## File Structure

- `frontend/src/components/asset/proxyConfig.ts` — **modify.** Add `layerTypeShortLabel`, `ProxyChainLayerError`, `proxyChainLayerErrors`, `reorderLayers`; refactor `proxyChainValidationKey` to delegate. Pure logic, no React/icon imports.
- `frontend/src/components/asset/__tests__/proxyConfig.test.ts` — **modify.** Add tests for the new helpers.
- `frontend/src/components/asset/ConnectionMethodFields.tsx` — **rewrite.** New chain-path editor: endpoints, rail spine, type-colored sortable hop cards, inline-expand, unified add `DropdownMenu`, empty/direct state, inline validation.
- `frontend/src/components/asset/__tests__/ConnectionMethodFields.test.tsx` — **rewrite.** Cover render, expand, add, validation display.
- `frontend/src/components/asset/detail/InfoItem.tsx` — **modify.** Add `ProxyChainDetailSection` (read-only vertical mini-path).
- `frontend/src/components/asset/detail/__tests__/InfoItem.test.tsx` — **create.** Test the read-only section.
- `frontend/src/components/asset/detail/{SSH,Database,Etcd,Kafka,Redis,RDP,K8s,MongoDB}DetailInfoCard.tsx` — **modify.** Add `proxy_chain?: ProxyChainJSON | null` to each config interface + render `<ProxyChainDetailSection>`. (OSS detail card: check for one and include if present.)
- `frontend/src/i18n/locales/{zh-CN,en}/common.json` — **modify.** New `asset.*` keys.

---

## Task 1: Proxy-chain logic helpers (`proxyConfig.ts`)

**Files:**
- Modify: `frontend/src/components/asset/proxyConfig.ts`
- Test: `frontend/src/components/asset/__tests__/proxyConfig.test.ts`

**Interfaces:**
- Produces:
  - `layerTypeShortLabel(type: ProxyChainLayerType): string` → `"SSH" | "SOCKS5" | "HTTP"`.
  - `interface ProxyChainLayerError { id: string; field: "sshAssetId"|"host"|"port"|"url"|"token"; messageKey: string }`.
  - `proxyChainLayerErrors(layers: ProxyChainLayerForm[]): ProxyChainLayerError[]` — enabled layers only, layer order, ≤1 error per field.
  - `proxyChainValidationKey(layers): string` — now delegates to `proxyChainLayerErrors(...)[0]?.messageKey ?? ""` (same first-error priority as before; consumed by the 11 `*ConfigSection.tsx` `validate` blocks — signature unchanged).
  - `reorderLayers(layers, activeId, overId): ProxyChainLayerForm[]` — move `activeId` to `overId`'s index; returns same array reference when no-op.

- [ ] **Step 1: Write failing tests**

Append to `frontend/src/components/asset/__tests__/proxyConfig.test.ts` (keep existing imports; add the new symbols to the import from `../proxyConfig`):

```ts
import {
  layerTypeShortLabel,
  proxyChainLayerErrors,
  proxyChainValidationKey,
  reorderLayers,
  sshProxyLayer,
  socks5ProxyLayer,
  httpTunnelProxyLayer,
} from "../proxyConfig";

describe("proxyChain helpers", () => {
  it("layerTypeShortLabel maps each type", () => {
    expect(layerTypeShortLabel("ssh")).toBe("SSH");
    expect(layerTypeShortLabel("socks5")).toBe("SOCKS5");
    expect(layerTypeShortLabel("http_tunnel")).toBe("HTTP");
  });

  it("proxyChainLayerErrors flags missing required fields on enabled layers only", () => {
    const ssh = sshProxyLayer(0, "b", "ssh-1"); // sshAssetId=0 -> invalid
    const socks = { ...socks5ProxyLayer(), id: "s-1", host: "", enabled: true };
    const disabled = { ...socks5ProxyLayer(), id: "s-2", host: "", enabled: false };
    const errs = proxyChainLayerErrors([ssh, socks, disabled]);
    expect(errs).toEqual([
      { id: "ssh-1", field: "sshAssetId", messageKey: "asset.proxyChainSSHRequired" },
      { id: "s-1", field: "host", messageKey: "asset.proxyChainProxyHostRequired" },
    ]);
  });

  it("proxyChainValidationKey returns first error key or empty", () => {
    expect(proxyChainValidationKey([])).toBe("");
    const bad = httpTunnelProxyLayer();
    bad.id = "h-1";
    expect(proxyChainValidationKey([bad])).toBe("asset.proxyChainHTTPURLRequired");
  });

  it("reorderLayers moves a layer and is a no-op for equal/unknown ids", () => {
    const a = { ...socks5ProxyLayer(), id: "a" };
    const b = { ...socks5ProxyLayer(), id: "b" };
    const c = { ...socks5ProxyLayer(), id: "c" };
    expect(reorderLayers([a, b, c], "a", "c").map((l) => l.id)).toEqual(["b", "c", "a"]);
    const same = [a, b, c];
    expect(reorderLayers(same, "b", "b")).toBe(same);
    expect(reorderLayers(same, "x", "c")).toBe(same);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd frontend && pnpm vitest run src/components/asset/__tests__/proxyConfig.test.ts`
Expected: FAIL — `layerTypeShortLabel`/`proxyChainLayerErrors`/`reorderLayers` are not exported.

- [ ] **Step 3: Implement the helpers**

In `frontend/src/components/asset/proxyConfig.ts`, add (place near the existing `proxyChainValidationKey`) and **replace** the current `proxyChainValidationKey` body:

```ts
export function layerTypeShortLabel(type: ProxyChainLayerType): string {
  if (type === "ssh") return "SSH";
  if (type === "http_tunnel") return "HTTP";
  return "SOCKS5";
}

export interface ProxyChainLayerError {
  id: string;
  field: "sshAssetId" | "host" | "port" | "url" | "token";
  messageKey: string;
}

/** 每个启用层的必填/取值校验;按层序返回,同一字段至多一条。 */
export function proxyChainLayerErrors(layers: ProxyChainLayerForm[]): ProxyChainLayerError[] {
  const errors: ProxyChainLayerError[] = [];
  for (const layer of layers) {
    if (!layer.enabled) continue;
    if (layer.type === "ssh" && layer.sshAssetId <= 0) {
      errors.push({ id: layer.id, field: "sshAssetId", messageKey: "asset.proxyChainSSHRequired" });
    }
    if (layer.type === "socks5" && !layer.host.trim()) {
      errors.push({ id: layer.id, field: "host", messageKey: "asset.proxyChainProxyHostRequired" });
    }
    if (layer.type === "socks5" && (layer.port <= 0 || layer.port > 65535)) {
      errors.push({ id: layer.id, field: "port", messageKey: "asset.proxyChainProxyPortInvalid" });
    }
    if (layer.type === "http_tunnel" && !layer.url.trim()) {
      errors.push({ id: layer.id, field: "url", messageKey: "asset.proxyChainHTTPURLRequired" });
    }
    if (layer.type === "http_tunnel" && !layer.token.trim() && !layer.encryptedToken) {
      errors.push({ id: layer.id, field: "token", messageKey: "asset.proxyChainHTTPTokenRequired" });
    }
  }
  return errors;
}

export function proxyChainValidationKey(layers: ProxyChainLayerForm[]): string {
  return proxyChainLayerErrors(layers)[0]?.messageKey ?? "";
}

/** 把 activeId 移动到 overId 的位置;无变化时返回原数组引用。 */
export function reorderLayers(
  layers: ProxyChainLayerForm[],
  activeId: string,
  overId: string
): ProxyChainLayerForm[] {
  const from = layers.findIndex((l) => l.id === activeId);
  const to = layers.findIndex((l) => l.id === overId);
  if (from < 0 || to < 0 || from === to) return layers;
  const next = [...layers];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}
```

Delete the old inline loop body of `proxyChainValidationKey` (it's now the delegating one-liner above).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd frontend && pnpm vitest run src/components/asset/__tests__/proxyConfig.test.ts`
Expected: PASS (existing tests + 4 new).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/proxyConfig.ts frontend/src/components/asset/__tests__/proxyConfig.test.ts
git commit -m "♻️ 抽取代理链类型/校验/重排纯函数"
```

---

## Task 2: Rewrite the editor as a visualized draggable chain path

**Files:**
- Rewrite: `frontend/src/components/asset/ConnectionMethodFields.tsx`
- Rewrite: `frontend/src/components/asset/__tests__/ConnectionMethodFields.test.tsx`
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`, `frontend/src/i18n/locales/en/common.json`

**Interfaces:**
- Consumes: `layerTypeShortLabel`, `proxyChainLayerErrors`, `reorderLayers` (Task 1); `sshProxyLayer`/`socks5ProxyLayer`/`httpTunnelProxyLayer`, `ConnectionFormFields`, `ProxyChainLayerForm`, `ProxyChainLayerType` (proxyConfig). Component public props are **unchanged** (`value`, `onChange`, `excludeIds`, plus the two ignored legacy label props) so all 11 `*ConfigSection.tsx` callers keep working with no edits.
- Produces: same default export `ConnectionMethodFields`.

- [ ] **Step 1: Add i18n keys (both locales)**

In `frontend/src/i18n/locales/zh-CN/common.json`, inside `asset`, next to the existing `proxyChain*` keys add:

```json
    "proxyChainAddNode": "添加节点",
    "proxyChainAddFirst": "添加第一跳",
    "proxyChainLocal": "本机",
    "proxyChainLocalHint": "当前设备 · 流量起点",
    "proxyChainTargetLabel": "目标资产",
    "proxyChainEmptyTitle": "还没有代理节点",
    "proxyChainEmptyHint": "点击「添加节点」建立第一跳",
    "proxyChainDirectHint": "切到「直连」将隐藏链路，流量直达目标。",
    "proxyChainTypeSSHName": "SSH 跳板",
    "proxyChainTypeSSHDesc": "经跳板机 SSH 转发",
    "proxyChainTypeSOCKS5Name": "SOCKS5 代理",
    "proxyChainTypeSOCKS5Desc": "标准 SOCKS5 代理",
    "proxyChainTypeHTTPName": "HTTP 隧道",
    "proxyChainTypeHTTPDesc": "HTTP CONNECT / 自定义脚本",
    "proxyChainProblems_one": "代理链存在 {{count}} 个问题，请修复后保存",
    "proxyChainProblems_other": "代理链存在 {{count}} 个问题，请修复后保存",
    "proxyChainReorderHint": "拖动手柄可重新排序",
```

In `frontend/src/i18n/locales/en/common.json`, inside `asset`, add the idiomatic English:

```json
    "proxyChainAddNode": "Add node",
    "proxyChainAddFirst": "Add first hop",
    "proxyChainLocal": "This device",
    "proxyChainLocalHint": "Local · traffic origin",
    "proxyChainTargetLabel": "Target asset",
    "proxyChainEmptyTitle": "No proxy nodes yet",
    "proxyChainEmptyHint": "Add the first hop with “Add node”",
    "proxyChainDirectHint": "Switch to Direct to hide the chain; traffic goes straight to the target.",
    "proxyChainTypeSSHName": "SSH jump",
    "proxyChainTypeSSHDesc": "Forward through an SSH bastion",
    "proxyChainTypeSOCKS5Name": "SOCKS5 proxy",
    "proxyChainTypeSOCKS5Desc": "Standard SOCKS5 proxy",
    "proxyChainTypeHTTPName": "HTTP tunnel",
    "proxyChainTypeHTTPDesc": "HTTP CONNECT / custom script",
    "proxyChainProblems_one": "The proxy chain has {{count}} problem to fix before saving",
    "proxyChainProblems_other": "The proxy chain has {{count}} problems to fix before saving",
    "proxyChainReorderHint": "Drag the handle to reorder",
```

- [ ] **Step 2: Write the failing component tests**

Replace `frontend/src/components/asset/__tests__/ConnectionMethodFields.test.tsx` with:

```tsx
import { render, fireEvent, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { CONNECTION_DEFAULTS, sshProxyLayer, socks5ProxyLayer } from "@/components/asset/proxyConfig";

function renderChain(over = {}, onChange = vi.fn()) {
  const value = { ...CONNECTION_DEFAULTS, connectionType: "jumphost" as const, ...over };
  return { onChange, ...render(<ConnectionMethodFields value={value} onChange={onChange} />) };
}

describe("ConnectionMethodFields", () => {
  it("renders the local and target endpoints in chain mode", () => {
    const { getByText } = renderChain({ proxyChainLayers: [sshProxyLayer(42, "Bastion")] });
    expect(getByText("本机")).toBeTruthy();
    expect(getByText("目标资产")).toBeTruthy();
    expect(getByText("Bastion")).toBeTruthy();
  });

  it("shows the empty state when chain mode has no layers", () => {
    const { getByText } = renderChain({ proxyChainLayers: [] });
    expect(getByText("还没有代理节点")).toBeTruthy();
  });

  it("expands a hop inline when its card is clicked", () => {
    const { getByRole, queryByDisplayValue } = renderChain({
      proxyChainLayers: [sshProxyLayer(42, "Bastion"), { ...socks5ProxyLayer(), id: "s1", name: "Proxy A" }],
    });
    // second layer not selected by default -> its name input not shown
    expect(queryByDisplayValue("Proxy A")).toBeNull();
    fireEvent.click(getByRole("button", { name: "Proxy A" }));
    expect(queryByDisplayValue("Proxy A")).not.toBeNull();
  });

  it("appends a layer via the add menu", () => {
    const { getByRole, onChange } = renderChain({ proxyChainLayers: [] });
    fireEvent.click(getByRole("button", { name: /添加节点|添加第一跳/ }));
    fireEvent.click(getByRole("menuitem", { name: /SOCKS5/ }));
    expect(onChange).toHaveBeenCalled();
    const patch = onChange.mock.calls.at(-1)![0];
    expect(patch.proxyChainLayers).toHaveLength(1);
    expect(patch.proxyChainLayers[0].type).toBe("socks5");
  });

  it("shows the validation banner when an enabled layer is incomplete", () => {
    const { getByText } = renderChain({
      proxyChainLayers: [{ ...socks5ProxyLayer(), id: "s1", host: "" }],
    });
    expect(getByText(/代理链存在/)).toBeTruthy();
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd frontend && pnpm vitest run src/components/asset/__tests__/ConnectionMethodFields.test.tsx`
Expected: FAIL (old component has no endpoints/empty-state/add-menu/banner).

- [ ] **Step 4: Rewrite the component**

Replace the entire contents of `frontend/src/components/asset/ConnectionMethodFields.tsx` with:

```tsx
import { useMemo, useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";
import {
  ChevronUp,
  CircleAlert,
  Copy,
  Globe,
  GripVertical,
  Monitor,
  Plus,
  Route,
  Server,
  Target,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  cn,
} from "@opskat/ui";
import {
  DndContext,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { SortableContext, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { Field, Segmented } from "@/components/asset/fields";
import {
  httpTunnelProxyLayer,
  proxyChainLayerErrors,
  reorderLayers,
  socks5ProxyLayer,
  sshProxyLayer,
  type ConnectionFormFields,
  type ProxyChainLayerError,
  type ProxyChainLayerForm,
  type ProxyChainLayerType,
} from "./proxyConfig";

interface ConnectionMethodFieldsProps {
  value: ConnectionFormFields;
  onChange: (patch: Partial<ConnectionFormFields>) => void;
  excludeIds?: number[];
  tunnelOptionLabelKey?: string;
  tunnelSelectLabelKey?: string;
}

interface LayerVisual {
  icon: ComponentType<{ className?: string }>;
  /** text-* class for the accent */
  text: string;
  /** soft background for the marker */
  softBg: string;
  /** solid background for the selected marker */
  solidBg: string;
  /** ring/border for a selected card */
  ring: string;
  nameKey: string;
  descKey: string;
}

const LAYER_VISUAL: Record<ProxyChainLayerType, LayerVisual> = {
  ssh: {
    icon: Server,
    text: "text-primary",
    softBg: "bg-primary/10",
    solidBg: "bg-primary text-primary-foreground",
    ring: "border-primary/60",
    nameKey: "asset.proxyChainTypeSSHName",
    descKey: "asset.proxyChainTypeSSHDesc",
  },
  socks5: {
    icon: Route,
    text: "text-success",
    softBg: "bg-success/15",
    solidBg: "bg-success text-success-foreground",
    ring: "border-success/60",
    nameKey: "asset.proxyChainTypeSOCKS5Name",
    descKey: "asset.proxyChainTypeSOCKS5Desc",
  },
  http_tunnel: {
    icon: Globe,
    text: "text-warning",
    softBg: "bg-warning/15",
    solidBg: "bg-warning text-warning-foreground",
    ring: "border-warning/50",
    nameKey: "asset.proxyChainTypeHTTPName",
    descKey: "asset.proxyChainTypeHTTPDesc",
  },
};

/** 连接方式与代理链配置,SSH 与数据库族共用。 */
export function ConnectionMethodFields({ value, onChange, excludeIds }: ConnectionMethodFieldsProps) {
  const { t } = useTranslation();
  const layers = useMemo(() => value.proxyChainLayers || [], [value.proxyChainLayers]);
  const isChainMode = value.connectionType !== "direct";
  const [selectedLayerIdValue, setSelectedLayerId] = useState("");
  const selectedLayerId = layers.some((l) => l.id === selectedLayerIdValue) ? selectedLayerIdValue : "";
  const errors = useMemo(() => proxyChainLayerErrors(layers), [layers]);
  const errorByLayer = useMemo(() => {
    const map = new Map<string, ProxyChainLayerError[]>();
    for (const e of errors) map.set(e.id, [...(map.get(e.id) || []), e]);
    return map;
  }, [errors]);

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  const updateLayers = (next: ProxyChainLayerForm[]) => onChange({ proxyChainLayers: next });
  const patchLayer = (id: string, patch: Partial<ProxyChainLayerForm>) =>
    updateLayers(layers.map((l) => (l.id === id ? { ...l, ...patch } : l)));
  const removeLayer = (id: string) => updateLayers(layers.filter((l) => l.id !== id));
  const duplicateLayer = (layer: ProxyChainLayerForm) => {
    const existing = new Set(layers.map((l) => l.id));
    let i = layers.length + 1;
    let nextId = `${layer.type}-copy-${i}`;
    while (existing.has(nextId)) nextId = `${layer.type}-copy-${(i += 1)}`;
    const copy = { ...layer, id: nextId, name: `${layer.name || t("asset.proxyChainLayer")} Copy`, password: "", token: "" };
    updateLayers([...layers, copy]);
    setSelectedLayerId(copy.id);
  };
  const addLayer = (layer: ProxyChainLayerForm) => {
    updateLayers([...layers, layer]);
    setSelectedLayerId(layer.id);
  };
  const setMode = (mode: "direct" | "chain") => {
    if (mode === "direct") {
      onChange({
        connectionType: "direct",
        sshTunnelId: 0,
        proxyHost: "",
        proxyPassword: "",
        encryptedProxyPassword: "",
        proxyChainLayers: [],
      });
      return;
    }
    onChange({ connectionType: value.connectionType === "direct" ? "jumphost" : value.connectionType });
  };
  const onDragEnd = (e: DragEndEvent) => {
    if (e.over && e.active.id !== e.over.id) {
      updateLayers(reorderLayers(layers, String(e.active.id), String(e.over.id)));
    }
  };

  return (
    <div className="flex flex-col gap-4">
      <Field label={t("asset.connectionType")}>
        <Segmented
          value={isChainMode ? "chain" : "direct"}
          onChange={(v) => setMode(v as "direct" | "chain")}
          aria-label={t("asset.connectionType")}
          options={[
            { value: "direct", label: t("asset.connectionDirect") },
            { value: "chain", label: t("asset.connectionTunnelProxy") },
          ]}
        />
      </Field>

      {isChainMode && (
        <div className="flex flex-col gap-3">
          {errors.length > 0 && (
            <div className="flex items-center gap-2 rounded-md bg-destructive/10 px-3 py-2 text-[12px] font-medium text-destructive">
              <TriangleAlert className="h-3.5 w-3.5 shrink-0" />
              {t("asset.proxyChainProblems", { count: errors.length })}
            </div>
          )}

          <div className="relative flex flex-col gap-2">
            {/* rail spine behind the markers */}
            <div className="pointer-events-none absolute left-[13px] top-3 bottom-3 w-px bg-border" aria-hidden />

            <EndpointRow icon={Monitor} tone="muted" title={t("asset.proxyChainLocal")} sub={t("asset.proxyChainLocalHint")} />

            {layers.length === 0 ? (
              <EmptySlot title={t("asset.proxyChainEmptyTitle")} hint={t("asset.proxyChainEmptyHint")} />
            ) : (
              <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
                <SortableContext items={layers.map((l) => l.id)} strategy={verticalListSortingStrategy}>
                  {layers.map((layer) => (
                    <SortableHop
                      key={layer.id}
                      layer={layer}
                      selected={layer.id === selectedLayerId}
                      hasError={errorByLayer.has(layer.id)}
                      onSelect={() => setSelectedLayerId(selectedLayerId === layer.id ? "" : layer.id)}
                      onDuplicate={() => duplicateLayer(layer)}
                      onRemove={() => removeLayer(layer.id)}
                      onPatch={(patch) => patchLayer(layer.id, patch)}
                      layerErrors={errorByLayer.get(layer.id) || []}
                      excludeIds={excludeIds}
                    />
                  ))}
                </SortableContext>
              </DndContext>
            )}

            <EndpointRow icon={Target} tone="primary" title={t("asset.proxyChainTargetLabel")} sub="" />
          </div>

          <div className="pl-[34px]">
            <AddNodeMenu
              empty={layers.length === 0}
              onAdd={(type) =>
                addLayer(type === "ssh" ? sshProxyLayer() : type === "socks5" ? socks5ProxyLayer() : httpTunnelProxyLayer())
              }
            />
          </div>

          <p className="pl-[34px] text-[11px] leading-relaxed text-muted-foreground/80">{t("asset.proxyChainDirectHint")}</p>
        </div>
      )}
    </div>
  );
}

function EndpointRow({
  icon: Icon,
  tone,
  title,
  sub,
}: {
  icon: ComponentType<{ className?: string }>;
  tone: "muted" | "primary";
  title: string;
  sub: string;
}) {
  return (
    <div className="relative z-10 flex items-center gap-3">
      <span
        className={cn(
          "flex h-7 w-7 shrink-0 items-center justify-center rounded-full border bg-background",
          tone === "primary" ? "border-primary/60 text-primary" : "border-border text-muted-foreground"
        )}
      >
        <Icon className="h-3.5 w-3.5" />
      </span>
      <div className="min-w-0">
        <div className="truncate text-[12.5px] font-semibold">{title}</div>
        {sub && <div className="truncate text-[11px] text-muted-foreground">{sub}</div>}
      </div>
    </div>
  );
}

function EmptySlot({ title, hint }: { title: string; hint: string }) {
  return (
    <div className="relative z-10 flex items-center gap-3">
      <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-dashed border-border bg-background text-muted-foreground">
        <Plus className="h-3.5 w-3.5" />
      </span>
      <div className="min-w-0 flex-1 rounded-lg border border-dashed bg-background px-3 py-2.5">
        <div className="text-[12.5px] font-medium text-muted-foreground">{title}</div>
        <div className="text-[11px] text-muted-foreground/70">{hint}</div>
      </div>
    </div>
  );
}

function AddNodeMenu({ empty, onAdd }: { empty: boolean; onAdd: (type: ProxyChainLayerType) => void }) {
  const { t } = useTranslation();
  const items: ProxyChainLayerType[] = ["ssh", "socks5", "http_tunnel"];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant={empty ? "default" : "outline"} size="sm" className="h-9 w-full gap-1.5">
          <Plus className="h-3.5 w-3.5" />
          {empty ? t("asset.proxyChainAddFirst") : t("asset.proxyChainAddNode")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-[260px]">
        {items.map((type) => {
          const v = LAYER_VISUAL[type];
          const Icon = v.icon;
          return (
            <DropdownMenuItem key={type} onSelect={() => onAdd(type)} className="gap-2.5 py-2">
              <span className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-md", v.softBg)}>
                <Icon className={cn("h-3.5 w-3.5", v.text)} />
              </span>
              <span className="flex min-w-0 flex-col">
                <span className="text-[12.5px] font-semibold">{t(v.nameKey)}</span>
                <span className="text-[11px] text-muted-foreground">{t(v.descKey)}</span>
              </span>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SortableHop({
  layer,
  selected,
  hasError,
  onSelect,
  onDuplicate,
  onRemove,
  onPatch,
  layerErrors,
  excludeIds,
}: {
  layer: ProxyChainLayerForm;
  selected: boolean;
  hasError: boolean;
  onSelect: () => void;
  onDuplicate: () => void;
  onRemove: () => void;
  onPatch: (patch: Partial<ProxyChainLayerForm>) => void;
  layerErrors: ProxyChainLayerError[];
  excludeIds?: number[];
}) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: layer.id });
  const v = LAYER_VISUAL[layer.type];
  const Icon = v.icon;
  const style = { transform: CSS.Transform.toString(transform), transition };
  const cardBorder = hasError ? "border-destructive/70" : selected ? v.ring : "border-border";

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn("relative z-10 flex items-start gap-3", isDragging && "opacity-70")}
    >
      <span
        className={cn(
          "mt-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-full border",
          selected ? v.solidBg : cn("bg-background", v.softBg, v.text),
          selected ? "border-transparent" : "border-transparent"
        )}
      >
        <Icon className="h-3.5 w-3.5" />
      </span>

      <div className={cn("min-w-0 flex-1 rounded-lg border bg-card", cardBorder, isDragging && "shadow-lg")}>
        <div className="flex items-center gap-2 px-3 py-2">
          <button
            type="button"
            className="shrink-0 cursor-grab touch-none text-muted-foreground active:cursor-grabbing"
            aria-label={t("asset.proxyChainReorderHint")}
            {...attributes}
            {...listeners}
          >
            <GripVertical className="h-4 w-4" />
          </button>
          <button
            type="button"
            className="min-w-0 flex-1 cursor-pointer text-left"
            onClick={onSelect}
          >
            <div className="truncate text-[13.5px] font-semibold">{layer.name || t("asset.proxyChainLayer")}</div>
            <div className="truncate text-[11.5px] text-muted-foreground">
              {t(v.nameKey)}
              {layer.type === "socks5" && layer.host ? ` · ${layer.host}:${layer.port}` : ""}
              {layer.type === "http_tunnel" && layer.url ? ` · ${layer.url}` : ""}
            </div>
          </button>
          <div className="flex shrink-0 items-center gap-0.5">
            <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onDuplicate}>
              <Copy className="h-3.5 w-3.5" />
            </Button>
            <Button type="button" variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={onRemove}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
            {selected && (
              <Button type="button" variant="ghost" size="icon" className="h-7 w-7" onClick={onSelect}>
                <ChevronUp className="h-3.5 w-3.5" />
              </Button>
            )}
          </div>
        </div>

        {selected && (
          <div className="border-t p-3">
            <ProxyChainLayerFields layer={layer} onChange={onPatch} excludeIds={excludeIds} errors={layerErrors} />
          </div>
        )}
      </div>
    </div>
  );
}

function fieldError(errors: ProxyChainLayerError[], field: ProxyChainLayerError["field"], t: (k: string) => string) {
  const e = errors.find((x) => x.field === field);
  return e ? t(e.messageKey) : "";
}

function ProxyChainLayerFields({
  layer,
  onChange,
  excludeIds,
  errors,
}: {
  layer: ProxyChainLayerForm;
  onChange: (patch: Partial<ProxyChainLayerForm>) => void;
  excludeIds?: number[];
  errors: ProxyChainLayerError[];
}) {
  const { t } = useTranslation();
  const invalid = "border-destructive focus-visible:ring-destructive/40";
  const errText = (msg: string) =>
    msg ? (
      <span className="mt-1 flex items-center gap-1 text-[11px] text-destructive">
        <CircleAlert className="h-3 w-3" />
        {msg}
      </span>
    ) : null;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-end gap-3">
        <Field label={t("asset.proxyChainLayerName")} className="flex-1">
          <Input value={layer.name} onChange={(e) => onChange({ name: e.target.value })} />
        </Field>
        <Field label={t("asset.proxyChainLayer")} className="w-[140px] shrink-0">
          <Select value={layer.type} onValueChange={(type) => onChange({ type: type as ProxyChainLayerType })}>
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ssh">SSH</SelectItem>
              <SelectItem value="socks5">SOCKS5</SelectItem>
              <SelectItem value="http_tunnel">HTTP Tunnel</SelectItem>
            </SelectContent>
          </Select>
        </Field>
      </div>

      {layer.type === "ssh" && (
        <Field label={t("asset.proxyChainSSHAsset")} required>
          <AssetSelect
            value={layer.sshAssetId}
            onValueChange={(sshAssetId) => onChange({ sshAssetId })}
            filterType="ssh"
            excludeIds={excludeIds}
            placeholder={t("asset.jumpHostNone")}
          />
          {errText(fieldError(errors, "sshAssetId", t))}
        </Field>
      )}

      {layer.type === "socks5" && (
        <>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyHost")} required className="flex-1">
              <Input
                className={cn(fieldError(errors, "host", t) && invalid)}
                value={layer.host}
                onChange={(e) => onChange({ host: e.target.value })}
                placeholder="127.0.0.1"
              />
              {errText(fieldError(errors, "host", t))}
            </Field>
            <Field label={t("asset.proxyPort")} required className="w-[110px] shrink-0">
              <Input
                className={cn(
                  "[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none",
                  fieldError(errors, "port", t) && invalid
                )}
                type="number"
                value={layer.port || ""}
                placeholder="1080"
                onChange={(e) => onChange({ port: Number(e.target.value) })}
              />
            </Field>
          </div>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyUsername")} className="flex-1">
              <Input value={layer.username} onChange={(e) => onChange({ username: e.target.value })} />
            </Field>
            <Field label={t("asset.proxyPassword")} className="flex-1">
              <Input
                type="password"
                value={layer.password}
                onChange={(e) => onChange({ password: e.target.value })}
                placeholder={layer.encryptedPassword ? t("asset.passwordUnchanged") : ""}
              />
            </Field>
          </div>
        </>
      )}

      {layer.type === "http_tunnel" && (
        <>
          <Field label={t("asset.proxyChainHTTPURL")} required>
            <Input
              className={cn(fieldError(errors, "url", t) && invalid)}
              value={layer.url}
              onChange={(e) => onChange({ url: e.target.value })}
              placeholder="https://dbx.example.com/dbx_tunnel.php"
            />
            {errText(fieldError(errors, "url", t))}
          </Field>
          <div className="flex items-end gap-3">
            <Field label={t("asset.proxyChainHTTPToken")} required className="flex-1">
              <Input
                className={cn(fieldError(errors, "token", t) && invalid)}
                type="password"
                value={layer.token}
                onChange={(e) => onChange({ token: e.target.value })}
                placeholder={layer.encryptedToken ? t("asset.passwordUnchanged") : "dbx_tunnel.php token"}
              />
              {errText(fieldError(errors, "token", t))}
            </Field>
            <Field label={t("asset.proxyChainTimeout")} className="w-[150px] shrink-0">
              <Input
                className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                type="number"
                min={1}
                value={layer.timeoutSeconds || ""}
                onChange={(e) => onChange({ timeoutSeconds: Number(e.target.value) })}
              />
            </Field>
          </div>
        </>
      )}
    </div>
  );
}
```

Notes for the implementer:
- The `enabled` checkbox from the old UI is dropped from the row; a hop is enabled by existing. Per-layer enable/disable is out of scope (the design has no per-row checkbox) — `buildProxyChainJSON` still filters on `enabled` and factory layers default `enabled: true`, so persistence is unchanged.
- This component does not receive the target asset's name, so the target endpoint is passed `sub=""` and `EndpointRow` renders the sub line only when non-empty. (A later enhancement could thread the asset name in as a prop.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd frontend && pnpm vitest run src/components/asset/__tests__/ConnectionMethodFields.test.tsx`
Expected: PASS (5 tests). If the add-menu test can't find the menuitem because `DropdownMenuContent` renders in a portal, use `getByRole("menuitem", ...)` (Radix portals into `document.body`, still queryable) — already handled by the test.

- [ ] **Step 6: Typecheck + lint the touched files**

Run: `cd frontend && pnpm exec tsc -b --noEmit && pnpm lint`
Expected: no errors. Fix any unused import (e.g. remove `layerTypeShortLabel` import if you did not use it — it IS used only if you add a badge; the code above does not use it, so remove it from the import and from Task 1 usage here). 

- [ ] **Step 7: Commit**

```bash
git add frontend/src/components/asset/ConnectionMethodFields.tsx \
        frontend/src/components/asset/__tests__/ConnectionMethodFields.test.tsx \
        frontend/src/i18n/locales/zh-CN/common.json frontend/src/i18n/locales/en/common.json
git commit -m "✨ 重构代理链编辑器为可视化可拖拽链路"
```

---

## Task 3: Read-only `ProxyChainDetailSection` + SSH detail card

**Files:**
- Modify: `frontend/src/components/asset/detail/InfoItem.tsx`
- Create: `frontend/src/components/asset/detail/__tests__/InfoItem.test.tsx`
- Modify: `frontend/src/components/asset/detail/SSHDetailInfoCard.tsx`
- Modify: `frontend/src/i18n/locales/{zh-CN,en}/common.json`

**Interfaces:**
- Consumes: `layerTypeShortLabel` (Task 1); `ProxyChainJSON`, `ProxyChainLayerJSON` (proxyConfig).
- Produces: `ProxyChainDetailSection({ chain, resolveSshName }: { chain?: ProxyChainJSON | null; resolveSshName: (id?: number) => string | null })`. Renders nothing when `!chain?.layers?.length`.

- [ ] **Step 1: i18n keys (both locales)**

Add to `asset` in both locale files (zh-CN shown; add idiomatic en equivalents `"Proxy chain"`, `"This device"`, `"Target"`, `"{{count}} hops"`):

```json
    "proxyChainTitle": "代理链",
    "proxyChainHops": "{{count}} 跳",
    "proxyChainReadonlyLocal": "本机",
    "proxyChainReadonlyTarget": "目标",
```

- [ ] **Step 2: Write the failing test**

Create `frontend/src/components/asset/detail/__tests__/InfoItem.test.tsx`:

```tsx
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProxyChainDetailSection } from "@/components/asset/detail/InfoItem";
import type { ProxyChainJSON } from "@/components/asset/proxyConfig";

describe("ProxyChainDetailSection", () => {
  it("renders nothing without layers", () => {
    const { container } = render(<ProxyChainDetailSection chain={null} resolveSshName={() => null} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders the ssh hop name and socks5 host", () => {
    const chain: ProxyChainJSON = {
      layers: [
        { type: "ssh", order: 1, ssh_asset_id: 42 },
        { type: "socks5", order: 2, host: "127.0.0.1", port: 1080 },
      ],
    };
    const { getByText } = render(<ProxyChainDetailSection chain={chain} resolveSshName={(id) => (id === 42 ? "bastion-01" : null)} />);
    expect(getByText("bastion-01")).toBeTruthy();
    expect(getByText(/127\.0\.0\.1:1080/)).toBeTruthy();
    expect(getByText("SOCKS5")).toBeTruthy();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd frontend && pnpm vitest run src/components/asset/detail/__tests__/InfoItem.test.tsx`
Expected: FAIL — `ProxyChainDetailSection` not exported.

- [ ] **Step 4: Implement `ProxyChainDetailSection`**

In `frontend/src/components/asset/detail/InfoItem.tsx`, add these imports at the top (merge with existing imports):

```tsx
import { Globe, Monitor, Route, Server, Target } from "lucide-react";
import type { ComponentType } from "react";
import { layerTypeShortLabel, type ProxyChainJSON, type ProxyChainLayerJSON } from "../proxyConfig";
```

Then append:

```tsx
const CHAIN_ICON: Record<ProxyChainLayerJSON["type"], { icon: ComponentType<{ className?: string }>; text: string; bg: string }> = {
  ssh: { icon: Server, text: "text-primary", bg: "bg-primary/10" },
  socks5: { icon: Route, text: "text-success", bg: "bg-success/15" },
  http_tunnel: { icon: Globe, text: "text-warning", bg: "bg-warning/15" },
};

function chainLayerLines(layer: ProxyChainLayerJSON, resolveSshName: (id?: number) => string | null) {
  if (layer.type === "ssh") return { name: resolveSshName(layer.ssh_asset_id) || `#${layer.ssh_asset_id ?? "?"}`, detail: "" };
  if (layer.type === "socks5") return { name: layer.name || "SOCKS5", detail: `${layer.host ?? ""}:${layer.port ?? ""}` };
  return { name: layer.name || "HTTP", detail: layer.url || "" };
}

/** 只读代理链:本机 → 各跳 → 目标。无 layers 时不渲染。 */
export function ProxyChainDetailSection({
  chain,
  resolveSshName,
}: {
  chain?: ProxyChainJSON | null;
  resolveSshName: (id?: number) => string | null;
}) {
  const { t } = useTranslation();
  const layers = [...(chain?.layers || [])].sort((a, b) => (a.order || 0) - (b.order || 0));
  if (!layers.length) return null;

  return (
    <DetailSection
      title={
        <span className="flex items-center gap-2">
          <Route className="h-3.5 w-3.5" />
          {t("asset.proxyChainTitle")}
          <span className="rounded-full bg-muted px-2 py-0.5 text-[10.5px] font-medium normal-case tracking-normal text-muted-foreground">
            {t("asset.proxyChainHops", { count: layers.length })}
          </span>
        </span>
      }
    >
      <div className="relative flex flex-col gap-0">
        <div className="pointer-events-none absolute left-[10px] top-3 bottom-3 w-px bg-border" aria-hidden />
        <ChainEndpoint icon={Monitor} tone="muted" text={t("asset.proxyChainReadonlyLocal")} />
        {layers.map((layer, i) => {
          const meta = CHAIN_ICON[layer.type];
          const Icon = meta.icon;
          const line = chainLayerLines(layer, resolveSshName);
          return (
            <div key={layer.id || i} className="relative z-10 flex items-center gap-3 py-1.5">
              <span className={cn("flex h-5 w-5 shrink-0 items-center justify-center rounded-full", meta.bg)}>
                <Icon className={cn("h-3 w-3", meta.text)} />
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className={cn("rounded bg-muted px-1.5 py-0.5 text-[10px] font-semibold", meta.text)}>
                    {layerTypeShortLabel(layer.type)}
                  </span>
                  <span className="truncate text-[12.5px] font-medium">{line.name}</span>
                </div>
                {line.detail && <div className="truncate font-mono text-[11px] text-muted-foreground">{line.detail}</div>}
              </div>
            </div>
          );
        })}
        <ChainEndpoint icon={Target} tone="primary" text={t("asset.proxyChainReadonlyTarget")} />
      </div>
    </DetailSection>
  );
}

function ChainEndpoint({
  icon: Icon,
  tone,
  text,
}: {
  icon: ComponentType<{ className?: string }>;
  tone: "muted" | "primary";
  text: string;
}) {
  return (
    <div className="relative z-10 flex items-center gap-3 py-1.5">
      <span
        className={cn(
          "flex h-5 w-5 shrink-0 items-center justify-center rounded-full border bg-card",
          tone === "primary" ? "border-primary/60 text-primary" : "border-border text-muted-foreground"
        )}
      >
        <Icon className="h-3 w-3" />
      </span>
      <span className="text-[12px] font-semibold">{text}</span>
    </div>
  );
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd frontend && pnpm vitest run src/components/asset/detail/__tests__/InfoItem.test.tsx`
Expected: PASS.

- [ ] **Step 6: Wire into the SSH detail card**

In `frontend/src/components/asset/detail/SSHDetailInfoCard.tsx`:
1. Import: change `import type { ProxyConfigJSON } from "../proxyConfig";` to also import `ProxyChainJSON`, and import `ProxyChainDetailSection` from `./InfoItem`.
2. Add `proxy_chain?: ProxyChainJSON | null;` to the `SSHConfig` interface.
3. Replace `<ProxyDetailSection proxy={cfg.proxy} />` with:

```tsx
      <ProxyDetailSection proxy={cfg.proxy} />
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
```

- [ ] **Step 7: Verify + commit**

Run: `cd frontend && pnpm vitest run src/components/asset/detail/__tests__/InfoItem.test.tsx && pnpm exec tsc -b --noEmit && pnpm lint`
Expected: PASS / no errors.

```bash
git add frontend/src/components/asset/detail/InfoItem.tsx \
        frontend/src/components/asset/detail/__tests__/InfoItem.test.tsx \
        frontend/src/components/asset/detail/SSHDetailInfoCard.tsx \
        frontend/src/i18n/locales/zh-CN/common.json frontend/src/i18n/locales/en/common.json
git commit -m "✨ 资产详情新增只读代理链展示"
```

---

## Task 4: Wire read-only chain into the remaining detail cards

**Files (modify each):**
- `frontend/src/components/asset/detail/DatabaseDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/EtcdDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/KafkaDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/RedisDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/RDPDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/K8sDetailInfoCard.tsx`
- `frontend/src/components/asset/detail/MongoDBDetailInfoCard.tsx`
- (also check for `OSSDetailInfoCard.tsx`; if it exists and shows proxy, wire it too.)

**Interfaces:** Consumes `ProxyChainDetailSection` (Task 3). Each card already has `sshTunnelName` from `DetailInfoCardProps`.

- [ ] **Step 1: For each card, make the same three edits**

For every file above:
1. Ensure `ProxyChainJSON` is imported from `../proxyConfig` (add to the existing `type` import) and `ProxyChainDetailSection` is imported from `./InfoItem`.
2. Add `proxy_chain?: ProxyChainJSON | null;` to that card's config interface (the one passed to `parseDetailConfig<...>`).
3. Immediately after the existing `<ProxyDetailSection proxy={cfg.proxy} />` (or, if a card has no `ProxyDetailSection`, at the end of the returned fragment), add:

```tsx
      <ProxyChainDetailSection chain={cfg.proxy_chain} resolveSshName={sshTunnelName} />
```

If a card's component signature destructures only `{ asset }` and not `sshTunnelName`, add `sshTunnelName` to the destructure.

- [ ] **Step 2: Typecheck + lint**

Run: `cd frontend && pnpm exec tsc -b --noEmit && pnpm lint`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/components/asset/detail/*DetailInfoCard.tsx
git commit -m "✨ 各资产详情卡接入只读代理链"
```

---

## Task 5: Cleanup + full verification

**Files:** possibly `ConnectionMethodFields.tsx` / `proxyConfig.ts` (remove any now-unused exports).

- [ ] **Step 1: Remove dead code**

- If the old `layerTypeLabel` local helper still exists anywhere, delete it (replaced by `layerTypeShortLabel` / `LAYER_VISUAL`).
- Confirm no remaining imports of `ArrowUp`/`ArrowDown`/`Checkbox` in `ConnectionMethodFields.tsx` (the redesign drops arrow-reorder and the per-row checkbox).
- Confirm `layerTypeShortLabel` is imported only where used (detail section). Remove unused imports flagged by lint.

- [ ] **Step 2: Run the full asset test suite + lint + typecheck**

Run:
```bash
cd frontend
pnpm vitest run src/components/asset
pnpm exec tsc -b --noEmit
pnpm lint
```
Expected: all PASS / no errors.

- [ ] **Step 3: Manual verification in the running app** (per AGENTS.md “verify by observing”)

Use the `/run` skill or `wails dev` to launch, then:
1. Add/edit an SSH asset → 隧道/代理 tab → confirm: endpoints render, add-menu inserts SSH/SOCKS5/HTTP hops, clicking a hop expands it inline, drag-handle reorders hops, deleting works, an incomplete hop shows the red banner + inline field error, switching to 直连 hides the chain.
2. Save an asset with a 2-hop chain → open its detail panel → confirm the read-only 代理链 section shows 本机 → hops → 目标 with type chips and the SSH hop’s resolved name.
3. Check `logs/opskat.log` for errors and confirm the saved `asset.Config` contains the expected `proxy_chain.layers` (via `opskat.db` or the reload round-trip).

- [ ] **Step 4: Final commit (if cleanup changed anything)**

```bash
git add -A
git commit -m "🧹 清理代理链重构遗留代码"
```

---

## Self-Review notes (addressed)

- **Spec coverage:** editor visualized path + rail spine (Task 2), real drag via @dnd-kit (Task 2), inline-expand (Task 2), unified add dropdown (Task 2), type colors SSH/SOCKS5/HTTP (Task 2 + Task 3), empty/direct state (Task 2), validation banner + inline field errors (Task 1 logic + Task 2 UI), read-only vertical mini-path (Task 3) across all cards (Task 4). The “compact single-line chip” read-only variant from the design is **not** implemented (the vertical card is the chosen production form); revisit only if a dense surface needs it.
- **Loop/self error** from the design’s error-state frame is intentionally **not** a new rule — `AssetSelect` already receives `excludeIds` to prevent selecting self as a jump host, so it is unreachable; the inline errors surface the existing `proxyChainLayerErrors` rules instead.
- **Type consistency:** `reorderLayers`, `proxyChainLayerErrors`, `ProxyChainLayerError`, `layerTypeShortLabel` names match between Task 1 (definition) and Tasks 2–3 (use). `ProxyChainDetailSection` prop shape (`chain`, `resolveSshName`) matches between Task 3 (def) and Tasks 3–4 (use).
