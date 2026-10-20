package opskat

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSettingsGetOmitsPasswords(t *testing.T) {
	Convey("Settings 只返回非密钥字段", t, func() {
		resetRegistries()
		th := NewTestHost()
		defer th.Close()
		th.SetSettings(map[string]string{"endpoint": "https://review.example.com", "model": "m"})

		got, err := Settings()
		So(err, ShouldBeNil)
		So(got["endpoint"], ShouldEqual, "https://review.example.com")
		So(got["model"], ShouldEqual, "m")
		_, has := got["token"]
		So(has, ShouldBeFalse)
	})
}

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
