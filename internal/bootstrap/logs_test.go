package bootstrap

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestResolvedDataDir(t *testing.T) {
	Convey("Init 用 Options.DataDir 时 GetLogsDir 跟随覆盖目录", t, func() {
		// 显式提供 MasterKey，避免 Init 触碰 Keychain；TempDir 隔离真实数据目录。
		tmp := t.TempDir()
		err := Init(context.Background(), Options{DataDir: tmp, MasterKey: "test-master-key"})
		So(err, ShouldBeNil)

		So(ResolvedDataDir(), ShouldEqual, tmp)
		So(GetLogsDir(), ShouldEqual, filepath.Join(tmp, "logs"))
	})
}
