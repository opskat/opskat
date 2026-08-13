---
name: local
description: "Local terminal assets — a shortcut that opens a shell on the user's own machine. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# Local terminal assets

A `local` asset is a shortcut that opens an interactive shell **on the user's own machine**,
not a remote server — it has no host/port/credentials to configure. There is **no command
surface**: `exec` is not supported for this type (unlike the `local_bash` / `local_write` / …
tool family, which is unrelated and always available regardless of asset type).

It still needs a help doc like every other asset type: `put_asset` must be able to create it,
and nothing else tells the model what a `local` asset's config object looks like.

## Asset config (for put_asset)

There are no required fields — an empty `config` object is valid.

| field | type | required | notes |
|---|---|---|---|
| `shell` | string | no | Shell executable path/name; empty uses the OS default |
| `args` | string | no | Comma/semicolon/newline separated extra shell arguments |
| `cwd` | string | no | Initial working directory; empty uses the OS default |
| `password` | string | no | Reserved compatibility key; any non-empty value is rejected as inapplicable |
| `credential_id` | number | no | Reserved compatibility key; any non-zero value is rejected as inapplicable |

Local automation never creates or references managed credentials; top-level
`credential_name` is therefore invalid.

Example:

    put_asset(name="my-shell", type="local", config={"shell":"/bin/zsh","cwd":"/Users/me/project"})
