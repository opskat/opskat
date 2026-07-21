---
name: kafka
description: "Manage Kafka topics, consumer groups, ACLs, schemas, connectors and messages via exec, using a family + verb + target command syntax."
---

# Kafka assets

## Command syntax

`<family> <verb> [target] [--flags]` — `family` is the resource kind, `verb` is the
operation, and `target` names the topic / consumer group / subject / connector.

Every example below is literally executable as written. Optional flags are not
marked with brackets; each useful combination is spelled out on its own line.

### cluster

- `cluster overview`
- `cluster brokers`
- `cluster broker-config --broker-id=1`
- `cluster cluster-configs`

### topic

- `topic list`
- `topic list --search=orders --include-internal --page=1 --page-size=50`
- `topic describe orders`
- `topic create orders --partitions=3 --replication-factor=2`
- `topic create orders --partitions=3 --replication-factor=2 --configs='{"retention.ms":"604800000"}'`
- `topic update-config orders --config-updates='[{"name":"retention.ms","value":"1000"}]'`
- `topic increase-partitions orders --partition-count=6`
- `topic delete-records orders --records='[{"partition":0,"offset":100}]'`
- `topic delete orders`

### consumer-group

- `consumer-group list`
- `consumer-group describe mygroup`
- `consumer-group reset-offset mygroup --topic=orders --mode=earliest`
- `consumer-group reset-offset mygroup --topic=orders --mode=offset --offset=1000 --partitions='[0,1]'`
- `consumer-group reset-offset mygroup --topic=orders --mode=timestamp --timestamp-millis=1700000000000`
- `consumer-group delete mygroup`

### acl

- `acl list`
- `acl list --resource-type=topic --resource-name=orders --principal=User:alice`
- `acl create --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow`
- `acl create --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow --pattern-type=prefixed --host=10.0.0.7`
- `acl delete --resource-type=topic --resource-name=orders --principal=User:alice --acl-operation=read --permission=allow --host='*'`

### schema

- `schema list-subjects`
- `schema list-versions mysubject`
- `schema describe mysubject`
- `schema describe mysubject --version=3`
- `schema check-compatibility mysubject --schema='{"type":"record","name":"X","fields":[]}'`
- `schema register mysubject --schema='{"type":"record","name":"X","fields":[]}' --schema-type=AVRO`
- `schema delete mysubject`
- `schema delete mysubject --permanent`

### connect

- `connect list-clusters`
- `connect list-connectors`
- `connect list-connectors --cluster=main`
- `connect describe myconnector`
- `connect create myconnector --config='{"connector.class":"io.confluent.connect.jdbc.JdbcSourceConnector","tasks.max":"1"}'`
- `connect update-config myconnector --config='{"tasks.max":"2"}'`
- `connect pause myconnector`
- `connect resume myconnector`
- `connect restart myconnector`
- `connect restart myconnector --include-tasks --only-failed`
- `connect delete myconnector`

### message

- `message browse orders`
- `message browse orders --partition=0 --start-mode=newest --limit=100 --decode-mode=json`
- `message browse orders --start-mode=offset --offset=1000 --limit=20`
- `message browse orders --start-mode=timestamp --timestamp-millis=1700000000000 --limit=20`
- `message inspect orders --partition=0 --offset=1000`
- `message produce orders --value='{"a":1}'`
- `message produce orders --key=k1 --value='{"a":1}' --value-encoding=text --headers='[{"key":"trace-id","value":"abc"}]'`

## Required flags

Omitting one of these does not default it — the command is rejected before it runs:

- `topic create`: `--partitions`, `--replication-factor`
- `topic update-config`: `--config-updates`
- `topic increase-partitions`: `--partition-count`
- `topic delete-records`: `--records`
- `consumer-group reset-offset`: `--topic`
- `acl create`: `--resource-type`, `--principal`, `--acl-operation`, `--permission`
- `acl delete`: the four above **plus** `--host` (deletion matches an exact ACL entry,
  and the host is part of that entry — use `--host='*'` to remove an any-host rule)
- `schema check-compatibility`, `schema register`: `--schema`
- `connect create`, `connect update-config`: `--config` (a non-empty JSON object)
- `message inspect`: `--partition` **and** `--offset` (unlike `message browse`,
  which defaults both)

## Flag values

- Enumerations are fixed; anything else is rejected by the broker call:
  - `consumer-group reset-offset --mode`: `earliest`, `latest`, `offset`, `timestamp`
    (default `latest`). `offset` mode needs `--offset`, `timestamp` mode needs
    `--timestamp-millis`.
  - `message browse --start-mode`: `newest`, `oldest`, `offset`, `timestamp`
    (default `newest`). Note it is `newest`/`oldest` here, **not** `latest`/`earliest`
    — those two spellings belong to `reset-offset --mode`.
  - `--decode-mode` (browse/inspect): `text`, `json`, `hex`, `base64` (default `text`).
  - `--key-encoding` / `--value-encoding` (produce): `text`, `json`, `hex`, `base64`
    (default `text`); `hex`/`base64` decode the value before producing it.
  - `acl --permission`: `allow` or `deny`. `acl --pattern-type`: `literal` (default)
    or `prefixed`. `acl --resource-type`: `topic`, `group`, `cluster`,
    `transactional_id`, `delegation_token`.
- Numeric flags (`--limit`, `--offset`, `--page`, `--partitions`, …) must be plain
  decimal integers: `1000`, not `1,000`, `1_000`, `1e3` or `3.0`.
- Boolean flags (`--include-internal`, `--permanent`, `--include-tasks`,
  `--only-failed`) may be written bare to mean true (`--permanent`) or with an
  explicit `--permanent=true` / `--permanent=false`. No other value is accepted.
- `--config-updates` entries use `name`, not `key`:
  `'[{"name":"retention.ms","value":"1000"}]'`. `--headers` entries do use
  `key`/`value`.
- `--partitions` (reset-offset) is a JSON array of partition numbers (`'[0,1]'`);
  omitting it resets every partition of the topic.
- Flags not shown in the examples above, all optional: `--references` on
  `schema register` / `schema check-compatibility`
  (`'[{"name":"a.avsc","subject":"a","version":1}]'`); `--max-bytes` and
  `--max-wait-millis` on `message browse` / `message inspect`; `--partition` and
  `--timestamp-millis` on `message produce`; `--page` and `--page-size` on
  `acl list`.

## Notes

- Flag names use hyphens (`--replication-factor`); underscores are accepted as the
  same flag, but do not pass both spellings in one command.
- Unknown flags are rejected rather than ignored, so a typo such as
  `--paritions=3` fails instead of silently creating a 0-partition topic.
- Single-quote any value containing JSON, spaces, braces or `*`. The command line
  is shell-tokenized but never shell-executed: `$`, `|`, `>`, `&` produce an error
  rather than expanding.
- Exactly one target is allowed, and only for verbs that take one:
  `topic list orders` is an error, not "describe orders".
- A target containing whitespace cannot be used at all — permission rules match
  two space-separated tokens, so such a name could never be authorized.
- `acl` commands take no target; the resource is named by `--resource-name`, which
  is required unless `--resource-type=cluster`. `acl create` without `--host`
  applies to every host.
- `cluster broker-config` without `--broker-id` describes broker `0`; take the id
  from `cluster brokers` first.
- `connect` commands need `--cluster` only when the asset has more than one
  Kafka Connect cluster configured; with a single cluster it is inferred.
- Approval is granted at `<action> <resource>` granularity, e.g. `topic.delete orders`,
  `message.write orders`, `consumer_group.offset.write mygroup`. Cluster-wide and
  ACL actions carry `*` as the resource (`acl.write *`), so approving one ACL change
  approves both granting and revoking.
- The `scope` parameter is not used by Kafka assets; the target position names the
  resource.
