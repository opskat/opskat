# Asset Config Schema-Driven · Phase 4 — K8s migration + SOCKS5 proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add SOCKS5 proxy support to K8s assets (the one backend-touching feature of this refactor) and migrate `K8sConfigSection` to the config-driven `useConfigSection` + in-component schema pattern, so K8s offers the standard 直连/SSH隧道/代理 tri-choice like the database family.

**Architecture:** Backend gains an optional `K8sConfig.Proxy` field and a proxy dial branch in `k8sClientOptions` (tunnel-preferred mutual exclusion, decision extracted into a pure `selectDialSource` helper for testability). Frontend folds `ConnectionFormFields` into `K8sFormState`, teaches `buildK8sConfig`/`parseK8sConfig` the proxy round-trip, and rewrites the section using `useConfigSection` + an in-component `ConfigGroupSchema[]` (kubeconfig reveal stays a `custom` field; tunnel becomes the standard `{kind:"tunnel"}`).

**Tech Stack:** Go 1.26 (testify), React 19 + TypeScript, vitest + RTL, Wails v2.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-06-25-asset-config-schema-driven-design.md` (§4, §5.2, **附录 A**). This phase implements 附录 A + the K8s row of §5.2.
- **Backend is the only backend touch in this whole refactor** — keep it isolated in its own commits (Tasks 1–2), separate from the frontend (Tasks 3–4), so the feature is independently reviewable/revertable.
- **Do NOT change** the credential / connection contracts: `proxyConfig.ts` (`ConnectionFormFields`/`buildProxyJSON`/`parseConnectionFields`/`resolveSaveProxyPassword`/`CONNECTION_DEFAULTS`), `ConnectionMethodFields`, `ConfigTabs`, `useConfigSection`, `configFields.tsx`, `socksdial.Dial`, `credential_resolver.DecryptProxyPassword`, `k8spkg.WithDial`. Consume them; don't edit them.
- **No new i18n keys.** Reuse `asset.formMissingKubeconfig`, `asset.k8sKubeconfig`, `asset.k8sNamespace`, `asset.k8sContext`, `asset.k8sKubeconfigPlaceholder`, `asset.k8sKubeconfigEditPlaceholder`, `asset.k8sRevealKubeconfig`, `asset.k8sEnterKubeconfig`, and the existing `ConnectionMethodFields` labels.
- **Tunnel-priority mutual exclusion:** mirror the entity convention `// SOCKS5 代理（与 SSH 隧道互斥，隧道优先）`. Proxy applies only when `asset.SSHTunnelID == 0`.
- **K8s has no Test-Connection button** (`buildTestConfig` stays `null`). Verify the proxy path by observation (`opsctl` / `logs/opskat.log`) per AGENTS.md, not an automated connect test.
- **Commits:** gitmoji prefix; **no** PR/review number in subject. Issue ref only if deliberately linking one.
- **Gates** (run from `frontend/` for TS): `pnpm exec tsc --noEmit`, `pnpm lint`, `pnpm test`. For Go (repo root): `go build ./...`, `go test ./...`, `golangci-lint run` (use golangci-lint, **not** `go vet`).
- **Regression nets that MUST stay green unchanged:** `frontend/src/components/asset/__tests__/K8sConfigSection.test.tsx` (component ref contract — verified compatible with the new serializer below, do not edit it). Verify the full backend + frontend suites at the end.

## File Structure

**Backend (Tasks 1–2, Go):**
- Modify `internal/model/entity/asset_entity/asset.go:270-275` — add `Proxy *ProxyConfig` to `K8sConfig`.
- Modify `internal/model/entity/asset_entity/asset_proxy_test.go` — add k8s round-trip subtest.
- Modify `internal/app/k8s/k8s_ops.go:184-208` — extract `selectDialSource`, add proxy dial branch + imports.
- Create `internal/app/k8s/k8s_ops_test.go` — `TestSelectDialSource`.

**Frontend (Tasks 3–4, TS):**
- Modify `frontend/src/components/asset/K8sConfigSection.config.ts` — `K8sFormState extends ConnectionFormFields`; proxy round-trip in `buildK8sConfig`/`parseK8sConfig`.
- Modify `frontend/src/components/asset/__tests__/K8sConfigSection.config.test.ts` — update fixtures + add proxy round-trip goldens.
- Modify `frontend/src/components/asset/K8sConfigSection.tsx` — rewrite using `useConfigSection` + in-component `ConfigGroupSchema[]`.

---

## Task 1: Backend — `K8sConfig.Proxy` field + round-trip test

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go:270-275`
- Test: `internal/model/entity/asset_entity/asset_proxy_test.go:47-54` (insert a new subtest alongside the existing per-type subtests)

**Interfaces:**
- Consumes: existing `ProxyConfig` struct (`asset.go:119-125`); existing `(*Asset).SetK8sConfig` / `GetK8sConfig` (`asset.go:528-537`).
- Produces: `K8sConfig.Proxy *ProxyConfig` — consumed by Task 2 (`k8sClientOptions`) and mirrored by the frontend `proxy` JSON key in Task 3.

- [ ] **Step 1: Write the failing test**

In `internal/model/entity/asset_entity/asset_proxy_test.go`, add a `k8s` subtest inside `TestProxyConfigRoundTrip` (after the `kafka` subtest, before the closing `}` at line 54):

```go
	t.Run("k8s", func(t *testing.T) {
		a := &Asset{Type: AssetTypeK8s}
		cfg := &K8sConfig{Kubeconfig: "enc-kubeconfig", Namespace: "prod", Proxy: sampleProxy()}
		require.NoError(t, a.SetK8sConfig(cfg))
		got, err := a.GetK8sConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/entity/asset_entity/ -run TestProxyConfigRoundTrip/k8s -v`
Expected: COMPILE FAIL — `unknown field 'Proxy' in struct literal of type ... K8sConfig`.

- [ ] **Step 3: Add the field**

In `internal/model/entity/asset_entity/asset.go`, change the `K8sConfig` struct (currently lines 270-275) to:

```go
// K8sConfig K8S集群类型的特定配置
type K8sConfig struct {
	Kubeconfig string       `json:"kubeconfig,omitempty"` // kubeconfig YAML 内容
	Namespace  string       `json:"namespace,omitempty"`  // 默认命名空间
	Context    string       `json:"context,omitempty"`    // kubeconfig context 名称
	Proxy      *ProxyConfig `json:"proxy,omitempty"`      // SOCKS5 代理（与 SSH 隧道互斥，隧道优先）
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/entity/asset_entity/ -run TestProxyConfigRoundTrip/k8s -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model/entity/asset_entity/asset.go internal/model/entity/asset_entity/asset_proxy_test.go
git commit -m "✨ K8sConfig 增 SOCKS5 代理字段"
```

---

## Task 2: Backend — proxy dial branch in `k8sClientOptions`

**Files:**
- Modify: `internal/app/k8s/k8s_ops.go` (imports block lines 3-22; `k8sClientOptions` lines 184-208)
- Test: `internal/app/k8s/k8s_ops_test.go` (create)

**Interfaces:**
- Consumes: `K8sConfig.Proxy` (Task 1); `credential_resolver.Default().DecryptProxyPassword(*ProxyConfig) *ProxyConfig` (`internal/service/credential_resolver/resolver.go:217` — returns a copy with plaintext password, original on failure/nil); `socksdial.Dial(ctx, *ProxyConfig, addr) (net.Conn, error)` (`internal/pkg/socksdial/socksdial.go:16`); `k8spkg.WithDial(func(ctx, network, address) (net.Conn, error)) ClientOption` (`internal/pkg/k8s/client.go:54`).
- Produces: unexported `selectDialSource(asset, cfg, poolAvailable) dialSource` + `dialSource` enum (`dialNone`/`dialTunnel`/`dialProxy`) — internal to package `k8s`, consumed only by `k8sClientOptions` and the test.

- [ ] **Step 1: Write the failing test**

Create `internal/app/k8s/k8s_ops_test.go`:

```go
package k8s

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
)

func TestSelectDialSource(t *testing.T) {
	proxyCfg := &asset_entity.K8sConfig{Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080}}
	noProxy := &asset_entity.K8sConfig{}

	cases := []struct {
		name          string
		tunnelID      int64
		cfg           *asset_entity.K8sConfig
		poolAvailable bool
		want          dialSource
	}{
		{"proxy only", 0, proxyCfg, true, dialProxy},
		{"tunnel only", 5, noProxy, true, dialTunnel},
		{"tunnel preferred over proxy", 5, proxyCfg, true, dialTunnel},
		{"direct (neither)", 0, noProxy, true, dialNone},
		{"tunnel set but pool unavailable", 5, noProxy, false, dialNone},
		{"tunnel set + proxy but pool unavailable (tunnel still claims the slot)", 5, proxyCfg, false, dialNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &asset_entity.Asset{SSHTunnelID: c.tunnelID}
			assert.Equal(t, c.want, selectDialSource(a, c.cfg, c.poolAvailable))
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/k8s/ -run TestSelectDialSource -v`
Expected: COMPILE FAIL — `undefined: selectDialSource`, `undefined: dialSource`, `undefined: dialProxy`...

- [ ] **Step 3: Implement the helper + rewire `k8sClientOptions`**

In `internal/app/k8s/k8s_ops.go`:

(a) Add to the imports block (lines 3-22) — `socksdial` and `credential_resolver` (keep imports grouped/sorted as the file already groups stdlib then module then third-party):

```go
	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"
```

(b) Replace the body of `k8sClientOptions` (lines 184-208) and add the helper. New `k8sClientOptions`:

```go
func (k *K8s) k8sClientOptions(asset *asset_entity.Asset, cfg *asset_entity.K8sConfig) []k8spkg.ClientOption {
	opts := make([]k8spkg.ClientOption, 0, 2)
	if cfg.Context != "" {
		opts = append(opts, k8spkg.WithContext(cfg.Context))
	}

	switch selectDialSource(asset, cfg, k.pool != nil) {
	case dialTunnel:
		tunnelID := asset.SSHTunnelID
		opts = append(opts, k8spkg.WithDial(func(ctx context.Context, network, address string) (net.Conn, error) {
			client, err := k.pool.Get(ctx, tunnelID)
			if err != nil {
				return nil, fmt.Errorf("get SSH tunnel: %w", err)
			}
			conn, err := client.Dial(network, address)
			if err != nil {
				k.pool.Release(tunnelID)
				return nil, fmt.Errorf("dial K8S API through SSH tunnel: %w", err)
			}
			return &k8sTunnelConn{Conn: conn, pool: k.pool, assetID: tunnelID}, nil
		}))
	case dialProxy:
		// 代理密码入 socksdial 前须明文（与数据库族约定一致）。
		proxy := credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		opts = append(opts, k8spkg.WithDial(func(ctx context.Context, _, address string) (net.Conn, error) {
			return socksdial.Dial(ctx, proxy, address)
		}))
	}
	return opts
}

// dialSource 决定 K8S client 的底层拨号方式。
type dialSource int

const (
	dialNone dialSource = iota
	dialTunnel
	dialProxy
)

// selectDialSource 镜像 entity「SSH 隧道与 SOCKS5 代理互斥、隧道优先」约定：
// 配了隧道且连接池可用 → 隧道；否则未配隧道且配了代理 → 代理；其余直连。
func selectDialSource(asset *asset_entity.Asset, cfg *asset_entity.K8sConfig, poolAvailable bool) dialSource {
	if asset.SSHTunnelID != 0 && poolAvailable {
		return dialTunnel
	}
	if asset.SSHTunnelID == 0 && cfg.Proxy != nil {
		return dialProxy
	}
	return dialNone
}
```

> Note: the `k8sTunnelConn` type (lines 210-221) is unchanged — leave it as-is below the helper.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/k8s/ -run TestSelectDialSource -v`
Expected: PASS (all 6 sub-cases).

- [ ] **Step 5: Build + lint the touched packages**

Run: `go build ./... && golangci-lint run ./internal/app/k8s/...`
Expected: no errors. (If `golangci-lint` is unavailable in the environment, fall back to `go vet ./internal/app/k8s/...` and note it.)

- [ ] **Step 6: Commit**

```bash
git add internal/app/k8s/k8s_ops.go internal/app/k8s/k8s_ops_test.go
git commit -m "✨ K8s 客户端支持 SOCKS5 代理拨号"
```

---

## Task 3: Frontend — proxy round-trip in K8s serializer

**Files:**
- Modify: `frontend/src/components/asset/K8sConfigSection.config.ts` (entire file)
- Test: `frontend/src/components/asset/__tests__/K8sConfigSection.config.test.ts` (update fixtures + add proxy cases)

**Interfaces:**
- Consumes: `ConnectionFormFields`, `CONNECTION_DEFAULTS`, `ProxyConfigJSON`, `buildProxyJSON`, `parseConnectionFields` from `./proxyConfig`.
- Produces:
  - `interface K8sFormState extends ConnectionFormFields { kubeconfig: string; showKubeconfig: boolean; namespace: string; context: string }`
  - `K8S_DEFAULTS: K8sFormState`
  - `buildK8sConfig(state: K8sFormState, kubeconfigCiphertext: string, proxyPassword?: string): string` — key order `kubeconfig → namespace → context → proxy`; **no** `ssh_asset_id` (tunnel stays Asset top-level).
  - `parseK8sConfig(configJSON: string, assetTunnelId?: number): K8sFormState` — kubeconfig always `""` / showKubeconfig `false`; connection fields derived via `parseConnectionFields(cfg.proxy, assetTunnelId)`.
  - These are consumed by the section in Task 4.

- [ ] **Step 1: Write the failing tests**

Replace the contents of `frontend/src/components/asset/__tests__/K8sConfigSection.config.test.ts` with:

```ts
import { describe, it, expect } from "vitest";
import {
  buildK8sConfig,
  parseK8sConfig,
  K8S_DEFAULTS,
  type K8sFormState,
} from "@/components/asset/K8sConfigSection.config";
import { CONNECTION_DEFAULTS } from "@/components/asset/proxyConfig";

const FULL: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "production",
  context: "my-context",
  ...CONNECTION_DEFAULTS,
};

describe("buildK8sConfig (锁旧 save 键序: kubeconfig → namespace → context → proxy, 无 ssh_asset_id)", () => {
  it("全字段(ciphertext + namespace + context, 直连无 proxy)", () => {
    expect(buildK8sConfig(FULL, "ENC_KUBECONFIG")).toBe(
      '{"kubeconfig":"ENC_KUBECONFIG","namespace":"production","context":"my-context"}'
    );
  });

  it("仅 kubeconfig(无 namespace/context)", () => {
    expect(buildK8sConfig({ ...K8S_DEFAULTS }, "CIPHER")).toBe('{"kubeconfig":"CIPHER"}');
  });

  it("空 ciphertext 省略 kubeconfig 键", () => {
    const json = buildK8sConfig({ ...FULL }, "");
    expect(json).not.toContain("kubeconfig");
    expect(json).toBe('{"namespace":"production","context":"my-context"}');
  });

  it("namespace 为空时省略 namespace 键", () => {
    const json = buildK8sConfig({ ...FULL, namespace: "" }, "ENC");
    expect(json).not.toContain("namespace");
    expect(json).toContain('"kubeconfig":"ENC"');
  });

  it("context 为空时省略 context 键", () => {
    const json = buildK8sConfig({ ...FULL, context: "" }, "ENC");
    expect(json).not.toContain("context");
  });

  it("全空(无 ciphertext/namespace/context, 直连) → {}", () => {
    expect(buildK8sConfig({ ...K8S_DEFAULTS }, "")).toBe("{}");
  });

  it("不含 ssh_asset_id 键(隧道走 asset 顶层)", () => {
    const json = buildK8sConfig({ ...FULL, connectionType: "jumphost", sshTunnelId: 5 }, "ENC");
    expect(json).not.toContain("ssh_asset_id");
  });

  it("proxy 模式 + host:写 proxy 块为尾键(明文密码经预解析)", () => {
    const state: K8sFormState = {
      ...FULL,
      connectionType: "proxy",
      proxyType: "socks5",
      proxyHost: "proxy.example.com",
      proxyPort: 1080,
      proxyUsername: "pu",
    };
    expect(buildK8sConfig(state, "ENC", "PROXY_CIPHER")).toBe(
      '{"kubeconfig":"ENC","namespace":"production","context":"my-context",' +
        '"proxy":{"type":"socks5","host":"proxy.example.com","port":1080,"username":"pu","password":"PROXY_CIPHER"}}'
    );
  });

  it("proxy 模式但无 host:不写 proxy 块", () => {
    const json = buildK8sConfig({ ...FULL, connectionType: "proxy", proxyHost: "" }, "ENC", "X");
    expect(json).not.toContain("proxy");
  });

  it("非 proxy 模式(jumphost):即便填了 proxyHost 也不写 proxy 块", () => {
    const json = buildK8sConfig(
      { ...FULL, connectionType: "jumphost", sshTunnelId: 7, proxyHost: "proxy.example.com" },
      "ENC",
      "X"
    );
    expect(json).not.toContain("proxy");
  });
});

describe("parseK8sConfig (解 namespace/context + 派生连接方式)", () => {
  it("直连:无 proxy 无 tunnel → connectionType direct", () => {
    const s = parseK8sConfig('{"kubeconfig":"ENC","namespace":"ns","context":"ctx"}');
    expect(s.namespace).toBe("ns");
    expect(s.context).toBe("ctx");
    expect(s.kubeconfig).toBe("");
    expect(s.showKubeconfig).toBe(false);
    expect(s.connectionType).toBe("direct");
  });

  it("assetTunnelId 派生 jumphost", () => {
    const s = parseK8sConfig('{"namespace":"ns"}', 9);
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(9);
  });

  it("proxy round-trip:派生 proxy 模式 + 回填字段(密文不回显为明文)", () => {
    const s = parseK8sConfig(
      '{"namespace":"ns","proxy":{"type":"socks5","host":"h","port":1080,"username":"pu","password":"CIPHER"}}'
    );
    expect(s.connectionType).toBe("proxy");
    expect(s.proxyHost).toBe("h");
    expect(s.proxyPort).toBe(1080);
    expect(s.proxyUsername).toBe("pu");
    expect(s.proxyPassword).toBe("");
    expect(s.encryptedProxyPassword).toBe("CIPHER");
  });

  it("非法 JSON 回退 K8S_DEFAULTS", () => {
    expect(parseK8sConfig("not-json")).toEqual({ ...K8S_DEFAULTS });
  });

  it("空字符串回退 K8S_DEFAULTS", () => {
    expect(parseK8sConfig("")).toEqual({ ...K8S_DEFAULTS });
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `frontend/`): `pnpm test K8sConfigSection.config`
Expected: FAIL — type errors / `buildK8sConfig` ignores the 3rd arg / `parseK8sConfig` returns `{namespace, context}` only (missing `connectionType` etc.).

- [ ] **Step 3: Rewrite the serializer**

Replace the contents of `frontend/src/components/asset/K8sConfigSection.config.ts` with:

```ts
import {
  CONNECTION_DEFAULTS,
  buildProxyJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyConfigJSON,
} from "./proxyConfig";

export interface K8sFormState extends ConnectionFormFields {
  kubeconfig: string;
  showKubeconfig: boolean;
  namespace: string;
  context: string;
}

export const K8S_DEFAULTS: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "",
  context: "",
  ...CONNECTION_DEFAULTS,
};

/**
 * 保存序列化(键序锁旧 save 分支: kubeconfig → namespace → context,proxy 为尾键)。
 * kubeconfigCiphertext 由调用方预解析(新值加密 or 编辑保留旧密文);
 * proxyPassword 由 resolveSaveProxyPassword(save=密文)或 state.proxyPassword(test 不适用 K8s)预解析。
 * 纯函数 — 无副作用,可直接做 golden 测试。
 * **不含 ssh_asset_id** — SSH 隧道走 Asset 顶层字段;隧道与代理互斥,按 connectionType 二选一。
 */
export function buildK8sConfig(state: K8sFormState, kubeconfigCiphertext: string, proxyPassword = ""): string {
  const cfg: Record<string, unknown> = {};
  if (kubeconfigCiphertext) cfg.kubeconfig = kubeconfigCiphertext;
  if (state.namespace) cfg.namespace = state.namespace;
  if (state.context) cfg.context = state.context;
  const proxy = buildProxyJSON(state, proxyPassword);
  if (proxy) cfg.proxy = proxy;
  return JSON.stringify(cfg);
}

/**
 * 编辑态回填(镜像旧 loadK8sConfig:解析 namespace/context)+ 派生连接方式。
 * kubeconfig 密文从不预填;connectionType 由 assetTunnelId(asset 顶层)与 proxy 派生。
 */
export function parseK8sConfig(configJSON: string, assetTunnelId = 0): K8sFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}") as {
      namespace?: string;
      context?: string;
      proxy?: ProxyConfigJSON;
    };
    return {
      kubeconfig: "",
      showKubeconfig: false,
      namespace: cfg.namespace || "",
      context: cfg.context || "",
      ...parseConnectionFields(cfg.proxy, assetTunnelId),
    };
  } catch {
    return { ...K8S_DEFAULTS };
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run (from `frontend/`): `pnpm test K8sConfigSection.config`
Expected: PASS (all build + parse cases).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/K8sConfigSection.config.ts frontend/src/components/asset/__tests__/K8sConfigSection.config.test.ts
git commit -m "✨ K8s 配置序列化支持 SOCKS5 代理 round-trip"
```

---

## Task 4: Frontend — migrate `K8sConfigSection` to config-driven rendering

**Files:**
- Modify: `frontend/src/components/asset/K8sConfigSection.tsx` (entire file)
- Test: `frontend/src/components/asset/__tests__/K8sConfigSection.test.tsx` — **do NOT edit**; it must stay green as the regression net.

**Interfaces:**
- Consumes: `useConfigSection` (`./useConfigSection`); `buildConfigGroups`, `type ConfigGroupSchema` (`./configFields`); `resolveSaveProxyPassword` (`./proxyConfig`); `buildK8sConfig`/`parseK8sConfig`/`K8S_DEFAULTS`/`K8sFormState` (Task 3); `Field` (`./fields`); `ConfigTabs` (`./ConfigTabs`); `AssetFormHandle`/`ConfigSectionProps` (`@/lib/assetTypes/formContract`).
- Produces: the migrated `K8sConfigSection` component (same `forwardRef` ref contract: `buildConfig` returns `{ configJSON, sshTunnelId }`, `buildTestConfig === null`, reports `SectionValidity` with `canTest:false`).
- **In-component schema pattern** (mirrors Database/SSH from Phase 3): `K8S_GROUPS` is defined *inside* the component so the kubeconfig `custom` `render` closure captures `t`/`isEditing`/`placeholder`.

- [ ] **Step 1: Confirm the regression net currently passes (baseline)**

Run (from `frontend/`): `pnpm test K8sConfigSection.test`
Expected: PASS (7 tests). This is the contract the rewrite must preserve. (No new failing test is authored for this task — the existing component test IS the spec; the rewrite is a refactor guarded by it. The serializer behavior changes were already TDD'd in Task 3.)

- [ ] **Step 2: Rewrite the section**

Replace the contents of `frontend/src/components/asset/K8sConfigSection.tsx` with:

```tsx
import { forwardRef } from "react";
import { useTranslation } from "react-i18next";
import { Eye, EyeOff } from "lucide-react";
import { Button, Textarea } from "@opskat/ui";
import { Field } from "@/components/asset/fields";
import { ConfigTabs } from "@/components/asset/ConfigTabs";
import { useConfigSection } from "@/components/asset/useConfigSection";
import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
import { resolveSaveProxyPassword } from "./proxyConfig";
import { buildK8sConfig, parseK8sConfig, K8S_DEFAULTS, type K8sFormState } from "./K8sConfigSection.config";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const K8sConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function K8sConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const isEditing = !!editAsset;
  const kubeconfigPlaceholder = isEditing
    ? t("asset.k8sKubeconfigEditPlaceholder")
    : t("asset.k8sKubeconfigPlaceholder");

  const { state, patch } = useConfigSection<K8sFormState>({
    ref,
    editAsset,
    onValidityChange,
    init: (a) => (a ? parseK8sConfig(a.Config ?? "", a.sshTunnelId || 0) : { ...K8S_DEFAULTS }),
    validate: (s) => {
      // kubeconfig 新建必填;编辑态空也可保存(保全旧 saveDisabledReason 逻辑)。
      const canSave = isEditing || !!s.kubeconfig.trim();
      return { canTest: false, canSave, saveDisabledReason: canSave ? "" : "asset.formMissingKubeconfig" };
    },
    build: async (s, ctx) => {
      let ciphertext = "";
      if (s.kubeconfig) {
        // 用户输入了新的 kubeconfig（明文 YAML），加密后落库；失败抛出由 handleSubmit catch 处理。
        ciphertext = await ctx.encryptPassword(s.kubeconfig);
      } else if (editAsset) {
        // 编辑模式且未输入新值：保留原 ciphertext。
        try {
          const old = JSON.parse(editAsset.Config || "{}") as { kubeconfig?: string };
          if (old.kubeconfig) ciphertext = old.kubeconfig;
        } catch {
          // 旧 config 解析失败：让 ciphertext 缺失冒到后端校验
        }
      }
      return {
        configJSON: buildK8sConfig(s, ciphertext, await resolveSaveProxyPassword(s, ctx.encryptPassword)),
        sshTunnelId: s.connectionType === "jumphost" ? s.sshTunnelId : 0,
      };
    },
    deps: [editAsset],
  });

  const K8S_GROUPS: ConfigGroupSchema<K8sFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        {
          kind: "custom",
          render: (s, p) => (
            <Field label={t("asset.k8sKubeconfig")} required={!isEditing}>
              {s.showKubeconfig ? (
                <div className="relative min-w-0 overflow-hidden">
                  <Textarea
                    value={s.kubeconfig}
                    onChange={(e) => p({ kubeconfig: e.target.value })}
                    placeholder={kubeconfigPlaceholder}
                    rows={4}
                    className="font-mono text-xs pr-9 whitespace-pre-wrap break-all"
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="absolute right-1 top-2 h-7 w-7"
                    onClick={() => p({ showKubeconfig: false })}
                  >
                    <EyeOff className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ) : (
                <Button type="button" variant="outline" className="w-full" onClick={() => p({ showKubeconfig: true })}>
                  <Eye className="h-3.5 w-3.5 mr-1" />
                  {isEditing ? t("asset.k8sRevealKubeconfig") : t("asset.k8sEnterKubeconfig")}
                </Button>
              )}
            </Field>
          ),
        },
        { kind: "text", key: "namespace", label: "asset.k8sNamespace", placeholder: "default" },
        { kind: "text", key: "context", label: "asset.k8sContext", placeholder: "current context" },
      ],
    },
    { key: "tunnel", label: "asset.tabTunnel", fields: [{ kind: "tunnel" }] },
  ];

  const groups = buildConfigGroups(K8S_GROUPS, { state, patch, ctx: { editAsset } });
  return <ConfigTabs groups={groups} />;
});
```

- [ ] **Step 3: Run the regression net to verify it stays green**

Run (from `frontend/`): `pnpm test K8sConfigSection`
Expected: PASS — both `K8sConfigSection.test.tsx` (7) and `K8sConfigSection.config.test.ts` (Task 3 cases).

> If `K8sConfigSection.test.tsx` fails, do NOT edit the test — the rewrite diverged from the contract. Common culprits: `init` not passing `a.sshTunnelId` (breaks the `sshTunnelId が引き継がれる` test), or `build`'s `connectionType==="jumphost"` gate dropping a tunnel that parsed as jumphost. Fix the section.

- [ ] **Step 4: Full frontend gates**

Run (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint && pnpm test`
Expected: tsc 0 errors, lint 0 errors/warnings, full vitest suite green.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/K8sConfigSection.tsx
git commit -m "♻️ K8s 配置区改用配置驱动渲染"
```

---

## Verification (observe, don't assert — AGENTS.md)

K8s has no Test-Connection button (`buildTestConfig === null`), so the proxy dial path is verified by observation, not an automated connect test:

1. **Automated coverage already in place:** `TestSelectDialSource` (decision logic), `TestProxyConfigRoundTrip/k8s` (entity round-trip), and the frontend serializer goldens (proxy JSON round-trip + `connectionType` derivation).
2. **Manual/observed proxy connect (optional, when a SOCKS5 proxy + cluster are available):** create a K8s asset with 连接方式 = 代理, save, open the cluster page, then read `logs/opskat.log` to confirm the API call dialed through the proxy (no direct connection to the API server) and `audit_logs` in `opskat.db` for the cluster-info fetch. See `docs/testing-debugging-guide.md`.

## Final gates (run once, after Task 4)

- Go: `go build ./... && go test ./... && golangci-lint run`
- Frontend (from `frontend/`): `pnpm exec tsc --noEmit && pnpm lint && pnpm test`

## Self-Review (completed during planning)

- **Spec coverage (附录 A):** entity field → Task 1; `k8sClientOptions` proxy branch + tunnel-priority → Task 2; `K8sFormState` folds `ConnectionFormFields` → Task 3; `parseK8sConfig`/`buildK8sConfig` proxy round-trip → Task 3; tunnel→standard `{kind:"tunnel"}` → Task 4; backend isolated as own commits → Tasks 1–2; backend TDD + frontend round-trip golden + opsctl/logs verification → Tasks 1–3 + Verification section. ✅
- **Regression compatibility verified by hand-trace:** all 7 cases in the unedited `K8sConfigSection.test.tsx` still pass under the new serializer (direct/jumphost derivation preserves `sshTunnelId` and the kubeconfig key-order; no proxy emitted for direct/jumphost). The existing `K8sConfigSection.config.test.ts` is intentionally rewritten because the serializer signature changes are part of 附录 A (the one exception to "don't touch serializers").
- **Type consistency:** `buildK8sConfig(state, ciphertext, proxyPassword?)` and `parseK8sConfig(configJSON, assetTunnelId?)` signatures match between Task 3 (definition) and Task 4 (call sites); `selectDialSource`/`dialSource`/`dialProxy` names match between Task 2 impl and test.
- **No placeholders:** every step has concrete code/commands. ✅
```
