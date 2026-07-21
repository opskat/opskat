package permission

import (
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestCanonicalTypeFor(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"ssh", asset_entity.AssetTypeSSH, true},
		{"exec", asset_entity.AssetTypeSSH, true},     // 协议别名
		{"sql", asset_entity.AssetTypeDatabase, true}, // opsctl batch 前缀沿用
		{"database", asset_entity.AssetTypeDatabase, true},
		{"mongo", asset_entity.AssetTypeMongoDB, true},
		{"redis", asset_entity.AssetTypeRedis, true},
		{"nonsense", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := CanonicalTypeFor(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("CanonicalTypeFor(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAssertAssetType(t *testing.T) {
	redis := &asset_entity.Asset{Name: "cache-1", Type: asset_entity.AssetTypeRedis}
	db := &asset_entity.Asset{Name: "prod-db", Type: asset_entity.AssetTypeDatabase}

	if err := AssertAssetType(redis, ""); err != nil {
		t.Fatalf("empty declaration must skip the assertion, got %v", err)
	}
	if err := AssertAssetType(redis, "redis"); err != nil {
		t.Fatalf("matching type must pass, got %v", err)
	}
	if err := AssertAssetType(db, "sql"); err != nil {
		t.Fatalf("protocol alias must resolve to the canonical type, got %v", err)
	}

	err := AssertAssetType(redis, "database")
	if err == nil {
		t.Fatal("mismatched type must fail")
	}
	// 报错必须点名双方，并指向 help——与 execGuidance 同格式（spec §4.6）。
	for _, want := range []string{`"cache-1"`, "type=redis", "type=database", "help"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}

	err = AssertAssetType(redis, "nonsense")
	if err == nil {
		t.Fatal("unknown type name must fail rather than silently pass")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error %q must name the unknown type", err.Error())
	}
}

// TestAssertAssetType_EchoesDeclaredAliasNotCanonical locks a review finding on the
// mismatch branch: it must echo what the caller actually passed (declared), not the
// canonical value that declared resolves to. "sql" and "database" share a canonical form
// ("database" — see registerPermissionType's alias registration in type_registry.go), so
// TestAssertAssetType's own `AssertAssetType(redis, "database")` case above can't tell
// the two apart: declared == canonical there. A model that calls exec/batch_exec with
// type="sql" on a non-database asset must be told "you passed type=sql", not
// "you passed type=database" — it never typed that word.
func TestAssertAssetType_EchoesDeclaredAliasNotCanonical(t *testing.T) {
	ssh := &asset_entity.Asset{Name: "web-1", Type: asset_entity.AssetTypeSSH}

	err := AssertAssetType(ssh, "sql")
	if err == nil {
		t.Fatal("mismatched alias must fail")
	}
	if !strings.Contains(err.Error(), "type=sql") {
		t.Errorf("error %q must echo the declared value %q", err.Error(), "sql")
	}
	if strings.Contains(err.Error(), "type=database") {
		t.Errorf("error %q must not echo the alias's canonical resolution instead of what the caller passed", err.Error())
	}
}
