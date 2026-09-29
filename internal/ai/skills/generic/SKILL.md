---
name: generic
description: "Generic assets built on a user-defined custom type (an internal HTTP API, Grafana, a local CLI, …). Create or update them with put_asset type=<custom type slug>; the config keys are that type's field names."
---

# Generic assets (custom types)

A generic asset is always based on a **custom type** that the user defines in the desktop
app. The custom type owns how the service is reached — the execution mode (HTTP requests or
a local command), the Base URL or command template, where credentials are injected, the
usage notes and the default policy. The asset itself only stores **field values** for that
type.

A custom type is identified by its **slug** (for example `grafana`). Wherever a type is
named — `put_asset` `type`, `opsctl create asset --type`, `help` — use the slug, never
`generic`: `generic` is only the type the asset is stored as, and creating with it is
rejected. Custom types are defined, changed, deleted, imported and exported only in the
desktop app; there is no AI or opsctl entry point for that.

The fields of a custom type (name, required, secret, default) are shown by `help` for its
slug or for any asset of that type. Read them before creating or updating an asset.

## Asset config (for put_asset)

The config keys are the custom type's **field names**; generic assets have no fixed field
list of their own. The type's field structure is the only contract:

- Unknown keys are rejected, naming the key; an unknown slug is rejected as an unknown type.
- On create, every required field must be given, unless the type defines a default for it. A
  field that is not given keeps resolving to the type's default.
- A non-secret field value is a string.
- A secret field value is either the plaintext string, which is encrypted into this asset
  only, or `{"credential_id": N}` to reference an existing managed password credential.
  Secret values never appear in approval details, audit logs or asset views.
- On update, only the given fields change; the others keep their stored values. A required
  field cannot be set to an empty string. `type` on update is only an assertion and must be
  the asset's slug.

Examples:

    put_asset(name="grafana-prod", type="grafana", config={"host":"grafana.internal","token":"glsa_example"})
    put_asset(name="grafana-prod", type="grafana", config={"host":"grafana.internal","token":{"credential_id":7}})
    put_asset(asset="grafana-prod", config={"host":"grafana2.internal"})

## opsctl

`opsctl create asset` and `opsctl update asset` take the same config object through
`--config` / `--config-file`:

    opsctl create asset --type grafana --name grafana-prod --config '{"host":"grafana.internal"}' --secret token
    opsctl update asset grafana-prod --config '{"host":"grafana2.internal"}'
    opsctl update asset grafana-prod --secret token

`--secret <field>` (repeatable) reads that secret field in the terminal without echo. Without
an interactive terminal opsctl exits with code 3 and a NEEDS TTY marker, like a bare
`--password`: put the value in `--config-file`, reference a managed credential, or let the
user run the command.

## Command syntax

`<METHOD> <PATH> [-H 'Name: value']... [-d <data>] [-i]` — for a custom type whose exec mode is
HTTP (`help` shows the mode). The same syntax works in `exec` and after `opsctl exec <asset> --`.

- `GET /api/search?query=cpu`
- `GET /api/dashboards/uid/abc -i`
- `HEAD /api/health`
- `POST /api/dashboards/db -H 'Content-Type: application/json' -d '{"dashboard":{"title":"CPU"}}'`
- `DELETE /api/dashboards/uid/abc`

## HTTP requests

- METHOD is one of GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS (any case). PATH must start
  with `/`, may carry a query string, and is appended to the asset's Base URL. Absolute URLs,
  `.` / `..` segments and `#` fragments are rejected.
- `-H` can repeat. `-d` sends a request body: literal data, `-d @<file>` for a local file, or
  `-d @-` for stdin. The two `@` forms read from the machine opsctl runs on and work only
  through `opsctl exec`; in `exec` pass the data inline. No header is added implicitly — pass
  the `Content-Type` yourself.
- Everything you pass is sent literally; `{{ ... }}` in a command is never rendered. The host
  injects the type's authentication (header, query or basic auth) and it overrides a header
  or query parameter of the same name you wrote. You never see or pass the credentials.
- Redirects to the same origin are followed (up to 10) with authentication; a redirect to
  another origin fails with an error instead of sending credentials there.
- The asset's SSH tunnel, proxy chain and TLS settings are used; tunnel, certificate and
  handshake errors are reported as they are — nothing falls back to a direct or unverified
  connection.
- A missing required field value (or a secret that cannot be decrypted) fails before any
  request is sent.
- `exec` returns the status line (for example `HTTP 200 OK`) followed by the body; `-i` adds
  the response headers. A body whose Content-Type is not text, JSON or XML comes back as a
  one-line summary with its size and type. A non-2xx status is a normal result, not an error.
- `opsctl exec` keeps each argument as given, writes the body to stdout and the status line to
  stderr (`-i` also puts the status line and headers on stdout), and exits 0 for 2xx, 1 for
  any other status (the body is still written) and 1 with nothing on stdout when the request
  did not complete.

## Policy

Generic assets use a command-style allow / deny policy (with policy groups). A new asset
starts with a copy of its custom type's default rules; after that each asset's policy is
managed on its own. Decisions go deny → allow → a saved grant → confirmation.

For HTTP the rules match `<METHOD> <path>` — upper-case method, path without the query string,
for example `GET /api/dashboards/uid/abc`. Rules are plain globs, not shell commands: `*`
matches any run of characters including `/` (`GET /api/*` covers `/api/a/b`), `?` matches one
character. New HTTP types allow `GET *`, `HEAD *` and `OPTIONS *` by default; everything else
asks for confirmation, and "always allow" saves the request's `<METHOD> <path>` as a grant.
