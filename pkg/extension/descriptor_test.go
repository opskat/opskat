package extension

import (
	"testing"

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
