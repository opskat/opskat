package extension

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

// The rules a describe() answer is judged by belong to the host's contribution
// points (internal/extreg, snippet_svc) and are tested there. What stays here is
// the runtime's half: decoding the wire shape and merging it onto the manifest.

// baseDescriptor is one asset type and the policy face it is checked under.
const baseDescriptor = `"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policy":{"type":"x"}`

// desc is the base descriptor plus extra contributes entries.
func desc(extra string) []byte {
	if extra != "" {
		extra = "," + extra
	}
	return describeJSON(baseDescriptor + extra)
}

// describeJSON is a describe() answer contributing exactly the given entries.
func describeJSON(contributes string) []byte {
	return []byte(`{"contributes":{` + contributes + `}}`)
}

func TestApplyDescriptor(t *testing.T) {
	Convey("Merging describe() onto the manifest", t, func() {
		m, err := ParseManifest([]byte(`{"name":"oss","version":"1.0.0","hostABI":"1.0",` +
			`"backend":{"runtime":"wasm","binary":"main.wasm"},` +
			`"permissions":{"credentials":"read"}}`))
		So(err, ShouldBeNil)

		d, err := ParseDescriptor([]byte(`{"icon":"cloud-storage",` +
			`"i18n":{"displayName":"manifest.displayName","description":"manifest.description"},` +
			`"contributes":{` + baseDescriptor + `,"pages":{"entry":"frontend/index.js","pages":[{"id":"browser","slot":"asset.connect","i18n":{"name":"pages.browser.name"},"component":"BrowserPage"}]},` +
			`"tools":[` +
			`{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{}}},` +
			`{"name":"delete_object","policyAction":"delete","parameters":{"type":"object","properties":{}}},` +
			`{"name":"get_object","policyAction":"list","parameters":{"type":"object","properties":{}}}]}}`))
		So(err, ShouldBeNil)

		m.apply(d)

		Convey("the security contract is untouched", func() {
			So(m.Name, ShouldEqual, "oss")
			So(m.Permissions.Credentials, ShouldEqual, extmanifest.CredentialAccessRead)
			So(m.Backend.Binary, ShouldEqual, "main.wasm")
		})

		Convey("the functional face comes from the guest", func() {
			So(m.Icon, ShouldEqual, "cloud-storage")
			So(m.I18n.DisplayName, ShouldEqual, "manifest.displayName")
			So(len(m.AssetTypes), ShouldEqual, 1)
			So(len(m.Tools), ShouldEqual, 3)
			So(m.Frontend.Pages[0].Slot, ShouldEqual, "asset.connect")
		})

		Convey("the action set is derived from the tools, not declared twice", func() {
			So(m.Policies.Actions, ShouldResemble, []string{"delete", "list"})
		})

		Convey("the raw contributions stay with it for the contribution points", func() {
			v := newExtensionView(m, nil, nil, "", "")
			So(v.Keys(), ShouldResemble, []string{"assetTypes", "pages", "policy", "tools"})
			So(string(v.Contribution("policy")), ShouldEqual, `{"type":"x"}`)
		})
	})
}

// TestDescribeWireShape: describe() answers icon, i18n and contributes keyed by
// contribution point; the host merges it onto the manifest in the shape the
// frontend has always read.
func TestDescribeWireShape(t *testing.T) {
	Convey("A describe() answer in the contributes shape", t, func() {
		payload := `{"icon":"cloud","i18n":{"displayName":"d","description":"x"},"contributes":{` +
			`"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
			`"tools":[{"name":"list","policyAction":"list","parameters":{"type":"object","properties":{}}}],` +
			`"policy":{"type":"x","groups":[{"id":"ext:x:ro","i18n":{"name":"g","description":"gd"},"policy":{"allow_list":["list"]}}],"default":["ext:x:ro"]},` +
			`"snippets":{"categories":[{"id":"xcat","assetType":"x","i18n":{"name":"c"}}],"seed":[{"key":"s","name":"s","category":"xcat","content":"ls"}]},` +
			`"pages":{"entry":"frontend/index.js","pages":[{"id":"p","slot":"asset.connect","i18n":{"name":"pn"},"component":"P"}]},` +
			`"databaseDrivers":[` + driverDescriptorJSON + `]}}`
		d, err := ParseDescriptor([]byte(payload))
		So(err, ShouldBeNil)
		m := &Manifest{}
		m.apply(d)

		Convey("is read into the merged manifest", func() {
			So(m.Icon, ShouldEqual, "cloud")
			So(m.I18n.DisplayName, ShouldEqual, "d")
			So(m.AssetTypes, ShouldHaveLength, 1)
			So(m.Tools[0].Name, ShouldEqual, "list")
			So(m.Policies.Type, ShouldEqual, "x")
			So(m.Policies.Actions, ShouldResemble, []string{"list"})
			So(m.Policies.Default, ShouldResemble, []string{"ext:x:ro"})
			So(m.Snippets.Categories[0].ID, ShouldEqual, "xcat")
			So(m.Snippets.Seed[0].Key, ShouldEqual, "s")
			So(m.Frontend.Entry, ShouldEqual, "frontend/index.js")
			So(m.Frontend.Pages[0].Component, ShouldEqual, "P")
			So(string(m.DatabaseDrivers[0].ID), ShouldEqual, "mysql-ext")
		})

		Convey("and handed to the frontend in the merged shape it already reads", func() {
			raw, err := json.Marshal(m)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(raw, &out), ShouldBeNil)
			for _, key := range []string{"icon", "i18n", "assetTypes", "tools", "policies", "frontend", "snippets", "databaseDrivers"} {
				So(out, ShouldContainKey, key)
			}
			So(out["policies"], ShouldContainKey, "actions")
			So(out["frontend"], ShouldResemble, map[string]any{
				"entry": "frontend/index.js", "styles": "",
				"pages": []any{map[string]any{"id": "p", "slot": "asset.connect", "i18n": map[string]any{"name": "pn"}, "component": "P"}},
			})
		})
	})

	Convey("The flat pre-1.0 shape is refused rather than read as an empty describe", t, func() {
		_, err := ParseDescriptor([]byte(`{` + baseDescriptor + `}`))
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "assetTypes")
	})
}

func TestParseDescriptorToolTimeout(t *testing.T) {
	tool := func(timeout string) []byte {
		return desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}` + timeout + `}]`)
	}
	Convey("A tool's declared call timeout is read off its declaration", t, func() {
		Convey("undeclared means the host default", func() {
			d, err := ParseDescriptor(tool(""))
			So(err, ShouldBeNil)
			So(d.Tools[0].Timeout(), ShouldEqual, time.Duration(0))
		})

		Convey("a declaration is kept", func() {
			d, err := ParseDescriptor(tool(`,"timeoutMs":600000`))
			So(err, ShouldBeNil)
			So(d.Tools[0].Timeout(), ShouldEqual, 10*time.Minute)
		})
	})
}

// 连接配置区（隧道 / 代理链 / TLS）由宿主拥有，资产类型只声明支持哪几项；
// 声明里出现宿主不认识的项要在解码时拒绝，而不是默默忽略成"不生效"。
func TestParseDescriptorConnection(t *testing.T) {
	Convey("An asset type declares which host-owned connection settings it supports", t, func() {
		withConnection := func(conn string) []byte {
			return describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string","format":"endpoint"}}},` +
				`"connection":` + conn + `}],"policy":{"type":"x"}`)
		}

		Convey("a declared subset is kept", func() {
			d, err := ParseDescriptor(withConnection(`{"sshTunnel":true}`))
			So(err, ShouldBeNil)
			So(d.AssetTypes[0].Connection, ShouldResemble, &ConnectionDef{SSHTunnel: true})
		})

		Convey("no declaration means no connection settings", func() {
			d, err := ParseDescriptor(desc(""))
			So(err, ShouldBeNil)
			So(d.AssetTypes[0].Connection, ShouldBeNil)
		})

		Convey("an unknown connection item is refused", func() {
			_, err := ParseDescriptor(withConnection(`{"sshTunnel":true,"vpn":true}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"vpn"`)
		})
	})
}

// 凭据注入的 auth 绑定读进宿主的 AuthDef，由宿主在请求 endpoint 时渲染；绑定的规则
// 由资产类型贡献点在加载时校验（internal/extreg）。
func TestParseDescriptorAuth(t *testing.T) {
	Convey("An asset type's auth bindings are read for the host to inject", t, func() {
		withAuth := func(auth string) []byte {
			return describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{` +
				`"endpoint":{"type":"string","format":"endpoint"},"authType":{"type":"string"},` +
				`"username":{"type":"string"},"password":{"type":"string","format":"password"}}},` +
				`"auth":` + auth + `}],"policy":{"type":"x"}`)
		}

		Convey("groups selected by a config field, over header / query / basic, are kept", func() {
			d, err := ParseDescriptor(withAuth(`{"selector":"authType","groups":[` +
				`{"when":"basic","bindings":[{"in":"basic","value":"{{username}}:{{password}}"}]},` +
				`{"when":"token","bindings":[{"in":"header","name":"Authorization","value":"ApiKey {{base64(username, \":\", password)}}"},` +
				`{"in":"query","name":"sig","value":"{{ password }}"}]}]}`))
			So(err, ShouldBeNil)
			auth := d.AssetTypes[0].Auth
			So(auth, ShouldNotBeNil)
			So(auth.Selector, ShouldEqual, "authType")
			So(auth.Groups, ShouldHaveLength, 2)
			So(auth.Groups[1].Bindings[0], ShouldResemble, AuthBinding{In: "header", Name: "Authorization", Value: `ApiKey {{base64(username, ":", password)}}`})
		})

		Convey("no declaration means no injection", func() {
			d, err := ParseDescriptor(desc(""))
			So(err, ShouldBeNil)
			So(d.AssetTypes[0].Auth, ShouldBeNil)
		})

		Convey("an unknown key is refused rather than ignored", func() {
			_, err := ParseDescriptor(withAuth(`{"groups":[{"bindings":[{"in":"header","header":"Authorization","value":"{{password}}"}]}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"header"`)
		})
	})

	Convey("A binding's template names the config fields it injects", t, func() {
		fields, err := AuthTemplateFields(`ApiKey {{base64(username, ":", password)}} {{ token }}`)
		So(err, ShouldBeNil)
		So(fields, ShouldResemble, []string{"username", "password", "token"})

		_, err = AuthTemplateFields(`{{password`)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "unclosed")
	})
}

// 一个工具要么声明固定动作（policyAction），要么声明按参数分类时可能给出的动作集合
// （policyActions，SDK 的 PolicyFunc）。两种声明都并入 policies.actions——宿主拿它核对
// guest 在 check_policy 里给出的动作。
func TestParseDescriptorPolicyActionSets(t *testing.T) {
	Convey("A classified tool's action set feeds policies.actions alongside fixed actions", t, func() {
		m, err := ParseManifest([]byte(`{"name":"x","version":"1.0.0","hostABI":"1.0",` +
			`"backend":{"runtime":"wasm","binary":"main.wasm"}}`))
		So(err, ShouldBeNil)
		d, err := ParseDescriptor(desc(`"tools":[` +
			`{"name":"search","policyActions":["search","index.read"],"parameters":{"type":"object","properties":{}}},` +
			`{"name":"list","policyAction":"list","parameters":{"type":"object","properties":{}}}]`))
		So(err, ShouldBeNil)
		m.apply(d)
		So(m.Policies.Actions, ShouldResemble, []string{"index.read", "list", "search"})
	})
}

func TestParseDescriptorFileParams(t *testing.T) {
	Convey("A tool's file-readable string parameters are kept on the tool", t, func() {
		d, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"body":{"type":"string"}}},"fileParams":["body"]}]`))
		So(err, ShouldBeNil)
		So(d.Tools[0].FileParams, ShouldResemble, []string{"body"})
	})
}

func TestLocalizeConfigSchemaEnumLabels(t *testing.T) {
	Convey("localizing a config schema translates the option labels", t, func() {
		out := localizeConfigSchema(map[string]any{"properties": map[string]any{
			"auth": map[string]any{"enum": []any{"a"}, "enumLabels": []any{"k.a"}, "default": "a"},
		}}, func(k string) string { return "T:" + k })
		auth := out["properties"].(map[string]any)["auth"].(map[string]any)
		So(auth["enumLabels"], ShouldResemble, []any{"T:k.a"})
		So(auth["enum"], ShouldResemble, []any{"a"})
	})
}

const driverDescriptorJSON = `{"id":"mysql-ext","name":"MySQL Ext","icon":"x","order":50,"aliases":[],"defaultPort":3306,` +
	`"form":[{"tab":"connection","fields":["host","port"]}],"capabilities":["schemas"],` +
	`"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}`

func TestDescribeIntoDatabaseDrivers(t *testing.T) {
	Convey("A described driver carries the extension's icon: it is the one icon the user sees on it", t, func() {
		useDescribeCache(t, newFakeDescribeCache(stubWasm, `{"icon":"ext-icon","contributes":{"databaseDrivers":[`+driverDescriptorJSON+`]}}`))
		m := &Manifest{Manifest: extmanifest.Manifest{
			Name:        "ext-a",
			HostABI:     extmanifest.HostABIVersion,
			Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"mysql-ext"}},
		}}
		_, err := (&Manager{}).describeInto(context.Background(), m, nil, stubWasm)
		So(err, ShouldBeNil)
		So(m.DatabaseDrivers, ShouldHaveLength, 1)
		So(m.DatabaseDrivers[0].Icon, ShouldEqual, "ext-icon")
	})
}
