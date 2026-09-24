package opskat

import (
	"encoding/json"
	"testing"
	"time"

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

func TestDescribeReportsConnectionDeclaration(t *testing.T) {
	Convey("an asset type's host-owned connection settings are reported only when declared", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "demo"})
		AssetType[demoConfig]("tunneled").Connection(Connection{SSHTunnel: true})
		AssetType[demoConfig]("direct")

		byType := map[string]map[string]any{}
		for _, raw := range decodeDescribe(t)["assetTypes"].([]any) {
			at := raw.(map[string]any)
			byType[at["type"].(string)] = at
		}
		So(byType["tunneled"]["connection"], ShouldResemble, map[string]any{"sshTunnel": true})
		So(byType["direct"], ShouldNotContainKey, "connection")
	})
}

func TestDescribeReportsAuthDeclaration(t *testing.T) {
	Convey("an asset type's credential injection is reported only when declared", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "demo"})
		AssetType[demoConfig]("injected").Auth(Auth{
			Selector: "mode",
			Groups: []AuthGroup{
				{When: "basic", Bindings: []AuthBinding{{In: "basic", Value: "{{user}}:{{pass}}"}}},
				{When: "key", Bindings: []AuthBinding{{In: "header", Name: "Authorization", Value: "ApiKey {{pass}}"}}},
			},
		})
		AssetType[demoConfig]("plain")

		byType := map[string]map[string]any{}
		for _, raw := range decodeDescribe(t)["assetTypes"].([]any) {
			at := raw.(map[string]any)
			byType[at["type"].(string)] = at
		}
		So(byType["injected"]["auth"], ShouldResemble, map[string]any{
			"selector": "mode",
			"groups": []any{
				map[string]any{"when": "basic", "bindings": []any{map[string]any{"in": "basic", "value": "{{user}}:{{pass}}"}}},
				map[string]any{"when": "key", "bindings": []any{map[string]any{"in": "header", "name": "Authorization", "value": "ApiKey {{pass}}"}}},
			},
		})
		So(byType["plain"], ShouldNotContainKey, "auth")
	})
}

type searchArgs struct {
	Index string `json:"index"`
	Write bool   `json:"write,omitempty"`
}

func classifySearch(args searchArgs) (action, resource string) {
	if args.Write {
		return "index.write", args.Index
	}
	return "index.read", args.Index
}

func TestPolicyFuncClassifiesEachCall(t *testing.T) {
	Convey("PolicyFunc answers check_policy from the call's own arguments", t, func() {
		resetRegistries()
		Extension(Meta{PolicyType: "demo"})
		Tool("request", func(_ *ToolContext, _ searchArgs) (any, error) { return nil, nil }).
			PolicyFunc([]string{"index.read", "index.write"}, classifySearch)

		check := func(args string) (map[string]string, error) {
			raw, err := dispatch("check_policy", []byte(`{"tool":"request","args":`+args+`}`))
			if err != nil {
				return nil, err
			}
			var decision map[string]string
			So(json.Unmarshal(raw, &decision), ShouldBeNil)
			return decision, nil
		}

		Convey("the action and resource come from the arguments", func() {
			d, err := check(`{"index":"logs-2026"}`)
			So(err, ShouldBeNil)
			So(d, ShouldResemble, map[string]string{"action": "index.read", "resource": "logs-2026"})
			d, err = check(`{"index":"logs-2026","write":true}`)
			So(err, ShouldBeNil)
			So(d, ShouldResemble, map[string]string{"action": "index.write", "resource": "logs-2026"})
		})

		Convey("arguments the tool cannot decode fail the check instead of classifying blind", func() {
			_, err := check(`{"index":7}`)
			So(err, ShouldNotBeNil)
		})

		Convey("describe declares the action set, not a fixed action", func() {
			tool := decodeDescribe(t)["tools"].([]any)[0].(map[string]any)
			So(tool["policyActions"], ShouldResemble, []any{"index.read", "index.write"})
			So(tool, ShouldNotContainKey, "policyAction")
		})

		Convey("a fixed-action tool still describes and answers exactly as before", func() {
			Tool("list", func(_ *ToolContext, _ listArgs) (any, error) { return nil, nil }).
				Policy("list").Resource(func(a listArgs) string { return a.Bucket })
			raw, err := dispatch("check_policy", []byte(`{"tool":"list","args":{"bucket":"b1"}}`))
			So(err, ShouldBeNil)
			So(string(raw), ShouldEqual, `{"action":"list","resource":"b1"}`)
			for _, rawTool := range decodeDescribe(t)["tools"].([]any) {
				tool := rawTool.(map[string]any)
				if tool["name"] == "list" {
					So(tool["policyAction"], ShouldEqual, "list")
					So(tool, ShouldNotContainKey, "policyActions")
				}
			}
		})
	})

	Convey("a PolicyFunc declaration that cannot be honored fails at init", t, func() {
		resetRegistries()
		noop := func(_ *ToolContext, _ searchArgs) (any, error) { return nil, nil }

		Convey("no declared actions", func() {
			So(func() { Tool("a", noop).PolicyFunc(nil, classifySearch) }, ShouldPanic)
		})
		Convey("combined with a fixed action", func() {
			So(func() { Tool("b", noop).Policy("index.read").PolicyFunc([]string{"index.read"}, classifySearch) }, ShouldPanic)
			So(func() { Tool("c", noop).PolicyFunc([]string{"index.read"}, classifySearch).Policy("index.read") }, ShouldPanic)
		})
		Convey("combined with Resource, which PolicyFunc already answers", func() {
			So(func() {
				Tool("d", noop).PolicyFunc([]string{"index.read"}, classifySearch).
					Resource(func(a searchArgs) string { return a.Index })
			}, ShouldPanic)
		})
	})
}

func TestDescribeReportsToolTimeout(t *testing.T) {
	Convey("a tool's own timeout is declared through describe", t, func() {
		resetRegistries()
		AssetType[demoConfig]("demo")
		Tool("slow", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("read").Timeout(2 * time.Minute)
		Tool("plain", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("read")

		byName := map[string]map[string]any{}
		for _, raw := range decodeDescribe(t)["tools"].([]any) {
			tool := raw.(map[string]any)
			byName[tool["name"].(string)] = tool
		}
		So(byName["slow"]["timeoutMs"], ShouldEqual, float64(120000))
		So(byName["plain"], ShouldNotContainKey, "timeoutMs")

		Convey("a timeout outside (0, 10m] fails at registration", func() {
			reg := Tool("bad", func(_ *ToolContext, _ struct{}) (any, error) { return nil, nil }).Policy("read")
			So(func() { reg.Timeout(10*time.Minute + time.Millisecond) }, ShouldPanic)
			So(func() { reg.Timeout(0) }, ShouldPanic)
			So(func() { reg.Timeout(10 * time.Minute) }, ShouldNotPanic)
		})
	})
}
