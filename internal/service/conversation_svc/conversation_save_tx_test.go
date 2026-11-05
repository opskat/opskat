package conversation_svc

import (
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/repository/conversation_repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestConversationSvc_SaveMessages_InsertFailureKeepsPreviousMessages 守护 R7：
// SaveMessages 的删除与插入必须在同一事务内，插入失败时旧消息保留。
func TestConversationSvc_SaveMessages_InsertFailureKeepsPreviousMessages(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	// 在 TempDir 清理之前关掉连接：Windows 上打开着的 SQLite 文件删不掉。
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.AutoMigrate(&conversation_entity.Message{}))

	db.SetDefault(gdb)
	prevRepo := conversation_repo.Conversation()
	conversation_repo.RegisterConversation(conversation_repo.NewConversation())
	t.Cleanup(func() { conversation_repo.RegisterConversation(prevRepo) })

	ctx := t.Context()
	old := []*conversation_entity.Message{
		{Role: "user", Content: "旧问题"},
		{Role: "assistant", Content: "旧回答"},
	}
	require.NoError(t, Conversation().SaveMessages(ctx, 1, 0, old))

	// 让插入新消息时失败（触发器拒绝内容为 "boom" 的行）。
	require.NoError(t, gdb.Exec(`CREATE TRIGGER reject_boom BEFORE INSERT ON conversation_messages
		WHEN NEW.content = 'boom' BEGIN SELECT RAISE(ABORT, 'insert rejected'); END`).Error)

	err = Conversation().SaveMessages(ctx, 1, 0, []*conversation_entity.Message{
		{Role: "user", Content: "新问题"},
		{Role: "assistant", Content: "boom"},
	})
	require.Error(t, err)

	got, err := Conversation().LoadMessages(ctx, 1)
	require.NoError(t, err)
	require.Len(t, got, 2, "failed insert must roll back the delete")
	require.Equal(t, "旧问题", got[0].Content)
	require.Equal(t, "旧回答", got[1].Content)
}
