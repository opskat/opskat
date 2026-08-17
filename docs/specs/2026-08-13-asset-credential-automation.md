# opsctl 与 AI 资产创建及托管凭据自动化

> Status: Approved
> Owner: OpsKat maintainers
> Last updated: 2026-08-13

**Objective:** 让自动化调用方能够通过 opsctl 或统一 AI 工具创建任意已注册的内建资产类型，安全地引用既有托管凭据，并通过统一的“密钥管理”查询面发现凭据、SSH 密钥和 SSH Agent 来源。

**Hard invariant:** `put_asset` / `opsctl create asset` 的 producer-owned 审批与 Audit projection、安全查询 DTO、安全创建结果和应用结构化日志必须省略 password、private key、passphrase、kubeconfig、Secret Access Key、SSH Agent endpoint 等 write-only material；通用 Audit、直接命令/tool result 和其他允许读取业务内容的表面按后续修正规格保留其边界收到的原值，不生成 `<redacted>` 近似值。既有资产连接、审批和凭据解析行为不得因本次扩展而回退。

## Problem

1. **opsctl 的资产创建面只覆盖部分内建类型。** `cmd/opsctl/command/create.go` 将 `--type` 和字段集合硬编码为 SSH、数据库、Redis、MongoDB、K8s，并在共享 CLI 中按类型组装配置；当前已注册 handler 还包括 etcd、Kafka、Serial、Local、RDP、VNC、OSS，自动化调用方无法通过同一命令创建这些资产，也会迫使以后新增类型继续修改共享分发代码。
2. **opsctl 无法在创建资产时安全地创建或引用托管凭据。** Issue [#283](https://github.com/opskat/opskat/issues/283) 的用户需要把常见的 IP、username、password 测试服务器交给 OpenCode + opsctl 自动录入；现有命令既没有 `credential_id` 引用入口，也没有 stdin 密码入口。
3. **AI 与 CLI 的凭据存储语义不一致且按类型不完整。** AI 已有统一 `put_asset` 和自由形态 `config`，但多数 handler 对 `config.password` 只保存资产内联密文；`credential_id` 仅在部分类型的创建路径中生效。相同意图在不同资产类型或调用面上会得到不同的数据形态。
4. **自动化调用方无法发现安全的凭据引用。** 桌面端已有凭据和 SSH Agent 来源管理 IPC，但 opsctl 与 AI 没有只读的凭据列表/详情工具；`--credential-id`、Agent 来源 ID 和身份指纹若没有发现入口，只能依赖用户返回 GUI 手工查询。
5. **SSH Agent 与数据库凭据在桌面端属于同一个“密钥管理”用户心智，但底层 ID 空间不同。** 将它们拆成互不相关的 CLI/AI 资源会造成体验漂移；仅使用裸数字 ID 又会让 `credentials` 与 `ssh_agent_sources` 中相同的 ID 发生歧义。

## Actors and user stories

1. As an OpenCode or shell automation user, I want to create any built-in OpsKat asset through one stable opsctl contract, so that adding a protocol does not require a new script shape.
2. As an automation user holding a plaintext password, I want to send it through stdin or an explicitly acknowledged command-line flag, so that OpsKat encrypts it in the asset without creating an unexpected managed credential.
3. As a user who already manages credentials in OpsKat, I want to discover a safe reference and reuse it when creating an asset, so that the secret is not duplicated.
4. As an OpsKat AI user, I want `put_asset` to use the same managed-credential semantics as opsctl, so that assets created by either surface are indistinguishable in the desktop app.
5. As an SSH Agent user, I want Agent sources and identities to appear in the same credential discovery surface as passwords and SSH keys, while remaining clearly typed and non-secret.

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | `opsctl create asset` gains generic `--config` and `--config-file` JSON-object inputs and delegates type validation/application to the registered asset handler. | This makes every registered built-in type reachable without adding protocol branches to shared CLI code. Rejected: add dozens of top-level protocol flags — it duplicates each handler contract and requires editing a shared switch for every new type. |
| 2 | Existing convenience flags remain compatible and explicitly supplied convenience values override the same non-secret keys from generic config. | Existing scripts must keep working. Rejected: remove or silently change old flags — unnecessary compatibility break. |
| 3 | Secret sources never use normal override precedence: plaintext secret, managed credential reference, and SSH Agent identity are mutually exclusive and conflicting inputs fail. | Silently choosing one secret source can bind the wrong account and leave misleading data. Rejected: “last flag wins” for credentials. |
| 4 | `--password-stdin`, `--password`, and plaintext secret fields accepted by AI/JSON are encrypted in the asset and never create a managed credential. | Credential creation must be explicit in the desktop key manager; automation reuses it through `credential_id`. Rejected: silently create globally reusable credentials as a side effect of asset creation. |
| 5 | `--password` is supported but visibly documented and warned as unsafe; stdin is the recommended plaintext path. | The user explicitly requested convenience support while accepting a warning. Rejected: only support `--password` — exposes secrets by default; rejected: prohibit it entirely — does not meet the confirmed interface. |
| 6 | Per-type credential behavior is declared by the registered type owner: accepted managed credential kinds, plaintext secret field, associated username field, and incompatible auth modes. | SSH password/key/Agent and OSS Secret Access Key do not share one universal field meaning. Rejected: shared type-string branches — violates the repository registration seam and will drift. |
| 7 | Asset create/update executes in one transaction after side-effect-free validation and, for opsctl, after desktop approval. | Asset creation does not create credentials, so failure cannot leave an orphan credential. |
| 8 | AI keeps the existing unified `put_asset`; no second credential-aware asset tool is added. | `put_asset` already owns all registered type configs. Rejected: add `create_asset_with_credential` — duplicates CRUD and splits the model’s decision path. |
| 9 | Credential discovery is unified as `list_credentials` / `get_credential` and `opsctl list credentials` / `opsctl get credential`, with typed refs `credential:<id>` and `agent-source:<id>`. | Matches the desktop “密钥管理” mental model while preventing cross-table ID ambiguity. Rejected: separate Agent commands — inconsistent user surface; rejected: bare ID for detail lookup — ambiguous. |
| 10 | Query surfaces return metadata and public identification only; they never expose secret material. | Automation needs IDs, names, usernames, fingerprints, status and usage—not decryption. Rejected: password/private-key reveal flags — creates a secret-exfiltration API for both CLI and AI. |
| 11 | Existing inline-encrypted asset secrets remain readable and are not migrated automatically. | The change is about new automation writes; rewriting historical assets is unrelated risk. Rejected: migration-on-read or bulk conversion — hidden data mutation and broader rollback scope. |

## Asset creation contract

`opsctl create asset` accepts every built-in asset type registered through the asset-type registry. The command must not maintain a hardcoded supported-type list; help and errors enumerate the registry. Extension-defined asset types that do not register through this built-in handler seam are outside this round.

The generic inputs are:

```text
--config '<JSON object>'
--config-file <path-to-JSON-object>
```

They are mutually exclusive. Invalid JSON, a non-object root, an unreadable file, or a field value rejected by the selected handler fails before approval and persistence. `--config-file` is a generic JSON config file; existing `--kubeconfig-file` remains a K8s convenience flag whose file content becomes the `kubeconfig` field.

Existing convenience flags remain available, including `--host`, `--port`, `--username`, `--auth-type`, database flags, SSH tunnel reference, K8s flags, group, description and icon. Only flags explicitly present on the command line override matching non-secret fields from generic config. Secret sources are handled by the separate rules below and never silently override each other.

The selected handler remains authoritative for required fields, defaults and legal combinations. `opsctl create asset --help` explains the generic mechanism and points callers to `opsctl help <type>` for the exact config contract. The create surface covers the database drivers and local/remote SQLite shapes that the built-in database model supports; SQLite does not accept password credentials. Nested or companion features that a handler does not advertise as creatable remain unavailable rather than being silently accepted.

A config key that the selected handler does not declare as accepted must produce a named validation error. Generic JSON must not silently discard misspelled or unsupported fields.

## Authentication inputs

The CLI exposes:

```text
--credential-id <id>
--password-stdin
--password <value>
```

`--credential-id`, `--password-stdin`, and `--password` are mutually exclusive with each other and with an equivalent plaintext/reference field already present in `--config` or `--config-file`. Plaintext is encrypted in the asset. Managed credentials and SSH keys must be created in the desktop key manager first and referenced by ID; asset automation never creates them.

`--password-stdin` reads the secret from standard input, removes one terminal `LF` or `CRLF` line ending, preserves all other bytes, and rejects an empty result. It produces no prompt echo. `--password` accepts the same content from argv but prints a warning to stderr without including the value. Help and warning text state that the value may be visible in shell history, process listings and CI/automation logs, and recommend `--password-stdin` or `--credential-id` instead.

Inline `--config` containing `password` or `secret_access_key` receives the same argv exposure warning. A config file containing plaintext secrets is accepted but help warns callers to use restrictive file permissions, avoid committing it, and remove it when no longer needed.

The type-owned credential contract at approval time is:

| Asset/auth shape | Accepted managed input | Plaintext storage | Additional rule |
|---|---|---|---|
| SSH password auth | `password` credential | encrypted asset-local password | `--password*` selects password auth; an explicit conflicting `auth_type` fails. |
| SSH key auth | existing `ssh_key` credential only | none | Create/import the key in the desktop key manager, then pass `credential_id`. `private_key` and `passphrase` are rejected automation fields. |
| SSH Agent auth | Agent source ID + fingerprint only | no credential row | Password, private key and `credential_id` inputs are rejected. |
| Non-SQLite database, Redis, MongoDB, etcd, Kafka primary SASL, RDP, VNC | `password` credential | encrypted asset-local password | A referenced non-password credential fails before persistence. |
| OSS | `password` credential | encrypted asset-local Secret Access Key | `access_key_id` remains required connection metadata. |
| K8s | none | kubeconfig remains directly encrypted in asset config | Password credential flags are rejected. |
| SQLite, Serial, Local | none | none | Password credential flags are rejected as inapplicable. |

Plaintext is encrypted by the canonical credential service inside the type-owned asset config and is omitted from approval, output, and Audit projections.

A referenced credential must exist and match the type/auth context before approval. For SSH, a password credential maps to password auth and an SSH-key credential maps to key auth when `auth_type` is omitted; an explicit mismatch fails. Other password-capable types accept only password credentials.

## Atomic write flow and failure behavior

For opsctl create, the observable sequence is:

1. Parse and merge config without persisting data.
2. Resolve type, files, SSH tunnel references and typed credential references; validate all fields and secret-source conflicts without persisting data.
3. Request the existing desktop create approval using a detail that identifies the asset type/name and safe endpoint metadata only.
4. On denial, stop with no credential or asset row created.
5. On approval, bind an already-validated managed credential reference or encrypt supported plaintext into the asset-local config, then create the asset in one transaction.
6. Commit the asset, notify the desktop data-change channel, write producer-projected audit data and return safe JSON.

Any validation, encryption, handler application or asset write failure returns a non-secret error and leaves no new asset row committed. Automation never creates a credential row, and a referenced existing credential is never modified. Replacing a credential association through AI update does not automatically delete the old credential because it may be shared; cleanup remains an explicit user operation.

The successful create/update result includes the asset ID and, when applicable, a safe authentication reference. It never returns supplied plaintext or stored ciphertext.

## AI `put_asset` parity

The AI continues to call `help(type)` and then the existing `put_asset` with a type-specific `config` object. All registered built-in types remain available through this one tool.

On create or update:

- `config.password` is encrypted in the asset and does not create a credential.
- OSS `config.secret_access_key` follows the same asset-local rule.
- SSH `config.private_key` and `config.passphrase` are rejected; use an existing SSH-key `credential_id`.
- `config.credential_id` reuses an existing credential after type/auth validation.
- plaintext material and an existing reference in the same request fail rather than choosing one.
- K8s kubeconfig and other non-password secret fields retain their established direct-encryption semantics.

AI create/update and opsctl create share the same credential-reference and asset-local encryption boundary. No CLI-only or AI-only type switch determines credential behavior.

The existing AI tool visibility/approval behavior is preserved. Tool descriptions and per-type help state that plaintext credential fields are write-only asset-local secrets and must never be echoed. Audit persistence uses the producer-owned allowlist projection (`SafeAuditArgs` / `SafeAuditArgsForResult`), omitting the write-only secret fields instead of redacting the original tool arguments.

## Unified credential discovery

The CLI adds:

```text
opsctl list credentials [--type password|ssh_key|ssh_agent]
opsctl get credential <typed-ref>
```

The AI adds read-only tools:

```text
list_credentials(type?)
get_credential(ref)
```

Omitting the filter returns all three user-facing key-management kinds. Every item has a stable typed `ref`:

- persisted password or SSH key: `credential:<id>`
- persisted SSH Agent source: `agent-source:<id>`

A detail lookup requires this typed ref; a bare numeric ID is rejected as ambiguous. `--credential-id` remains a numeric database-credential reference for asset creation, while SSH Agent creation uses its dedicated source ID and fingerprint fields.

List responses are summaries:

- Common: `ref`, numeric `id`, `type`, `name`, optional `description`, create/update times.
- Password: optional associated username; no password presence/value field.
- SSH key: optional username, key type/size, fingerprint and comment; no private key or passphrase.
- SSH Agent: endpoint type, availability (`ok`, `empty`, `unavailable`, `unsupported`), identity count and source-level usage count; no endpoint value.

Agent connectivity problems are represented as availability and do not fail the whole list. Database/repository failures still fail the operation.

Detail responses add:

- Password/SSH key: assets currently referencing the credential, returned as safe ID/name/type summaries.
- SSH key: public key, because it is public identification material already exposed by the desktop manager.
- SSH Agent: current identity summaries (`fingerprint`, key type, sanitized comment, per-identity usage) and source-level usage. An unavailable/unsupported/empty source returns persisted metadata plus status and an empty identity list rather than exposing its endpoint or failing for an expected runtime state.

Neither opsctl nor AI receives password plaintext/ciphertext, SSH private keys, passphrases, master keys, Agent endpoint values, signatures, challenges or challenge answers. Agent full public keys are not added to these automation surfaces in this round; source ID plus fingerprint is sufficient for asset creation.

## SSH Agent asset creation and detail

opsctl adds SSH convenience flags:

```text
--agent-source-id <id>
--agent-key-fingerprint <SHA256 fingerprint>
```

They may also be supplied through generic config as `agent_source_id` and `agent_key_fingerprint`. Agent auth requires both, validates that the source exists and the fingerprint is canonical, and rejects password/private-key/credential inputs. The source need not be online at save time; runtime availability remains observable through credential detail and asset detail.

`get_asset` and the corresponding AI tool return one safe `authentication` object when an asset references managed authentication:

- Managed password/key: type, `credential:<id>` ref, name, optional username and availability (`stored` or `missing`). Existing inline-encrypted assets remain usable but do not fabricate a managed credential ref.
- SSH Agent: type `ssh_agent`, `agent-source:<id>` ref, source name, endpoint type, selected fingerprint, availability (`ok`, `empty`, `unavailable`, `unsupported`, `missing`) and, only when currently available, key type and sanitized comment.

The asset detail never returns Agent endpoint values or public-key blobs. `list_assets` remains lightweight and does not expand credential or Agent detail for every row.

## Security, privacy and audit

Approval requests and desktop notifications contain only safe asset metadata. Passwords and other secrets are never interpolated into approval detail or error text.

opsctl encrypts plaintext into the asset-local config inside the shared write boundary; its audit projection omits that write-only value and includes a typed association only when the request reused an existing credential or SSH Agent source. AI `put_asset` audit persists the same producer-owned allowlist projection, omitting write-only secret fields instead of replacing values; default Audit keeps the raw values it receives.

Structured logs for asset create/reference/query flows record start/end/fail with safe correlation fields such as asset ID, credential ID/ref, source ID and asset type. They do not record secret values, full config JSON, Agent endpoint values, private/public key blobs or kubeconfig.

The feature adds no password reveal, private-key export, Agent signing, challenge-response or credential decryption operation to opsctl or AI.

## Compatibility

Existing `opsctl create asset` invocations using convenience flags keep their current meaning and default-port behavior. Existing K8s raw kubeconfig file input remains supported. The old output fields remain present; new authentication references are additive.

Existing assets with inline encrypted passwords, keys or kubeconfig continue resolving through current credential resolvers. They are not rewritten when listed, fetched or connected. New CLI/AI plaintext password writes keep the established asset-local encrypted representation.

Existing AI callers that pass `config.password` or OSS `secret_access_key` retain the same connection result and asset-local encrypted representation. Existing valid `credential_id` calls continue to work and gain type validation where it was previously absent.

## Out of scope

- Credential or SSH Agent source creation through opsctl/AI; asset put only reuses credentials and Agent sources that were already configured in the desktop app.
- Credential/source update, rename, password rotation or deletion through opsctl/AI.
- Password, private-key, passphrase, Agent endpoint or signing-material reveal/export through opsctl/AI.
- Automatic migration or deletion of existing inline/orphaned credentials.
- Generic `opsctl update asset --config` parity; AI `put_asset` update is covered because it is already one public create/update contract.
- Kafka Schema Registry/Kafka Connect companion credential creation and other nested config surfaces not currently advertised by the type handler.
- Extension-defined asset types outside the built-in asset-type handler registry.

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| Generic opsctl create parser/normalizer | JSON object/file parsing, legacy flag precedence, unknown-key rejection, all secret-source conflicts, stdin newline handling and warning text without secret echo | Existing command parser tests under `cmd/opsctl/command`; no create-focused regression suite currently covers these contracts. |
| Registered asset handler create boundary | Every registered built-in type is reachable through generic config without a shared type switch; handler-owned required/default/accepted-field rules remain authoritative, including SQLite and non-password rejections | Existing per-handler tests in `internal/assettype` and help-coverage tests. |
| Shared asset write service | Existing credential type validation, SSH auth inference/mismatch, inline encryption, existing-reference binding and transaction rollback on every write failure | Existing credential resolver, asset service and `dbutil.WithTransactionRunner` tests. |
| AI `put_asset` handler | Plaintext becomes a managed association, reference reuse works across supported types, unsupported/mutually exclusive inputs fail, output is non-secret | Existing `internal/ai/tool/tool_handlers_crud_test.go`. |
| Unified credential read handlers | Typed-ref parsing, filters, safe metadata, usage lists, SSH public key detail, Agent status/identities, unavailable Agent degradation and absence of all secret/endpoint fields | Existing credential manager and SSH Agent service tests; new shared handlers serve both CLI and AI. |
| Safe asset detail | Managed credential and Agent association structures, missing managed credential state, Agent identity availability, and no expansion in asset lists | Existing safe-view and Agent asset-detail tests. |
| Audit producer projection | CLI/AI `put_asset` audit request omits write-only plaintext fields via the producer-owned allowlist (`SafeAuditArgs` / `SafeAuditArgsForResult`); default Audit keeps raw values; direct outputs, errors and approval details are never rewritten | Producer-owned projection tests, desktop `assetAuditView` allowlist tests and external-edit field-allowlist tests |
| Real opsctl runtime | In an isolated data directory with the desktop approval channel: create representative password, SSH-key/Agent, non-password and generic-config assets; query credentials and assets; verify DB rows/audit and execute at least one created connection where a test endpoint is available | `docs/VERIFICATION.md` and the sandbox/oracle workflow. |

Automation cannot prove shell-history or operating-system process-list retention across every shell/platform; help text and stderr warning are asserted exactly enough to preserve the safety message, and wrap-up source review verifies no alternative argv path is documented as safe. Runtime verification must use synthetic secrets in an isolated data directory and must never inspect or modify the user's real credential store.

## Relevant links

- [Issue #283](https://github.com/opskat/opskat/issues/283)
- [Architecture: opsctl and multi-process flow](../ARCHITECTURE.md#8-opsctl--the-multi-process-flow)
- [Adding an asset type: shared credential layer](../references/adding-an-asset-type.md#f4-shared-credential-layer)
- [Verification workflow](../VERIFICATION.md)
