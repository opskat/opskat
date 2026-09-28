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

## Policy

Generic assets use a command-style allow / deny policy (with policy groups). A new asset
starts with a copy of its custom type's default rules; after that each asset's policy is
managed on its own.
