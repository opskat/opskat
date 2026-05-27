package etcd_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in         string
		wantOp     string
		wantKey    string
		wantPrefix bool
		wantLimit  int64
		wantValue  string
		wantErr    bool
	}{
		{"get /config", "get", "/config", false, 0, "", false},
		{"get /config --prefix", "get", "/config", true, 0, "", false},
		{"get /config --prefix --limit=100", "get", "/config", true, 100, "", false},
		{"put /flags/x true", "put", "/flags/x", false, 0, "true", false},
		{"del /locks/a --prefix", "del", "/locks/a", true, 0, "", false},
		{"member list", "member_list", "", false, 0, "", false},
		{"endpoint status", "endpoint_status", "", false, 0, "", false},
		{"endpoint health", "endpoint_health", "", false, 0, "", false},
		{"lease list", "lease_list", "", false, 0, "", false},
		{"user list", "", "", false, 0, "", true}, // 未实现 user/role/txn/lease_ttl op
		{"role list", "", "", false, 0, "", true},
		{"txn", "", "", false, 0, "", true},
		{"lease ttl 0xabc", "", "", false, 0, "", true},
		{"GET /case", "get", "/case", false, 0, "", false},          // 大小写归一
		{"  get  /spaced  ", "get", "/spaced", false, 0, "", false}, // 空白容忍
		{"", "", "", false, 0, "", true},
		{"unknown-op /x", "", "", false, 0, "", true},
		{"put /k", "", "", false, 0, "", true},             // put 缺 value
		{"get /x --limit=abc", "", "", false, 0, "", true}, // limit 非数字
		{"get /x --unknown", "", "", false, 0, "", true},   // 未知 flag
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			req, err := ParseCommand(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOp, req.Op)
			assert.Equal(t, tc.wantKey, req.Key)
			assert.Equal(t, tc.wantPrefix, req.Prefix)
			assert.Equal(t, tc.wantLimit, req.Limit)
			assert.Equal(t, tc.wantValue, req.Value)
		})
	}
}

func TestParseCommand_PutMultiWordValue(t *testing.T) {
	req, err := ParseCommand(`put /msg hello world`)
	require.NoError(t, err)
	assert.Equal(t, "put", req.Op)
	assert.Equal(t, "/msg", req.Key)
	assert.Equal(t, "hello world", req.Value)
}

func TestParseCommand_LeaseHex(t *testing.T) {
	req, err := ParseCommand(`put /k v --lease=abc`)
	require.NoError(t, err)
	assert.Equal(t, int64(0xabc), req.LeaseID)
}

func TestParseCommand_Revision(t *testing.T) {
	req, err := ParseCommand(`get /k --revision=42`)
	require.NoError(t, err)
	assert.Equal(t, int64(42), req.Revision)
}
