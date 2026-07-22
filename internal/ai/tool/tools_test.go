package tool

import (
	"strings"

	"testing"

	"github.com/opskat/opskat/internal/ai/permission"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	. "github.com/smartystreets/goconvey/convey"
)

func TestTools_RegistryShape(t *testing.T) {
	Convey("Tools 返回的工具集与既定契约一致", t, func() {
		tools := Tools()

		// batch_exec 属于桌面端并行批量能力；spawn_agent 不属于 OpsKat 工具集。
		names := make(map[string]tool.Tool, len(tools))
		for _, t := range tools {
			names[t.Name()] = t
		}

		Convey("spawn_agent 不属于工具集", func() {
			So(names, ShouldNotContainKey, "spawn_agent")
		})

		expected := []string{
			// asset
			"list_assets", "get_asset",
			"list_groups", "get_group",
			// crud
			"put_asset", "put_group",
			"delete_asset", "delete_group",
			// exec
			"upload_file", "download_file",
			"request_permission", "batch_exec",
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

		// ShouldContainKey 不检查穷尽性，所以单靠上面那条断言，删掉一批工具之后测试
		// 会反常地继续通过。这条数量断言是唯一能发现"注册了却没人知道"的检查——
		// run_serial_command 正是这样漂移了很久（注册着，却不在任何清单里，
		// 删除它的那一版才被补进 expected 记账）。删工具时必须同步改这里。
		Convey("没有清单之外的工具（穷尽性）", func() {
			// 比的是**去重后**的名字数（names 是按名字建的 map），不是 tools 的切片长度：
			// 同名重复注册会让切片长于 map，于是"N 个切片项 == N 个 expected 名字"仍然
			// 成立，却藏着一个没进清单的工具。
			So(len(names), ShouldEqual, len(expected))
			So(len(tools), ShouldEqual, len(names))
		})

		Convey("命令类工具标 Serial", func() {
			serialNames := []string{
				"upload_file", "download_file",
				"request_permission",
				"exec_tool",
				"exec",
				"put_asset", "put_group",
				"delete_asset", "delete_group",
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

// TestExecTool_DescriptionListsEveryRegisteredType 锁住 exec 的工具描述与执行器注册表
// 一致。这段描述随每个工具列表下发给模型，是它判断"exec 管不管这个类型"的第一手依据。
//
// 手写那份曾经停在 "(ssh, serial, database, redis, k8s)"：mongodb / etcd / kafka 先后
// 接入统一 exec，没有一次想起来改它——于是模型能看到的工具列表里，那三种类型压根不存在。
// 漏一个类型不会报错、不会挂测试，只会让模型对该类型放弃使用 exec。
func TestExecTool_DescriptionListsEveryRegisteredType(t *testing.T) {
	var execDesc string
	for _, tl := range Tools() {
		if tl.Name() == "exec" {
			execDesc = tl.Description()
			break
		}
	}
	if execDesc == "" {
		t.Fatal("exec tool not found in Tools(), or it has an empty description")
	}

	types := permission.RegisteredExecTypes()
	if len(types) == 0 {
		t.Fatal("RegisteredExecTypes() is empty — execimpl's init did not run; " +
			"without it this test would trivially pass while the description lists nothing")
	}
	for _, typ := range types {
		if !strings.Contains(execDesc, typ) {
			t.Errorf("exec description does not mention registered type %q; got: %s", typ, execDesc)
		}
	}
}
