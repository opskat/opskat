package ai

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/repository/conversation_repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChatSaveDB(t *testing.T) *AI {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.AutoMigrate(&conversation_entity.Message{}))

	db.SetDefault(gdb)
	prevRepo := conversation_repo.Conversation()
	conversation_repo.RegisterConversation(conversation_repo.NewConversation())
	t.Cleanup(func() { conversation_repo.RegisterConversation(prevRepo) })
	return &AI{ctx: context.Background(), lang: providerTestLang{}}
}

func displayMsgs(contents ...string) []ConversationDisplayMessage {
	out := make([]ConversationDisplayMessage, len(contents))
	for i, c := range contents {
		out[i] = ConversationDisplayMessage{
			Role:    "assistant",
			Content: c,
			Blocks:  []conversation_entity.ContentBlock{{Type: "text", Content: c}},
		}
	}
	return out
}

func loadedContents(t *testing.T, a *AI, convID int64) []string {
	t.Helper()
	got, err := a.loadConversationDisplayMessages(context.Background(), convID)
	require.NoError(t, err)
	out := make([]string, len(got))
	for i, m := range got {
		out[i] = m.Content
		require.Equal(t, []conversation_entity.ContentBlock{{Type: "text", Content: m.Content}}, m.Blocks)
	}
	return out
}

// SaveConversationMessages 的 from 是「从哪个位置起替换」：只发尾部即可续写，重载结果与全量保存一致。
func TestSaveConversationMessagesReplacesFromIndex(t *testing.T) {
	a := setupChatSaveDB(t)

	require.NoError(t, a.SaveConversationMessages(1, 0, displayMsgs("q1", "a1", "q2")))
	require.NoError(t, a.SaveConversationMessages(1, 3, displayMsgs("tool-event")))
	require.Equal(t, []string{"q1", "a1", "q2", "tool-event"}, loadedContents(t, a, 1))

	// 中间编辑重发：从 1 起替换，后面的旧消息被删掉。
	require.NoError(t, a.SaveConversationMessages(1, 1, displayMsgs("a1-edited")))
	require.Equal(t, []string{"q1", "a1-edited"}, loadedContents(t, a, 1))
}

// IPC 是边界：负的 from 直接拒绝，不能落到 `sort_order >= -1` 把整段历史删掉后写出负序号。
func TestSaveConversationMessagesRejectsNegativeFrom(t *testing.T) {
	a := setupChatSaveDB(t)

	require.NoError(t, a.SaveConversationMessages(1, 0, displayMsgs("q1", "a1")))
	require.Error(t, a.SaveConversationMessages(1, -1, displayMsgs("x")))
	require.Equal(t, []string{"q1", "a1"}, loadedContents(t, a, 1))
}
