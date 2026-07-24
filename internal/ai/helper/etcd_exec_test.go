package helper

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// TestCanonicalizeEtcdCommand_NormalizesToPolicyForm 锁住 canonicalizer 的契约：
// 输出必须是策略层认得的形式，且对内置组 "get *" 仍然匹配。
func TestCanonicalizeEtcdCommand_NormalizesToPolicyForm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"get /app/config", "get /app/config"},
		{"GET /app/config", "get /app/config"},
		{"get /app/ --prefix", "get /app/ --prefix"},
		{"member list", "member list"},
		{"lease grant --ttl=3600", "lease grant --ttl=3600"},
		{"put /app/k 'hello world'", "put /app/k 'hello world'"},
	}
	for _, c := range cases {
		got, err := CanonicalizeEtcdCommand(&asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeEtcd}, c.in)
		if err != nil {
			t.Fatalf("CanonicalizeEtcdCommand(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("CanonicalizeEtcdCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanonicalizeEtcdCommand_RejectsUnsupportedOp(t *testing.T) {
	_, err := CanonicalizeEtcdCommand(&asset_entity.Asset{ID: 1}, "nonsense /x")
	if err == nil {
		t.Fatal("CanonicalizeEtcdCommand with unsupported op = nil error, want rejection")
	}
}
