---
name: notebook
description: Per-asset notebook stored inside OpsKat. Use it to keep short operational notes (runbook steps, incident findings, connection quirks) next to the asset they belong to.
---

# Notebook

A `notebook` asset is a named collection of short text notes stored inside OpsKat
itself. Nothing leaves the machine: the notes live in the app's database, keyed by
the notebook name the asset is configured with. Two assets configured with the same
notebook name share their notes; that is the intended way to point several
environments at one set of runbooks.

## When to use it

Reach for a note when a fact is worth keeping but too small to be a document: the
exact failover command for a cluster, why a host needs a non-standard port, what an
alert meant last time. Read the notebook before improvising on an asset the user has
notes for.

## Calling the tools

The notebook a call works on is the asset you ran `exec` against; no tool takes an
asset id.

```
exec <asset> -- note_list
exec <asset> -- note_list --prefix=runbook/
exec <asset> -- note_get --key=runbook/failover
exec <asset> -- note_put --key=runbook/failover --content="1. drain 2. promote" --tags=runbook,db
exec <asset> -- note_delete --key=scratch
```

Note keys are paths by convention, not by rule: `runbook/failover` and
`incident/2026-03-11` sort and filter well with `--prefix`.

## What the permissions mean

- Reading (`note_list`, `note_get`) is granted to a new notebook asset, so listing
  and reading run without asking.
- Writing (`note_put`) asks the user unless they have granted the notebook's write
  group; expect a confirmation prompt the first time.
- Deleting (`note_delete`) is refused by a group granted to every new notebook
  asset. If the user wants a note gone, say so and let them revoke that group
  themselves — retrying will not help.

Overwriting an existing key replaces its content outright; there is no history. When
the user's intent is "add to a note", read it first and put back the merged body.
