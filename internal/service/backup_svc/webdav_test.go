package backup_svc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestWebDAVBackups(t *testing.T) {
	Convey("WebDAV backups", t, func() {
		var putPath string
		var putBody []byte
		var putUser, putPass string
		var mkcolPath string
		var propfindPath string
		var propfindUser, propfindPass, propfindDepth string
		var propfindAuthOK bool
		var getPath, getUser, getPass string
		var getAuthOK bool

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "PROPFIND":
				propfindPath = r.URL.Path
				propfindUser, propfindPass, propfindAuthOK = r.BasicAuth()
				propfindDepth = r.Header.Get("Depth")
				w.WriteHeader(207)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/dav/opskat/</d:href>
    <d:propstat>
      <d:prop><d:resourcetype><d:collection /></d:resourcetype></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/opskat/opskat-backup.encrypted.json</d:href>
    <d:propstat>
      <d:prop>
        <d:getcontentlength>12</d:getcontentlength>
        <d:getlastmodified>Sat, 25 Apr 2026 10:00:00 GMT</d:getlastmodified>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/opskat/notes.txt</d:href>
    <d:propstat>
      <d:prop><d:getcontentlength>5</d:getcontentlength></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`))
			case "PUT":
				putPath = r.URL.Path
				putUser, putPass, _ = r.BasicAuth()
				putBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusCreated)
			case "MKCOL":
				mkcolPath = r.URL.Path
				w.WriteHeader(http.StatusCreated)
			case "GET":
				getPath = r.URL.Path
				getUser, getPass, getAuthOK = r.BasicAuth()
				_, _ = w.Write([]byte("backup-bytes"))
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		}))
		defer srv.Close()

		cfg := WebDAVConfig{
			URL:      srv.URL + "/dav/opskat/",
			Username: "dav-user",
			Password: "dav-pass",
		}

		Convey("uploads the canonical encrypted backup file", func() {
			info, err := CreateOrUpdateWebDAVBackup(cfg, []byte("backup-bytes"))
			So(err, ShouldBeNil)
			So(info.Name, ShouldEqual, "opskat-backup.encrypted.json")
			So(info.Size, ShouldEqual, len("backup-bytes"))
			So(mkcolPath, ShouldEqual, "/dav/opskat/")
			So(putPath, ShouldEqual, "/dav/opskat/opskat-backup.encrypted.json")
			So(string(putBody), ShouldEqual, "backup-bytes")
			So(putUser, ShouldEqual, "dav-user")
			So(putPass, ShouldEqual, "dav-pass")
		})

		Convey("lists only OpsKat backup files", func() {
			backups, err := ListWebDAVBackups(cfg)
			So(err, ShouldBeNil)
			So(backups, ShouldHaveLength, 1)
			So(propfindAuthOK, ShouldBeTrue)
			So(propfindUser, ShouldEqual, "dav-user")
			So(propfindPass, ShouldEqual, "dav-pass")
			So(propfindDepth, ShouldEqual, "1")
			So(backups[0].Name, ShouldEqual, "opskat-backup.encrypted.json")
			So(backups[0].Size, ShouldEqual, int64(12))
			So(backups[0].UpdatedAt, ShouldEqual, "Sat, 25 Apr 2026 10:00:00 GMT")
		})

		Convey("uses opskat as the default storage directory", func() {
			rootCfg := cfg
			rootCfg.URL = srv.URL + "/dav/"

			info, err := CreateOrUpdateWebDAVBackup(rootCfg, []byte("backup-bytes"))
			So(err, ShouldBeNil)
			So(info.Path, ShouldEndWith, "/dav/opskat/opskat-backup.encrypted.json")
			So(mkcolPath, ShouldEqual, "/dav/opskat/")
			So(putPath, ShouldEqual, "/dav/opskat/opskat-backup.encrypted.json")

			_, err = ListWebDAVBackups(rootCfg)
			So(err, ShouldBeNil)
			So(propfindPath, ShouldEqual, "/dav/opskat/")
		})

		Convey("downloads a selected backup file", func() {
			content, err := GetWebDAVBackupContent(cfg, "opskat-backup.encrypted.json")
			So(err, ShouldBeNil)
			So(string(content), ShouldEqual, "backup-bytes")
			So(getPath, ShouldEqual, "/dav/opskat/opskat-backup.encrypted.json")
			So(getAuthOK, ShouldBeTrue)
			So(getUser, ShouldEqual, "dav-user")
			So(getPass, ShouldEqual, "dav-pass")
		})
	})
}
