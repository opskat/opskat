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
