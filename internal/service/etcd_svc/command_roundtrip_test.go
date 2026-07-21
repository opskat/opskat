package etcd_svc

import "testing"

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
		{Op: "put", Key: "/app/config", Value: ""}, // IMPORTANT-2: etcd 允许空 value
		{Op: "del", Key: "/app/", Prefix: true},
		// 空 key 是一个正常请求，不是"没给 key"：`etcdctl get "" --prefix` 就是列出
		// 整个 key 空间，HandleExecEtcd 在模型只给 op=get/prefix=true 时正是这样构造
		// （ArgString 缺省返回 ""），dispatchGet/dispatchDel 也接受 Key == ""。
		// key 必须被渲染成显式空词 ''，否则读回来会变成"缺少 key"的报错。
		{Op: "get", Prefix: true},
		{Op: "get", Limit: 10},
		{Op: "del", Prefix: true},
		{Op: "put", Value: "v"},
		{Op: "lease_grant", Args: map[string]any{"ttl": int64(3600)}},
		{Op: "lease_revoke", LeaseID: 0x694d5c0f},
		{Op: "lease_list"},
		{Op: "member_list"},
		{Op: "endpoint_status"},
		{Op: "endpoint_health"},
	}

	for _, want := range reqs {
		rendered := FormatCommand(want)
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
		wantTTL, wantHasTTL := ttlOf(want)
		gotTTL, gotHasTTL := ttlOf(got)
		if wantHasTTL != gotHasTTL || wantTTL != gotTTL {
			t.Fatalf("[%s] ttl = (%v,%v), want (%v,%v)", rendered, gotTTL, gotHasTTL, wantTTL, wantHasTTL)
		}
	}
}

// ttlOf 返回 Args["ttl"] 的原始值与是否存在，不做类型收窄——早前版本直接对
// v.(int64) 做类型断言，把断言失败也当成"不存在"处理，这让 (0,false) == (0,false)
// 对"absent"和"present but wrong type"两种完全不同的情况都成立，测试形同虚设：
// FormatCommand 真把 float64(3600)/int(3600) 悄悄丢掉，这里也测不出来。
func ttlOf(r *ExecRequest) (any, bool) {
	if r.Args == nil {
		return nil, false
	}
	v, ok := r.Args["ttl"]
	return v, ok
}

// TestParseCommand_RejectsUnknownFlag 锁住"未知 flag 必须报错"——静默忽略会让
// 模型以为参数生效了。
func TestParseCommand_RejectsUnknownFlag(t *testing.T) {
	if _, err := ParseCommand("get /app --nonsense=1"); err == nil {
		t.Fatal("ParseCommand with unknown flag = nil error, want rejection")
	}
}

// TestFormatCommand_TTLTypeHandling 锁住 IMPORTANT-1：ttl 的来源不止 ParseCommand
// 自己产的规范 int64——ExecRequest 的字段注释写明它"既给 IPC 用，也给 Dispatch
// 用"，Args 可能装着 JSON 解码给的 float64，或者调用方手写的无类型 int 字面量。
// 两者都要被 FormatCommand 认出来并渲染 --ttl=；int64(0) 是"显式给了 0"和"完全
// 没给"的边界，ttl != 0 的判断会把前者也吞掉，必须覆盖到。
func TestFormatCommand_TTLTypeHandling(t *testing.T) {
	tests := []struct {
		name string
		ttl  any
		want string
	}{
		{"int64", int64(3600), "lease grant --ttl=3600"},
		{"float64（JSON 解码 tool 参数的默认数值类型）", float64(3600), "lease grant --ttl=3600"},
		{"未类型化 int 字面量", 3600, "lease grant --ttl=3600"},
		{"int64(0)：显式给了 0，ttl!=0 会丢掉，视为未设置", int64(0), "lease grant"},
		{"字符串：坏数据，丢弃但不能崩", "3600", "lease grant"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatCommand(&ExecRequest{Op: "lease_grant", Args: map[string]any{"ttl": tc.ttl}})
			if got != tc.want {
				t.Fatalf("FormatCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseCommand_DashPrefixedKeyFailsClosed 锁住 CRITICAL-2：一个以 "-" 开头的
// key/value 一旦经过 FormatCommand 再 ParseCommand，无法在不改变命令串规范形状的
// 前提下正确还原（canonical 形状本身就是策略匹配用的字符串，不能为了这个改）。
// 唯一安全的选择是拒绝到底：把"看似 --prefix flag、实际是调用方想要的 key"这种
// 歧义变成一次响亮的报错，而不是让 del 静默变成删空整个 key 空间。
func TestParseCommand_DashPrefixedKeyFailsClosed(t *testing.T) {
	t.Run("del 的 key 是字面量 --prefix：渲染后必须报错，不能悄悄变成 prefix delete 整个 keyspace", func(t *testing.T) {
		rendered := FormatCommand(&ExecRequest{Op: "del", Key: "--prefix"})
		if rendered != "del --prefix" {
			t.Fatalf("FormatCommand() = %q, want %q", rendered, "del --prefix")
		}
		got, err := ParseCommand(rendered)
		if err == nil {
			t.Fatalf("ParseCommand(%q) = %+v, want error (got would otherwise prefix-delete the whole keyspace)", rendered, got)
		}
	})

	t.Run("put 的 value 是字面量 --prefix：渲染后必须报错，不能悄悄丢掉 value", func(t *testing.T) {
		rendered := FormatCommand(&ExecRequest{Op: "put", Key: "/k", Value: "--prefix"})
		if rendered != "put /k --prefix" {
			t.Fatalf("FormatCommand() = %q, want %q", rendered, "put /k --prefix")
		}
		if _, err := ParseCommand(rendered); err == nil {
			t.Fatalf("ParseCommand(%q) = nil error, want rejection", rendered)
		}
	})

	t.Run("显式引号也救不了——cmdline.Words 在 flag 判断之前就已经剥掉引号", func(t *testing.T) {
		if _, err := ParseCommand("del '--prefix'"); err == nil {
			t.Fatal("ParseCommand(\"del '--prefix'\") = nil error, want rejection")
		}
	})

	t.Run("bare del/get 不带 key 也要拒绝，不是只在看到 -- token 时才拒绝", func(t *testing.T) {
		if _, err := ParseCommand("del"); err == nil {
			t.Fatal("ParseCommand(\"del\") = nil error, want rejection (del requires a key)")
		}
		if _, err := ParseCommand("get"); err == nil {
			t.Fatal("ParseCommand(\"get\") = nil error, want rejection (get requires a key)")
		}
	})
}
