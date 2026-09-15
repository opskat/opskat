# Windows local IPC

`internal/localipc` owns the transport under the desktop↔`opsctl` approval
endpoint: a Windows named pipe, a Unix-domain socket on macOS/Linux. This
reference owns that transport's contract and how to diagnose it. The process
topology and the approval/audit flow that endpoint carries stay in
[Architecture §8](../ARCHITECTURE.md#8-opsctl--the-multi-process-flow).

## Why named pipes on Windows

A user hit Winsock **10022 / InvalidArgument** at the **connect** stage on the
local IPC endpoints in their `%LOCALAPPDATA%\opskat` directory, in a normal user
context. An independent .NET AF_UNIX listener failed the same way in that
directory, while the same operations succeeded on another drive and through a
Junction to it. Moving the data directory restored them.

That establishes a directory-dependent AF_UNIX failure and an application
dependency on it. It does **not** identify the underlying cause — path length,
ACLs, antivirus, filesystem corruption and a Windows defect are all still open,
and the failure has not been reproduced on any other machine or in CI. **This
change removes the dependency rather than fixing a diagnosed root cause.**

Moving the data directory or relocating the sockets would keep the AF_UNIX
dependency and add migration/discovery concerns. Loopback TCP would need port
discovery and network-facing security handling. Named pipes are the platform's
native local IPC and fit the existing `net.Conn` protocols unchanged.

## Transport contract

`internal/localipc` owns `Listen`, `Dial` and `DialContext`; every listener and
every client connection site goes through it. The existing `SocketPath` helpers
stay logical identifiers — on Windows they are not filesystem addresses.

**Windows.** `github.com/Microsoft/go-winio` byte-stream named pipes. The pipe
name hashes the current process user's SID, the final normalized NT path of the
opened *directory*, and the endpoint basename. `GetFinalPathNameByHandle`
resolves the real target including drive aliases and short names; the legacy
`.sock` entry is never opened or resolved, since it may be a stale reparse
point. So directory aliases (Junctions included) reach the same endpoint while
separate directories and separate services do not, and long or non-ASCII paths
never reach AF_UNIX. Directory resolution errors are returned — there is no
common-name or TCP fallback.

The pipe carries a protected DACL granting only the current user's SID.
go-winio rejects remote clients, reserves the first instance exclusively, and
opens client handles with anonymous security quality of service. Application
token checks are unchanged and still run on top. This does **not** isolate
mutually untrusted processes running as the same Windows user.

**macOS/Linux.** Unchanged addresses, stream protocols and 0600 permissions.
Stale-socket recovery is now shared and narrower: only a socket node whose probe
reports refusal or not-found may be removed. Permission errors, regular files
and symlinks are never treated as stale sockets. Failing to set owner-only
permissions closes the listener and returns an error.

**Both.** Connection establishment is bounded at two seconds; waiting for a
human approval decision and streaming execution get no new deadline. Failures
are returned and logged with the wrapped cause, and requests are never replayed
automatically after a broken connection. Policy/grant/TTY/refusal behavior and
the SSH client's direct-connection fallback are untouched.

**Upgrading Windows requires both binaries at once, then a desktop restart.**
Old binaries speak AF_UNIX and cannot reach the new pipe; no dual listener or
silent downgrade exists. Existing `.sock` entries are left in place. Process
exit releases the pipe handles and the next process binds a fresh listener. No
data migration is involved.

## Verification

The `Local IPC` workflow runs the real OS transport on Windows and macOS, which
the ubuntu-only `Go Test` job does not cover. It exercises approval
request/response under valid, missing and invalid tokens, user allow and deny,
refusal after shutdown, exclusive startup and restart; pool authentication
before dispatch plus the real client handshake and stdout/stderr/exit framing;
large bidirectional streams, dial cancellation and blocked-accept shutdown; on
Windows the Junction/case aliases, service and directory isolation, the
installed pipe DACL, a long Unicode directory with an untouched legacy endpoint,
the busy-pipe deadline and rebinding after abrupt process death; on Unix stale
socket recovery, 0600 permissions and preservation of regular files/symlinks;
and the CLI approval probe choosing desktop vs structured refusal.

None of that reproduces or disproves the original AppData failure, and none of
it asserts that a native Wails dialog was clicked or a production audit row
written. **Confirm that end-to-end flow on the affected Windows machine after
upgrading both binaries** — without undoing the data directory migration that
currently keeps it working.

### Manual AF_UNIX probe

[`scripts/Test-WindowsUnixSocket.ps1`](../../scripts/Test-WindowsUnixSocket.ps1)
(Windows, PowerShell 7) isolates AF_UNIX from OpsKat entirely: .NET sockets
only, no assets, credentials or database. It emits JSON Lines per stage —
socket, bind, listen, connect, accept, transfer, cleanup — with the underlying
`SocketException` native code, and stops at the first failing stage. It is not
in CI; run it by hand when diagnosing a report like the one above.

Run it in the **same user context and elevation** as the application, and hold
the identity, ASCII basename and full pathname length constant across cases so
one variable changes per run:

```powershell
$id = [guid]::NewGuid().ToString('N').Substring(0,8)
$plain = Join-Path $env:LOCALAPPDATA "ipc-a-$id"
$junction = Join-Path $env:LOCALAPPDATA "ipc-j-$id"
$target = "D:\ipc-d-$id"
$target += 'x' * ($plain.Length - $target.Length)
New-Item -ItemType Directory $plain,$target | Out-Null
New-Item -ItemType Junction -Path $junction -Target $target | Out-Null

./scripts/Test-WindowsUnixSocket.ps1 `
  -BindDirectory $plain,$target,$junction,$junction,$target `
  -ConnectDirectory $plain,$target,$junction,$target,$junction |
  Set-Content ./ipc-probe.jsonl
```

That compares ordinary AppData, another drive, and both alias/target directions
at equal textual lengths. Report long-path *bind* failures separately from a
connect-stage 10022 — they are different findings. Ancestor Junctions may not
show in the final directory's attributes, so keep the directory setup alongside
the results; the probe records attributes but does not interpret them. It
removes only the unique endpoint it bound, never an existing application
endpoint or data directory.
