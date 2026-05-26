# etcd Demo Fixture

A local etcd server for manually verifying the OpsKat etcd asset feature (KV browser, query panel, AI tool).

## Quick start

### Option A: Embed etcd (built-in, no external install)

From repo root:

```bash
go test -tags etcdfixture -run TestEtcdFixtureUp ./tests/fixtures/etcd_demo/ -timeout 32m
```

The test starts an `embed.Etcd` listening on `127.0.0.1:12379` and blocks for 30 minutes. Use Ctrl+C to stop.

### Option B: Existing etcd

Any reachable etcd (e.g. `127.0.0.1:2379`) works. Skip the fixture test in that case.

## Add the asset in OpsKat

1. Launch the OpsKat desktop app (`make dev`).
2. **Add asset** → choose type **etcd**.
3. Endpoints: `127.0.0.1:12379` (Option A) or your real endpoints.
4. Leave auth empty (the embed fixture has RBAC disabled).
5. **Save**.

## Verification checklist (acceptance for Phase E)

Tick each item after manually exercising it in the running app.

### Connection
- [ ] Asset list shows the new etcd asset with status green.
- [ ] Open the asset → connects within ~1s.
- [ ] Adding a bad endpoint (`badhost:2379`) → save fails / connect fails with a readable error.

### KV browser
- [ ] Tree pane lists root `/` keys immediately.
- [ ] Click a directory → lazy-loads children, only one network call per first expand.
- [ ] Click a leaf → right pane shows value + metadata (modRev / version / lease).
- [ ] Limit truncation: insert >1000 keys under a prefix; tree shows the "+N hidden" indicator.

### Query panel
- [ ] `get /config` → table shows matching rows.
- [ ] `get / --prefix --limit=10` → table shows up to 10 keys.
- [ ] `put /flags/example true` → **ConfirmDialog** appears with the command preview; "Cancel" skips execution; "Confirm" writes and the result table shows count=1.
- [ ] `del /flags/example` → also gated by ConfirmDialog.
- [ ] `del /flags/ --prefix` → ConfirmDialog should preview the destructive scope.

### AI tool (`exec_etcd`)
- [ ] Open AI tab; ask "list the keys under /config". The AI calls `exec_etcd` with op=get prefix=true.
- [ ] AI calls `member list` (op=member_list). Result shows cluster members.
- [ ] AI tries `member remove …` → permission policy denies (default `BuiltinEtcdDangerousDeny`).

### TLS / mTLS / SSH-tunnel (optional, if you have the setup)
- [ ] Create an asset against a TLS-only etcd (custom CA). Connection succeeds.
- [ ] Create an mTLS asset (client cert + key) — connection succeeds.
- [ ] Create an asset with `ssh_tunnel_id` pointing at a bastion SSH asset; first endpoint reaches via tunnel.

### Audit / logging
- [ ] In a separate terminal: `tail -f ~/Library/Application\ Support/OpsKat/opskat.log`. Confirm three-state log entries (`etcd exec start` / `etcd exec end`) appear for each operation.
- [ ] No password / TLS key paths appear in the log lines.

## Notes

- `make test` runs Go unit tests including the `etcd_svc` and `connpool` packages but NOT the integration `embed.Etcd` test (it's behind `-tags integration`).
- `make test-integration` (if defined) or `go test -tags integration ./internal/connpool/` will run the in-process etcd dial test.
- The fixture test in this directory uses a **different** build tag (`etcdfixture`) because it's a long-running server, not a short-lived test.
