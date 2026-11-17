package backup_svc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// fakeWebDAV 是一个最小的内存 WebDAV 目录：PUT / MOVE / DELETE / PROPFIND / MKCOL。
type fakeWebDAV struct {
	mu       sync.Mutex
	files    map[string][]byte // 目录内文件名 → 内容
	failMove bool
	methods  []string
	moveDest string
	moveOver string
}

func newFakeWebDAV() *fakeWebDAV { return &fakeWebDAV{files: map[string][]byte{}} }

func (f *fakeWebDAV) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.files))
	for n := range f.files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *fakeWebDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, r.Method)
	name, _ := url.PathUnescape(path.Base(r.URL.EscapedPath()))
	switch r.Method {
	case "MKCOL":
		w.WriteHeader(http.StatusMethodNotAllowed)
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		f.files[name] = body
		w.WriteHeader(http.StatusCreated)
	case "MOVE":
		f.moveDest = r.Header.Get("Destination")
		f.moveOver = r.Header.Get("Overwrite")
		if f.failMove {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		dst, err := url.Parse(f.moveDest)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		dstName, _ := url.PathUnescape(path.Base(dst.EscapedPath()))
		f.files[dstName] = f.files[name]
		delete(f.files, name)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		delete(f.files, name)
		w.WriteHeader(http.StatusNoContent)
	case "PROPFIND":
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">`)
		b.WriteString(`<d:response><d:href>/dav/opskat/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
		for n, c := range f.files {
			fmt.Fprintf(&b, `<d:response><d:href>/dav/opskat/%s</d:href><d:propstat><d:prop><d:getcontentlength>%d</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, url.PathEscape(n), len(c))
		}
		b.WriteString(`</d:multistatus>`)
		w.WriteHeader(207)
		_, _ = w.Write([]byte(b.String()))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestAutoBackupName(t *testing.T) {
	Convey("自动备份文件名", t, func() {
		at := time.Date(2026, 11, 17, 14, 28, 3, 0, time.FixedZone("CST", 8*3600))
		name := AutoBackupName(at)
		So(name, ShouldEqual, "opskat-backup-auto-20261117T062803Z.encrypted.json")
		So(isOpsKatBackupName(name), ShouldBeTrue)

		parsed, ok := ParseAutoBackupName(name)
		So(ok, ShouldBeTrue)
		So(parsed.Equal(at), ShouldBeTrue)

		for _, other := range []string{
			"opskat-backup.encrypted.json",
			"opskat-backup-auto-garbage.encrypted.json",
			".opskat-backup-auto-20261117T062803Z.encrypted.json.tmp",
			"opskat-backup-auto-20261117T062803Z.json",
		} {
			_, ok := ParseAutoBackupName(other)
			So(ok, ShouldBeFalse)
		}
	})

	Convey("超出保留份数的自动备份按时间从旧到新选出，非自动备份文件从不入选", t, func() {
		names := []string{
			"opskat-backup.encrypted.json",
			"opskat-backup-auto-20261117T060000Z.encrypted.json",
			"notes.txt",
			"opskat-backup-auto-20261115T060000Z.encrypted.json",
			"opskat-backup-auto-20261116T060000Z.encrypted.json",
		}
		So(AutoBackupsToPrune(names, 2), ShouldResemble, []string{"opskat-backup-auto-20261115T060000Z.encrypted.json"})
		So(AutoBackupsToPrune(names, 3), ShouldBeEmpty)
	})
}

func TestUploadWebDAVAutoBackup(t *testing.T) {
	Convey("WebDAV 自动备份写入", t, func() {
		dav := newFakeWebDAV()
		dav.files["opskat-backup.encrypted.json"] = []byte("manual")
		srv := httptest.NewServer(dav)
		defer srv.Close()
		cfg := WebDAVConfig{URL: srv.URL + "/dav/opskat/", AuthType: WebDAVAuthNone}
		at := time.Date(2026, 11, 17, 6, 28, 3, 0, time.UTC)

		Convey("先上传临时文件再 MOVE 成正式名，目录里只剩正式文件", func() {
			info, err := UploadWebDAVAutoBackup(cfg, []byte("v1"), at)
			So(err, ShouldBeNil)
			So(info.Name, ShouldEqual, "opskat-backup-auto-20261117T062803Z.encrypted.json")
			So(dav.methods, ShouldContain, "MOVE")
			So(dav.moveOver, ShouldEqual, "T")
			So(dav.moveDest, ShouldEndWith, "/dav/opskat/opskat-backup-auto-20261117T062803Z.encrypted.json")
			So(dav.names(), ShouldResemble, []string{
				"opskat-backup-auto-20261117T062803Z.encrypted.json",
				"opskat-backup.encrypted.json",
			})
			So(string(dav.files[info.Name]), ShouldEqual, "v1")
		})

		Convey("MOVE 失败时删掉临时文件、已有文件不受影响，并返回错误", func() {
			dav.files["opskat-backup-auto-20261116T000000Z.encrypted.json"] = []byte("old")
			dav.failMove = true
			_, err := UploadWebDAVAutoBackup(cfg, []byte("v2"), at)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "502")
			So(dav.names(), ShouldResemble, []string{
				"opskat-backup-auto-20261116T000000Z.encrypted.json",
				"opskat-backup.encrypted.json",
			})
			So(string(dav.files["opskat-backup-auto-20261116T000000Z.encrypted.json"]), ShouldEqual, "old")
		})

		Convey("清理只删除超出份数的旧自动备份，手动备份保留", func() {
			for _, n := range []string{
				"opskat-backup-auto-20261114T000000Z.encrypted.json",
				"opskat-backup-auto-20261115T000000Z.encrypted.json",
				"opskat-backup-auto-20261116T000000Z.encrypted.json",
			} {
				dav.files[n] = []byte("x")
			}
			So(PruneWebDAVAutoBackups(cfg, 2), ShouldBeNil)
			So(dav.names(), ShouldResemble, []string{
				"opskat-backup-auto-20261115T000000Z.encrypted.json",
				"opskat-backup-auto-20261116T000000Z.encrypted.json",
				"opskat-backup.encrypted.json",
			})
		})

		Convey("列表把手动备份排在前，自动备份按时间倒序并带时间", func() {
			dav.files["opskat-backup-auto-20261115T000000Z.encrypted.json"] = []byte("x")
			dav.files["opskat-backup-auto-20261116T000000Z.encrypted.json"] = []byte("x")
			list, err := ListWebDAVBackups(cfg)
			So(err, ShouldBeNil)
			So(list, ShouldHaveLength, 3)
			So(list[0].Name, ShouldEqual, "opskat-backup.encrypted.json")
			So(list[0].Auto, ShouldBeFalse)
			So(list[1].Name, ShouldEqual, "opskat-backup-auto-20261116T000000Z.encrypted.json")
			So(list[1].Auto, ShouldBeTrue)
			So(list[1].CreatedAt, ShouldEqual, "2026-11-16T00:00:00Z")
			So(list[2].Name, ShouldEqual, "opskat-backup-auto-20261115T000000Z.encrypted.json")
		})
	})
}

func TestCreateOrUpdateAutoGist(t *testing.T) {
	Convey("自动备份 Gist", t, func() {
		Convey("新建时为私密 Gist、描述以 OpsKat Auto Backup 开头、文件名与手动备份相同", func() {
			var body map[string]any
			withTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				w.WriteHeader(http.StatusCreated)
				writeJSON(w, map[string]string{"id": "auto-id"})
			}), func() {
				info, err := CreateOrUpdateAutoGist("tok", "", []byte("enc"))
				So(err, ShouldBeNil)
				So(info.ID, ShouldEqual, "auto-id")
				So(body["public"], ShouldEqual, false)
				So(body["description"], ShouldStartWith, "OpsKat Auto Backup - ")
				files := body["files"].(map[string]any)
				So(files, ShouldContainKey, gistBackupFilename)
			})
		})

		Convey("记住的 Gist 已被删除时返回 ErrGistNotFound", func() {
			withTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				writeBytes(w, []byte(`{"message":"Not Found"}`))
			}), func() {
				_, err := CreateOrUpdateAutoGist("tok", "gone", []byte("enc"))
				So(errors.Is(err, ErrGistNotFound), ShouldBeTrue)
			})
		})

		Convey("401 带上登录失效标记", func() {
			withTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}), func() {
				_, err := CreateOrUpdateAutoGist("tok", "id", []byte("enc"))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, githubTokenInvalidMarker)
			})
		})
	})
}
