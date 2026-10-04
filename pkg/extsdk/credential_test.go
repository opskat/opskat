package opskat

import (
	"encoding/json"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type credConfig struct {
	Endpoint string      `json:"endpoint" format:"endpoint"`
	Password Credential  `json:"password,omitempty" title:"config.password.title"`
	Token    *Credential `json:"token,omitempty"`
}

func TestCredentialDecodesWhatTheHostServes(t *testing.T) {
	Convey("A Credential config field", t, func() {
		decode := func(raw string) credConfig {
			var cfg credConfig
			So(json.Unmarshal([]byte(raw), &cfg), ShouldBeNil)
			return cfg
		}

		Convey("decodes the opaque handle served without credentials:read, holding no plaintext", func() {
			cfg := decode(`{"endpoint":"https://es:9200","password":{"__credential_handle":"cred_0011223344556677"}}`)
			So(cfg.Password.IsSet(), ShouldBeTrue)
			plain, err := cfg.Password.Plaintext()
			So(errors.Is(err, ErrCredentialWithheld), ShouldBeTrue)
			So(plain, ShouldEqual, "")
		})

		Convey("decodes the plaintext served with credentials:read", func() {
			cfg := decode(`{"endpoint":"https://es:9200","password":"s3cr3t"}`)
			So(cfg.Password.IsSet(), ShouldBeTrue)
			plain, err := cfg.Password.Plaintext()
			So(err, ShouldBeNil)
			So(plain, ShouldEqual, "s3cr3t")
		})

		Convey("an absent, null or empty field is unset and reads as empty", func() {
			for _, raw := range []string{`{}`, `{"password":null}`, `{"password":""}`} {
				cfg := decode(raw)
				So(cfg.Password.IsSet(), ShouldBeFalse)
				plain, err := cfg.Password.Plaintext()
				So(err, ShouldBeNil)
				So(plain, ShouldEqual, "")
			}
		})

		Convey("a pointer field is nil when absent and decodes when present", func() {
			So(decode(`{}`).Token, ShouldBeNil)
			cfg := decode(`{"token":{"__credential_handle":"cred_1"}}`)
			So(cfg.Token, ShouldNotBeNil)
			So(cfg.Token.IsSet(), ShouldBeTrue)
		})

		Convey("any other shape is an error rather than a silently empty secret", func() {
			for _, raw := range []string{`{"password":42}`, `{"password":{"other":"x"}}`, `{"password":{"__credential_handle":""}}`, `{"password":["a"]}`} {
				var cfg credConfig
				So(json.Unmarshal([]byte(raw), &cfg), ShouldNotBeNil)
			}
		})

		Convey("re-encoding yields what was decoded", func() {
			for _, raw := range []string{
				`{"endpoint":"e","password":"s3cr3t","token":{"__credential_handle":"cred_1"}}`,
				`{"endpoint":"e","password":null}`,
			} {
				cfg := decode(raw)
				out, err := json.Marshal(cfg)
				So(err, ShouldBeNil)
				So(string(out), ShouldEqual, raw)
			}
		})
	})
}

func TestCredentialFieldSchema(t *testing.T) {
	Convey("A Credential field in an asset config", t, func() {
		resetRegistries()

		Convey("is reported as a string secret without a format tag", func() {
			AssetType[credConfig]("demo")
			props := assetTypes[0].schema["properties"].(map[string]any)
			So(props["password"], ShouldResemble, map[string]any{"type": "string", "format": "password", "title": "config.password.title"})
			So(props["token"], ShouldResemble, map[string]any{"type": "string", "format": "password"})
			So(assetTypes[0].schema["required"], ShouldResemble, []string{"endpoint"})
		})

		Convey("with a contradicting format tag panics at registration", func() {
			So(func() {
				AssetType[struct {
					Key Credential `json:"key" format:"endpoint"`
				}]("demo")
			}, ShouldPanicWith, `opskat: asset type "demo" config: field Key is a Credential, which is always format "password", but is tagged format "endpoint"`)
		})

		Convey("is refused as a tool argument", func() {
			So(func() {
				Tool("login", func(_ *ToolContext, _ struct {
					Key Credential `json:"key"`
				}) (any, error) {
					return nil, nil
				})
			}, ShouldPanicWith, `opskat: tool "login" arguments: field Key is a Credential, which is only an asset config field`)
		})
	})
}
