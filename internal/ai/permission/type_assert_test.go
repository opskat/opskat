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
		{"db", asset_entity.AssetTypeDatabase, true},
		{"mongo", asset_entity.AssetTypeMongoDB, true},
		{"redis", asset_entity.AssetTypeRedis, true},
		{"kubernetes", asset_entity.AssetTypeK8s, true},
		{"kube", asset_entity.AssetTypeK8s, true},
		// Driver names resolve to the database type too, so the opsctl batch prefix
		// (`mysql:prod-db:SELECT 1`) validates the same set of words --type accepts.
		// The driver constraint itself is enforced by AssertAssetType, not here.
		{"mysql", asset_entity.AssetTypeDatabase, true},
		{"postgres", asset_entity.AssetTypeDatabase, true},
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

// newDBAsset builds a database asset carrying a real DatabaseConfig, which is what the
// driver-level assertion reads back. Constructing it through SetDatabaseConfig (rather
// than hand-writing Config JSON) keeps the test honest about the serialization the
// production path actually parses.
func newDBAsset(t *testing.T, name string, driver asset_entity.DatabaseDriver) *asset_entity.Asset {
	t.Helper()
	a := &asset_entity.Asset{Name: name, Type: asset_entity.AssetTypeDatabase}
	if err := a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
		Driver: driver, Host: "10.0.0.1", Port: 3306, Username: "app",
	}); err != nil {
		t.Fatalf("SetDatabaseConfig: %v", err)
	}
	return a
}

// TestAssertAssetType_DriverAlias covers the whole point of admitting driver names as
// --type values: they must assert the *driver*, not just resolve to "database". A plain
// alias (mysql→database) would let type=mysql pass on a PostgreSQL asset, which turns the
// assertion into a lie exactly where it is supposed to catch a wrong dialect.
func TestAssertAssetType_DriverAlias(t *testing.T) {
	mysqlAsset := newDBAsset(t, "prod-db", asset_entity.DriverMySQL)
	pgAsset := newDBAsset(t, "analytics", asset_entity.DriverPostgreSQL)

	if err := AssertAssetType(mysqlAsset, "mysql"); err != nil {
		t.Fatalf("driver alias matching the asset's driver must pass, got %v", err)
	}
	if err := AssertAssetType(pgAsset, "postgres"); err != nil {
		t.Fatalf("spelling variant of the driver must pass, got %v", err)
	}
	// The canonical type name still skips the driver check — declaring "database" says
	// nothing about the dialect, so every database asset satisfies it.
	if err := AssertAssetType(pgAsset, "database"); err != nil {
		t.Fatalf("canonical type must not imply a driver, got %v", err)
	}

	err := AssertAssetType(pgAsset, "mysql")
	if err == nil {
		t.Fatal("driver alias must fail on an asset with a different driver")
	}
	// Same shape as the type-mismatch error: name both sides, echo what the caller
	// actually passed, point at help.
	for _, want := range []string{`"analytics"`, "driver=postgresql", "type=mysql", "help"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must contain %q", err.Error(), want)
		}
	}

	// A driver name on a non-database asset fails at the type layer, and must still echo
	// the declared word rather than its canonical resolution.
	redis := &asset_entity.Asset{Name: "cache-1", Type: asset_entity.AssetTypeRedis}
	err = AssertAssetType(redis, "mysql")
	if err == nil {
		t.Fatal("driver alias must fail on a non-database asset")
	}
	if !strings.Contains(err.Error(), "type=mysql") {
		t.Errorf("error %q must echo the declared value %q", err.Error(), "mysql")
	}
}

// TestDriverAliasStaysOutOfPermissionRegistry locks the seam between the two tables.
// permissionTypes is the source of truth for permission dispatch, approval labels and
// grant support; driver names are none of those things. Registering them there would make
// SupportsGrantApproval("mysql") report true, widening the grant contract to a name that
// is not an approval type.
func TestDriverAliasStaysOutOfPermissionRegistry(t *testing.T) {
	for _, name := range []string{"mysql", "postgres", "postgresql", "sqlite", "mssql"} {
		if SupportsGrantApproval(name) {
			t.Errorf("driver alias %q must not register as an approval type", name)
		}
		if got := ApprovalTypeFor(name); got != name {
			t.Errorf("ApprovalTypeFor(%q) = %q, want passthrough %q", name, got, name)
		}
	}
}

func TestCanonicalExecTypeFor(t *testing.T) {
	tests := []struct {
		name string
		want string
		ok   bool
	}{
		{name: "ssh", want: asset_entity.AssetTypeSSH, ok: true},
		{name: "exec", want: asset_entity.AssetTypeSSH, ok: true},
		{name: "database", want: asset_entity.AssetTypeDatabase, ok: true},
		{name: "sql", want: asset_entity.AssetTypeDatabase, ok: true},
		{name: "mongodb", want: asset_entity.AssetTypeMongoDB, ok: true},
		{name: "mongo", want: asset_entity.AssetTypeMongoDB, ok: true},
		{name: "etcd", want: asset_entity.AssetTypeEtcd, ok: true},
		{name: "kafka", want: asset_entity.AssetTypeKafka, ok: true},
		{name: "k8s", want: asset_entity.AssetTypeK8s, ok: true},
		{name: "kubernetes", want: asset_entity.AssetTypeK8s, ok: true},
		{name: "mysql", want: asset_entity.AssetTypeDatabase, ok: true},
		{name: "serial", want: asset_entity.AssetTypeSerial, ok: true},
		{name: "cp", ok: false},
		{name: "rdp", ok: false},
		{name: "bogus", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CanonicalExecTypeFor(tt.name)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("CanonicalExecTypeFor(%q) = (%q, %v), want (%q, %v)", tt.name, got, ok, tt.want, tt.ok)
			}
		})
	}
}
