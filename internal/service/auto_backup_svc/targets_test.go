package auto_backup_svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/service/backup_svc"
)

func dirNames(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func TestLocalTarget(t *testing.T) {
	Convey("本地文件夹位置", t, func() {
		dir := t.TempDir()
		target := localTarget{}
		settings := Settings{LocalDir: dir}
		at := time.Date(2026, 11, 17, 6, 28, 3, 0, time.UTC)
		So(os.WriteFile(filepath.Join(dir, "opskat-backup.encrypted.json"), []byte("manual"), 0o600), ShouldBeNil)
		So(os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("n"), 0o600), ShouldBeNil)

		Convey("未选文件夹时不可用", func() {
			So(target.Available(Settings{}), ShouldBeFalse)
			So(target.Available(settings), ShouldBeTrue)
		})

		Convey("写入带时间戳的文件，不留临时文件", func() {
			_, err := target.Write(context.Background(), WriteRequest{Content: []byte("enc"), At: at, Keep: 10, Settings: settings})
			So(err, ShouldBeNil)
			So(dirNames(t, dir), ShouldResemble, []string{
				"notes.txt",
				"opskat-backup-auto-20261117T062803Z.encrypted.json",
				"opskat-backup.encrypted.json",
			})
			got, _ := os.ReadFile(filepath.Join(dir, backup_svc.AutoBackupName(at))) //nolint:gosec // test temp dir
			So(string(got), ShouldEqual, "enc")
		})

		Convey("只保留最新 N 份自动备份，其他文件不动", func() {
			for i := 0; i < 4; i++ {
				_, err := target.Write(context.Background(), WriteRequest{Content: []byte(fmt.Sprint(i)), At: at.Add(time.Duration(i) * time.Hour), Keep: 2, Settings: settings})
				So(err, ShouldBeNil)
			}
			So(dirNames(t, dir), ShouldResemble, []string{
				"notes.txt",
				"opskat-backup-auto-20261117T082803Z.encrypted.json",
				"opskat-backup-auto-20261117T092803Z.encrypted.json",
				"opskat-backup.encrypted.json",
			})
		})

		Convey("文件夹不存在时报错且不创建", func() {
			missing := filepath.Join(dir, "unmounted")
			_, err := target.Write(context.Background(), WriteRequest{Content: []byte("enc"), At: at, Keep: 10, Settings: Settings{LocalDir: missing}})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "文件夹不存在")
			_, statErr := os.Stat(missing)
			So(os.IsNotExist(statErr), ShouldBeTrue)
		})
	})
}

func TestGistTarget(t *testing.T) {
	Convey("Gist 位置", t, func() {
		var calls []string
		target := gistTarget{
			token: func() (string, error) { return "tok", nil },
			upsert: func(token, id string, content []byte) (*backup_svc.GistInfo, error) {
				calls = append(calls, id)
				if id == "gone" {
					return nil, fmt.Errorf("wrap: %w", backup_svc.ErrGistNotFound)
				}
				if id == "" {
					return &backup_svc.GistInfo{ID: "new-id"}, nil
				}
				return &backup_svc.GistInfo{ID: id}, nil
			},
		}

		Convey("第一次创建并返回 ID，之后更新同一个", func() {
			ref, err := target.Write(context.Background(), WriteRequest{Content: []byte("x")})
			So(err, ShouldBeNil)
			So(ref, ShouldEqual, "new-id")
			ref, err = target.Write(context.Background(), WriteRequest{Content: []byte("x"), Ref: ref})
			So(err, ShouldBeNil)
			So(ref, ShouldEqual, "new-id")
			So(calls, ShouldResemble, []string{"", "new-id"})
		})

		Convey("记住的 Gist 被删除时重新创建", func() {
			ref, err := target.Write(context.Background(), WriteRequest{Content: []byte("x"), Ref: "gone"})
			So(err, ShouldBeNil)
			So(ref, ShouldEqual, "new-id")
			So(calls, ShouldResemble, []string{"gone", ""})
		})
	})
}
