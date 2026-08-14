package conversation_entity

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMessageBlocksRoundtrip(t *testing.T) {
	Convey("SetBlocks/GetBlocks 往返", t, func() {
		msg := &Message{}
		blocks := []ContentBlock{
			{Type: "text", Content: "hi"},
			{Type: "tool", ToolName: "exec", ToolCallID: "call_1", Status: "completed"},
		}

		Convey("非空写入后能读回", func() {
			So(msg.SetBlocks(blocks), ShouldBeNil)
			got, err := msg.GetBlocks()
			So(err, ShouldBeNil)
			So(got, ShouldResemble, blocks)
		})

		Convey("空数组写入后 Blocks 列为空字符串", func() {
			So(msg.SetBlocks(nil), ShouldBeNil)
			So(msg.Blocks, ShouldEqual, "")
			got, err := msg.GetBlocks()
			So(err, ShouldBeNil)
			So(got, ShouldBeNil)
		})
	})
}

func TestMessageBlocksPersistenceStoresRawToolResultErrorAndNestedAgentBlocks(t *testing.T) {
	Convey("SetBlocks 对 tool content / error 详情 / 嵌套 agent child blocks 原样写入，不做脱敏", t, func() {
		msg := &Message{}
		secret := "nested-" + "credential-sentinel" // 运行时拼接避免 gosec G101
		blocks := []ContentBlock{
			{
				Type: "tool", ToolName: "query", Status: "completed",
				Content: `{"rows":3,"token":"` + secret + `"}`,
			},
			{
				Type: "error", ErrorKind: "server",
				Content:     "failed: --password " + secret,
				ErrorDetail: "Authorization: Bearer " + secret,
			},
			{
				Type: "agent", Status: "completed",
				ChildBlocks: []ContentBlock{
					{
						Type: "tool", ToolName: "put_asset", Status: "completed",
						ToolInput: `{"password":"` + secret + `"}`,
						Content:   "ok: --api-key " + secret,
					},
				},
			},
			{Type: "text", Content: "safe output"},
		}
		So(msg.SetBlocks(blocks), ShouldBeNil)
		So(msg.Blocks, ShouldContainSubstring, secret)
		So(msg.Blocks, ShouldContainSubstring, "safe output")

		Convey("嵌套 agent 子块结构原样写入（GetBlocks 读回 childBlocks 且值逐字一致）", func() {
			got, err := msg.GetBlocks()
			So(err, ShouldBeNil)
			So(got, ShouldHaveLength, 4)
			So(got, ShouldResemble, blocks)
			So(got[2].ChildBlocks, ShouldHaveLength, 1)
			So(got[2].ChildBlocks[0].ToolInput, ShouldEqual, `{"password":"`+secret+`"}`)
			So(got[2].ChildBlocks[0].Content, ShouldEqual, "ok: --api-key "+secret)
		})
	})
}

func TestMessageGetBlocksReturnsStoredValueUnchanged(t *testing.T) {
	Convey("GetBlocks 原样返回存储的 blocks；旧值包含字面 <redacted> 时也作为普通值保留", t, func() {
		secret := "legacy-" + "credential-sentinel"
		msg := &Message{Blocks: `[{"type":"agent","content":"","childBlocks":[{"type":"tool","content":"Authorization: Basic ` + secret + `","toolInput":"{\"password\":\"` + secret + `\"}"}]}]`}

		got, err := msg.GetBlocks()
		So(err, ShouldBeNil)
		So(got, ShouldHaveLength, 1)
		So(got[0].ChildBlocks, ShouldHaveLength, 1)
		So(got[0].ChildBlocks[0].ToolInput, ShouldEqual, `{"password":"`+secret+`"}`)
		So(got[0].ChildBlocks[0].Content, ShouldEqual, "Authorization: Basic "+secret)
	})

	Convey("已持久化的字面 <redacted> 作为普通不可恢复字面值原样返回", t, func() {
		msg := &Message{Blocks: `[{"type":"tool","content":"ok: --api-key <redacted>","toolInput":"{\"password\":\"<redacted>\"}"}]`}
		got, err := msg.GetBlocks()
		So(err, ShouldBeNil)
		So(got, ShouldHaveLength, 1)
		So(got[0].ToolInput, ShouldEqual, `{"password":"<redacted>"}`)
		So(got[0].Content, ShouldEqual, "ok: --api-key <redacted>")
	})
}

func TestMessageBlocksPersistenceStoresToolCredentials(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&Message{}))

	const secret = "review-only-credential-sentinel"
	msg := &Message{ConversationID: 1, Role: "assistant"}
	require.NoError(t, msg.SetBlocks([]ContentBlock{{
		Type:     "tool",
		ToolName: "put_asset",
		ToolInput: `{"name":"prod","config":{"host":"db.internal","password":"` + secret +
			`","private_key":"` + secret + `","token":"` + secret + `"}}`,
	}}))
	require.NoError(t, gdb.Create(msg).Error)

	var stored string
	require.NoError(t, gdb.Raw("SELECT blocks FROM conversation_messages WHERE id = ?", msg.ID).Scan(&stored).Error)
	require.Contains(t, stored, secret)
	require.Contains(t, stored, "db.internal")
	require.False(t, strings.Contains(stored, "<redacted>"), "stored blocks must keep the raw credential value")
}
