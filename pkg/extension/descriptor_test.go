package extension

import (
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// base is the smallest descriptor a guest may answer with: one asset type (the
// only way its tools are reachable) and the policy face they are checked under.
const baseDescriptor = `"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policies":{"type":"x"}`

func desc(extra string) []byte {
	if extra != "" {
		extra = "," + extra
	}
	return []byte("{" + baseDescriptor + extra + "}")
}

func TestParseDescriptorAssetScope(t *testing.T) {
	Convey("A descriptor has to give its tools a reachable entry point", t, func() {
		Convey("no asset type at all", func() {
			_, err := ParseDescriptor([]byte(`{"policies":{"type":"x"}}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "assetTypes")
		})

		Convey("no policy face", func() {
			_, err := ParseDescriptor([]byte(`{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policies.type")
		})

		Convey("asset type without config properties", func() {
			_, err := ParseDescriptor([]byte(`{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object"}}],"policies":{"type":"x"}}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "configSchema")
		})

		Convey("duplicate asset type", func() {
			_, err := ParseDescriptor([]byte(`{"assetTypes":[` +
				`{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}},` +
				`{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}` +
				`],"policies":{"type":"x"}}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("policy group outside the ext: namespace", func() {
			_, err := ParseDescriptor(desc(`"policies":{"type":"x","groups":[{"id":"nope:bad",` +
				`"i18n":{"name":"n","description":"d"},"policy":{"allow_list":["read"]}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext:")
		})
	})
}

func TestParseDescriptorTools(t *testing.T) {
	Convey("Tool parameter schemas are what the exec flag DSL parses against", t, func() {
		Convey("a descriptor without tools is accepted", func() {
			_, err := ParseDescriptor(desc(""))
			So(err, ShouldBeNil)
		})

		Convey("the shapes a real extension uses", func() {
			d, err := ParseDescriptor(desc(`"tools":[
				{"name":"list_buckets","policyAction":"list","parameters":{"type":"object","properties":{}}},
				{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{"maxKeys":{"type":"integer"}}}},
				{"name":"delete_objects","policyAction":"delete","parameters":{"type":"object","properties":{"keys":{"type":"array","items":{"type":"string"}}},"required":["keys"]}}
			]`))
			So(err, ShouldBeNil)
			So(len(d.Tools), ShouldEqual, 3)
		})

		Convey("a tool that declares the injected asset as a flag", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"asset_id":{"type":"integer"}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset_id")
			So(err.Error(), ShouldContainSubstring, "exec target")
		})

		Convey("a tool with no policy action", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","parameters":{"type":"object","properties":{}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy action")
		})

		Convey("a tool missing parameters", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read"}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "parameters")
		})

		Convey("parameters whose type is not object", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"array"}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "parameters.type")
		})

		Convey("a property missing type", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"k":{"description":"no type"}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "k")
		})

		Convey("a dangling required entry", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{},"required":["ghost"]}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ghost")
		})

		Convey("a duplicate tool name", func() {
			one := `{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}}`
			_, err := ParseDescriptor(desc(`"tools":[` + one + `,` + one + `]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("a tool without a name, named by its index", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"policyAction":"read","parameters":{"type":"object","properties":{}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "tools[0].name")
		})

		Convey("object parameters without a properties object", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object"}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "properties")
		})

		Convey("a property whose value is not an object", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"k":"x"}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "properties.k")
			So(err.Error(), ShouldContainSubstring, "must be an object")
		})

		Convey("a genuinely unsupported property type", func() {
			// "object" is deliberately unsupported: nested structures go through exec's
			// --json escape hatch instead of inventing a nested flag syntax.
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"nested":{"type":"object"}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "properties.nested")
			So(err.Error(), ShouldContainSubstring, `unsupported type "object"`)
		})

		Convey("an array property without a usable items object", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"tags":{"type":"array"}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "properties.tags")
			So(err.Error(), ShouldContainSubstring, "items")
		})

		Convey("an array property with a non-string item type", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"integer"}}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "array<integer>")
		})

		Convey("required that is not an array", func() {
			_, err := ParseDescriptor(desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":"key"}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "parameters.required")
			So(err.Error(), ShouldContainSubstring, "must be an array")
		})
	})
}

func TestParseDescriptorToolTimeout(t *testing.T) {
	tool := func(timeout string) []byte {
		return desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}` + timeout + `}]`)
	}
	Convey("A tool may declare its own call timeout, within the host's ceiling", t, func() {
		Convey("undeclared means the host default", func() {
			d, err := ParseDescriptor(tool(""))
			So(err, ShouldBeNil)
			So(d.Tools[0].Timeout(), ShouldEqual, time.Duration(0))
		})

		Convey("a declaration up to ten minutes is kept", func() {
			d, err := ParseDescriptor(tool(`,"timeoutMs":600000`))
			So(err, ShouldBeNil)
			So(d.Tools[0].Timeout(), ShouldEqual, 10*time.Minute)
		})

		Convey("a declaration over ten minutes is refused at load", func() {
			_, err := ParseDescriptor(tool(`,"timeoutMs":600001`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools["t"].timeoutMs`)
			So(err.Error(), ShouldContainSubstring, "10m0s")
		})

		Convey("a negative declaration is refused at load", func() {
			_, err := ParseDescriptor(tool(`,"timeoutMs":-1`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools["t"].timeoutMs`)
		})
	})
}

func TestParseDescriptorSnippets(t *testing.T) {
	Convey("Snippet declarations", t, func() {
		Convey("a valid block", func() {
			d, err := ParseDescriptor(desc(`"snippets":{
				"categories":[{"id":"kafka","assetType":"x","i18n":{"name":"category.kafka"}}],
				"seed":[
					{"key":"list-topics","name":"List topics","category":"kafka","content":"kafka-topics --list"},
					{"key":"ls","name":"ls","category":"shell","content":"ls -al"}
				]}`))
			So(err, ShouldBeNil)
			So(len(d.Snippets.Categories), ShouldEqual, 1)
			So(len(d.Snippets.Seed), ShouldEqual, 2)
		})

		Convey("a category id colliding with a builtin", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"categories":[{"id":"shell","assetType":"x","i18n":{"name":"n"}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "builtin")
		})

		Convey("a duplicate category id", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"categories":[` +
				`{"id":"kafka","assetType":"x","i18n":{"name":"n"}},` +
				`{"id":"kafka","assetType":"x","i18n":{"name":"n2"}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("a category id in the wrong format", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"categories":[{"id":"Kafka_1","assetType":"x","i18n":{"name":"n"}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "must match")
		})

		Convey("a category attached to an asset type this extension does not own", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"categories":[{"id":"missing","assetType":"other","i18n":{"name":"n"}}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "asset types")
		})

		Convey("a seed referencing an unknown category", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"seed":[{"key":"x","name":"x","category":"nope","content":"echo"}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "neither builtin nor declared")
		})

		Convey("duplicate seed keys", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"seed":[` +
				`{"key":"k1","name":"a","category":"shell","content":"x"},` +
				`{"key":"k1","name":"b","category":"shell","content":"y"}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("a seed key in the wrong format", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"seed":[{"key":"BadKey!","name":"a","category":"shell","content":"x"}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "must match")
		})

		Convey("a seed with empty content", func() {
			_, err := ParseDescriptor(desc(`"snippets":{"seed":[{"key":"k1","name":"a","category":"shell","content":"   "}]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "content")
		})
	})
}

func TestApplyDescriptor(t *testing.T) {
	Convey("Merging describe() onto the manifest", t, func() {
		m, err := ParseManifest([]byte(`{"name":"oss","version":"1.0.0","hostABI":"2.0",` +
			`"backend":{"runtime":"wasm","binary":"main.wasm"},` +
			`"capabilities":{"credentials":"read"}}`))
		So(err, ShouldBeNil)

		d, err := ParseDescriptor(desc(`"icon":"cloud-storage",` +
			`"i18n":{"displayName":"manifest.displayName","description":"manifest.description"},` +
			`"frontend":{"entry":"frontend/index.js","pages":[{"id":"browser","slot":"asset.connect","i18n":{"name":"pages.browser.name"},"component":"BrowserPage"}]},` +
			`"tools":[` +
			`{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{}}},` +
			`{"name":"delete_object","policyAction":"delete","parameters":{"type":"object","properties":{}}},` +
			`{"name":"get_object","policyAction":"list","parameters":{"type":"object","properties":{}}}]`))
		So(err, ShouldBeNil)

		m.apply(d)

		Convey("the security contract is untouched", func() {
			So(m.Name, ShouldEqual, "oss")
			So(m.Capabilities.Credentials, ShouldEqual, CredentialAccessRead)
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
	})
}

// 权限组 ID 按声明者的策略面命名空间化（ext:<policies.type>:<name>），策略面名本身
// 与资产类型同一套命名规则——组 ID 与 grant（ext:<policyType>:<action>:<resource>）都以它为段，
// 一个不受约束的策略面名会让两者的命名空间失去意义。
func TestParseDescriptorPolicyNamespace(t *testing.T) {
	group := func(id string) string {
		return `{"id":"` + id + `","i18n":{"name":"n","description":"d"},"policy":{"allow_list":["read"]}}`
	}
	parse := func(policyType string, groups ...string) error {
		body := `{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
			`"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}],` +
			`"policies":{"type":"` + policyType + `","groups":[` + strings.Join(groups, ",") + `]}}`
		_, err := ParseDescriptor([]byte(body))
		return err
	}

	Convey("policies.type and policy group IDs are namespaced", t, func() {
		Convey("a group under the extension's own policy type is accepted", func() {
			So(parse("x", group("ext:x:read"), group("ext:x:write")), ShouldBeNil)
		})

		Convey("policies.type must be a plain name", func() {
			for _, bad := range []string{"ext:oss", "X", "a b", "a:b", "-x"} {
				err := parse(bad)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "policies.type")
			}
		})

		Convey("a group in another policy type's namespace is refused", func() {
			err := parse("x", group("ext:acme:readonly"))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext:x:")
		})

		Convey("the bare namespace is not a group ID", func() {
			So(parse("x", group("ext:x")), ShouldNotBeNil)
			So(parse("x", group("ext:x:")), ShouldNotBeNil)
		})

		Convey("a duplicate group ID is refused", func() {
			err := parse("x", group("ext:x:read"), group("ext:x:read"))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})
	})
}

// 连接配置区（隧道 / 代理链 / TLS）由宿主拥有，资产类型只声明支持哪几项；
// 声明里出现宿主不认识的项要在加载时拒绝，而不是默默忽略成"不生效"。
func TestParseDescriptorConnection(t *testing.T) {
	Convey("An asset type declares which host-owned connection settings it supports", t, func() {
		withConnection := func(conn string) []byte {
			return []byte(`{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string","format":"endpoint"}}},` +
				`"connection":` + conn + `}],"policies":{"type":"x"}}`)
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

		// Connection settings (and auth) apply only to the asset's endpoint: on a
		// type with no endpoint field they would show in the form and never apply.
		Convey("connection or auth on a type with no endpoint field is refused", func() {
			for _, binding := range []string{
				`"connection":{"sshTunnel":true}`,
				`"auth":{"groups":[{"bindings":[{"in":"header","name":"X-Key","value":"{{endpoint}}"}]}]}`,
			} {
				_, err := ParseDescriptor([]byte(`{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
					`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}},` +
					binding + `}],"policies":{"type":"x"}}`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `format:"endpoint"`)
			}
		})
	})
}

// 凭据注入的 auth 绑定由宿主在请求 endpoint 时渲染；模板只能引用该资产类型自己的
// configSchema 字段——引用不存在的字段意味着宿主注入的东西永远是空的，必须在加载时拒绝。
func TestParseDescriptorAuth(t *testing.T) {
	Convey("An asset type declares auth bindings the host injects into requests to its endpoint", t, func() {
		withAuth := func(auth string) []byte {
			return []byte(`{"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{` +
				`"endpoint":{"type":"string","format":"endpoint"},"authType":{"type":"string"},` +
				`"username":{"type":"string"},"password":{"type":"string","format":"password"}}},` +
				`"auth":` + auth + `}],"policies":{"type":"x"}}`)
		}
		refused := func(auth, want string) {
			_, err := ParseDescriptor(withAuth(auth))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, want)
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

		Convey("a single group needs no selector", func() {
			d, err := ParseDescriptor(withAuth(`{"groups":[{"bindings":[{"in":"header","name":"X-Token","value":"{{password}}"}]}]}`))
			So(err, ShouldBeNil)
			So(d.AssetTypes[0].Auth.Groups, ShouldHaveLength, 1)
		})

		Convey("no declaration means no injection", func() {
			d, err := ParseDescriptor(desc(""))
			So(err, ShouldBeNil)
			So(d.AssetTypes[0].Auth, ShouldBeNil)
		})

		Convey("a template referencing a field the configSchema does not declare is refused", func() {
			refused(`{"groups":[{"bindings":[{"in":"header","name":"Authorization","value":"Bearer {{token}}"}]}]}`, `"token"`)
			refused(`{"groups":[{"bindings":[{"in":"basic","value":"{{base64(username, apiKey)}}"}]}]}`, `"apiKey"`)
		})

		Convey("a selector the configSchema does not declare is refused", func() {
			refused(`{"selector":"mode","groups":[{"when":"a","bindings":[{"in":"basic","value":"{{username}}:{{password}}"}]}]}`, `"mode"`)
		})

		// The selector's value picks a group and is plain data the host logs and
		// compares; a secret has no business choosing one, and reading it as a
		// selector would put its plaintext into the host's logs.
		Convey("a selector naming a password field is refused", func() {
			refused(`{"selector":"password","groups":[{"when":"a","bindings":[{"in":"basic","value":"{{username}}:{{password}}"}]}]}`, `"password"`)
		})

		Convey("a malformed template is refused", func() {
			refused(`{"groups":[{"bindings":[{"in":"header","name":"A","value":"{{password"}]}]}`, "unclosed")
			refused(`{"groups":[{"bindings":[{"in":"header","name":"A","value":"{{md5(password)}}"}]}]}`, "md5(password)")
			refused(`{"groups":[{"bindings":[{"in":"header","name":"A","value":"{{base64()}}"}]}]}`, "base64")
		})

		Convey("a location the host does not inject into is refused", func() {
			refused(`{"groups":[{"bindings":[{"in":"cookie","name":"sid","value":"{{password}}"}]}]}`, `"cookie"`)
		})

		Convey("header and query bindings need a name, basic takes none", func() {
			refused(`{"groups":[{"bindings":[{"in":"header","value":"{{password}}"}]}]}`, "name")
			refused(`{"groups":[{"bindings":[{"in":"query","value":"{{password}}"}]}]}`, "name")
			refused(`{"groups":[{"bindings":[{"in":"basic","name":"x","value":"{{username}}:{{password}}"}]}]}`, "name")
			refused(`{"groups":[{"bindings":[{"in":"header","name":"Bad Header","value":"{{password}}"}]}]}`, "Bad Header")
		})

		Convey("groups must be selectable unambiguously", func() {
			// several groups without a selector: which one applies is undecidable
			refused(`{"groups":[{"bindings":[{"in":"basic","value":"{{username}}"}]},{"bindings":[{"in":"basic","value":"{{password}}"}]}]}`, "selector")
			// with a selector every group names the value that selects it, once
			refused(`{"selector":"authType","groups":[{"bindings":[{"in":"basic","value":"{{username}}"}]}]}`, "when")
			refused(`{"selector":"authType","groups":[{"when":"a","bindings":[{"in":"basic","value":"{{username}}"}]},`+
				`{"when":"a","bindings":[{"in":"basic","value":"{{password}}"}]}]}`, "duplicate")
			refused(`{"groups":[]}`, "groups")
			refused(`{"groups":[{"bindings":[]}]}`, "bindings")
		})

		Convey("an unknown key is refused rather than ignored", func() {
			refused(`{"groups":[{"bindings":[{"in":"header","header":"Authorization","value":"{{password}}"}]}]}`, `"header"`)
		})
	})
}

// 一个工具要么声明固定动作（policyAction），要么声明按参数分类时可能给出的动作集合
// （policyActions，SDK 的 PolicyFunc）。两种声明都并入 policies.actions——宿主拿它核对
// guest 在 check_policy 里给出的动作。
func TestParseDescriptorPolicyActionSets(t *testing.T) {
	Convey("A tool declares the actions its policy classification can produce", t, func() {
		tool := func(policy string) string {
			return `"tools":[{"name":"t",` + policy + `,"parameters":{"type":"object","properties":{}}}]`
		}

		Convey("a classified tool's action set feeds policies.actions alongside fixed actions", func() {
			m, err := ParseManifest([]byte(`{"name":"x","version":"1.0.0","hostABI":"2.0",` +
				`"backend":{"runtime":"wasm","binary":"main.wasm"}}`))
			So(err, ShouldBeNil)
			d, err := ParseDescriptor(desc(`"tools":[` +
				`{"name":"search","policyActions":["search","index.read"],"parameters":{"type":"object","properties":{}}},` +
				`{"name":"list","policyAction":"list","parameters":{"type":"object","properties":{}}}]`))
			So(err, ShouldBeNil)
			m.apply(d)
			So(m.Policies.Actions, ShouldResemble, []string{"index.read", "list", "search"})
		})

		Convey("a tool declaring both a fixed action and an action set", func() {
			_, err := ParseDescriptor(desc(tool(`"policyAction":"read","policyActions":["read"]`)))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policyActions")
		})

		Convey("an empty entry in the action set", func() {
			_, err := ParseDescriptor(desc(tool(`"policyActions":["read",""]`)))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy action")
		})

		Convey("a duplicate entry in the action set", func() {
			_, err := ParseDescriptor(desc(tool(`"policyActions":["read","read"]`)))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		// 规则是 <action>[:<resource-glob>]，动作段在第一个 ':' 处结束；一个
		// 自带 ':' 的动作名会让 "动作:资源" 与 "动作" 无从区分。
		Convey("an action name that could be read as action:resource", func() {
			_, err := ParseDescriptor(desc(tool(`"policyAction":"read:secret"`)))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "read:secret")

			_, err = ParseDescriptor(desc(tool(`"policyActions":["read","write:all"]`)))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "write:all")
		})
	})
}
