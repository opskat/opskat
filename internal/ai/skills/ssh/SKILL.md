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
