package redis_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommandHistory(t *testing.T) {
	h := NewCommandHistory(2)

	h.Add(CommandHistoryEntry{AssetID: 1, DB: 0, Command: "GET a", Timestamp: 1})
	h.Add(CommandHistoryEntry{AssetID: 1, DB: 0, Command: "GET b", Timestamp: 2})
	h.Add(CommandHistoryEntry{AssetID: 2, DB: 0, Command: "GET c", Timestamp: 3})

	all := h.List(0, 0)
	assert.Len(t, all, 2)
	assert.Equal(t, "GET c", all[0].Command)
	assert.Equal(t, "GET b", all[1].Command)

	assetOnly := h.List(1, 10)
	assert.Len(t, assetOnly, 1)
	assert.Equal(t, "GET b", assetOnly[0].Command)
}

func TestFormatCommandForHistoryPreservesWriteValues(t *testing.T) {
	tests := []struct {
		name string
		args []any
		want string
	}{
		{name: "SET", args: []any{"SET", "my key", "value with spaces"}, want: `SET "my key" "value with spaces"`},
		{name: "SETEX", args: []any{"SETEX", "key", "10", "secret value"}, want: `SETEX key 10 "secret value"`},
		{name: "GETSET", args: []any{"GETSET", "key", "new value"}, want: `GETSET key "new value"`},
		{name: "HSET", args: []any{"HSET", "session", "token", "secret"}, want: `HSET session token secret`},
		{name: "HMSET", args: []any{"HMSET", "user", "name", "alice", "pass", "p@ss"}, want: `HMSET user name alice pass p@ss`},
		{name: "MSET", args: []any{"MSET", "k1", "v1", "k2", "v 2"}, want: `MSET k1 v1 k2 "v 2"`},
		{name: "LPUSH", args: []any{"LPUSH", "queue", "payload", "p2"}, want: `LPUSH queue payload p2`},
		{name: "RPUSH", args: []any{"RPUSH", "queue", "payload"}, want: `RPUSH queue payload`},
		{name: "SADD", args: []any{"SADD", "set", "member1", "member 2"}, want: `SADD set member1 "member 2"`},
		{name: "ZADD", args: []any{"ZADD", "zset", "1.5", "member"}, want: `ZADD zset 1.5 member`},
		{name: "XADD", args: []any{"XADD", "events", "*", "token", "secret", "data", "hello world"}, want: `XADD events * token secret data "hello world"`},
		{name: "GET read only", args: []any{"GET", "session:token"}, want: "GET session:token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatCommandForHistory(tc.args))
		})
	}
}

func TestFormatCommandForHistoryQuotesArguments(t *testing.T) {
	got := formatCommandForHistory([]any{"SET", "my key", "value with spaces"})
	assert.Equal(t, `SET "my key" "value with spaces"`, got)
}
