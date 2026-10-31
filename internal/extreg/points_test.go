package extreg

import (
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"
)

// The rules of describe()'s contributions, judged by the host's contribution points
// with a view of the whole extension — what a load and a cached describe() read
// both conclude (the runtime only routes the keys, see pkg/extension).

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

// check judges a describe() answer under a manifest that grants nothing.
func check(t *testing.T, payload []byte) error {
	return checkDescribe(t, extmanifest.Manifest{Name: "ext"}, payload)
}

// withAssetEndpoint is a manifest granting network.assetEndpoint, which auth and
// connection declarations need.
func withAssetEndpoint() extmanifest.Manifest {
	return extmanifest.Manifest{Name: "ext", Permissions: extmanifest.Permissions{
		Network: extmanifest.NetworkPermission{AssetEndpoint: true}}}
}

func TestContributesKeys(t *testing.T) {
	Convey("Every contributes key is one of the host's contribution points", t, func() {
		Convey("an unknown key refuses the extension, naming it and the key", func() {
			err := check(t, desc(`"widgets":[{"id":"w"}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, "ext: contributes.widgets")
		})
	})
}

// An asset type is no longer required of every extension (decision 8): only a tool
// that declares a policy action needs one to be reached through.
func TestExtensionsWithoutAnAssetType(t *testing.T) {
	Convey("An extension that contributes no asset type", t, func() {
		Convey("loads when it only contributes snippets, and attaches", func() {
			payload := describeJSON(`"snippets":{"seed":[{"key":"ls","name":"ls","category":"shell","content":"ls -al"}]}`)
			So(check(t, payload), ShouldBeNil)
		})

		Convey("loads when it only provides database drivers", func() {
			mf := extmanifest.Manifest{Name: "ext", Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"mysql-ext"}}}
			So(checkDescribe(t, mf, describeJSON(`"databaseDrivers":[`+driverDescriptorJSON+`]`)), ShouldBeNil)
		})

		Convey("loads with nothing to contribute at all", func() {
			So(check(t, describeJSON(``)), ShouldBeNil)
		})

		Convey("is refused when it contributes a tool, naming the tool", func() {
			err := check(t, describeJSON(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}}],"policy":{"type":"x"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, "ext: tools[t]: ")
			So(err.Error(), ShouldContainSubstring, "asset type")
		})

		Convey("is refused when it contributes a tool next to its drivers", func() {
			mf := extmanifest.Manifest{Name: "ext", Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"mysql-ext"}}}
			err := checkDescribe(t, mf, describeJSON(`"databaseDrivers":[`+driverDescriptorJSON+`],`+
				`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "tools")
		})
	})

	Convey("A snippet-only extension attaches through every point and revokes cleanly", t, func() {
		w, err := extwire.Parse(describeJSON(`"snippets":{"seed":[{"key":"ls","name":"ls","category":"shell","content":"ls -al"}]}`))
		So(err, ShouldBeNil)
		v := &testView{
			l:  loaded{name: "snips", plugin: &fakePlugin{}},
			rt: extension.Described(extmanifest.Manifest{Name: "snips"}, &extension.Descriptor{Contributes: w.Contributes}),
		}
		So(testContributions.Attach(v), ShouldBeNil)
		testContributions.Revoke("snips")
		So(testContributions.Attach(v), ShouldBeNil) // nothing of the first attach is left to collide with
		testContributions.Revoke("snips")
	})
}

func TestAssetTypeRules(t *testing.T) {
	Convey("Asset type declarations", t, func() {
		Convey("asset type without config properties", func() {
			err := check(t, describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},`+
				`"configSchema":{"type":"object"}}],"policy":{"type":"x"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "assetTypes[x]: configSchema")
		})

		Convey("duplicate asset type", func() {
			err := check(t, describeJSON(`"assetTypes":[`+
				`{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}},`+
				`{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}`+
				`],"policy":{"type":"x"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})

		Convey("an asset type with neither tools nor a policy face loads", func() {
			So(check(t, describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},`+
				`"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}]`)), ShouldBeNil)
		})

		// Connection settings (and auth) apply only to the asset's endpoint: on a
		// type with no endpoint field they would show in the form and never apply.
		Convey("connection or auth on a type with no endpoint field is refused", func() {
			for _, binding := range []string{
				`"connection":{"sshTunnel":true}`,
				`"auth":{"groups":[{"bindings":[{"in":"header","name":"X-Key","value":"{{endpoint}}"}]}]}`,
			} {
				err := checkDescribe(t, withAssetEndpoint(), describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},`+
					`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}},`+
					binding+`}],"policy":{"type":"x"}`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `format:"endpoint"`)
			}
		})

		// ... and an endpoint exists only for an extension declaring
		// network.assetEndpoint: without it they would load, be shown in the asset
		// form, and never apply.
		Convey("connection or auth without permissions.network.assetEndpoint is refused", func() {
			for _, binding := range []string{
				`"connection":{"sshTunnel":true}`,
				`"auth":{"groups":[{"bindings":[{"in":"header","name":"Authorization","value":"Bearer {{token}}"}]}]}`,
			} {
				payload := describeJSON(`"assetTypes":[{"type":"stub","i18n":{"name":"n"},"configSchema":` +
					`{"type":"object","properties":{"endpoint":{"type":"string","format":"endpoint"},"token":{"type":"string","format":"password"}}},` +
					binding + `}],"policy":{"type":"stub"}`)
				err := check(t, payload)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "ext: assetTypes[stub]: ")
				So(err.Error(), ShouldContainSubstring, "network.assetEndpoint")
				So(checkDescribe(t, withAssetEndpoint(), payload), ShouldBeNil)
			}
		})

		// A connection item the type opts into stores the host's own field in the
		// asset config; a configSchema property of that name would collide with it.
		// The check refuses it, so a cached read concludes what a load does.
		Convey("a configSchema property named like a host connection field the type opts into is refused", func() {
			err := checkDescribe(t, withAssetEndpoint(), describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},`+
				`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string","format":"endpoint"},"ssh_asset_id":{"type":"string"}}},`+
				`"connection":{"sshTunnel":true}}],"policy":{"type":"x"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, "ext: assetTypes[x]: ")
			So(err.Error(), ShouldContainSubstring, `"ssh_asset_id"`)
		})
	})
}

func TestAssetTypeAuthRules(t *testing.T) {
	Convey("An asset type declares auth bindings the host injects into requests to its endpoint", t, func() {
		withAuth := func(auth string) []byte {
			return describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
				`"configSchema":{"type":"object","properties":{` +
				`"endpoint":{"type":"string","format":"endpoint"},"authType":{"type":"string"},` +
				`"username":{"type":"string"},"password":{"type":"string","format":"password"}}},` +
				`"auth":` + auth + `}],"policy":{"type":"x"}`)
		}
		refused := func(auth, want string) {
			err := checkDescribe(t, withAssetEndpoint(), withAuth(auth))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, want)
		}

		Convey("groups selected by a config field, over header / query / basic, are accepted", func() {
			So(checkDescribe(t, withAssetEndpoint(), withAuth(`{"selector":"authType","groups":[`+
				`{"when":"basic","bindings":[{"in":"basic","value":"{{username}}:{{password}}"}]},`+
				`{"when":"token","bindings":[{"in":"header","name":"Authorization","value":"ApiKey {{base64(username, \":\", password)}}"},`+
				`{"in":"query","name":"sig","value":"{{ password }}"}]}]}`)), ShouldBeNil)
		})

		Convey("a single group needs no selector", func() {
			So(checkDescribe(t, withAssetEndpoint(), withAuth(`{"groups":[{"bindings":[{"in":"header","name":"X-Token","value":"{{password}}"}]}]}`)), ShouldBeNil)
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
	})
}

func TestAssetTypeEnumPresentation(t *testing.T) {
	withProp := func(prop string) []byte {
		return describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
			`"configSchema":{"type":"object","properties":{"auth":` + prop + `}}}],"policy":{"type":"x"}`)
	}
	Convey("enumLabels and default are checked against the enum they decorate", t, func() {
		Convey("matching labels and a member default are accepted", func() {
			So(check(t, withProp(`{"type":"string","enum":["a","b"],"enumLabels":["A","B"],"default":"a"}`)), ShouldBeNil)
		})
		Convey("labels of another length", func() {
			err := check(t, withProp(`{"type":"string","enum":["a","b"],"enumLabels":["A"]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "enumLabels")
		})
		Convey("labels without an enum", func() {
			err := check(t, withProp(`{"type":"string","enumLabels":["A"]}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "enumLabels")
		})
		Convey("non-string labels", func() {
			So(check(t, withProp(`{"type":"string","enum":["a"],"enumLabels":[1]}`)), ShouldNotBeNil)
		})
		Convey("a default outside the enum", func() {
			err := check(t, withProp(`{"type":"string","enum":["a","b"],"default":"c"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "default")
		})
	})
}

func TestToolRules(t *testing.T) {
	Convey("Tool parameter schemas are what the exec flag DSL parses against", t, func() {
		refused := func(tools string, wants ...string) {
			err := check(t, desc(`"tools":[`+tools+`]`))
			So(err, ShouldNotBeNil)
			for _, want := range wants {
				So(err.Error(), ShouldContainSubstring, want)
			}
		}

		Convey("a descriptor without tools is accepted", func() {
			So(check(t, desc("")), ShouldBeNil)
		})

		Convey("the shapes a real extension uses", func() {
			So(check(t, desc(`"tools":[
				{"name":"list_buckets","policyAction":"list","parameters":{"type":"object","properties":{}}},
				{"name":"list_objects","policyAction":"list","parameters":{"type":"object","properties":{"maxKeys":{"type":"integer"}}}},
				{"name":"delete_objects","policyAction":"delete","parameters":{"type":"object","properties":{"keys":{"type":"array","items":{"type":"string"}}},"required":["keys"]}}
			]`)), ShouldBeNil)
		})

		Convey("an error names the extension, the contribution and the tool", func() {
			err := check(t, desc(`"tools":[{"name":"bulk","policyAction":"write","parameters":{"type":"object","properties":{"body":{"type":"object"}}}}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, `ext: tools[bulk]: parameters.properties.body`)
		})

		Convey("a tool that declares the injected asset as a flag", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"asset_id":{"type":"integer"}}}}`, "asset_id", "exec target")
		})

		Convey("a tool with no policy action", func() {
			refused(`{"name":"t","parameters":{"type":"object","properties":{}}}`, "policy action")
		})

		Convey("a tool missing parameters", func() {
			refused(`{"name":"t","policyAction":"read"}`, "parameters")
		})

		Convey("parameters whose type is not object", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"array"}}`, "parameters.type")
		})

		Convey("a property missing type", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"k":{"description":"no type"}}}}`, "k")
		})

		Convey("a dangling required entry", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{},"required":["ghost"]}}`, "ghost")
		})

		Convey("a duplicate tool name", func() {
			one := `{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}}`
			refused(one+`,`+one, "duplicate")
		})

		Convey("a tool without a name, named by its index", func() {
			refused(`{"policyAction":"read","parameters":{"type":"object","properties":{}}}`, "tools[0]: name")
		})

		Convey("object parameters without a properties object", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object"}}`, "properties")
		})

		Convey("a property whose value is not an object", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"k":"x"}}}`, "properties.k", "must be an object")
		})

		Convey("a genuinely unsupported property type", func() {
			// "object" is deliberately unsupported: nested structures go through exec's
			// --json escape hatch instead of inventing a nested flag syntax.
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"nested":{"type":"object"}}}}`,
				"properties.nested", `unsupported type "object"`)
		})

		Convey("an array property without a usable items object", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"tags":{"type":"array"}}}}`, "properties.tags", "items")
		})

		Convey("an array property with a non-string item type", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"integer"}}}}}`, "array<integer>")
		})

		Convey("required that is not an array", func() {
			refused(`{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"key":{"type":"string"}},"required":"key"}}`,
				"parameters.required", "must be an array")
		})
	})

	Convey("A tool may declare its own call timeout, within the host's ceiling", t, func() {
		tool := func(timeout string) []byte {
			return desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}` + timeout + `}]`)
		}
		Convey("up to ten minutes is accepted", func() {
			So(check(t, tool(`,"timeoutMs":600000`)), ShouldBeNil)
		})
		Convey("over ten minutes is refused at load", func() {
			err := check(t, tool(`,"timeoutMs":600001`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools[t]: timeoutMs`)
			So(err.Error(), ShouldContainSubstring, "10m0s")
		})
		Convey("a negative declaration is refused at load", func() {
			err := check(t, tool(`,"timeoutMs":-1`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools[t]: timeoutMs`)
		})
	})

	// 一个工具要么声明固定动作（policyAction），要么声明按参数分类时可能给出的动作集合
	// （policyActions，SDK 的 PolicyFunc）。
	Convey("A tool declares the actions its policy classification can produce", t, func() {
		tool := func(policy string) []byte {
			return desc(`"tools":[{"name":"t",` + policy + `,"parameters":{"type":"object","properties":{}}}]`)
		}
		Convey("a tool declaring both a fixed action and an action set", func() {
			err := check(t, tool(`"policyAction":"read","policyActions":["read"]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policyActions")
		})
		Convey("an empty entry in the action set", func() {
			err := check(t, tool(`"policyActions":["read",""]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy action")
		})
		Convey("a duplicate entry in the action set", func() {
			err := check(t, tool(`"policyActions":["read","read"]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "duplicate")
		})
		// 规则是 <action>[:<resource-glob>]，动作段在第一个 ':' 处结束；一个
		// 自带 ':' 的动作名会让 "动作:资源" 与 "动作" 无从区分。
		Convey("an action name that could be read as action:resource", func() {
			err := check(t, tool(`"policyAction":"read:secret"`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "read:secret")
			err = check(t, tool(`"policyActions":["read","write:all"]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "write:all")
		})
	})

	Convey("A tool may mark string parameters as large", t, func() {
		tool := func(large string) []byte {
			return desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"body":{"type":"string"},"n":{"type":"integer"}}},"large":` + large + `}]`)
		}
		Convey("a string parameter is accepted", func() {
			So(check(t, tool(`["body"]`)), ShouldBeNil)
		})
		Convey("a non-string parameter is refused at load", func() {
			err := check(t, tool(`["n"]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools[t]: large`)
		})
		Convey("an undeclared parameter is refused at load", func() {
			err := check(t, tool(`["nope"]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `tools[t]: large`)
		})
		// opsctl reads --body-file as body's file form, so a real body-file
		// parameter could never be passed.
		Convey("a parameter whose -file spelling is itself a parameter is refused at load", func() {
			err := check(t, desc(`"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{"body":{"type":"string"},"body-file":{"type":"string"}}},"large":["body"]}]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"body-file"`)
		})
	})
}

// 每个工具恰好二选一：策略动作，或只给页面用；都有或都无是点名该工具的错误。
func TestToolIsPolicyOrPageOnly(t *testing.T) {
	const params = `"parameters":{"type":"object","properties":{}}`
	tools := func(entries ...string) []byte {
		return describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
			`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
			`"tools":[` + strings.Join(entries, ",") + `],"policy":{"type":"x"}`)
	}
	refused := func(entry string, wants ...string) {
		err := check(t, tools(entry))
		So(err, ShouldNotBeNil)
		for _, w := range wants {
			So(err.Error(), ShouldContainSubstring, w)
		}
	}
	Convey("A tool is exactly a policy tool or a page-only tool", t, func() {
		Convey("a page-only tool needs no policy action", func() {
			So(check(t, tools(`{"name":"ui","pageOnly":true,`+params+`}`)), ShouldBeNil)
		})
		Convey("a tool with neither is refused, naming it", func() {
			refused(`{"name":"bare",`+params+`}`, "tools[bare]", "pageOnly")
		})
		Convey("a tool with both is refused, naming it", func() {
			refused(`{"name":"both","pageOnly":true,"policyAction":"read",`+params+`}`, "tools[both]", "pageOnly")
			refused(`{"name":"both","pageOnly":true,"policyActions":["read"],`+params+`}`, "tools[both]", "pageOnly")
		})
		Convey("only a page-only tool may declare no timeout", func() {
			So(check(t, tools(`{"name":"ui","pageOnly":true,"noTimeout":true,`+params+`}`)), ShouldBeNil)
			refused(`{"name":"p","policyAction":"read","noTimeout":true,`+params+`}`, "tools[p]", "noTimeout")
			refused(`{"name":"ui","pageOnly":true,"noTimeout":true,"timeoutMs":1000,`+params+`}`, "tools[ui]", "noTimeout")
		})
		Convey("a page-only tool does not need an asset type or a policy type", func() {
			So(check(t, describeJSON(`"tools":[{"name":"ui","pageOnly":true,`+params+`}]`)), ShouldBeNil)
		})
		Convey("the policy type is required once any tool declares a policy action", func() {
			noType := func(entries ...string) error {
				return check(t, describeJSON(`"assetTypes":[{"type":"x","i18n":{"name":"n"},`+
					`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],`+
					`"tools":[`+strings.Join(entries, ",")+`]`))
			}
			So(noType(`{"name":"ui","pageOnly":true,`+params+`}`), ShouldBeNil)
			err := noType(`{"name":"ui","pageOnly":true,`+params+`}`, `{"name":"p","policyAction":"read",`+params+`}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy.type")
		})
	})
}

// 权限组 ID 按声明者的策略面命名空间化（ext:<policy.type>:<name>），策略面名本身
// 与资产类型同一套命名规则——组 ID 与 grant（ext:<policyType>:<action>:<resource>）都以它为段，
// 一个不受约束的策略面名会让两者的命名空间失去意义。
func TestPolicyRules(t *testing.T) {
	group := func(id string) string {
		return `{"id":"` + id + `","i18n":{"name":"n","description":"d"},"policy":{"allow_list":["read"]}}`
	}
	parse := func(policyType string, groups ...string) error {
		body := `"assetTypes":[{"type":"x","i18n":{"name":"n"},` +
			`"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}],` +
			`"policy":{"type":"` + policyType + `","groups":[` + strings.Join(groups, ",") + `]}`
		return check(t, describeJSON(body))
	}

	Convey("policy.type and policy group IDs are namespaced", t, func() {
		Convey("a group under the extension's own policy type is accepted", func() {
			So(parse("x", group("ext:x:read"), group("ext:x:write")), ShouldBeNil)
		})

		Convey("policy.type must be a plain name", func() {
			for _, bad := range []string{"ext:oss", "X", "a b", "a:b", "-x"} {
				err := parse(bad)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "policy.type")
			}
		})

		Convey("a group in another policy type's namespace is refused", func() {
			err := parse("x", group("ext:acme:readonly"))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext:x:")
		})

		Convey("a group outside the ext: namespace is refused", func() {
			err := parse("x", group("nope:bad"))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext:")
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

	Convey("A policy face is required exactly when a tool declares a policy action", t, func() {
		const tool = `"tools":[{"name":"t","policyAction":"read","parameters":{"type":"object","properties":{}}}]`
		const assetType = `"assetTypes":[{"type":"x","i18n":{"name":"n"},"configSchema":{"type":"object","properties":{"e":{"type":"string"}}}}]`

		Convey("tools without one are refused", func() {
			err := check(t, describeJSON(assetType+`,`+tool))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy.type")
		})

		Convey("a driver-only extension may declare one, held to the same rule", func() {
			mf := extmanifest.Manifest{Name: "ext", Contributes: extmanifest.Contributes{DatabaseDrivers: []string{"mysql-ext"}}}
			err := checkDescribe(t, mf, describeJSON(`"databaseDrivers":[`+driverDescriptorJSON+`],"policy":{"type":"a:b"}`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "policy.type")
			So(checkDescribe(t, mf, describeJSON(`"databaseDrivers":[`+driverDescriptorJSON+`],"policy":{"type":"mydb"}`)), ShouldBeNil)
		})
	})
}

const driverDescriptorJSON = `{"id":"mysql-ext","name":"MySQL Ext","icon":"x","order":50,"aliases":[],"defaultPort":3306,` +
	`"form":[{"tab":"connection","fields":["host","port","username"]}],"capabilities":["schemas"],` +
	`"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}`

func TestDatabaseDriverRules(t *testing.T) {
	declaring := func(ids ...string) extmanifest.Manifest {
		return extmanifest.Manifest{Name: "ext-a", Contributes: extmanifest.Contributes{DatabaseDrivers: ids}}
	}
	const drivers = `"databaseDrivers":[` + driverDescriptorJSON + `]`

	Convey("A descriptor may provide SQL database drivers", t, func() {
		Convey("an invalid driver descriptor fails naming the driver id", func() {
			for _, bad := range []string{
				`"form":[{"tab":"connection","fields":["nope"]}]`,
				`"capabilities":["teleport"]`,
				`"form":[{"tab":"nowhere","fields":["host"]}]`,
				`"driverFields":[{"key":"host","type":"text","tab":"connection"}]`,
			} {
				err := checkDescribe(t, declaring("bad-one"), describeJSON(`"databaseDrivers":[{"id":"bad-one",`+bad+`}]`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "bad-one")
			}
		})

		Convey("duplicate driver ids in one descriptor are refused", func() {
			err := checkDescribe(t, declaring("mysql-ext"), describeJSON(`"databaseDrivers":[`+driverDescriptorJSON+`,`+driverDescriptorJSON+`]`))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "mysql-ext")
		})

		Convey("an extension may mix asset types and drivers", func() {
			So(checkDescribe(t, declaring("mysql-ext"), desc(drivers)), ShouldBeNil)
		})

		// An extension driver reaches a database over the network: a form without
		// host / port / username, or with a database-file field, is refused by the
		// check — a cached read concludes what a load does.
		Convey("a driver form that does not fit the network rules is refused, naming the driver and the field", func() {
			for field, form := range map[string]string{
				`"username"`:      `["host","port"]`,
				`"sqlite_source"`: `["host","port","username","sqlite_source"]`,
			} {
				err := checkDescribe(t, declaring("net-ext"), describeJSON(`"databaseDrivers":[{"id":"net-ext","name":"N","icon":"x",`+
					`"order":50,"aliases":[],"defaultPort":1,"form":[{"tab":"connection","fields":`+form+`}],`+
					`"capabilities":[],"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}]`))
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldStartWith, "ext-a: databaseDrivers[net-ext]: ")
				So(err.Error(), ShouldContainSubstring, field)
			}
		})
	})

	Convey("The manifest's driver ids and describe()'s must be the same set", t, func() {
		Convey("matching sets load", func() {
			So(checkDescribe(t, declaring("mysql-ext"), describeJSON(drivers)), ShouldBeNil)
		})
		Convey("a driver described but not declared", func() {
			err := checkDescribe(t, declaring(), describeJSON(drivers))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldStartWith, "ext-a: databaseDrivers[mysql-ext]: ")
		})
		Convey("a driver declared but not described", func() {
			err := checkDescribe(t, declaring("mysql-ext", "ghost"), describeJSON(drivers))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext-a")
			So(err.Error(), ShouldContainSubstring, "ghost")
		})
		Convey("a driver declared while describe() contributes no drivers at all", func() {
			err := checkDescribe(t, declaring("ghost"), describeJSON(``))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ghost")
		})
	})

	Convey("A driver declaring the Kerberos field group needs the manifest permission that lets it ask for tickets", t, func() {
		const krbDriver = `{"id":"krb-ext","name":"Krb Ext","icon":"x","order":50,"aliases":[],"defaultPort":1521,` +
			`"form":[{"tab":"connection","fields":["host","port","username","password","kerberos"]}],` +
			`"capabilities":[],"createDatabaseOptions":[],"alterTableAttributes":[],"driverFields":[]}`
		loadWith := func(kerberos bool) error {
			mf := declaring("krb-ext")
			mf.Permissions.Kerberos = kerberos
			return checkDescribe(t, mf, describeJSON(`"databaseDrivers":[`+krbDriver+`]`))
		}
		Convey("without permissions.kerberos the extension is refused, naming the driver and the reason", func() {
			err := loadWith(false)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ext-a")
			So(err.Error(), ShouldContainSubstring, "krb-ext")
			So(err.Error(), ShouldContainSubstring, `"kerberos"`)
			So(err.Error(), ShouldContainSubstring, "permissions.kerberos")
		})
		Convey("with the permission it loads", func() {
			So(loadWith(true), ShouldBeNil)
		})
	})
}

// Attaching across the points is all or nothing: a refusal at a later point takes
// back what the earlier ones attached.
func TestAttachAcrossPointsIsAllOrNothing(t *testing.T) {
	Convey("An extension refused by the driver registry leaves no asset type, executor or rule landing", t, func() {
		m := testManifest()
		m.Name, m.Policies.Type = "clash", "clash"
		m.AssetTypes[0].Type = "clash-store"
		m.Policies.Groups[0].ID, m.Policies.Default = "ext:clash:readonly", []string{"ext:clash:readonly"}
		// Taken by the database asset type's own built-in driver.
		m.DatabaseDrivers = []dbdriver.Descriptor{driverDescriptor("clashdb", "mysql")}
		m.Contributes.DatabaseDrivers = []string{"clashdb"}
		err := attachLoaded(loaded{name: m.Name, manifest: m, plugin: &fakePlugin{}}, "", "")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldStartWith, "clash: databaseDrivers[clashdb]: ")

		_, ok := assettype.Get("clash-store")
		So(ok, ShouldBeFalse)
		_, ok = permission.ExecutorFor("clash-store")
		So(ok, ShouldBeFalse)
		So(permission.TypeRulesSupported("clash-store"), ShouldBeFalse)
		_, lookupErr := dbregistry.Lookup("clashdb")
		So(lookupErr, ShouldNotBeNil)

		Convey("and a later policy-face refusal revokes the drivers and asset types already attached", func() {
			registerFake(t, &fakePlugin{}) // owns the "acme" policy face
			m.DatabaseDrivers = []dbdriver.Descriptor{driverDescriptor("clashdb")}
			m.Policies.Type = "acme"
			m.Policies.Groups[0].ID, m.Policies.Default = "ext:acme:other", []string{"ext:acme:other"}
			err := attachLoaded(loaded{name: m.Name, manifest: m, plugin: &fakePlugin{}}, "", "")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `already owned by extension "acme"`)
			_, ok := assettype.Get("clash-store")
			So(ok, ShouldBeFalse)
			_, lookupErr := dbregistry.Lookup("clashdb")
			So(lookupErr, ShouldNotBeNil)
			_, owned := DriverOwnerOf("clashdb")
			So(owned, ShouldBeFalse)
		})
	})
}
