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
  "tunnel": true
}
```

Each one is enforced at the host call it guards: `fs` patterns are absolute path
prefixes (`${EXT_DIR}` resolves to the installed extension's directory), the `http`
allowlist is matched as a URL prefix and private/loopback destinations are refused
unless `tunnel` is also granted, and `credentials: "read"` is what lets
`GetAssetConfig` return decrypted password fields.

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
    AssetID int64    `json:"asset_id" desc:"ID of the notebook asset this call runs against"`
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

### Tools have no ambient asset

A tool call carries only its arguments. If a tool needs the asset it is running
against — its config, its notebook name — declare an `asset_id` parameter and read the
config with `opskat.GetAssetConfig(assetID)`, as `notebook` does. Say so in `SKILL.md`
too; the model has to pass it.

### The policy face

Every tool declares the action it requests through `.Policy(action)`. The host does
not take that as permission: it matches the action against the permission groups
granted on the asset, and the answer is one of three.

- an action in a granted group's **allow** list runs unattended;
- an action in a granted group's **deny** list is refused, and a denial beats every
  allow;
- anything else **asks the user**, and "always allow" saves a grant.

`.Default()` marks a group granted to every new asset of the extension's types.
Group ids must be namespaced `ext:<extension>:<group>`. The action set itself is never
declared — the host derives it from the tools.

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
reload**: the install unloads the old module first. Point `--data-dir` /
`OPSKAT_DATA_DIR` at the verification sandbox ([docs/VERIFICATION.md](../docs/VERIFICATION.md));
the app refuses the request when `OPSKAT_ENV=production`.

Then drive it like any other asset type:

```bash
opsctl help <asset>                                     # SKILL.md + the parameter table
opsctl exec <asset> -- note_list --asset_id=<id>
```

## Testing

`opskat.NewTestHost` installs a fake host and dispatches through the real registry, so
a test calls a tool the way the WASM entry point does — no wazero, no build step:

```go
host := opskat.NewTestHost(opskat.WithAssetConfig(7, notebookConfig{Notebook: "team"}))
defer host.Close()
result, err := host.CallTool("note_put", putArgs{AssetID: 7, Key: "k", Content: "v"})
```

`WithMockHTTP`, `WithMockTCP` and `WithActionCancel` stand in for the other host
capabilities; `CallAction` captures the events an action emits, and `CheckPolicy`
returns the action a call requests.

## Frontend pages (optional)

`opskat.Frontend(...)` declares an ESM entry the app loads from
`/extensions/<name>/<entry>`, served straight out of the installed extension
directory. A page slotted as `asset.connect` is what opening the asset shows. The app
injects `window.__OPSKAT_EXT__` (`React`, `ReactDOM`, `i18n`, `@opskat/ui`, and the
extension API) before importing the module, so a page uses the host's React rather
than bundling its own.
