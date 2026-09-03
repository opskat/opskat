package opskat

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTestHost(t *testing.T) {
	Convey("TestHost", t, func() {
		Convey("CallTool dispatches to registered handler", func() {
			resetRegistries()
			Tool("echo", func(_ *ToolContext, args struct {
				Msg string `json:"msg"`
			}) (any, error) {
				return map[string]string{"echo": args.Msg}, nil
			}).Policy("read")

			th := NewTestHost()
			defer th.Close()

			result, err := th.CallTool(Asset{}, "echo", map[string]string{"msg": "hi"})
			So(err, ShouldBeNil)

			var out map[string]string
			b, _ := json.Marshal(result)
			_ = json.Unmarshal(b, &out)
			So(out["echo"], ShouldEqual, "hi")
		})

		Convey("SetAssetConfig and GetAssetConfig", func() {
			resetRegistries()
			Tool("config_test", func(ctx *ToolContext, _ struct{}) (any, error) {
				cfg, err := GetAssetConfig(1)
				if err != nil {
					return nil, err
				}
				return json.RawMessage(cfg), nil
			})

			cfg := map[string]string{"endpoint": "https://oss.example.com"}
			th := NewTestHost(WithAssetConfig(1, cfg))
			defer th.Close()

			result, err := th.CallTool(Asset{ID: 1}, "config_test", map[string]any{})
			So(err, ShouldBeNil)

			b, _ := json.Marshal(result)
			var out map[string]string
			_ = json.Unmarshal(b, &out)
			So(out["endpoint"], ShouldEqual, "https://oss.example.com")
		})

		Convey("MockHTTP intercepts HTTP requests", func() {
			resetRegistries()
			Tool("http_test", func(ctx *ToolContext, _ struct{}) (any, error) {
				transport := NewHTTPTransport()
				client := &http.Client{Transport: transport}
				resp, err := client.Get("http://mock/data")
				if err != nil {
					return nil, err
				}
				defer func() { _ = resp.Body.Close() }()
				body, _ := io.ReadAll(resp.Body)
				return map[string]any{
					"status": resp.StatusCode,
					"body":   string(body),
				}, nil
			})

			th := NewTestHost(WithMockHTTP(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"items":["a","b"]}`))
			}))
			defer th.Close()

			result, err := th.CallTool(Asset{}, "http_test", map[string]any{})
			So(err, ShouldBeNil)

			b, _ := json.Marshal(result)
			var out map[string]any
			_ = json.Unmarshal(b, &out)
			So(out["status"], ShouldEqual, 200)
			So(out["body"], ShouldEqual, `{"items":["a","b"]}`)
		})

		Convey("KVGet/KVSet persistence", func() {
			resetRegistries()
			Tool("kv_test", func(ctx *ToolContext, _ struct{}) (any, error) {
				_ = KVSet("counter", []byte("42"))
				val, err := KVGet("counter")
				if err != nil {
					return nil, err
				}
				return map[string]string{"val": string(val)}, nil
			})

			th := NewTestHost()
			defer th.Close()

			result, err := th.CallTool(Asset{}, "kv_test", map[string]any{})
			So(err, ShouldBeNil)

			b, _ := json.Marshal(result)
			var out map[string]string
			_ = json.Unmarshal(b, &out)
			So(out["val"], ShouldEqual, "42")
		})

		Convey("CallAction captures events", func() {
			resetRegistries()
			RegisterAction("upload", func(ctx *ActionContext) (any, error) {
				_ = ctx.Events.Send("progress", map[string]int{"done": 50, "total": 100})
				_ = ctx.Events.Send("progress", map[string]int{"done": 100, "total": 100})
				return map[string]string{"status": "done"}, nil
			})

			th := NewTestHost()
			defer th.Close()

			var events []TestEvent
			result, err := th.CallAction(Asset{}, "upload", map[string]any{}, func(e TestEvent) {
				events = append(events, e)
			})
			So(err, ShouldBeNil)
			So(len(events), ShouldEqual, 2)
			So(events[0].Type, ShouldEqual, "progress")

			b, _ := json.Marshal(result)
			var out map[string]string
			_ = json.Unmarshal(b, &out)
			So(out["status"], ShouldEqual, "done")
		})
	})
}
