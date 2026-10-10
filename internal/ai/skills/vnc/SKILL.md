---
name: vnc
description: "VNC remote desktop assets. Config fields for put_asset; no command surface — exec is not supported for this type."
---

# VNC assets

VNC assets are opened as an interactive desktop session in the app. There is **no command
surface**: `exec` is not supported for this type, and there is nothing to script.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | Hostname or IP |
| `port` | number | no | Defaults to 5900 |
| `username` | string | no | Only needed if the VNC server requires one |
| `password` | string | no | **Write-only.** Encrypted in the asset; does not create a credential |
| `credential_id` | number | no | Existing managed password credential ID; mutually exclusive with `password` |
| `file_ssh_asset_id` | number | no | SSH asset backing the SSH/SFTP file-transfer channel; omit to disable file transfer |
| `proxy_chain` | array | no | Ordered list of hops, nearest to this machine first; replaces the whole stored chain, `[]` clears it. Layers: `{"type":"ssh","ssh_asset_id":N}`, `{"type":"socks5","host":"...","port":N,"username":"...","password":"..."}`, `{"type":"http_tunnel","url":"https://...","token":"...","timeout_seconds":N}` (first layer only). `password` / `token` are **write-only**, encrypted in the asset |
| `encryption` | string | no | `server` (default), `always_maximum`, `always_on`, `prefer_on`, or `prefer_off` |

Plaintext is never returned, is encrypted in the asset, and never creates a managed credential.
Unknown non-empty `encryption` values are rejected; omitted or empty values keep server-order compatibility.

Example:

    put_asset(name="lab-desktop", type="vnc", config={"host":"10.0.1.20","password":"s3cret"})
