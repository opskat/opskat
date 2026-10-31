# Extensions

Example extensions, built and tested inside this repository. They share the module,
the SDK (`pkg/extsdk`) and the test run with the host, so an SDK change that breaks an
extension breaks `go test ./...` instead of a repository nobody rebuilt.

- [`notebook/`](./notebook) — the reference example. One asset type, four tools, a
  policy face that allows, asks and refuses, a `SKILL.md` and two locales. It stores
  everything in the host KV, so it runs offline with no permission grants at all.

The runtime that loads these — wazero, the reactor ABI, the permission enforcement,
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
`version`, `minAppVersion`, `hostABI`, `backend`, `permissions`, and
`contributes.databaseDrivers`. That is what a user must be able to audit *before* the
code runs, so it can never come from the code. Everything else — tools, asset types,
policies, snippets, pages, display strings — is answered by `describe()`.

```json
{
  "name": "notebook",
  "version": "0.1.0",
  "hostABI": "1.0",
  "backend": { "runtime": "wasm", "binary": "main.wasm" },
  "permissions": {}
}
```

`hostABI` is `1.0`, and only `1.0` (`extmanifest.SupportedHostABIs`): an extension
declaring any other value is refused at load, with the version it declared and the
ones this app supports. The manifest is parsed strictly — an unknown field, or a
`contributes` key other than `databaseDrivers`, is a parse error naming the extension
and the field. An extension built for the earlier manifest shape (capabilities, tools
and asset types written into the manifest) therefore shows up in Settings as failed to
load, with that parse error; nothing of it is registered.

`permissions` defaults to deny-all, and the notebook needs nothing: the host KV, the
asset config and logging are available without a grant. Declare only what you use:

```json
"permissions": {
  "fs":          { "read": ["${EXT_DIR}/**"], "write": ["/var/tmp/myext/**"] },
  "credentials": "read",
  "kerberos":    true,
  "network": {
    "http":          ["https://api.example.com/"],
    "allowPrivate":  false,
    "assetEndpoint": true,
    "assetRoute":    false
  }
}
```

Each one is enforced at the host call it guards:

- `fs.read` / `fs.write` are absolute path prefixes (`${EXT_DIR}` resolves to the
  installed extension's directory and may only start an entry); an optional trailing
  `/**` is the same prefix. An empty, relative or `..` entry fails manifest parsing.
- `network.http` is a list of URL prefixes. A request is admitted by one that names
  its scheme, host and port (the scheme's default port filled in, the host
  case-insensitive) and whose path starts the request's path, so
  `https://api.example.com` admits `https://api.example.com/v1/x` but not
  `https://api.example.com.evil.net/`.
  Each must be an `http://` or `https://` URL with a host, or manifest parsing fails.
  Private and loopback destinations are refused unless `network.allowPrivate` is also
  set.
- `credentials: "read"` is what lets `ctx.AssetConfig()` return decrypted password
  fields (see [Reading secret fields](#reading-secret-fields)).
- `network.assetEndpoint` lets a call scoped to an asset reach the addresses in that
  asset's config fields tagged `format:"endpoint"` (a URL or `host:port`) over HTTP and
  TCP — private addresses included, since the user typed them. The target's scheme,
  host and port must match an endpoint field (a bare `host:port` admits http and
  https); anything else, a redirect off the endpoints included, is refused with "not
  an endpoint of the asset".
- `network.assetRoute` lets a call scoped to an asset dial **any other address along
  that asset's connection path** — the SSH tunnel, proxy or proxy chain it is
  configured with: a database driver following a redirect to a cluster node or a
  replica, a tool reaching a node behind the asset's tunnel. Through a tunnel, proxy
  or chain the name resolves at the far end; on a direct path it resolves on the
  user's machine and a target on that machine or its link — loopback, link-local,
  unspecified, or any address of the machine's own network interfaces — is refused,
  naming the extension, the asset and the target. Only the asset's own address is
  exempt: an asset on a loopback or link-local address may reach such addresses, and
  an asset whose endpoint resolves to one of the machine's interface addresses may
  reach that address itself, on any port — not loopback, not the machine's other
  addresses.
- `kerberos` lets a call whose scope carries a ticket source ask the host for a
  Kerberos ticket (`opskat.KerberosTicket`); today only a database asset that logs in
  with Kerberos has one.

The install confirm and `opsctl ext dev` list every permission and every declared
driver id; `credentials: "read"`, `network.assetRoute` and `kerberos` are marked high
risk, and an upgrade marks the entries it adds as new.

### The call scope decides what is reachable

Every call runs in a **scope** that the subsystem owning the call's asset resolves
(`extension.Scope`): the asset, its endpoints, its connection path, its TLS settings,
its credential injection, its ticket source, and — for a database driver session — the
fixed list of host calls the session may make. The runtime only admits each host call
against that scope and the manifest's `permissions`; it never reads an asset type's
declaration itself. In order:

1. a `network.http` URL is dialed directly;
2. one of the scope's endpoints is reachable with `network.assetEndpoint`, along the
   asset's connection path, with its TLS settings and injected credentials;
3. any other address is reachable only with `network.assetRoute` and a scope that has a
   connection path;
4. anything else is refused, and the refusal is logged naming the extension, the asset
   and the target.

A call with no asset has an empty scope, so only `network.http` applies. Without
`network.assetEndpoint` an asset's endpoint is not reachable at all, TCP included.
`auth.kerberos_ticket` answers only when the scope has a ticket source **and** the
manifest declares `kerberos`.

**Everything else is answered by the module itself**, through `describe()`. You never
write that answer: the SDK derives it from the registration calls, so the host's view
of the extension and the code that runs cannot disagree. `describe()` answers `icon`,
`i18n` and `contributes`; each `contributes` key — `assetTypes`, `tools`, `policy`,
`snippets`, `pages`, `databaseDrivers` — is checked by the subsystem that owns that
kind of contribution when the extension loads, and an error names the extension, the
contribution and the entry. A key no contribution point is registered for, or a
contribution that fails to attach, keeps the whole extension from loading.

## Registering

The guest is a **WASI reactor** — the host runs `_initialize` and never calls `main`,
so registration happens in `init()` and `func main() {}` stays empty.

```go
opskat.Extension(opskat.Meta{
    Icon:        "archive",              // an icon name from the app's icon set
    DisplayName: "extension.displayName", // i18n keys, resolved against locales/
    Description: "extension.description",
    PolicyType:  "notebook",             // required when a tool declares a policy action: the policy face the asset types are checked under
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

**Asset types are required only by tools that go through `exec`.** A tool that declares a
policy action is reached through `exec` on an asset, so the extension must register at
least one asset type for it, and `Meta.PolicyType` must be set (it is the policy face
the asset types are checked under). An extension whose tools are all page-only, or that
only provides snippets or database drivers, needs neither. A database-driver extension's
entry point is the user's own database asset.

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

### Policy tool or page-only tool

A tool is exactly one of two things, and declares which:

- a **policy tool** declares the action it requests (`.Policy`, `.PolicyFunc` or
  `.PolicyResources`, see [The policy face](#the-policy-face)). AI `exec` and opsctl can
  call it, checked against the asset's permission groups; the extension's own pages can
  call it too.
- a **page-only tool** declares `.PageOnly()`. It has no policy action, is left out of
  `help`, and `exec` refuses it. Only the extension's own pages call it.

Declaring both is a registration panic; declaring neither fails `describe()`, naming the
tool. A policy tool needs the extension to register an asset type (above).

Every tool, whoever calls it, can emit events and can be cancelled:

```go
func tail(ctx *opskat.ToolContext, args tailArgs) (any, error) {
    for !ctx.ShouldStop() {          // true once the page cancels this call
        ctx.Events.Send("line", map[string]any{"text": next()})
    }
    return nil, nil
}

opskat.Tool("tail", tail).PageOnly().NoTimeout()
```

Events reach the page that made the call (the `onEvent` callback of `api.callTool`,
below); AI `exec` and opsctl callers ignore them, so a tool never has to know who called
it. A cancelled call ends as cancelled whatever the handler returns, and is cut off if it
does not return within the host's grace period after the cancel. A tool's timeout is 30
seconds unless `.Timeout(d)` sets another, up to 10 minutes; `.NoTimeout()` — page-only
tools only, exclusive with `.Timeout` — removes it, and the call runs until it returns or
the page cancels it.

### Large parameters opsctl can read from a file

A payload too large or too awkward for a command line — an NDJSON bulk body — can be
marked large on the tool's registration handle:

```go
opskat.Tool("request", handleRequest).
    PolicyResources(actions, classify).
    Large("body") // the JSON name of a string parameter
```

`opsctl exec <asset> -- request --body-file payload.ndjson` (or `--body-file -` for
stdin) is then exactly `--body <file content>`: opsctl reads the file and runs the
inline form, so `PolicyFunc` / `PolicyResources`, the approval dialog, grants and
audit all see the content, and the handler receives it as the ordinary `body`
argument — there is nothing to implement. `describe()` reports the marker as
`tools[].large`, and `opsctl help <asset>` lists the `--body-file` form marked
"opsctl only".

- `Large` panics at registration unless the name is a declared **string**
  parameter whose `<name>-file` spelling is not itself a parameter, and on a repeat;
  the tools contribution point refuses such an entry in `describe()` as well.
- opsctl refuses, sending nothing and exiting non-zero: both `--body` and
  `--body-file`; an unreadable file; content over 16 MiB or not valid UTF-8; stdin
  named twice.
- AI `exec` and the extension's own pages do not accept `--body-file` — it is an
  unknown flag there. Reading files happens only inside the opsctl process.
- opsctl has no file dialog: under `opsctl exec`, a tool that calls
  `opskat.FileDialog` fails with an error pointing at `--<flag>-file`. A tool meant to
  be driven from the command line takes its file through a `Large` parameter instead.

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
[Test connection](#test-connection).) An extension page that *does* work on a saved
asset passes its `assetId` prop — `api.callTool(ext, tool, args, assetId, { signal,
onEvent })` — and the handler reads it from `ctx.Asset` the same way. A page may call
any tool of its own extension, page-only or not, with or without an asset id.
`api.callTool` runs directly: it is the user's own action in the page, so there is no
policy check, approval dialog, grant or audit row (those belong to AI `exec` and
opsctl, which stay gated on the same asset). It is still scoped to the asset —
`ctx.AssetConfig()`, endpoint gating, the connection settings and credential injection
all apply — and its arguments are checked against the tool's declared parameters
(unknown keys are rejected). `onEvent` receives what the tool sends through
`ctx.Events`; aborting `signal` cancels the call, and the tool's `ctx.ShouldStop()`
turns true. A tool failure, the handler's error included, reaches the page as a
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

Every policy tool declares the action it requests (a page-only tool has none, and is
not checked). A tool whose action is fixed uses
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
literal, so a key that happens to contain `*` means exactly that key. A policy tool uses exactly one
of `.Policy`, `.PolicyFunc`, `.PolicyResources`; a page-only tool uses none.

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
`.PolicyResources`. Keep it for arguments that can never
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

## Snippets

`opskat.SnippetCategory(id, assetType, name)` adds a snippet category shown for one of
the extension's own asset types, and `opskat.SnippetSeed(opskat.Seed{...})` ships a
read-only snippet into a category. Which categories are built in (`shell`, `sql`, `k8s`,
…) is decided by the host's snippet category registry alone: declaring a category with a
built-in id fails the load, naming it, while a seed may go into any built-in category or
one the extension declares. An extension may provide snippets alone, with no asset type.

## Providing a database driver

An extension can provide a driver for the `database` asset type — a SQL database the
app has no built-in driver for. The driver contract is the public `pkg/dbdriver`, the
same one the built-in drivers implement; how a driver is shaped (descriptor, form
fields, capabilities, the session's operations) is in
[docs/references/adding-a-database-driver.md](../docs/references/adding-a-database-driver.md#provided-by-an-extension).
This section is what is different in an extension.

The manifest declares the driver ids in `contributes.databaseDrivers` — the one
contribution the user audits before the code runs — and the permissions the driver
needs. `describe()` must answer with exactly those drivers or the extension is refused
at load:

```json
{
  "name": "mydb-driver",
  "version": "0.1.0",
  "hostABI": "1.0",
  "backend": { "runtime": "wasm", "binary": "main.wasm" },
  "permissions": { "network": { "assetEndpoint": true, "assetRoute": true } },
  "contributes": { "databaseDrivers": ["mydb"] }
}
```

The driver registers its descriptor and how a session connects with
`sdkdbdriver.DatabaseDriver` (`pkg/extsdk/sdkdbdriver`). It is a package of its own:
serving a driver links `database/sql` and `pkg/dbdriver/sqldb`, and an extension that
does not import it carries neither. The SDK assembles the session from what `open`
returns with `sqldb`, so the extension writes a `database/sql` driver (or embeds one
compiled for `wasip1`) and the dialect, nothing else:

```go
opskat.Extension(opskat.Meta{Icon: "database", DisplayName: "extension.displayName"})
sdkdbdriver.DatabaseDriver(dbdriver.Descriptor{
    ID: "mydb", Name: "MyDB", Order: 500, Aliases: []string{}, DefaultPort: 4000,
    Form: []dbdriver.FormTabFields{{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
        dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldDatabase,
    }}},
    Capabilities: []dbdriver.Capability{dbdriver.CapViewDDL},
}, func(ctx context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) {
    db := sql.OpenDB(newConnector(req)) // dials with req.Transport.Dial
    if err := sqldb.Establish(ctx, db, nil); err != nil {
        return nil, nil, err
    }
    return db, myDialect{}, nil
})
```

- **`open` gets the config, the password and the reach path.** `req.Config` is the
  asset's connection config without what the host keeps to itself (tunnel, proxies,
  credential); `req.Password` is the decrypted password, handed over in this call only.
  Every network connection must be made with `req.Transport.Dial`: the host dials it
  along the asset's SSH tunnel or proxy chain. `open` returns once the database
  answers; its error is shown to the user as the driver's error.
- **A driver session runs in a fixed scope.** The asset's own `host:port` is the
  scope's one endpoint, so the extension declares `network.assetEndpoint` to connect at
  all. The scope admits only these host calls — logging, TCP streams and their
  deadlines, the stop check and Kerberos tickets; KV, file dialogs, asset config, HTTP
  and files are refused. The restriction belongs to the scope, not to a special case in
  the runtime.
- **The driver reaches the asset's `host:port`, and other addresses only with
  `network.assetRoute`.** Without it any other address fails, naming it. With it, any
  other `host:port` (a cluster node, a replica, a routed listener the database
  answers with) is dialed along the asset's route — direct, SSH tunnel, proxy or proxy
  chain — to that address itself: through a tunnel, proxy or chain the name resolves at
  the far end, directly it resolves on the user's machine, and then a loopback
  (`127.0.0.0/8`, `::1`, `localhost`), unspecified (`0.0.0.0`, `::`), link-local or
  own-interface (any address of the user's machine's network interfaces) target is
  refused, except the asset's own: a loopback / link-local asset may reach loopback /
  link-local targets, and an asset at one of the machine's interface addresses may
  reach that address itself on any port, nothing else on the machine. The first dial
  of each such
  address in a session is logged (extension, driver, asset, address); one the route
  cannot reach fails with the route's error naming the address.
- **Certificate TLS is the driver's own.** Declare `dbdriver.FieldTLSCert` on the
  `TabTLS` tab (not together with `FieldTLS`) and the asset form shows the same
  certificate fields as a Redis asset: enable TLS, skip verification, server name, and
  the CA certificate, client certificate and client key, each a file path or pasted
  content. When the asset has TLS on, `open` gets them as `req.TLS`
  (`*dbdriver.TLSMaterial`): `Insecure`, `ServerName` and the `CA` / `Cert` / `Key`
  contents — paths already read by the process opening the session, the key decrypted —
  handed over like the password, only in this call; with TLS off `req.TLS` is `nil`.
  The driver builds its own `tls.Config` from them (its database library may start or
  redo TLS mid-protocol); the host never wraps the connection in TLS. A file that cannot
  be read or a key that does not decrypt fails the open naming the field; invalid
  contents or a failed handshake are the driver's own TLS error, never a fallback to
  plaintext or to no verification.
- **Kerberos tickets come from the host.** Declare `dbdriver.FieldKerberos` on the
  `TabConnection` tab (the manifest must declare `permissions.kerberos`; otherwise the
  extension is refused at load, naming the driver) and the asset form offers an
  authentication method: password or Kerberos. When the asset uses Kerberos, `open`
  gets `req.Kerberos`, `req.Config.AuthMethod` set to `dbdriver.AuthMethodKerberos` and
  an empty `req.Password`; the username is handed over as usual (the user may leave it
  empty under Kerberos), the Kerberos password, keytab and keys never are.
  In the login handshake, call `req.Kerberos.Ticket(ctx, dbdriver.KerberosSPN(service,
  host))` with the service and host names the database sent (go-ora's
  `Authenticate(server, service)`), and write the bytes it returns — the Kerberos
  AP-REQ — into the handshake as they are; `opskat.KerberosTicket(spn)` is the same
  call. The host fetches the ticket with the asset's Kerberos credentials and refuses,
  with the reason, a request from a scope without a ticket source (a call outside a
  driver session, or an asset that does not use Kerberos), a name that is not
  `service/host` (neither may contain `/` or `@`), and a service principal other than
  the one the asset pins (naming both). A failed ticket fails the login with the host's
  reason; nothing falls back to the password. The first ticket for each service
  principal in a session is logged (extension, driver, asset, principal, service
  principal), never a credential or a ticket. In tests, `opskat.WithKerberosTickets`
  gives a `TestHost` a ticket source.
- **The driver can split and classify SQL itself.** Declare
  `dbdriver.CapClassifyStatements` and pass
  `sdkdbdriver.WithStatementClassifier(classify)` to `DatabaseDriver` — one without the
  other panics at init. `classify(ctx, sql)` returns each statement of the text as a
  `dbdriver.Statement`: byte offsets `Start` / `End` into the input, `Text`, an
  uppercase `Type`, and `Dangerous` with a `Reason` from `dbdriver.DangerReasons()`. It
  must not touch the database: the host calls it outside any session, on an instance of
  its own where logging is the only host call allowed (any other one fails), and cuts
  it off after 30 seconds. The AI / opsctl
  permission check and the query panel then go by these statements instead of the
  host's own SQL parsing. Use the host's type names (`SELECT`, `UPDATE`, `DROP TABLE`,
  ...) only for the same kind of statement and a name of your own (`MERGE`, a PL/SQL
  block) otherwise. The host checks the answer — offsets inside the input, in order and
  not overlapping, type uppercase, a known reason only on a dangerous statement — and
  treats one breaking a rule, like an error `classify` returns for text it cannot split,
  as SQL it cannot split, naming the driver and the reason.
- **The form is a network database's.** An extension driver is checked by the host's
  network rules (host and port required, and a username unless the asset logs in with
  Kerberos), so its `Form` must declare
  `FieldHost`, `FieldPort` and `FieldUsername` and none of the database-file fields
  (`FieldSQLiteSource`, `FieldPath`, `FieldSSHAsset`); otherwise the extension is
  refused when it loads, naming the driver.
- **Declare exactly what the dialect implements.** The optional `sqldb` dialect
  interfaces decide which session operations exist; a capability in the descriptor the
  dialect does not implement offers the user an entry point that fails. A read-only
  config is refused by the SDK unless the descriptor declares `CapReadOnly`, in which
  case `open` must make the connection read-only.
- **The table designer's column features are opt-in.** A descriptor may declare
  `ColumnTypes` (grouped common types and the presets for a new column and a new
  table's `id`; group labels may be keys of your `locales/` files), `NameFolding`
  (`NameFoldUpper` for a database that stores unquoted names in upper case), and the
  two that change requests: `ExplicitDefaults` (defaults given as none / NULL / value /
  expression) and `AutoIncrement` (new tables and new columns). Switch those two on in
  the dialect — `sqldb.DefaultClassifier` (judge an existing column's default) and
  `sqldb.AutoIncrementer` (the clause after the type) — and set the descriptor fields
  from `sqldb.ClassifiesDefaults(dialect)` / `sqldb.AutoIncrements(dialect)`, so the
  declaration cannot drift from the implementation. `CreateTableComments` says a create
  table writes the table and column comments (the dialect's `sqldb.SchemaCommenter`, or
  a `sqldb.TableCreator` that writes them); without it the designer offers no comment
  inputs for a new table. `EmptyStringIsNull` (from
  `sqldb.StoresEmptyStringAsNull(dialect)`, the same `sqldb.EmptyStringNull` the filters
  use) only makes the designer warn that an empty-string default is NULL. The host
  refuses, before sending, a request using a feature the descriptor does not declare
  (`driverSession.checkCreateColumns` / `checkAlterColumns`, `ErrUnsupported`). Under
  `hostABI` 1.0 these fields are all optional: a driver that declares none of them keeps
  creating and altering tables, and simply does not get them.
- **Index editing is opt-in the same way.** Declare `IndexEditing` on the descriptor
  (`*dbdriver.IndexEditing`: the index types you offer with their labels — built-in ids
  `dbdriver.IndexTypePrimary` / `IndexTypeUnique` / `IndexTypeNormal` /
  `IndexTypeFullText` or ids of your own, e.g. `bitmap`; whether the primary key may be
  changed; descending columns, prefix lengths and index comments; where index names are
  unique, `IndexScopeTable` / `IndexScopeSchema` / `IndexScopeDatabase`; and the extra
  per-index options, each an enum with its values or a switch). The labels are plain
  strings — a host i18n key for the built-in names, a key of your own `locales/` files
  for anything else — and the host shows whatever it cannot translate as it is. Switch
  it on in the dialect with `sqldb.TableIndexer`: `IndexEditing()` returns that same
  declaration, `IndexSQL(tableRef, index)` / `DropIndexSQL(tableRef, name)` write one
  index, and your `AlterableTable` lists the table's indexes (the primary key as
  `IndexTypePrimary`, an index you cannot fully represent with `ReadOnlyReason`, one a
  `UNIQUE` constraint owns with `Undroppable`) plus the index names other tables in the
  declared scope already use (`TakenIndexNames`). Set the descriptor field from
  `sqldb.IndexEditing(dialect)` so the two cannot drift. The shared session then hands
  your dialect the changes as `TableChange.Indexes` / `IndexDrops` (`sqldb.IndexDropStatements`
  before your column changes and `sqldb.IndexCreateStatements` after them write them for you) and refuses on its own what the
  declaration does not allow: a read-only index can only be dropped, an undroppable one
  not at all, a primary-key change needs `AlterPrimaryKey`, an index on a dropped column
  goes with it. A create table's indexes and its table / column comments reach
  `CreateTableStatements`; write them either by implementing `sqldb.TableCreator` (a whole
  `CREATE TABLE` of your own, needed when comments live inside the table definition) or by
  implementing `sqldb.SchemaCommenter` (`TableCommentSQL` / `ColumnCommentSQL`, written
  after the shared `CREATE TABLE` and its `IndexSQL`s). A dialect that writes neither is
  refused a request carrying a comment rather than quietly dropping it. The host refuses,
  before sending, a create / alter table request whose indexes the descriptor does not
  declare (`driverSession.checkCreateIndexes` / `checkAlterIndexes`, `ErrUnsupported`);
  `IndexEditing` is optional under `hostABI` 1.0, and a driver without it keeps its
  create and alter table.
- **Fields of the driver's own go in `DriverFields`.** Each one declares its key, type,
  tab, whether it is required and its default; the values are stored in the asset's
  `driver_options` and reach `open` as `req.DriverOptions`, password fields decrypted.
  `Label` and `Description` may be keys of your `locales/` files: the host shows them in
  the user's language.
- **A session lives in instances of its own.** The host runs each session on up to
  three module instances dedicated to it — operations that overlap get one each, later
  ones wait — and keeps each instance — the `*sql.DB`, its open connections, a cursor
  half read — between calls, so later operations do not log in again and a whole-table
  export reads one cursor over many calls. `open` runs once per instance (again for one
  that replaces an instance discarded after a failure), so connection state such as a
  current schema is not shared between a session's instances. Every operation and
  result crosses the boundary as JSON ([`pkg/extwire/driver.go`](../pkg/extwire/driver.go)).

`pkg/extension/testdata/fixture-dbdriver` is a complete driver over an in-memory
`database/sql` driver; the host's tests run the driver conformance suites against it
through the boundary (`internal/extreg/extdriver/driver_session_test.go`).

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
through the same path the "install from directory" button uses — permission
enforcement, registries and all. **Re-running the two commands after an edit is the
reload**: the new build is loaded and checked beside the old one and swapped in only
if it loads — a broken build keeps the previous version running. Point `--data-dir` /
`OPSKAT_DATA_DIR` at the verification sandbox ([docs/VERIFICATION.md](../docs/VERIFICATION.md)).
The app asks you to confirm each install in its opsctl approval dialog (source
directory, extension name/version, declared permissions, whether it replaces an
installed extension); re-runs of the same directory for the same extension with
unchanged permissions are remembered until the app restarts, so the reload loop
does not re-prompt.

Then drive it like any other asset type:

```bash
opsctl help <asset>                                     # SKILL.md + the parameter table
opsctl exec <asset> -- note_list
```

`opsctl exec` runs the tool in opsctl's own process: it loads just this extension from
the data dir's `extensions/` directory, with the host capabilities the app gives it
(asset config and credentials, KV, the asset's connection path), so it works whether
or not the desktop app is running — a running app only answers approvals. Each
command loads the build on disk, so after an `ext dev` reload the next `exec` already
runs the new one. An extension disabled in the app is refused.

## Testing

`opskat.NewTestHost` installs a fake host and dispatches through the real registry, so
a test calls a tool the way the WASM entry point does — no wazero, no build step:

```go
asset := opskat.Asset{ID: 7, Name: "team runbooks", Type: "notebook"}
host := opskat.NewTestHost(opskat.WithAssetConfig(asset.ID, notebookConfig{Notebook: "team"}))
defer host.Close()
result, err := host.CallTool(asset, "note_put", putArgs{Key: "k", Content: "v"})
```

`WithMockHTTP`, `WithMockTCP`, `WithToolCancel` and `WithKerberosTickets` stand in for the
other host capabilities; `CallTool` also records the events the tool sends through
`ctx.Events`, which `Events` returns afterwards, and `CheckPolicy` returns the action
and the resources a call requests (a single-resource tool's as a one-element list).

## Frontend pages (optional)

`opskat.Frontend(opskat.FrontendSpec{Entry, Styles, Pages})` declares an ESM entry the
app loads from `/extensions/<name>/<entry>`, served straight out of the installed
extension directory. Each `opskat.Page` names an `ID`, a `Slot`, a display `Name` (an
i18n key) and the `Component` it renders (an exported symbol of the entry module). The
slot is required and must be one the host defines; today that is `asset.connect` — the
page opened by double-clicking an asset of the extension's type. The entry and styles
paths must stay inside the extension directory; both rules are checked when the
extension loads. The app injects `window.__OPSKAT_EXT__` (`React`, `ReactDOM`, `i18n`,
`ui` (`@opskat/ui`), `hostUI` (`@opskat/host-ui` — see below) and `api`) before importing
the module, so a page uses the host's React rather than bundling its own. There is no
import map for bare specifiers: a plain ESM page (no build step, like
`extensions/notebook`) reads everything off `window.__OPSKAT_EXT__` directly, e.g.
`const { React, hostUI } = window.__OPSKAT_EXT__;` — see `extensions/notebook/frontend/page.js`.

The page API has one entry point, `api.callTool(ext, tool, args, assetId?, { signal?,
onEvent? })` (see [The asset comes from the host](#the-asset-comes-from-the-host-not-from-the-arguments)):
events the tool sends arrive at `onEvent({ eventType, data })`, and aborting `signal`
cancels the call. There is no separate action call.

### `@opskat/host-ui`

`window.__OPSKAT_EXT__.hostUI` gives a page three ready-made, on-theme components
instead of bundling its own. Each is a wrapper whose props are defined by `hostUI`
itself (`frontend/src/extension/hostui/`), not the host's internal component, so the
host can change its own components without breaking an extension page. They follow the
host's theme (CSS variables flip with `ThemeProvider`, no prop needed) and language
(the shared `react-i18next` instance):

- `CodeEditor` — Monaco. Props: `value`, `onChange`, `language` (`"sql"`,
  `"javascript"`, `"json"`, `"plaintext"`, `"shell"`, `"markdown"`, `"yaml"`),
  `readOnly`, `height`, `fontSize`, `placeholder`, `options` (merged over the host's
  editor options), `className`, and `onMount(editor, monaco)` — called once the editor
  is ready with the editor and Monaco instances, so a page can register a language,
  highlighting and completions. The Monaco API is part of the contract: a host upgrade
  of Monaco's major version is a `hostUI` contract change.
- `DataTable` — a result grid. Props: `columns`, `rows`, `loading`, `showRowNumber`,
  `rowNumberOffset`, `rowDensity`, `sortColumn` / `sortDir` / `onSortChange`
  (controlled sorting; local when `onSortChange` is not given), `onCopyAs(format,
  cell)` (adds a "copy as" submenu; the page performs the copy), `onSelectedCellChange`,
  `onSelectedRowsChange`.
- `JsonTreeView` — a read-only, expand/collapse JSON tree. Props: `data` (any shape),
  `className`.

```js
const { React, hostUI } = window.__OPSKAT_EXT__;
const { createElement: h } = React;
h(hostUI.DataTable, { columns: ["key", "size"], rows: notes });
h(hostUI.JsonTreeView, { data: someNote });
```

`hostUI.version` is `"1.0"`, bound to `hostABI`. The exported props types are in
`frontend/src/extension/hostui/` (`HostCodeEditorProps`, `HostDataTableProps`,
`HostJsonTreeViewProps`).
