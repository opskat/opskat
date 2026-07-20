package etcd_svc

import "testing"

// formatForTest 复制 helper.FormatEtcdCommand 的调用形态。放在本包是为了避免
// etcd_svc -> helper 的反向导入；helper 侧有一个断言两者一致的测试（见 Task 2 Step 5）。
func formatForTest(req *ExecRequest) string { return FormatCommand(req) }

func TestParseFormat_RoundTrip(t *testing.T) {
	reqs := []*ExecRequest{
		{Op: "get", Key: "/app/config"},
		{Op: "get", Key: "/app/", Prefix: true},
		{Op: "get", Key: "/app/config", Limit: 10},
		{Op: "get", Key: "/app/config", Revision: 42},
		{Op: "get", Key: "/app/", Prefix: true, Limit: 5, Revision: 7},
		{Op: "put", Key: "/app/config", Value: "hello"},
		{Op: "put", Key: "/app/config", Value: "hello world"},
		{Op: "put", Key: "/app/config", Value: `{"a": 1}`},
		{Op: "put", Key: "/app/config", Value: "v", LeaseID: 0x694d5c0f},
		{Op: "del", Key: "/app/", Prefix: true},
		{Op: "lease_grant", Args: map[string]any{"ttl": int64(3600)}},
		{Op: "lease_revoke", LeaseID: 0x694d5c0f},
		{Op: "lease_list"},
		{Op: "member_list"},
		{Op: "endpoint_status"},
		{Op: "endpoint_health"},
	}

	for _, want := range reqs {
		rendered := formatForTest(want)
		got, err := ParseCommand(rendered)
		if err != nil {
			t.Fatalf("ParseCommand(%q) unexpected error: %v", rendered, err)
		}
		if got.Op != want.Op {
			t.Fatalf("[%s] Op = %q, want %q", rendered, got.Op, want.Op)
		}
		if got.Key != want.Key {
			t.Fatalf("[%s] Key = %q, want %q", rendered, got.Key, want.Key)
		}
		if got.Value != want.Value {
			t.Fatalf("[%s] Value = %q, want %q", rendered, got.Value, want.Value)
		}
		if got.Prefix != want.Prefix {
			t.Fatalf("[%s] Prefix = %v, want %v", rendered, got.Prefix, want.Prefix)
		}
		if got.Limit != want.Limit {
			t.Fatalf("[%s] Limit = %d, want %d", rendered, got.Limit, want.Limit)
		}
		if got.Revision != want.Revision {
			t.Fatalf("[%s] Revision = %d, want %d", rendered, got.Revision, want.Revision)
		}
		if got.LeaseID != want.LeaseID {
			t.Fatalf("[%s] LeaseID = %d, want %d", rendered, got.LeaseID, want.LeaseID)
		}
		wantTTL, hasTTL := ttlOf(want)
		gotTTL, gotHasTTL := ttlOf(got)
		if hasTTL != gotHasTTL || wantTTL != gotTTL {
			t.Fatalf("[%s] ttl = (%d,%v), want (%d,%v)", rendered, gotTTL, gotHasTTL, wantTTL, hasTTL)
		}
	}
}

func ttlOf(r *ExecRequest) (int64, bool) {
	if r.Args == nil {
		return 0, false
	}
	v, ok := r.Args["ttl"]
	if !ok {
		return 0, false
	}
	n, ok := v.(int64)
	return n, ok
}

// TestParseCommand_RejectsUnknownFlag 锁住"未知 flag 必须报错"——静默忽略会让
// 模型以为参数生效了。
func TestParseCommand_RejectsUnknownFlag(t *testing.T) {
	if _, err := ParseCommand("get /app --nonsense=1"); err == nil {
		t.Fatal("ParseCommand with unknown flag = nil error, want rejection")
	}
}
