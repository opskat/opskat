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
must be able to audit *before* the code runs, so it can never come from the code.
Everything else — tools, asset types, policies, pages, display strings — is answered by
`describe()`.

```json
{
  "name": "notebook",
  "version": "0.1.0",
  "hostABI": "2.1",
  "backend": { "runtime": "wasm", "binary": "main.wasm" },
  "capabilities": {}
}
```

`hostABI` is checked as an exact-set membership, not a minimum: `pkg/extension.SupportedHostABIs`
currently accepts `2.0`, `2.1` and `2.2`, so an already-built `2.0` extension keeps loading
unchanged (it just doesn't get `@opskat/host-ui` — see below), while one declaring
anything not in that set (`2.3`, `3.0`, …) is refused at load with the list of what is
supported. Declare `2.2` when a tool classifies with `.PolicyResources` or refuses
arguments with `.RejectArgs` (see [The policy face](#the-policy-face)): an older app
would read either reply as a call on no resource at all, so it must refuse the extension
instead.

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
`ctx.AssetConfig()` return decrypted password fields (see
[Reading secret fields](#reading-secret-fields)). `network.assetEndpoint` lets a
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

The validator runs when the asset form saves and whenever the asset is written. Each
`ValidationError.Field` that names a config field is shown under that field in the form
and the save is refused; an error with an empty or unknown `Field` is shown as the save
error instead. A stored secret the user leaves untouched reaches the validator as its
ciphertext, so "is it filled" checks work when editing.

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
  a select. A select shows the raw option values unless you add
  `enumLabels:"key.a,key.b"` — one i18n key per option, in option order (a different
  count is refused at registration and at load) — and `default:"a"` preselects an
  option when a new asset is created (it must be one of the options; `default` is
  supported on string fields only, and an asset already saved without the field is not
  given it). Declare a secret as an `opskat.Credential` field, which is always
  `format:"password"` (see [Reading secret fields](#reading-secret-fields)).

### Parameters opsctl can read from a file

A payload too large or too awkward for a command line — an NDJSON bulk body — can be
marked file-readable on the tool's registration handle:

```go
opskat.Tool("request", handleRequest).
    PolicyResources(actions, classify).
    FileParam("body") // the JSON name of a string parameter
```

`opsctl exec <asset> -- request --body-file payload.ndjson` (or `--body-file -` for
stdin) is then exactly `--body <file content>`: opsctl reads the file and sends the
inline form, so `PolicyFunc` / `PolicyResources`, the approval dialog, grants and
audit all see the content, and the handler receives it as the ordinary `body`
argument — there is nothing to implement. `describe()` reports the marker as
`tools[].fileParams`, and `opsctl help <asset>` lists the `--body-file` form marked
"opsctl only".

- `FileParam` panics at registration unless the name is a declared **string**
  parameter whose `<name>-file` spelling is not itself a parameter, and on a repeat;
  the host's describe validation refuses such an entry as well. It ships under host
  ABI 2.2, no further bump.
- opsctl refuses, sending nothing and exiting non-zero: both `--body` and
  `--body-file`; an unreadable file; content over 16 MiB or not valid UTF-8; stdin
  named twice.
- AI `exec` and the extension's own pages do not accept `--body-file` — it is an
  unknown flag there. Reading files happens only inside the opsctl process.

### The asset comes from the host, not from the arguments

`exec <asset> -- <tool>` already names an asset, so the host puts it in the call
envelope and the handler reads it off the context:

```go
func listNotes(ctx *opskat.ToolContext, args listArgs) (any, error) {
    raw, err := ctx.AssetConfig() // config of the exec target
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
asset. (The asset form's "Test connection" on an unsaved configuration is not such a
call: it runs the type's declared handler with the submitted config — see
[Test connection](#test-connection).) An extension page that *does* work on a saved asset passes its `assetId` prop —
`api.callTool(ext, tool, args, assetId)` / `api.executeAction(ext, action, args,
onEvent, assetId)` — and the handler reads it from `ctx.Asset` the same way.
`api.callTool` against a saved asset runs directly: it is the user's own action in the
page, so there is no policy check, approval dialog, grant or audit row (those belong to
AI `exec` and opsctl, which stay gated on the same asset). It is still scoped to that
asset — `ctx.AssetConfig()`, endpoint gating, the connection settings and credential
injection all apply — its arguments are checked against the tool's declared
parameters (unknown keys are rejected), and it can be cancelled and honours the
tool's timeout. A tool failure, the handler's error included, reaches the page as a
rejected promise carrying the message as-is.

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
settings it cannot itself apply. HTTP and TCP opens to the asset's endpoint are dialed
through the declared tunnel/chain and wrapped in the declared TLS, and an endpoint's
hostname is resolved on the far side of a tunnel; anything else the same call reaches
(an allowlisted public API) is dialed directly. Since these settings apply only to the
endpoint, declaring any of them needs `network.assetEndpoint` and a `format:"endpoint"`
config field — the host refuses the extension at load otherwise. The user gives the CA and client
certificates either as file paths or as PEM content (a client key given as content is stored
encrypted). A certificate that cannot be read or does not parse, a
failed TLS handshake, or a chain hop that cannot be reached all fail the open with
the host's error; there is no fallback to a direct or unverified connection — with TLS
enabled, a plain `http://` request to the endpoint is refused, and the process's
`HTTP(S)_PROXY` never reroutes an endpoint request. With both
`SSHTunnel` and `ProxyChain` declared, an asset uses one or the other — an SSH jump host
goes into the chain as a hop — and one that sets both is refused rather than dialed
without the tunnel. An item left undeclared is neither shown nor applied, and the host
refuses a `connection` item it does not know.

The same declaration opens these settings to `opsctl create asset` / `update asset` and the
assistant's `put_asset`, under the field names built-in types use: `ssh_asset_id` for
`SSHTunnel`, `proxy_chain` for `ProxyChain`, and `tls`, `tls_insecure`, `tls_server_name`,
`tls_{ca,cert,key}_file` / `tls_{ca,cert,key}_pem` for `TLS`. They are host fields, not yours:
the host stores them where the form does, lists them in the type's `help` document, and
refuses at load an asset type whose `configSchema` declares a property with one of the
names its own `connection` items claim.

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
does not declare, the selector is a `format:"password"` field, or a group cannot be
selected unambiguously, and — like
`Connection` — when the manifest lacks `network.assetEndpoint` or the config has no
`format:"endpoint"` field: requests to any target other than the asset's endpoint get
no credentials. The injected values never reach the guest — not in the response metadata
and not in a failed request's error; a `TRACE`, which a server answers by echoing the
request, is refused when it would carry them — and a password that cannot be decrypted
fails the request instead of sending it unauthenticated. A `query` binding replaces a
same-name parameter and leaves the rest of the query exactly as the extension wrote it. `credentials: "read"` stays for
protocols the host cannot authenticate for you (raw TCP handshakes); the install
confirmation and the extension's details in Settings warn about it prominently.

### Reading secret fields

Declare a secret config field as `opskat.Credential`. The asset form shows it as a
password, and it decodes from `ctx.AssetConfig()` whether or not the extension may read
it:

```go
type esConfig struct {
	Endpoint string            `json:"endpoint" format:"endpoint"`
	Username string            `json:"username,omitempty"`
	Password opskat.Credential `json:"password,omitempty" title:"config.password.title"`
}

raw, err := ctx.AssetConfig()
...
var cfg esConfig
if err := json.Unmarshal(raw, &cfg); err != nil {
	return nil, err
}
cfg.Password.IsSet()                // the asset has a password, readable or not
password, err := cfg.Password.Plaintext()
```

Without `credentials: "read"`, the host serves the field as an opaque handle that
carries no plaintext: `IsSet` still reports whether the asset has a value, `Plaintext`
fails with `opskat.ErrCredentialWithheld`, and the host keeps injecting the secret through
`Auth`. With `credentials: "read"`, `Plaintext` returns the decrypted value (`""` when
unset). A plain `string` field tagged `format:"password"` decodes only the plaintext, so
it suits only an extension that declares `credentials: "read"`. Without it,
`json.Unmarshal` fails on the handle. `Credential` is refused as a tool argument; a secret
belongs on the asset. `TestConnection` decodes the form into the same struct, and there,
without `credentials: "read"`, a secret field the host withheld arrives unset.

### Test connection

The asset form's "Test connection" button appears only when the asset type declares
a handler for it:

```go
opskat.AssetType[esConfig]("es").TestConnection(func(cfg esConfig) error {
	h, err := opskat.IOOpen("http", map[string]any{"method": "GET", "url": cfg.Endpoint})
	if err != nil {
		return err
	}
	defer h.Close()
	meta, err := h.Flush()
	if err != nil {
		return err
	}
	if meta.Status >= 400 {
		return fmt.Errorf("unexpected status %d", meta.Status)
	}
	return nil
})
```

`fn` receives the form's current values decoded into the same config struct
`AssetType` reflected the schema from — including for a brand-new, not-yet-saved
asset — and reaches the endpoint exactly like a tool does (`IOOpen`, `Dial`, an
`*http.Client` on `NewHTTPTransport`), gated and dialed by `network.assetEndpoint`
and any declared `Connection` / `Auth` the same way, but resolved from the form's
submitted connection settings (tunnel, proxy chain, TLS) rather than a saved
asset's row: what is under test is exactly what the caller is about to save, or
never will. A nil error means success. Test connection never goes through policy —
it is not an operation on the asset — and, editing a saved asset, a password field
the user has left untouched is filled in from the stored value before `fn` runs (one
the user cleared is tested empty, as it will be saved); the plaintext never reaches
the frontend to do this.

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

A call that may touch several resources at once — one request naming several indices,
a bulk body spanning many — classifies with `.PolicyResources(actions, fn)`: `fn(args)`
returns the action and every resource the call touches (zero, one or many). In these
resources `*` and `?` are **wildcards** — `logs-*` stands for every index it could
match — and every other character is literal; `.PolicyFunc` / `.Resource` resources stay
literal, so a key that happens to contain `*` means exactly that key. A tool uses one of
`.Policy`, `.PolicyFunc`, `.PolicyResources`. `.PolicyResources` needs `"hostABI": "2.2"`.

```go
opskat.Tool("request", doRequest).
    PolicyResources([]string{"read", "write", "delete"}, func(args requestArgs) (string, []string) {
        return classify(args.Method, args.Path) // e.g. "delete", []string{"a", "prod-1"}
    })
```

Some arguments are wrong whatever the user's rules say — a request path naming a host of
its own, when every request must go to the asset. `.RejectArgs(fn)` refuses them
outright: `fn(args)` returns `nil` to accept the call, or an error whose text is the
reason. The host then denies the call with that reason before any rule or grant is
consulted and **never asks the user** (there is nothing to approve: the call would only
fail afterwards), and the audit log records it as a deny whose error is the reason.
`fn` runs before the classifier, which only ever sees accepted arguments, and again
before the handler on every call — including a page's, which skips policy — so the
handler needs no second copy of the check. It works with `.Policy`, `.PolicyFunc` and
`.PolicyResources`, and needs `"hostABI": "2.2"`. Keep it for arguments that can never
run; what a user may do is the rules' to decide.

```go
opskat.Tool("request", doRequest).
    RejectArgs(func(args requestArgs) error {
        _, err := parseRequestPath(args.Path) // "path must not name a host", …
        return err
    }).
    PolicyResources(requestActions, classifyRequest)
```

In a unit test, `TestHost.CheckPolicy` and `TestHost.CallTool` return the refusal as an
`*opskat.ArgsRejectedError` whose `Reason` is `fn`'s error text:

```go
_, _, err := host.CheckPolicy("request", requestArgs{Path: "http://evil/x"})
var rejected *opskat.ArgsRejectedError
if !errors.As(err, &rejected) { t.Fatalf("want a rejection, got %v", err) }
```

Any other failure to classify — the guest erring, a malformed reply, an action the tool
never declared — is not a refusal: the host asks the user about the call instead.

The host does not take the classification as permission: it matches the action and
each resource against the rules on the asset and the permission groups granted on it,
in this order — **deny → allow → grant → ask**.

- a **deny** rule matching **any** resource refuses the call, and a denial beats every
  allow (`deny delete:prod-*` refuses `delete [x, prod-1]` even under a bare `delete`
  allow);
- the call runs unattended when **every** resource is covered by an **allow** rule or a
  grant saved by an earlier "always allow" — different resources may be covered by
  different rules or grants, and allow rules are consulted before grants;
- anything else **asks the user**.

A call with no resource is judged as the empty resource. For a wildcard resource, a deny
rule hits when its glob **could** match one of the names the resource stands for (and
when that cannot be decided), while an allow rule or grant covers it only when its glob
matches **all** of them (`write:logs-*` covers `logs-2026-*` but not `logs*` or `*`).
The audit log's matched pattern lists every rule and grant that allowed a
multi-resource call, or each deny rule followed by the resources it denied
(`delete:prod-* (prod-1)`).

A rule is `<action>` or `<action>:<resource-glob>`. A rule without a resource covers
the action on every resource; a glob uses the same `path.Match` semantics as command
rules (`*` does not cross `/`) and is matched against the whole resource, which may
itself contain `:` — the rule is split at the first `:` only. Group allow/deny lists
hold rules in this form, and so do the permanent allow/deny rules users write on an
asset (its detail page, or `opsctl policy allow|deny`, e.g.
`opsctl policy allow my-notes -- 'write:runbook/*'`) or on an asset group
(`opsctl policy … --group`, kept per policy type). Action names therefore may not
contain `:` or whitespace.
A grant request for an extension asset — the AI's `request_permission`, or one delivered
over the opsctl approval channel (opsctl has no user-facing grant command) — is written
the same way (`write:runbook/*`, or `write` for every resource) and is stored as
`ext:<PolicyType>:<rule>`, so the next call it covers runs without asking — a grant
covers a multi-resource call resource by resource, like a rule. A
command-shaped pattern (`note_put *`) or an undeclared action is refused rather than
stored as a grant nothing would ever match. The help the host generates for the
extension's asset type lists each tool's action and this format for the model.

An action `fn` returns that the extension never declared is a defect, not a
decision: the host logs an error and asks the user, without consulting rules or grants.

`.Default()` marks a group granted to every new asset of the extension's types.
Group ids must be namespaced by the extension's policy type — `ext:<PolicyType>:<group>`.
A policy type belongs to one extension: loading a second extension that claims the same
policy type, or a group id that is already registered, is refused. The action set itself
is never declared separately — the host derives it from the tools' `.Policy` actions
and `.PolicyFunc` / `.PolicyResources` action sets.

## SKILL.md and locales

`SKILL.md` is what the model reads before working with the asset type. It is
optional, but when present it must open with a frontmatter carrying `description` —
the one line that appears in the model's skill list (an extension without `SKILL.md`
falls back to its `i18n.description`); a missing or broken frontmatter fails the load.
The body is injected only when the model actually asks for `help`, and the host appends a
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
returns the action and the resources a call requests (a single-resource tool's as a
one-element list).

## Frontend pages (optional)

`opskat.Frontend(...)` declares an ESM entry the app loads from
`/extensions/<name>/<entry>`, served straight out of the installed extension
directory. A page slotted as `asset.connect` is what opening the asset shows. The app
injects `window.__OPSKAT_EXT__` (`React`, `ReactDOM`, `i18n`, `@opskat/ui`,
`@opskat/host-ui` — see below — and the extension API) before importing the module,
so a page uses the host's React rather than bundling its own. There is no import map
for bare specifiers: a plain ESM page (no build step, like `extensions/notebook`)
reads everything off `window.__OPSKAT_EXT__` directly, e.g.
`const { React, hostUI } = window.__OPSKAT_EXT__;` — see `extensions/notebook/frontend/page.js`.

### `@opskat/host-ui`

`window.__OPSKAT_EXT__.hostUI` gives a page three ready-made, on-theme components
instead of bundling its own (design decision in
[docs/specs/2026-09-24-ext-platform-capabilities.md](../docs/specs/2026-09-24-ext-platform-capabilities.md)):

- `CodeEditor` — Monaco, with a selectable `language` (including `"json"`).
- `JsonTreeView` — a read-only, expand/collapse JSON tree for `data` of any shape.
- `QueryResultTable` — a `columns` / `rows` result grid with sorting and copy built in.

They are the *exact same component instances* the host itself renders, so they
already follow the host's theme (CSS variables flip with `ThemeProvider`, no prop
needed) and language (the shared `react-i18next` instance) — a page just renders
them:

```js
const { React, hostUI } = window.__OPSKAT_EXT__;
const { createElement: h } = React;
h(hostUI.QueryResultTable, { columns: ["key", "size"], rows: notes });
h(hostUI.JsonTreeView, { data: someNote });
```

`hostUI.version` is bound to `hostABI` (currently `"2.2"`) — it tells a page which
host-ui revision it's running against. Declare `"hostABI": "2.1"` (or later) in your own
manifest once your page uses `hostUI`: that is the contract you are relying on, and
it is what keeps a future host free to drop `hostUI` behind a still-higher ABI
without silently breaking a `2.0` extension that never touched it.
