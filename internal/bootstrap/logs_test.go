package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	. "github.com/smartystreets/goconvey/convey"
)

func TestResolvedDataDir(t *testing.T) {
	Convey("Init 用 Options.DataDir 时 GetLogsDir 跟随覆盖目录", t, func() {
		// Init 会写包级 resolvedDataDir（指向即将被删除的 TempDir），测试后还原，
		// 避免同包后续测试经 ResolvedDataDir()/GetLogsDir() 读到失效路径。
		prev := resolvedDataDir
		defer func() { resolvedDataDir = prev }()

		// 显式提供 MasterKey，避免 Init 触碰 Keychain；TempDir 隔离真实数据目录。
		tmp := t.TempDir()
		err := Init(context.Background(), Options{DataDir: tmp, MasterKey: "test-master-key"})
		So(err, ShouldBeNil)
		// 在 TempDir 清理之前关掉 Init 打开的数据库：Windows 上打开着的 SQLite 文件删不掉。
		sqlDB, err := db.Default().DB()
		So(err, ShouldBeNil)
		t.Cleanup(func() { _ = sqlDB.Close() })

		So(ResolvedDataDir(), ShouldEqual, tmp)
		So(GetLogsDir(), ShouldEqual, filepath.Join(tmp, "logs"))
	})
}
