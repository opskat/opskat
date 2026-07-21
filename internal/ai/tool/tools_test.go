package tool

import (
	"testing"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	. "github.com/smartystreets/goconvey/convey"
)

func TestTools_RegistryShape(t *testing.T) {
	Convey("Tools 返回的工具集与既定契约一致", t, func() {
		tools := Tools()

		// batch_command 属于桌面端并行批量能力；spawn_agent 不属于 OpsKat 工具集。
		names := make(map[string]tool.Tool, len(tools))
		for _, t := range tools {
			names[t.Name()] = t
		}

		Convey("spawn_agent 不属于工具集", func() {
			So(names, ShouldNotContainKey, "spawn_agent")
		})

		expected := []string{
			// asset
			"list_assets", "get_asset", "add_asset", "update_asset",
			"list_groups", "get_group", "add_group", "update_group",
			// exec
			"run_command", "run_serial_command", "upload_file", "download_file",
			"request_permission", "batch_command",
			// data
			"exec_sql", "exec_redis", "exec_mongo", "exec_k8s", "exec_etcd",
			// extension
			"exec_tool",
			// unified
			"exec", "help",
		}

		Convey("所有契约里的工具都注册了", func() {
			for _, name := range expected {
				So(names, ShouldContainKey, name)
			}
		})

		// ShouldContainKey 不检查穷尽性，所以单靠上面那条断言，删掉 7 个 kafka 工具
		// 之后测试会反常地继续通过。这条数量断言是唯一能发现"注册了却没人知道"的
		// 检查——run_serial_command 正是这样漂移了很久（注册着，却不在任何清单里，
		// 本次才补进 expected）。删工具时必须同步改这里。
		Convey("没有清单之外的工具（穷尽性）", func() {
			So(len(tools), ShouldEqual, len(expected))
		})

		Convey("命令类工具标 Serial", func() {
			serialNames := []string{
				"run_command", "run_serial_command", "upload_file", "download_file",
				"request_permission",
				"exec_sql", "exec_redis", "exec_mongo", "exec_k8s", "exec_etcd",
				"exec_tool",
				"exec",
			}
			for _, name := range serialNames {
				st, ok := names[name].(agent.SerialTool)
				So(ok, ShouldBeTrue)
				So(st.Serial(), ShouldBeTrue)
			}
		})

		Convey("Schema 结构合法（type=object，必填字段存在）", func() {
			for _, t := range names {
				schema := t.Schema()
				So(schema.Type, ShouldEqual, "object")
				for _, req := range schema.Required {
					So(schema.Properties, ShouldContainKey, req)
				}
			}
		})
	})
}
