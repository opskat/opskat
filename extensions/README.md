# Extensions

Example extensions, built and tested inside this repository. They share the module,
the SDK (`pkg/extsdk`) and the test run with the host, so an SDK change that breaks an
extension breaks `go test ./...` instead of a repository nobody rebuilt.

- [`notebook/`](./notebook) — the reference example. One asset type, four tools, a
  policy face that allows, asks and refuses, a `SKILL.md` and two locales. It stores
  everything in the host KV, so it runs offline with no capability grants at all.

The runtime that loads these — wazero, the reactor ABI, the capability enforcement,
the descriptor cache — is described in
[docs/ARCHITECTURE.md §7](../docs/ARCHITECTURE.md#7-extensions--wasm-plugins). This
file is about writing one.

## Anatomy

```
extensions/notebook/
  main.go          # package main, func main() {}, everything declared in init()
  store.go         # the rest of the implementation
  notebook_test.go # tools driven through opskat.TestHost
  manifest.json    # the security contract — and nothing else
  SKILL.md         # what the model reads before using the asset type
  locales/         # en.json, zh-CN.json — the i18n keys the declarations reference
  dist/            # build output (gitignored): main.wasm + the files above
```

`make build-ext EXT=notebook` produces `dist/`, which is the directory shape the app
installs.

## The two declarations

**`manifest.json` carries the security contract and nothing else** — `name`,
`version`, `minAppVersion`, `hostABI`, `backend`, `capabilities`. That is what a user
must be able to audit *before* the code runs, so it can never come from the code. A
manifest that also declares `tools` / `assetTypes` / `policies` / `frontend` / `i18n` /
`icon` / `snippets` is **refused at load** with the list of retired keys: those moved
into `describe()`.

```json
{
  "name": "notebook",
  "version": "0.1.0",
  "hostABI": "2.0",
  "backend": { "runtime": "wasm", "binary": "main.wasm" },
  "capabilities": {}
}
```

`capabilities` defaults to deny-all, and the notebook needs nothing: the host KV, the
asset config and logging are available without a grant. Declare only what you use:

```json
"capabilities": {
  "fs":   { "read": ["${EXT_DIR}/**"], "write": ["/var/tmp/myext/**"] },
  "http": { "allowlist": ["https://api.example.com/"] },
  "credentials": "read",
  "tunnel": true,
  "network": { "assetEndpoint": true }
}
```

Each one is enforced at the host call it guards: `fs` patterns are absolute path
prefixes (`${EXT_DIR}` resolves to the installed extension's directory), the `http`
allowlist is matched as a URL prefix and private/loopback destinations are refused
unless `tunnel` is also granted, and `credentials: "read"` is what lets
`ctx.AssetConfig()` return decrypted password fields. `network.assetEndpoint` lets a
call scoped to an asset reach the addresses in that asset's config fields tagged
`format:"endpoint"` (a URL or `host:port`) over HTTP and TCP — private addresses
included, since the user typed them. The target's scheme, host and port must match
an endpoint field (a bare `host:port` admits http and https); anything else, a
redirect off the endpoints included, is refused with "not an endpoint of the asset".
Declaring it also puts TCP under that rule; without it TCP is ungated and HTTP reach
is the static allowlist alone.

**Everything else is answered by the module itself**, through `describe()`. You never
write that answer: the SDK derives it from the registration calls, so the host's view
of the extension and the code that runs cannot disagree.

## Registering

The guest is a **WASI reactor** — the host runs `_initialize` and never calls `main`,
so registration happens in `init()` and `func main() {}` stays empty.

```go
opskat.Extension(opskat.Meta{
    Icon:        "archive",              // an icon name from the app's icon set
    DisplayName: "extension.displayName", // i18n keys, resolved against locales/
    Description: "extension.description",
    PolicyType:  "notebook",             // required: the policy face the asset types are checked under
})

opskat.AssetType[notebookConfig]("notebook").Name("assetType.notebook.name")
opskat.RegisterConfigValidator(func(raw json.RawMessage) []opskat.ValidationError { ... })

opskat.PolicyGroup("ext:notebook:read").
    Name("policy.read.name").Description("policy.read.description").
    Allow("read").Default()

opskat.Tool("note_list", listNotes).Policy("read").Doc("tools.note_list.description")
```

**An extension must declare at least one asset type**, and `Meta.PolicyType` must be
set. Extension tools are reached through `exec` on an asset, so an extension without
one has no reachable entry point and is refused at load.

### Schemas come from your Go types

`opskat.Tool[T]` reflects the parameter schema from the handler's own argument type,
and `opskat.AssetType[C]` reflects the configuration form from `C`. A renamed field
renames the flag; there is no second declaration to keep in step.

```go
type putArgs struct {
    Key     string   `json:"key" desc:"Key of the note to create or overwrite"`
    Tags    []string `json:"tags,omitempty" desc:"Optional labels"`
}
```

- `string`, `bool`, integer, float and `[]string` are expressible; anything else
  panics at registration — that is `init()`, so a schema the host could not use fails
  the whole extension at load rather than on the first call.
- `,omitempty` (or a pointer field) makes a parameter optional; everything else is
  required.
- `desc` on a **tool** argument is shown to the model as written — plain text, not an
  i18n key. On an **asset config** field, `title` / `placeholder` / `desc` are i18n
  keys, and `format:"password"` marks a secret the host encrypts, `enum:"a,b"` renders
  a select.

### The asset comes from the host, not from the arguments

`exec <asset> -- <tool>` already names an asset, so the host puts it in the call
envelope and the handler reads it off the context:

```go
func listNotes(ctx *opskat.ToolContext, args listArgs) (any, error) {
    raw, err := ctx.AssetConfig() // config of the exec target, passwords decrypted
    ...                           // ctx.Asset is {ID, Name, Type}
}
```

A tool **may not declare an `asset_id` parameter**: registration panics and the host
refuses the `describe()` answer. Policy, approval and grant are all keyed on the exec
target, so a second asset id supplied in the arguments would reach an asset the user
never granted — there is no "the flag wins" case to reason about.

`ctx.AssetConfig()` only ever reads the call's own asset, and only when that asset's
type is one this extension registers; there is no by-id lookup, so an extension cannot
read a builtin asset or another extension's. It fails when the call is not scoped to an
asset. That happens for
the one caller that legitimately has none: the asset configuration form runs an
extension **action** (`test_connection`) on a configuration that has not been saved
yet. An extension page that *does* work on a saved asset passes its `assetId` prop —
`api.callTool(ext, tool, args, assetId)` / `api.executeAction(ext, action, args,
onEvent, assetId)` — and the handler reads it from `ctx.Asset` the same way.

### Connection settings belong to the host

Tunnels, proxy chains and TLS are not config fields you define and handle yourself.
An asset type names the ones it supports, and the host shows them on the asset form
and detail card and applies them when the extension dials the asset's endpoint:

```go
opskat.AssetType[esConfig]("es").Connection(opskat.Connection{SSHTunnel: true, ProxyChain: true, TLS: true})
```

With `SSHTunnel` declared, the form offers the same SSH-asset picker built-in types
use; the choice is stored on the asset, never in its config, so `ctx.AssetConfig()`
and the config validator never see it. `ProxyChain` offers the same multi-hop
SSH/SOCKS5/HTTP-tunnel builder built-in types use, and `TLS` offers the same
enable / skip-verify / server name / CA / client cert & key fields — both are stored
in a host-reserved key inside the asset's config JSON, stripped before
`ctx.AssetConfig()` and the config validator ever see it, for the same reason the
tunnel choice is kept off the asset: an extension has no legitimate reason to read
settings it cannot itself apply. HTTP and TCP opens scoped to the asset are dialed
through the declared tunnel/chain and wrapped in the declared TLS, and an endpoint's
hostname is resolved on the far side of a tunnel. A cert file that cannot be read, a
failed TLS handshake, or a chain hop that cannot be reached all fail the open with
the host's error; there is no fallback to a direct or unverified connection. An item
left undeclared is neither shown nor applied, and the host refuses a `connection`
item it does not know.

### Credentials are injected by the host

An HTTP extension does not need to hold its asset's password. The asset type declares
how a request authenticates, and the host renders that from the asset's config —
decrypting `format:"password"` fields itself — into every request the extension sends
to the asset's endpoint (redirect hops that stay on it included):

```go
opskat.AssetType[esConfig]("es").Auth(opskat.Auth{
	Selector: "authType", // config field that picks the group; omit for a single group
	Groups: []opskat.AuthGroup{
		{When: "basic", Bindings: []opskat.AuthBinding{{In: "basic", Value: "{{username}}:{{password}}"}}},
		{When: "apiKey", Bindings: []opskat.AuthBinding{
			{In: "header", Name: "Authorization", Value: `ApiKey {{base64(apiKeyId, ":", apiKey)}}`},
		}},
		{When: "token", Bindings: []opskat.AuthBinding{{In: "query", Name: "access_token", Value: "{{token}}"}}},
	},
})
```

`In` is `header` (`Name` is the header), `query` (`Name` is the parameter) or `basic`
(no `Name`; `Value` renders `user:password` and is sent as `Authorization: Basic …`).
`Value` is literal text with `{{field}}` and `{{base64(part, …)}}` placeholders, each
part a config field or a double-quoted literal; a field the config leaves unset renders
empty. A `Selector` value no group names injects nothing (e.g. `authType: "none"`).
The host refuses the extension at load when a template references a field the config
does not declare, or a group cannot be selected unambiguously. It needs
`network.assetEndpoint`: requests to any target other than the asset's endpoint get no
credentials. The injected values never reach the guest — not in the response metadata
and not in a failed request's error — and a password that cannot be decrypted fails the
request instead of sending it unauthenticated. `credentials: "read"` stays for
protocols the host cannot authenticate for you (raw TCP handshakes); the install
confirmation and the extension's details in Settings warn about it prominently.

### The policy face

Every tool declares the action it requests. A tool whose action is fixed uses
`.Policy(action)` — optionally with `.Resource(fn)` to report what the call touches.
A tool whose action depends on its arguments classifies each call with
`.PolicyFunc(actions, fn)`: `fn(args)` returns the action and a resource (any string,
possibly empty), and `actions` is every action `fn` can return.

```go
opskat.Tool("note_put", putNote).
    PolicyFunc([]string{"write"}, func(args putArgs) (string, string) {
        return "write", strings.TrimSpace(args.Key)
    })
```

The host does not take the classification as permission: it matches the action and
resource against the rules on the asset and the permission groups granted on it, in
this order — **deny → allow → grant → ask**.

- a matching **deny** rule refuses the call, and a denial beats every allow;
- a matching **allow** rule runs it unattended;
- otherwise a grant saved by an earlier "always allow" runs it;
- anything else **asks the user**.

A rule is `<action>` or `<action>:<resource-glob>`. A rule without a resource covers
the action on every resource; a glob uses the same `path.Match` semantics as command
rules (`*` does not cross `/`) and is matched against the whole resource, which may
itself contain `:` — the rule is split at the first `:` only. Group allow/deny lists
hold rules in this form; permanent rules on an asset or asset group are written as
`ext:<PolicyType>:<rule>`, e.g. `opsctl policy allow my-notes -- 'write:runbook/*'`
lands `ext:notebook:write:runbook/*`. Action names therefore may not contain `:` or
whitespace.

An action `fn` returns that the extension never declared is a defect, not a
decision: the host logs an error and asks the user, without consulting rules or grants.

`.Default()` marks a group granted to every new asset of the extension's types.
Group ids must be namespaced by the extension's policy type — `ext:<PolicyType>:<group>`,
the same segment the host writes permanent rules under.
A policy type belongs to one extension: loading a second extension that claims the same
policy type, or a group id that is already registered, is refused. The action set itself
is never declared separately — the host derives it from the tools' `.Policy` actions
and `.PolicyFunc` action sets.

## SKILL.md and locales

`SKILL.md` is what the model reads before working with the asset type. Frontmatter is
optional but recommended: `description` is the one line that appears in the model's
skill list, and without it the extension's `i18n.description` is used instead. The
body is injected only when the model actually asks for `help`, and the host appends a
tool/parameter reference rendered from the reflected schemas — so document *intent*,
not flag syntax.

`locales/<lang>.json` is a flat key → string map. The keys are the ones the
registrations reference. `en` is the fallback for every other language, and a key with
no entry anywhere is shown as-is.

## Build, install, reload

```bash
make build-ext EXT=notebook                     # → extensions/notebook/dist
opsctl ext dev "$PWD/extensions/notebook/dist"  # install into the running app
opsctl ext list                                 # name, version, asset types, tools
```

`opsctl ext dev` hands the directory to the running desktop app, which installs it
through the same path the "install from directory" button uses — capability
enforcement, registries and all. **Re-running the two commands after an edit is the
reload**: the new build is loaded and checked beside the old one and swapped in only
if it loads — a broken build keeps the previous version running. Point `--data-dir` /
`OPSKAT_DATA_DIR` at the verification sandbox ([docs/VERIFICATION.md](../docs/VERIFICATION.md)).
The app asks you to confirm each install in its opsctl approval dialog (source
directory, extension name/version, declared capabilities, whether it replaces an
installed extension); re-runs of the same directory for the same extension with
unchanged capabilities are remembered until the app restarts, so the reload loop
does not re-prompt.

Then drive it like any other asset type:

```bash
opsctl help <asset>                                     # SKILL.md + the parameter table
opsctl exec <asset> -- note_list
```

## Testing

`opskat.NewTestHost` installs a fake host and dispatches through the real registry, so
a test calls a tool the way the WASM entry point does — no wazero, no build step:

```go
asset := opskat.Asset{ID: 7, Name: "team runbooks", Type: "notebook"}
host := opskat.NewTestHost(opskat.WithAssetConfig(asset.ID, notebookConfig{Notebook: "team"}))
defer host.Close()
result, err := host.CallTool(asset, "note_put", putArgs{Key: "k", Content: "v"})
```

`WithMockHTTP`, `WithMockTCP` and `WithActionCancel` stand in for the other host
capabilities; `CallAction` captures the events an action emits, and `CheckPolicy`
returns the action and resource a call requests.

## Frontend pages (optional)

`opskat.Frontend(...)` declares an ESM entry the app loads from
`/extensions/<name>/<entry>`, served straight out of the installed extension
directory. A page slotted as `asset.connect` is what opening the asset shows. The app
injects `window.__OPSKAT_EXT__` (`React`, `ReactDOM`, `i18n`, `@opskat/ui`, and the
extension API) before importing the module, so a page uses the host's React rather
than bundling its own.
