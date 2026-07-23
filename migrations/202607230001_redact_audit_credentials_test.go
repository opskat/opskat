package migrations

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMigration202607230001RedactsExistingAuditCredentials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE audit_logs (id INTEGER PRIMARY KEY, request TEXT, result TEXT, command TEXT, error TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO audit_logs (id, request, result, command, error) VALUES (1, ?, ?, ?, ?)`,
		`{"config":{"host":"db.internal","password":"old-request-secret","kubeconfig":"old-kube-secret"}}`,
		`{"api_key":"old-result-secret"}`,
		`client --token old-command-secret`,
		`Authorization: Bearer old-error-secret`).Error; err != nil {
		t.Fatal(err)
	}

	if err := migration202607230001().Migrate(db); err != nil {
		t.Fatal(err)
	}

	var row struct{ Request, Result, Command, Error string }
	if err := db.Table("audit_logs").Where("id = ?", 1).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	for column, value := range map[string]string{"request": row.Request, "result": row.Result, "command": row.Command, "error": row.Error} {
		if strings.Contains(value, "old-") {
			t.Fatalf("migrated %s leaked credential plaintext: %s", column, value)
		}
	}
	if !strings.Contains(row.Request, "db.internal") {
		t.Fatalf("migration removed safe audit context: %s", row.Request)
	}
}
