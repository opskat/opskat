package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestSettingsSchemaValidation(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"endpoint": map[string]any{"type": "string", "format": "endpoint"},
			"token":    map[string]any{"type": "string", "format": "password"},
			"model":    map[string]any{"type": "string"},
		},
		"required": []any{"endpoint"},
	}

	Convey("settingsSchema 只接受声明过的字段，并认出密钥与地址", t, func() {
		So(PasswordFieldsFromSchema(schema), ShouldResemble, []string{"token"})
		So(EndpointFieldsFromSchema(schema), ShouldResemble, []string{"endpoint"})

		err := ValidateSettings(schema, map[string]any{"endpoint": "https://review.example.com", "model": "m"})
		So(err, ShouldBeNil)

		err = ValidateSettings(schema, map[string]any{"model": "m"})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "endpoint")

		err = ValidateSettings(schema, map[string]any{"endpoint": "https://review.example.com", "extra": "x"})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "extra")
	})
}

func TestSubstituteSettingHeaders(t *testing.T) {
	secrets := map[string]string{"token": "s3cret"}
	endpoints := []string{"https://review.example.com"}

	Convey("{{setting.x}} 只替换发往声明地址的请求头", t, func() {
		headers := map[string]string{"Authorization": "Bearer {{setting.token}}", "X-Plain": "ok"}
		got, err := SubstituteSettingHeaders("https://review.example.com/v1", headers, secrets, endpoints)
		So(err, ShouldBeNil)
		So(got["Authorization"], ShouldEqual, "Bearer s3cret")
		So(got["X-Plain"], ShouldEqual, "ok")

		_, err = SubstituteSettingHeaders("https://evil.example.com/v1", headers, secrets, endpoints)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "setting.token")

		plain := map[string]string{"X-Plain": "ok"}
		got, err = SubstituteSettingHeaders("https://evil.example.com/v1", plain, secrets, endpoints)
		So(err, ShouldBeNil)
		So(got["X-Plain"], ShouldEqual, "ok")
	})
}

func TestParseDescriptorSettingsSchema(t *testing.T) {
	raw := desc(`"settingsSchema":{"type":"object","properties":{"endpoint":{"type":"string","format":"endpoint"}}}`)
	d, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.SettingsSchema == nil {
		t.Fatal("settingsSchema was dropped")
	}
	if got := EndpointFieldsFromSchema(d.SettingsSchema); len(got) != 1 || got[0] != "endpoint" {
		t.Fatalf("endpoint fields: %v", got)
	}
}

func TestLocalizedTranslatesSettingsSchema(t *testing.T) {
	m := &Manifest{SettingsSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"token": map[string]any{"type": "string", "format": "password", "title": "settings.token"},
		},
	}}
	got := m.Localized(func(key string) string {
		if key == "settings.token" {
			return "API key"
		}
		return key
	})
	title := got.SettingsSchema["properties"].(map[string]any)["token"].(map[string]any)["title"]
	if title != "API key" {
		t.Fatalf("settings title not localized: %v", title)
	}
	if m.SettingsSchema["properties"].(map[string]any)["token"].(map[string]any)["title"] != "settings.token" {
		t.Fatal("Localized must not modify the original schema")
	}
}
