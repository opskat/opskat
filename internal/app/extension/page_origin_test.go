package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/extension"
)

// A page's tool call that needs approval pops the shared in-app approval dialog,
// which names the extension whose page is asking. The name it shows is the one the
// user knows the extension by — its localized display name — falling back to the
// extension's name when the manifest declares none.
func TestPageDisplayName(t *testing.T) {
	Convey("pageDisplayName", t, func() {
		ext := &extension.Extension{
			Name:     "esverify",
			Manifest: &extension.Manifest{Name: "esverify", I18n: extension.ManifestI18n{DisplayName: "manifest.displayName"}},
			Locales: map[string]map[string]string{
				"en":    {"manifest.displayName": "ES Verify"},
				"zh-cn": {"manifest.displayName": "ES 校验"},
			},
		}

		Convey("resolves the display name in the UI language", func() {
			So(pageDisplayName(ext, "en"), ShouldEqual, "ES Verify")
			So(pageDisplayName(ext, "zh-cn"), ShouldEqual, "ES 校验")
		})

		Convey("falls back to the extension name when no display name is declared", func() {
			ext.Manifest.I18n.DisplayName = ""
			So(pageDisplayName(ext, "en"), ShouldEqual, "esverify")
		})
	})
}
