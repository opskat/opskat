package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 返回一个空库。表由 RunMigrations 自己建——不要预建
// conversations：migration202603220001 用的是 CREATE TABLE IF NOT EXISTS，
// 预建会留下一张缺列的桩表，让后续迁移拿到错误的结构。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	return db
}

func TestMigrateConfigToDB(t *testing.T) {
	t.Run("从传入的 dataDir 读取 config.json 并导入", func(t *testing.T) {
		db := newTestDB(t)
		dataDir := t.TempDir()
		cfg := `{"ai_provider_type":"openai","ai_api_base":"https://api.example.com","ai_api_key":"sk-test","ai_model":"gpt-4"}`
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(cfg), 0o600); err != nil {
			t.Fatalf("准备 config.json 失败: %v", err)
		}

		if err := RunMigrations(db, dataDir); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var apiBase string
		if err := db.Raw("SELECT api_base FROM ai_providers WHERE type = ?", "openai").Scan(&apiBase).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if apiBase != "https://api.example.com" {
			t.Errorf("api_base = %q, 期望 %q", apiBase, "https://api.example.com")
		}
	})

	t.Run("dataDir 内无 config.json 时不导入任何记录", func(t *testing.T) {
		db := newTestDB(t)

		// 传入空目录：即便宿主机 ~/.config/opskat/config.json 存在，也不应被读取。
		if err := RunMigrations(db, t.TempDir()); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&count).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if count != 0 {
			t.Errorf("ai_providers 记录数 = %d, 期望 0（不得回退读平台目录）", count)
		}
	})

	t.Run("非 openai 类型不导入", func(t *testing.T) {
		db := newTestDB(t)
		dataDir := t.TempDir()
		cfg := `{"ai_provider_type":"local_cli","ai_api_base":"http://localhost"}`
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(cfg), 0o600); err != nil {
			t.Fatalf("准备 config.json 失败: %v", err)
		}

		if err := RunMigrations(db, dataDir); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&count).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if count != 0 {
			t.Errorf("ai_providers 记录数 = %d, 期望 0（local_cli 不再支持）", count)
		}
	})
}
