package opskat

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestDescribeSettingsSchema(t *testing.T) {
	Convey("describe 带上 SettingsSchema 声明的 schema", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "x"})
		AssetType[struct {
			Endpoint string `json:"endpoint"`
		}]("x")
		SettingsSchema[struct {
			Endpoint string `json:"endpoint" format:"endpoint"`
			Token    string `json:"token,omitempty" format:"password"`
		}]()

		desc := decodeDescribe(t)
		schema := desc["settingsSchema"].(map[string]any)
		props := schema["properties"].(map[string]any)
		So(props["endpoint"].(map[string]any)["format"], ShouldEqual, "endpoint")
		So(props["token"].(map[string]any)["format"], ShouldEqual, "password")
	})
}
