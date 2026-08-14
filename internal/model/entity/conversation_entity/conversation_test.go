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

func TestMessageBlocksPersistenceRedactsToolResultErrorAndNestedAgentBlocks(t *testing.T) {
	Convey("SetBlocks 对 tool content / error 详情 / 嵌套 agent child blocks 递归脱敏", t, func() {
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
		So(msg.Blocks, ShouldNotContainSubstring, secret)
		So(msg.Blocks, ShouldContainSubstring, "<redacted>")
		So(msg.Blocks, ShouldContainSubstring, "safe output")

		Convey("嵌套 agent 子块仍保留结构（GetBlocks 读回 childBlocks）", func() {
			got, err := msg.GetBlocks()
			So(err, ShouldBeNil)
			So(got, ShouldHaveLength, 4)
			So(got[0].Content, ShouldContainSubstring, `"rows":3`)
			So(got[0].Content, ShouldNotContainSubstring, secret)
			So(got[2].Type, ShouldEqual, "agent")
			So(got[2].ChildBlocks, ShouldHaveLength, 1)
			So(got[2].ChildBlocks[0].ToolInput, ShouldContainSubstring, "<redacted>")
			So(got[2].ChildBlocks[0].ToolInput, ShouldNotContainSubstring, secret)
			So(got[2].ChildBlocks[0].Content, ShouldContainSubstring, "<redacted>")
			So(got[2].ChildBlocks[0].Content, ShouldNotContainSubstring, secret)
		})
	})
}

func TestMessageGetBlocksRedactsLegacyPlaintextBeforeDisplay(t *testing.T) {
	Convey("GetBlocks 在旧明文历史离开后端前递归投影安全副本", t, func() {
		secret := "legacy-" + "credential-sentinel"
		msg := &Message{Blocks: `[{"type":"agent","content":"","childBlocks":[{"type":"tool","content":"Authorization: Basic ` + secret + `","toolInput":"{\"password\":\"` + secret + `\"}"}]}]`}

		got, err := msg.GetBlocks()
		So(err, ShouldBeNil)
		So(got, ShouldHaveLength, 1)
		So(got[0].ChildBlocks, ShouldHaveLength, 1)
		So(got[0].ChildBlocks[0].ToolInput, ShouldNotContainSubstring, secret)
		So(got[0].ChildBlocks[0].Content, ShouldNotContainSubstring, secret)
		So(got[0].ChildBlocks[0].ToolInput, ShouldContainSubstring, "<redacted>")
		So(got[0].ChildBlocks[0].Content, ShouldContainSubstring, "<redacted>")
		So(msg.Blocks, ShouldContainSubstring, secret) // 加载只投影，不原地改写旧行。
	})
}

func TestMessageBlocksPersistenceRedactsToolCredentials(t *testing.T) {
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
	require.NotContains(t, stored, secret)
	require.Contains(t, stored, "db.internal")
	require.True(t, strings.Contains(stored, "redacted"), "stored blocks should retain explicit redaction markers")
}
