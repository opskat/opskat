# Adding a Built-In Asset Type

> This guide is the concrete how-to for adding a new built-in asset type to OpsKat: which interfaces to implement, which files to edit, which parts are registration-driven, and which parts still require shared-code edits.
>
> Engineering principles such as SOLID, high cohesion/low coupling, Reuse first, and the Fix policy live in [`../AGENTS.md`](../AGENTS.md). The architecture and subsystem map live in [`./ARCHITECTURE.md`](./ARCHITECTURE.md). This guide only covers the asset-type integration path. Before editing this file, read [`./DOC-MAINTENANCE.md`](./DOC-MAINTENANCE.md).

## Core Idea: Register, Do Not Switch

Asset types are extended through registries where possible. A new type means registering it in the backend and frontend registries, then providing that type's own form, detail view, serialization, and connection logic. Do not add `if assetType == "xxx"` or `switch protocol` to shared dispatchers. That coupling is what the registries are meant to remove.

- Backend registry: `internal/assettype/`. Implement `AssetTypeHandler` and call `Register(&xxxHandler{})` from the file's `init()`.
- Frontend registry: `frontend/src/lib/assetTypes/`. Call `registerAssetType({...})`.

The built-in set must be enumerated from committed code, not hardcoded by count:

```bash
git grep -n "Register(&" -- internal/assettype/*.go | grep -v '_test.go'
```

Current built-ins on this branch are `ssh`, `database`, `redis`, `mongodb`, `kafka`, `k8s`, `serial`, `local`, and `etcd`. Frontend registration is the side-effect import list in `frontend/src/lib/assetTypes/index.ts`.

## Integration Surface Depends on Capabilities

Not every new type touches every item below. A minimal type such as `local` has no credentials, no connection pool, and no dedicated policy kind, so it touches relatively little. Query-surface types are heavier because query routing, panel selection, tab state, and persistence are not yet registry-driven; see [section 7](#7-shared-code-coupling-points-that-are-still-not-registered).

| Area | Registration-driven | Shared-code edit still required |
| --- | --- | --- |
| AI add/update/get/list safe-view handlers | Yes, via `assettype.Get(type)` | Update the AI tool schema/descriptions when the model-facing args change |
| Connection test button | Yes, once a binder calls `conntest.Register` from `New()` | None in `System.TestAssetConnection` |
| Policy test / built-in policy groups when reusing an existing kind | Yes, via `PolicyKind()` returning that kind | None |
| Type selector, filters, grouping, labels, detail-card choice, config-section rendering, connect action dispatch, new-tab visibility, file-manager menu visibility, and Test button visibility | Yes, derived from the frontend asset-type registry | File-manager opening still has an `ssh` guard in `App.tsx`; see section 7 |
| Entity type constants, config struct/accessors, entity validation, and entity `CanConnect()` | No | `internal/model/entity/asset_entity/asset.go` still has literals and type switches here |
| Frontend registration side-effect import | No | Add one `import "./<newtype>";` in `assetTypes/index.ts` |
| i18n | No | Add keys to both `common.json` locale files |
| Brand-new policy kind with new semantics | Mostly table/register based | Const aliases, policy structs, test handler registration, built-in group data, and possibly a group-policy migration |
| Connection pool `DialXxx` for networked types | No central registry | Add a type-specific `internal/connpool/<type>.go`, called by name from binders/testers |
| App binder for runtime panels | No | Wire `main.go` when the type needs its own runtime panel |
| Query route / terminal transport / AI mention | No | `queryStore.ts`, `terminalStore.ts`, `MainPanel.tsx`, `App.tsx`, and `ai/*` as needed |

## Shared Capability Map

Several asset-type features are shared capabilities rather than type-local inventions. Before adding code for one of them, find the owner module and plug into it.

| Capability | Backend owner | Frontend owner | Notes |
| --- | --- | --- | --- |
| Asset-type registration and AI safe views | `internal/assettype/` | `src/lib/assetTypes/` | Backend add/update/get/list dispatch is registration-driven through `assettype.Get(type)`. Frontend selector/filter/detail/config rendering is derived from `registerAssetType`. |
| Config form contract and serialization | Entity config structs plus type-specific services | `src/lib/assetTypes/formContract.ts` and `<Name>ConfigSection*.ts` | The shell calls `buildConfig` / `buildTestConfig`; type sections own parsing, validation, and exact JSON output. |
| SSH tunnels and TLS | `internal/connpool/` (`NewSSHTunnel`, `BuildTLSConfig`) | `AssetSelect`, `sshTunnelId`, and type config sections | Networked types resolve the tunnel from top-level `asset.SSHTunnelID` first, then legacy config fields. Save/test serialization differs; see [F3](#f3-configsection-and-pure-configts-serialization). |
| Password credentials and SSH keys | `credential_svc`, `credential_mgr_svc`, `credential_resolver` | `credentialConfig.ts`, `useAssetCredential.ts`, `PasswordSourceField.tsx`, and SSH key controls | `credential_id` is not globally one thing. In SSH password-auth it refers to a password credential; in SSH key-auth it refers to an `ssh_key` credential. |
| Connection tests | `internal/service/conntest` plus binder `New()` registration | `testable` and `buildTestConfig` | Do not edit `System.TestAssetConnection`; register a tester and let the common binding dispatch. |
| Policy and policy groups | `internal/model/entity/policy`, `internal/ai/policy`, policy-group entities | `PolicyDefinition`, `CommandPolicyCard`, `PolicyGroupManager` | Reuse an existing policy kind when semantics match; add a new kind only for genuinely new policy behavior. |
| Runtime routing and panels | Type-specific app binders and services | `connectAction`, `terminalStore.ts`, `queryStore.ts`, `MainPanel.tsx`, `App.tsx` | Registry covers basic connect action dispatch, but query tab state and panel selection still have shared-code coupling points. |
| Detail display | Asset config structs | `DetailInfoCardProps`, `parseDetailConfig`, `InfoItem`, `TunnelInfo` | Detail-card selection is registered. Tunnel-capable cards should display top-level `asset.sshTunnelId` first, then legacy config fields. |
| File manager | SSH/SFTP services | `canOpenFileManager` plus `App.tsx` handler | Menu visibility is registered; the current handler is still SSH-only. |
| AI tool schema | `internal/ai/tool/tools_asset.go` | Mention/open helpers when needed | Asset add/update handlers are registry-driven, but model-facing schema/descriptions still need shared edits when args change. |

## Backend Integration

All paths below are relative to the repository root. Use file and symbol names rather than line numbers; line numbers drift, `git grep <symbol>` does not.

### B1. Entity Type, Config, Validation, and CanConnect

File: `internal/model/entity/asset_entity/asset.go`

Add the entity-layer literals and helpers:

- `AssetTypeXxx = "xxx"` in the existing `AssetType*` const block.
- `IsXxx()` beside helpers such as `IsRedis()`.
- `XxxConfig` plus `GetXxxConfig()` and `SetXxxConfig()`.
- `validateXxx()` plus a `Validate()` case. Current built-in validation still uses a type switch in this entity file.
- A `CanConnect()` case. Current built-in connectability checks also still use a type switch in this entity file.

Asset configuration is stored in a single JSON `config` column, not in one table per asset type. Adding the asset type itself does not require a database migration unless you add or change top-level table columns.

### B2. Core Handler

File: `internal/assettype/<type>.go`

Implement `AssetTypeHandler` from `internal/assettype/registry.go`:

```go
type AssetTypeHandler interface {
	Type() string
	DefaultPort() int
	SafeView(a *asset_entity.Asset) map[string]any
	ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error)
	DefaultPolicy() any
	PolicyKind() string
	ValidateCreateArgs(args map[string]any) error
	ApplyCreateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
	ApplyUpdateArgs(ctx context.Context, a *asset_entity.Asset, args map[string]any) error
}
```

| Method | Responsibility |
| --- | --- |
| `Type()` | Canonical type string, normally `asset_entity.AssetTypeXxx`; this is the registry and dispatch key |
| `DefaultPort()` | Default port, for example Redis `6379`, SSH `22`, etcd `2379`; return `0` for types without a port |
| `SafeView(a)` | Non-sensitive field projection for AI asset list/detail responses; never expose passwords, private keys, certificate paths, or kubeconfig |
| `ResolvePassword(ctx, a)` | Decrypt or resolve plaintext credentials; DB-family types use `credential_resolver.Default().ResolvePasswordGeneric`, SSH uses `ResolveSSHCredentials`, no-credential types return `"", nil` |
| `DefaultPolicy()` | The default policy object for this type, such as `asset_entity.DefaultRedisPolicy()` |
| `PolicyKind()` | Canonical policy kind from `internal/model/entity/policy.PolicyKind*`; `Register()` uses this to register the asset-type-to-kind mapping automatically |
| `ValidateCreateArgs(args)` | Required-argument validation for AI-created assets; called before `ApplyCreateArgs` |
| `ApplyCreateArgs` / `ApplyUpdateArgs` | Fill or update the config from AI tool args; encrypt secrets through `credential_svc.Default().Encrypt` |

Registration example:

```go
func init() {
	Register(&redisHandler{})
	policy.RegisterDefaultPolicy("redis", func() any { return asset_entity.DefaultRedisPolicy() })
}
```

`Register(h)` stores the handler and, when `h.PolicyKind() != ""`, calls `policy.RegisterAssetKind(h.Type(), h.PolicyKind())`. The asset-type-to-policy-kind mapping is automatic; do not maintain a separate central map.

`policy.RegisterDefaultPolicy(...)` is a separate registry used by `System.GetDefaultPolicy`. Types without an exposed asset policy, such as `local`, can omit it even if the interface's `DefaultPolicy()` returns a value to satisfy the interface.

Reuse shared argument parsing:

- `ArgString`
- `ArgInt`
- `ArgInt64`
- `ArgBool`
- `ArgStringSlice`
- `validateRemoteServerArgs` for the existing SSH/database/Redis/MongoDB host/port/username validation shape

The AI handlers in `internal/ai/tool/tool_handlers_asset.go` dispatch add/update/get/list through `assettype.Get(type)`, so handler code is registration-driven. Also update `internal/ai/tool/tools_asset.go` when the new type has model-facing create/update args; that schema is descriptive, but it is the contract the model sees.

### B3. Policy: Reuse an Existing Kind or Add a New Kind

Most new types should reuse an existing policy kind. In that case, return the existing `PolicyKind*` constant from `PolicyKind()` and skip the rest of this section. The new type inherits that kind's policy behavior.

Only add a new policy kind when the policy semantics are genuinely new:

1. Kind constants:
   - Add `PolicyKindXxx` in `internal/model/entity/policy/registry.go`.
   - Add the alias in `internal/ai/policy/policy_kind.go`.
2. Policy struct and default:
   - Add `XxxPolicy` and `DefaultXxxPolicy()` in `internal/model/entity/policy/policy.go`.
   - Add the asset-entity alias in `internal/model/entity/asset_entity/asset.go`.
3. Policy test handler:
   - Register the kind in `internal/ai/policy/policy_kind.go` with `registerPolicyKind(...)`.
   - Add `testXxxPolicy` in `internal/ai/policy/policy_tester.go`.
   - Add `ResolveXxxGroups` in `internal/ai/policy/policy_group_resolve.go` if the kind supports reusable policy groups.
4. Built-in policy groups:
   - If the kind has built-ins, add `PolicyTypeXxx` in `internal/model/entity/policy_group_entity/policy_group.go`.
   - Add a `registerBuiltinGroups(PolicyTypeXxx, ...)` block in that file.
   - `Validate`, `BuiltinGroups`, and built-in ordering are derived from that registration data. This package is in the entity layer and must not import `assettype`.
5. Group-policy inheritance:
   - The `groups` table has one column per inherited policy kind.
   - Add a new migration such as `migrations/<ts>_group_<kind>_policy.go` with `ALTER TABLE groups ADD COLUMN <kind>_policy TEXT`; use `migrations/202605260001_group_etcd_policy.go` as the pattern.
   - Append the migration to the slice in `migrations/migrations.go`; migrations are append-only.
   - Add the field and accessors in `internal/model/entity/group_entity/group.go`.

### B4. Connection Pool

Directory: `internal/connpool/`

`connpool` is not a registry. It is a set of explicit `DialXxx` functions called by binders and testers by name. Networked types should add `internal/connpool/<type>.go` and expose the type-specific dial function. Reuse `NewSSHTunnel` and `BuildTLSConfig` when applicable.

SSH tunnel resolution convention:

```go
tunnelID := asset.SSHTunnelID
if tunnelID == 0 {
	tunnelID = cfg.SSHAssetID // backward compat
}
```

Current frontend sections prefer the asset row's top-level `sshTunnelId` when loading forms. Saved config behavior is mixed for historical built-ins: SSH, Redis, and MongoDB omit their legacy tunnel field on save, while database, etcd, and Kafka still serialize `ssh_asset_id` in saved config. Test config is different because no asset row exists yet; see [F3](#f3-configsection-and-pure-configts-serialization).

Types without a connection pool, such as `serial` and `local`, do not add anything here.

### B5. Connection Test

Package: `internal/service/conntest`

Testers are binder instance methods because they hold live pools/managers, so they cannot register from `init()`. Register them from binder `New()`:

```go
// TestFunc = func(ctx context.Context, configJSON, plainPassword string) error
conntest.Register(asset_entity.AssetTypeXxx, b.testConnection)
```

`System.TestAssetConnection` in `internal/app/system/asset.go` is the single binding. It applies the shared envelope, then dispatches through `conntest.Lookup`:

- i18n context
- 10 second timeout
- `testreg` cancellation

Do not edit that dispatcher for a new asset type. The tester body should parse config, resolve credentials when needed, dial through the relevant service or `connpool`, and close the connection. A type without a registered tester has no Test button if the frontend definition leaves `testable` unset.

Frontend flow:

1. Set `testable: true` in `AssetTypeDefinition`.
2. Implement `buildTestConfig` in the `ConfigSection`.
3. `AssetForm` calls `TestAssetConnection(testID, tc.assetType, tc.configJSON, tc.password)`.
4. `System.TestAssetConnection` applies cancellation/timeout, then dispatches through the registered tester.

### B6. App Binder

File: `main.go`

Only types that need a runtime panel or public Wails binding need their own `internal/app/<type>/` binder. Wire a new binder in three places:

- Construct it, for example `xxxB := xxx.New(...)`.
- Add it to the `binders []Lifecycle` slice.
- Add it to the Wails `Bind: []interface{}{...}` list.

Pure config types and types whose connection test can be registered from an existing binder do not need a new binder.

### Backend Checklist

1. Add entity constants, `XxxConfig`, accessors, `validateXxx`, `Validate()` case, and `CanConnect()` case in `asset_entity/asset.go`. This is shared entity code and is not fully registered yet.
2. Add `internal/assettype/<type>.go`, implement `AssetTypeHandler`, and call `Register(...)`.
3. Policy kind: reuse an existing kind when possible; add a new kind only when the semantics are new. See B3.
4. Add `connpool/<type>.go` only for networked types that need pooled/dialed connections.
5. Register a connection tester with `conntest.Register` if the type supports Test.
6. Add an app binder and wire `main.go` only when the type needs runtime panel bindings.
7. AI add/update/get/list handler code needs no per-type edit, but `tools_asset.go` schema/descriptions should be updated when AI-visible args change.

## Frontend Integration

All paths below are relative to `frontend/`.

### F1. Registry

Directory: `src/lib/assetTypes/`

Core registry:

```ts
// _register.ts
export const registry = new Map<string, AssetTypeDefinition>();
export function registerAssetType(def: AssetTypeDefinition) { registry.set(def.type, def); }

// index.ts
export function getAssetType(type: string): AssetTypeDefinition | undefined { return registry.get(type); }
export function isBuiltinType(type: string): boolean { return registry.has(type); }
export function getBuiltinTypes(): AssetTypeDefinition[] { return [...registry.values()]; }
```

`AssetTypeDefinition` from `types.ts`:

```ts
export interface AssetTypeDefinition {
  type: string;
  icon: ComponentType<{ className?: string; style?: React.CSSProperties }>;
  aliases: string[];
  label: string;
  category: AssetTypeCategory; // "servers" | "databases" | "middleware" | "extension"
  canConnect: boolean;
  canConnectInNewTab: boolean;
  connectAction: "terminal" | "query";
  canOpenFileManager?: boolean;
  DetailInfoCard: ComponentType<DetailInfoCardProps>;
  ConfigSection?: ConfigSectionComponent;
  testable?: boolean;
  policy?: PolicyDefinition;
}
```

Registration example:

```ts
registerAssetType({
  type: "redis",
  icon: RedisIcon,
  aliases: ["redis"],
  label: "nav.redis",
  category: "databases",
  canConnect: true,
  canConnectInNewTab: false,
  connectAction: "query",
  DetailInfoCard: RedisDetailInfoCard,
  ConfigSection: RedisConfigSection,
  testable: true,
  policy: {
    policyType: "redis",
    titleKey: "asset.redisPolicy",
    hintKey: "asset.redisPolicyHint",
    testPlaceholderKey: "asset.policyTestRedisPlaceholder",
    fields: [
      { key: "allow_list", labelKey: "asset.redisPolicyAllowList", placeholderKey: "asset.redisPolicyPlaceholder", variant: "allow" },
      { key: "deny_list", labelKey: "asset.redisPolicyDenyList", placeholderKey: "asset.redisPolicyPlaceholder", variant: "deny" },
    ],
  },
});
```

Useful patterns:

- `ssh.ts`: `connectAction: "terminal"`, `canConnectInNewTab: true`, `canOpenFileManager: true`.
- `serial.ts`: terminal transport type, `testable: true`, command-policy UI through `policyType: "ssh"`.
- `local.ts`: no exposed policy and no Test button.
- `k8s.ts`: aliases `["k8s", "kubernetes"]`, no Test button, opens a bespoke page through `App.tsx`.
- `database.ts`: aliases `["database", "mysql", "postgresql"]`; driver differences fold into the single `database` type.

The unavoidable shared edit is the side-effect import. `registerAssetType` only runs when the module is imported, so add this line to `src/lib/assetTypes/index.ts`:

```ts
import "./<newtype>";
```

`src/lib/assetTypes/__tests__/registry.test.ts` asserts the exact `getBuiltinTypes()` order. Add the new type to that ordered assertion.

Once registered, the type selector, filters, grouping, labels, detail-card selection, config-section render path, connect action dispatch, new-tab visibility, menu visibility, and Test button visibility are derived from the registry. Do not add new type-string branches for those surfaces.

### F2. Form Contract

File: `src/lib/assetTypes/formContract.ts`

```ts
export interface AssetFormContext {
  isEdit: boolean;
  encryptPassword: (plain: string) => Promise<string>;
}

export interface AssetConfigBuildResult {
  configJSON: string;
  sshTunnelId: number;
}

export interface AssetTestConfig {
  assetType: string;
  configJSON: string;
  password: string;
}

export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
}

export interface SectionValidity {
  canTest: boolean;
  canSave: boolean;
  saveDisabledReason?: string;
}

export interface ConfigSectionProps {
  editAsset?: asset_entity.Asset;
  ctx: AssetFormContext;
  onValidityChange: (v: SectionValidity) => void;
  onIconChange?: (icon: string) => void;
}

export type ConfigSectionComponent = ForwardRefExoticComponent<ConfigSectionProps & RefAttributes<AssetFormHandle>>;
```

Contract:

- `ConfigSection` is a `forwardRef<AssetFormHandle, ConfigSectionProps>`.
- The section owns its own form state.
- It reports validity through `onValidityChange`.
- It exposes `buildConfig` and, when testable, `buildTestConfig` through `useImperativeHandle`.
- Untestable types return `null` for `buildTestConfig`.

`AssetForm.tsx` renders registered sections generically with `key={assetType}` so switching type remounts the section. Save calls `sectionRef.current.buildConfig(ctx)`. Test calls `buildTestConfig`. The shell has no per-type config-building branches; the remaining shared edits there are decorative defaults such as `DEFAULT_ICONS` and name placeholders.

### F3. ConfigSection and Pure `.config.ts` Serialization

Standard files for a type with persisted config:

- `<Name>ConfigSection.tsx`: `forwardRef` UI component. It initializes state from `editAsset`, reports validity, and exposes build methods.
- `<Name>ConfigSection.config.ts`: pure `buildXxxConfig` / `parseXxxConfig`, defaults, and `XxxFormState`. No React and no side effects.
- `__tests__/<Name>ConfigSection.config.test.ts`: golden tests for exact JSON output when key order, default omission, tunnel behavior, or credential fragments matter.

Most non-trivial serialized configs should have golden tests. Current simple types such as `local` and `serial` have config helpers but no committed golden tests; do not copy that omission for a new type whose saved/test JSON can drift.

Why split pure functions: the JSON key order and default-omission rules are byte-sensitive. Pure functions let tests lock the exact output without rendering React.

For new tunnel-capable types, SAVE and TEST serialization should differ for tunnel IDs:

- SAVE stores the tunnel in the asset's top-level `sshTunnelId` column and omits the legacy tunnel field from config.
- TEST has no saved asset row, so it must include the tunnel ID in `configJSON`.

Current built-ins are not all identical:

- Redis and MongoDB implement the split with `buildXxxConfig(state, cred, includeSshAssetId = false)`: save uses the default `false`, test passes `true`.
- SSH uses the same idea with `SSHBuildOptions.includeJumpHost`: save omits `jump_host_id`, test includes it.
- Database, etcd, and Kafka still write `ssh_asset_id` in saved config as well as returning top-level `sshTunnelId` from `buildConfig`.

New tunnel-capable types should follow the SAVE/TEST split and lock both paths with tests.

### F4. Shared Credential Layer

Files:

- `src/components/asset/credentialConfig.ts`
- `src/components/asset/useAssetCredential.ts`
- `src/components/asset/PasswordSourceField.tsx`

Database-family password/managed-credential types reuse this layer: `database`, `redis`, `mongodb`, `kafka`, and `etcd`. SSH password-auth also reuses it.

- `useAssetCredential(editAsset, initialCredentialConfig?)` owns credential sub-state, loads `ListCredentialsByType("password")`, and initializes from either the explicit credential fragment or `editAsset.Config`.
- `credentialConfig.ts` exposes `initCredentialFromConfig`, `resolveTestCredential`, and `resolveSaveCredential(s, encrypt)`.
- `resolveSaveCredential` emits `credential_id` for managed credentials, encrypts new inline passwords, or reuses the existing encrypted password. Encryption errors propagate; they are not swallowed.
- UI uses the shared `PasswordSourceField` primitive.

SSH keeps its own authentication-mode and private-key handling:

- Password auth passes only password-auth config into `useAssetCredential`; key-auth `credential_id` must not initialize a password credential.
- Managed key auth loads `ListCredentialsByType("ssh_key")` and stores the selected SSH-key credential ID in `credential_id`.
- File key auth stores selected private-key paths in `private_keys`; passphrase handling is local to `SSHConfigSection`.

On the backend, credential storage and SSH key management live in `credential_svc`, `credential_mgr_svc`, and `credential_resolver`. Do not add type-local encryption or key-storage code. K8s encrypts kubeconfig directly through `ctx.encryptPassword(kubeconfig)`. `local` and `serial` have no credentials.

### F5. Detail Card

Directory: `src/components/asset/detail/`

Add `<Name>DetailInfoCard.tsx`, implement `DetailInfoCardProps` (`{ asset, sshTunnelName }`), and reference it from the registration as `DetailInfoCard: XxxDetailInfoCard`.

Reuse:

- `parseDetailConfig<T>`
- `DetailSection`
- `DetailGrid`
- `InfoItem`
- `TunnelInfo`
- `MASKED_SECRET`
- `ENABLED_VALUE`

The detail panel selects the card through `getAssetType(...).DetailInfoCard`, not through type-string branches.

For tunnel-capable types, display the top-level asset tunnel first and keep the legacy config field only as a fallback:

```ts
const tunnelName = sshTunnelName(asset.sshTunnelId || cfg.ssh_asset_id);
```

SSH uses the same display rule with `cfg.jump_host_id`.

### F6. i18n

Files:

- `src/i18n/locales/en/common.json`
- `src/i18n/locales/zh-CN/common.json`

Add keys to both locales; there is no cross-language fallback policy for new UI strings.

Required or likely keys:

- `nav.<type>` for `AssetTypeDefinition.label`.
- `asset.*` field labels used by the config section.
- `asset.formMissing*` keys returned as `saveDisabledReason`.
- Policy keys when the type exposes a policy: `titleKey`, `hintKey`, `testPlaceholderKey`, each field's `labelKey`, and each field's `placeholderKey`.

### Frontend Checklist

1. Add `src/lib/assetTypes/<newtype>.ts` and call `registerAssetType`.
2. Add `import "./<newtype>";` in `src/lib/assetTypes/index.ts`.
3. Add the type to the ordered assertion in `src/lib/assetTypes/__tests__/registry.test.ts`.
4. Add `src/components/asset/<Name>ConfigSection.config.ts`.
5. Add golden tests for serialized config when the type has meaningful saved/test JSON behavior.
6. Add `src/components/asset/<Name>ConfigSection.tsx`; reuse `useAssetCredential` and `PasswordSourceField` when the type uses password/managed credentials.
7. Add `src/components/asset/detail/<Name>DetailInfoCard.tsx`.
8. Add i18n keys to both locale files.
9. Optionally update decorative defaults in `AssetForm.tsx` such as `DEFAULT_ICONS` and name placeholders.

## 7. Shared-Code Coupling Points That Are Still Not Registered

The following surfaces still branch on type strings. A new type only needs these edits if it needs the gated capability.

| File | Branches on | Capability gated | Edit when |
| --- | --- | --- | --- |
| `internal/model/entity/asset_entity/asset.go` (`Validate`, `CanConnect`) | Built-in `AssetType*` cases | Entity validation and active/connectable checks | Every new built-in type with config validation or connectability |
| `src/stores/terminalStore.ts` (`transportForAsset`) | `serial`, `local`, else `ssh` | Terminal transport kind | New terminal type whose transport is not SSH |
| `src/stores/queryStore.ts` (`openQueryTab`, persistence rehydrate, `QueryTabMeta.assetType` union) | `database`, `mongodb`, `redis`, plus query union literals | Query tab metadata, config parsing, initial state, persisted restore | `connectAction: "query"` types that need query-store state |
| `src/components/layout/MainPanel.tsx` | `database`, `redis`, `kafka`, `etcd`, else MongoDB | Which query panel renders | Types that need their own query panel |
| `src/App.tsx` (`handleConnectAsset`) | `k8s` | Bespoke page tab (`k8s-cluster`) instead of generic connection | Types that need a bespoke page |
| `src/App.tsx` (`handleOpenFileManager`) | `asset.Type !== "ssh"` early return | File-manager opening behavior; menu visibility is registered, but the handler is still SSH-only | Types that need SFTP/file-manager opening |
| `src/components/asset/CommandPolicyCard.tsx` and `PolicyGroupManager.tsx` | `ssh`, `k8s`, `database`, `redis`, `mongodb`, and built-in tab keys | Policy editor tab mapping and labels | Types whose policy UI needs custom tab mapping or labels |
| `src/components/ai/MentionList.tsx`, `ai/input/content.ts`, `lib/mentionXml.ts`, `lib/openMentionTarget.ts` | `database` | AI mention databases/tables | Types that need mention autocomplete/open behavior |
| `internal/ai/tool/tools_asset.go` | Supported type and field descriptions | Model-facing AI tool schema | Types with AI-visible create/update args |

Already registered; do not add branches:

- Connect action dispatch through `connectAction`.
- New-tab visibility through `canConnectInNewTab`.
- File-manager menu visibility through `canOpenFileManager`.
- Test button visibility through `testable`.
- Type selector, filtering, grouping, and labels through `getBuiltinTypes()`.
- Detail-card selection.
- ConfigSection rendering path.

Practical impact:

- Terminal types like SSH are relatively light unless they introduce a new transport.
- Query-surface types are heavier because query route, panel selection, tab state, and persisted restore are not yet registry-driven.
- AI mention support is database-specific today; adding mention support for another query type requires the AI mention files listed above.

## Verification

Backend:

- `go build ./...`
- `go test ./internal/...`
- Add `-race` for changed backend packages when the touched code has concurrency, connection pools, cancellation, or shared registries.
- `make lint` or `golangci-lint run --timeout 10m ./internal/...`
- Connection tests should flow through `conntest` and `System.TestAssetConnection`.

Frontend, from `frontend/`:

- `npx tsc --noEmit`
- `npx vitest run`
- `pnpm lint` or focused `npx eslint <paths>`
- Serialization helpers that affect saved/test JSON should have golden tests.
- Registry order is locked by `src/lib/assetTypes/__tests__/registry.test.ts`.

Wails bindings:

- `frontend/wailsjs` is generated and gitignored.
- When bindings change, regenerate through the project's Wails flow described in [`DEVELOP.md`](./DEVELOP.md), such as `make dev` / `wails build` or a deliberate binding-generation command when appropriate.
- Verify backend truth from `internal/app/*.go`, not from generated TypeScript files.

Observable verification:

- GUI clicks are not the only verification path. Prefer headless or observable side effects when possible.
- Use `opsctl`, structured logs in `logs/opskat.log`, and the SQLite DB `opskat.db` (especially `audit_logs`).
- See [`./testing-debugging-guide.md`](./testing-debugging-guide.md).

## Reference: Design Evolution

The asset-type and policy registration work was introduced over several phases. Historical snapshots live in:

- `docs/superpowers/specs/2026-06-04-asset-type-decoupling-design.md`
- `docs/superpowers/specs/2026-06-05-assetform-registration-phase4-design.md`

Those date-named files are archives for specific pieces of work, not current truth. Current truth is this branch's committed code.
