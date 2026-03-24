# opsctl init — Asset Environment Discovery

Discover server environment via SSH, generate a structured description, and persist it to the asset's `description` field.

## Arguments

- `init <asset-id or name>` — Single asset
- `init --group <group-id>` — All assets under a group
- `init` (no args) — Run `opsctl list assets`, let user pick

## Language

Infer description language from the user's conversation language. Default to English.

## Execution Strategy

### Phase 1 — Quick Scan (single SSH call)

Collect OS info, hardware, network, processes, and listening ports in one command:

```bash
opsctl exec <asset> -- "
echo '=== OS ===' && uname -a && cat /etc/os-release 2>/dev/null;
echo '=== HOSTNAME ===' && hostname;
echo '=== CPU ===' && nproc;
echo '=== MEMORY ===' && free -h;
echo '=== DISK ===' && df -h;
echo '=== NETWORK ===' && ip -4 addr show 2>/dev/null | grep inet;
echo '=== PROCESS ===' && ps aux --sort=-%mem 2>/dev/null | head -30;
echo '=== LISTENERS ===' && ss -tlnp 2>/dev/null | head -25;
echo '=== CONTAINERS ===' && docker ps --format '{{.Names}}: {{.Image}}' 2>/dev/null || podman ps --format '{{.Names}}: {{.Image}}' 2>/dev/null;
echo '=== K8S ===' && kubectl version --short 2>/dev/null && kubectl get nodes 2>/dev/null;
"
```

Key signals:
- **`ps aux`** — actual running processes (nginx, mysqld, redis-server, kubelet, java, node, etc.)
- **`ss -tlnp`** — listening ports + process names, reveals externally facing services
- Together they identify services not managed by systemd, custom apps, and manually started processes

### Phase 2 — Deep Dive (conditional, based on Phase 1)

Analyze Phase 1 process list and port info. Run additional commands only for detected environments:

| Detected (from processes/ports) | Follow-up commands |
|---------------------------------|-------------------|
| kubelet / kube-apiserver | `kubectl get pods -A --no-headers \| wc -l`, `kubectl get namespaces`, `kubectl cluster-info` |
| dockerd / containerd | `docker info`, `docker stats --no-stream` |
| mysqld / postgres / redis-server | Corresponding `--version`, data directory size |
| nginx / httpd / caddy | Version, `nginx -T 2>/dev/null \| grep server_name` |
| java / node / python processes | Corresponding `--version` for runtime details |
| Cloud hostname patterns (e.g. `ip-*`, `*.compute.internal`) | Cloud metadata API for instance-type |

Phase 2 is not a fixed script — use judgment based on what Phase 1 reveals.

## Description Format

```
[OS] Ubuntu 22.04 LTS (kernel 5.15.x)
[Hardware] 4C/8G/100G SSD
[Network] 10.0.1.5 (private), 203.0.113.1 (public)
[Kubernetes] Worker node (v1.28.2), 3-node cluster, 45 pods
[Containers] Docker 24.0.5, 12 running
[Services] nginx 1.24, PostgreSQL 15, Redis 7.0
[Runtime] Python 3.11, Node.js 18
[Purpose] K8s worker node running web application stack
```

Rules:
- Only include tags that have actual data — omit empty ones
- `[Purpose]` is inferred from the combination of all collected info
- Keep each line concise — version numbers and counts, not raw output

## Handling Existing Descriptions

Before updating, read current description via `opsctl get asset <id>`:
- Lines matching `[Tag] ...` pattern → auto-generated, will be overwritten
- Other lines → manual notes by user, preserve at the end

## Updating

```bash
opsctl update asset <id> --description "<generated description>"
```

## Batch Processing (group)

Use a session to avoid per-operation approval prompts:

```bash
SESSION=$(opsctl session start)

# For each asset in the group:
opsctl --session $SESSION exec <asset> -- "..."   # Phase 1 scan
opsctl --session $SESSION exec <asset> -- "..."   # Phase 2 if needed
opsctl --session $SESSION update asset <id> --description "..."

opsctl session end
```

## Summary Report

After all assets are processed, output a table:

```
| Asset        | ID | Status  | Key Info                          |
|--------------|----|---------|-----------------------------------|
| web-server   | 1  | Updated | Ubuntu 22.04, 4C/8G, nginx+redis |
| db-server    | 2  | Updated | CentOS 8, 8C/32G, PostgreSQL 15  |
| k8s-node-01  | 3  | Failed  | SSH connection timeout            |
```
