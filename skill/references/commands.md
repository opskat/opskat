# opsctl Command Reference

## list

### `list assets [flags]`

List managed server assets.

**Flags**:
- `--type <string>` — Filter by asset type (e.g., "ssh")
- `--group-id <int>` — Filter by group ID (0 = all)

```bash
opsctl list assets
opsctl list assets --type ssh --group-id 2
```

### `list groups`

List all asset groups.

```bash
opsctl list groups
```

## get

### `get asset <asset>`

Get asset details including SSH config (host, port, username, auth method).

```bash
opsctl get asset web-server
opsctl get asset 1
opsctl get asset production/web-01
```

## ssh

### `ssh <asset>`

Open interactive SSH terminal. No approval needed (human use).

- Full terminal emulation (xterm-256color)
- Terminal resize via SIGWINCH
- Exit code propagation

```bash
opsctl ssh web-server
```

## exec

### `exec <asset> [--] <command>`

Execute remote command via SSH with stdio piping.

**Behavior**:
- If stdin is piped (not a terminal), data forwards to remote stdin
- stdout/stderr pass through directly
- Remote exit code propagated as opsctl exit code

**Approval flow**:
1. Command policy check (allow-list/deny-list per asset)
2. Session check (plan item consumption or session auto-approve)
3. Desktop app approval (blocks until response)

```bash
opsctl exec web-server -- uptime
opsctl exec 1 -- ls -la /var/log
echo "data" | opsctl exec web-server -- cat
opsctl --session $SESSION exec web-01 -- systemctl restart nginx
```

## create

### `create asset [flags]`

Create a new SSH asset. Requires approval.

**Required flags**:
- `--name <string>` — Display name
- `--host <string>` — Hostname or IP
- `--username <string>` — SSH username

**Optional flags**:
- `--port <int>` — SSH port (default: 22)
- `--auth-type <string>` — "password" or "key" (default: "password")
- `--group-id <int>` — Group ID (0 = ungrouped)
- `--description <string>` — Description

```bash
opsctl create asset --name "Web Server" --host 10.0.0.1 --username root
opsctl create asset --name "DB" --host db.internal --port 2222 --username admin --auth-type key --group-id 2
```

## update

### `update asset <asset> [flags]`

Update an existing asset. Only provided fields change. Requires approval.

**Optional flags**:
- `--name <string>` — New display name
- `--host <string>` — New hostname/IP
- `--port <int>` — New SSH port (0 = unchanged)
- `--username <string>` — New SSH username
- `--description <string>` — New description
- `--group-id <int>` — New group ID (-1 = unchanged, 0 = ungrouped)

```bash
opsctl update asset web-server --name "New Name"
opsctl update asset 1 --host 192.168.1.100 --port 2222
```

## cp

### `cp <source> <destination>`

SCP-style file transfer via SFTP. Requires approval.

**Path format**:
- Local: `/path/to/file` or `./relative`
- Remote: `<asset>:<remote-path>`

**Transfer modes**:
- Local → Remote: `opsctl cp ./config.yml web-server:/etc/app/config.yml`
- Remote → Local: `opsctl cp 1:/var/log/app.log ./app.log`
- Remote → Remote: `opsctl cp 1:/etc/hosts 2:/tmp/hosts` (direct streaming, no local disk)

## plan

### `plan submit`

Submit batch plan for approval. Read JSON from stdin.

**Input JSON**:
```json
{
  "description": "Plan description",
  "items": [
    {"type": "exec", "asset": "web-server", "command": "uptime"},
    {"type": "cp", "asset": "web-server", "detail": "upload config.yml"},
    {"type": "create", "asset": "", "detail": "create new server"},
    {"type": "update", "asset": "web-server", "detail": "update config"}
  ]
}
```

**Item fields**:
- `type` — "exec", "cp", "create", "update"
- `asset` — Asset name or ID (optional for create)
- `command` — Command string (for exec)
- `detail` — Human-readable description

**Output**: Session ID (UUID) on approval, error on denial.

```bash
SESSION=$(opsctl plan submit < plan.json)
opsctl --session $SESSION exec web-01 -- uptime
```

## session

### `session start`

Create a new approval session. Writes session ID to `<data-dir>/active-session` and prints to stdout.

### `session end`

End the current active session (removes the active-session file).

### `session status`

Show the current active session ID.

```bash
SESSION=$(opsctl session start)
opsctl --session $SESSION exec web-01 -- uptime
opsctl session end
```

## version

Print CLI version.

```bash
opsctl version
```
