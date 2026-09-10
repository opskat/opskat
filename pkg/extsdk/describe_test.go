package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

type listArgs struct {
	Bucket  string   `json:"bucket,omitempty" desc:"Bucket name"`
	Keys    []string `json:"keys" desc:"Object keys"`
	MaxKeys int      `json:"maxKeys,omitempty"`
	Dry     bool     `json:"dry,omitempty"`
}

type demoConfig struct {
	Endpoint string `json:"endpoint" title:"config.endpoint.title" placeholder:"config.endpoint.placeholder"`
	Secret   string `json:"secret,omitempty" title:"config.secret.title" format:"password"`
	Mode     string `json:"mode,omitempty" enum:"fast,safe"`
}

func decodeDescribe(t *testing.T) map[string]any {
	t.Helper()
	raw, err := dispatch("describe", nil)
	So(err, ShouldBeNil)
	var out map[string]any
	So(json.Unmarshal(raw, &out), ShouldBeNil)
	return out
}

func TestDescribeIsDerivedFromRegistrations(t *testing.T) {
	Convey("describe reports what the registration calls declared", t, func() {
		resetRegistries()
		Extension(Meta{
			Icon:        "cloud",
			DisplayName: "manifest.displayName",
			Description: "manifest.description",
			PolicyType:  "demo",
		})
		AssetType[demoConfig]("demo").Name("assetType.demo.name")
		Tool("list_objects", func(_ *ToolContext, args listArgs) (any, error) {
			return map[string]any{"bucket": args.Bucket, "keys": args.Keys}, nil
		}).Policy("list").Doc("tools.list_objects.description")
		Tool("delete_object", func(_ *ToolContext, _ struct{}) (any, error) {
			return nil, nil
		}).Policy("delete")
		PolicyGroup("ext:demo:readonly").Name("policy.readonly.name").
			Description("policy.readonly.description").Allow("list").Deny("delete").Default()

		desc := decodeDescribe(t)

		Convey("metadata and policy face come from Extension()", func() {
			So(desc["icon"], ShouldEqual, "cloud")
			i18n := desc["i18n"].(map[string]any)
			So(i18n["displayName"], ShouldEqual, "manifest.displayName")
			policies := desc["policies"].(map[string]any)
			So(policies["type"], ShouldEqual, "demo")
			So(policies["default"], ShouldResemble, []any{"ext:demo:readonly"})
			groups := policies["groups"].([]any)
			So(len(groups), ShouldEqual, 1)
			g := groups[0].(map[string]any)
			So(g["id"], ShouldEqual, "ext:demo:readonly")
			So(g["policy"], ShouldResemble, map[string]any{
				"allow_list": []any{"list"},
				"deny_list":  []any{"delete"},
			})
		})

		Convey("every registered tool is reported, with a schema reflected from its args", func() {
			byName := map[string]map[string]any{}
			for _, raw := range desc["tools"].([]any) {
				tool := raw.(map[string]any)
				byName[tool["name"].(string)] = tool
			}
			// Set equality against the table dispatch serves from: a tool describe
			// omits, or one it invents, is a tool the host and the guest disagree
			// about. This is the assertion a second, hand-kept list would break.
			So(len(byName), ShouldEqual, len(tools))
			for name := range tools {
				So(byName, ShouldContainKey, name)
			}
			So(byName, ShouldContainKey, "list_objects")
			So(byName, ShouldContainKey, "delete_object")

			list := byName["list_objects"]
			So(list["i18n"].(map[string]any)["description"], ShouldEqual, "tools.list_objects.description")
			So(list["policyAction"], ShouldEqual, "list")
			params := list["parameters"].(map[string]any)
			So(params["type"], ShouldEqual, "object")
			props := params["properties"].(map[string]any)
			So(props["bucket"], ShouldResemble, map[string]any{"type": "string", "description": "Bucket name"})
			So(props["keys"], ShouldResemble, map[string]any{
				"type": "array", "items": map[string]any{"type": "string"}, "description": "Object keys",
			})
			So(props["maxKeys"].(map[string]any)["type"], ShouldEqual, "integer")
			So(props["dry"].(map[string]any)["type"], ShouldEqual, "boolean")
			// a field without ,omitempty is required
			So(params["required"], ShouldResemble, []any{"keys"})

			// a no-arg tool still declares an object schema
			So(byName["delete_object"]["parameters"], ShouldResemble,
				map[string]any{"type": "object", "properties": map[string]any{}})
		})

		Convey("asset type configSchema is reflected from the config struct", func() {
			assetTypes := desc["assetTypes"].([]any)
			So(len(assetTypes), ShouldEqual, 1)
			at := assetTypes[0].(map[string]any)
			So(at["type"], ShouldEqual, "demo")
			So(at["i18n"].(map[string]any)["name"], ShouldEqual, "assetType.demo.name")
			schema := at["configSchema"].(map[string]any)
			props := schema["properties"].(map[string]any)
			So(props["endpoint"], ShouldResemble, map[string]any{
				"type": "string", "title": "config.endpoint.title", "placeholder": "config.endpoint.placeholder",
			})
			So(props["secret"].(map[string]any)["format"], ShouldEqual, "password")
			So(props["mode"].(map[string]any)["enum"], ShouldResemble, []any{"fast", "safe"})
			So(schema["required"], ShouldResemble, []any{"endpoint"})
			So(schema["propertyOrder"], ShouldResemble, []any{"endpoint", "secret", "mode"})
		})

		Convey("a tool registered after the first describe still shows up", func() {
			// The registry dispatch reads is the registry describe reports; there is no
			// second list that could be left behind.
			_ = decodeDescribe(t)
			Tool("late", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("list")
			tools := decodeDescribe(t)["tools"].([]any)
			names := make([]string, 0, len(tools))
			for _, raw := range tools {
				names = append(names, raw.(map[string]any)["name"].(string))
			}
			So(names, ShouldContain, "late")
		})
	})
}

func TestTypedToolDispatch(t *testing.T) {
	Convey("a typed tool decodes its own args", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "demo"})
		Tool("list_objects", func(ctx *ToolContext, args listArgs) (any, error) {
			return map[string]any{"tool": ctx.Tool, "bucket": args.Bucket, "max": args.MaxKeys}, nil
		}).Policy("list")

		input, _ := json.Marshal(map[string]any{
			"tool": "list_objects",
			"args": json.RawMessage(`{"bucket":"logs","maxKeys":7}`),
		})
		result, err := dispatch("execute_tool", input)
		So(err, ShouldBeNil)
		var out map[string]any
		So(json.Unmarshal(result, &out), ShouldBeNil)
		So(out["bucket"], ShouldEqual, "logs")
		So(out["max"], ShouldEqual, float64(7))

		Convey("check_policy answers from the same registration — no second switch", func() {
			policyInput, _ := json.Marshal(map[string]any{
				"tool": "list_objects",
				"args": json.RawMessage(`{}`),
			})
			raw, err := dispatch("check_policy", policyInput)
			So(err, ShouldBeNil)
			var decision map[string]string
			So(json.Unmarshal(raw, &decision), ShouldBeNil)
			So(decision["action"], ShouldEqual, "list")
		})

		Convey("an unknown tool is an error in both dispatch paths", func() {
			_, err := dispatch("execute_tool", []byte(`{"tool":"nope","args":{}}`))
			So(err, ShouldNotBeNil)
			_, err = dispatch("check_policy", []byte(`{"tool":"nope","args":{}}`))
			So(err, ShouldNotBeNil)
		})
	})
}

func TestRegistrationRejectsBrokenDeclarations(t *testing.T) {
	Convey("a registration that cannot produce a schema fails at init, not at call time", t, func() {
		resetRegistries()

		Convey("duplicate tool name", func() {
			Tool("dup", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("list")
			So(func() {
				Tool("dup", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("list")
			}, ShouldPanic)
		})

		Convey("args type the flag DSL cannot express", func() {
			type nested struct {
				Inner map[string]string `json:"inner"`
			}
			So(func() {
				Tool("bad", func(_ *ToolContext, _ nested) (any, error) { return nil, nil })
			}, ShouldPanic)
		})

		Convey("args type that is not a struct", func() {
			So(func() {
				Tool("bad", func(_ *ToolContext, _ string) (any, error) { return nil, nil })
			}, ShouldPanic)
		})
	})
}
