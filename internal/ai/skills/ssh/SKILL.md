---
name: ssh
description: "Run shell commands on a remote server over SSH via exec. Covers command syntax and the remote-vs-local distinction."
---

# SSH assets

## Command syntax

Pass the shell command verbatim as `command`:

- `uptime`
- `systemctl status nginx`
- `cat /etc/nginx/nginx.conf`
- `df -h | grep -v tmpfs`

## Notes

- The command runs on the **remote** server, never on the user's machine. Tools
  named `local_*` operate on the user's own machine and are not interchangeable
  with this one.
- Use `cat` / `ls` / `grep` inside the command to inspect remote files.
- The `scope` parameter is not used by SSH assets.
- Credentials are resolved automatically; never ask the user for a password.

## Asset config (for put_asset)

| field | type | required | notes |
|---|---|---|---|
| `host` | string | yes | |
| `port` | number | yes | No server-side default — pass `22` explicitly |
| `username` | string | yes | |
| `password` | string | no | Stored encrypted; never echoed back |
| `auth_type` | string | no | `"password"`, `"key"`, or `"agent"`; defaults to `"key"` when `private_key` is set, else `"password"` |
| `private_key` | string | no | SSH private key in PEM format; imported into the credential store |
| `passphrase` | string | no | Passphrase for `private_key`, if encrypted |
| `agent_source_id` | number | yes when `auth_type="agent"` | SSH agent source to authenticate through; must be a positive ID of an existing source |
| `agent_key_fingerprint` | string | yes when `auth_type="agent"` | SHA256 fingerprint of the identity to use. With `agent_source_id`, mutually exclusive with `password` / `private_key` / `passphrase` / `credential_id`; non-agent auth types must not carry either agent field |
