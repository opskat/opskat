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
| `host` | string | yes | Hostname or IP |
| `port` | number | no | Defaults to `22` |
| `username` | string | yes | Login username |
| `auth_type` | string | no | `"password"`, `"key"`, or `"agent"`; inferred from plaintext/reference/Agent inputs when omitted |
| `password` | string | no | **Write-only.** Encrypted in the asset; never returned and does not create a credential |
| `credential_id` | number | no | Existing managed password or SSH-key credential ID; its type infers auth when `auth_type` is omitted and must match an explicit auth type |
| `agent_source_id` | number | yes for Agent | Existing SSH Agent source ID; the source may be offline at save time |
| `agent_key_fingerprint` | string | yes for Agent | Canonical SHA256 identity fingerprint; both Agent fields are required and infer Agent auth when `auth_type` is omitted |
| `ssh_asset_id` | number | no | Accepted compatibility key; the current automation handler does not persist it |

`password` and `credential_id` are mutually exclusive. Agent auth rejects both; non-Agent auth
rejects Agent fields. `private_key` and `passphrase` are not accepted by asset automation:
create/import the SSH-key credential in the desktop key manager, then pass `credential_id`.
Changing auth clears the old asset association but does not delete a possibly shared credential.

Example:

    put_asset(name="web-01", type="ssh", config={"host":"10.0.0.7","username":"root","password":"..."})
