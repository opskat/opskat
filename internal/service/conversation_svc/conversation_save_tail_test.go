package conversation_svc

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/repository/conversation_repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// rowCounter 统计 conversation_messages 上实际删除 / 插入的行数。
type rowCounter struct{ deleted, inserted int64 }

func (c *rowCounter) reset() { c.deleted, c.inserted = 0, 0 }

// setupSQLiteConversation 用真实 SQLite 跑 service + repository，并挂上行数统计回调。
func setupSQLiteConversation(t *testing.T) *rowCounter {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "conversation.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	// 在 TempDir 清理之前关掉连接：Windows 上打开着的 SQLite 文件删不掉。
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.AutoMigrate(&conversation_entity.Message{}))

	counter := &rowCounter{}
	require.NoError(t, gdb.Callback().Create().After("gorm:create").Register("test:count_insert", func(tx *gorm.DB) {
		counter.inserted += tx.RowsAffected
	}))
	require.NoError(t, gdb.Callback().Delete().After("gorm:delete").Register("test:count_delete", func(tx *gorm.DB) {
		counter.deleted += tx.RowsAffected
	}))

	db.SetDefault(gdb)
	prevRepo := conversation_repo.Conversation()
	conversation_repo.RegisterConversation(conversation_repo.NewConversation())
	t.Cleanup(func() { conversation_repo.RegisterConversation(prevRepo) })
	return counter
}

func makeMessages(contents ...string) []*conversation_entity.Message {
	msgs := make([]*conversation_entity.Message, 0, len(contents))
	for i, c := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, &conversation_entity.Message{Role: role, Content: c})
	}
	return msgs
}

func longHistory(n int) []string {
	contents := make([]string, n)
	for i := range contents {
		contents[i] = fmt.Sprintf("msg-%03d", i)
	}
	return contents
}

// loadedView 把加载结果投影成 (sort_order, role, content)，用于比较两种保存方式的落库结果。
func loadedView(t *testing.T, convID int64) []string {
	t.Helper()
	got, err := Conversation().LoadMessages(t.Context(), convID)
	require.NoError(t, err)
	view := make([]string, len(got))
	for i, m := range got {
		view[i] = fmt.Sprintf("%d|%s|%s", m.SortOrder, m.Role, m.Content)
	}
	return view
}

// TestConversationSvc_SaveMessages_AppendWritesOnlyTail 守护 R17：长会话追加一条工具事件，
// 只写入尾部，不删除也不重写前面的消息。
func TestConversationSvc_SaveMessages_AppendWritesOnlyTail(t *testing.T) {
	counter := setupSQLiteConversation(t)
	ctx := t.Context()

	history := longHistory(200)
	require.NoError(t, Conversation().SaveMessages(ctx, 1, 0, makeMessages(history...)))
	counter.reset()

	full := append(append([]string{}, history...), "tool-event")
	require.NoError(t, Conversation().SaveMessages(ctx, 1, 200, makeMessages(full...)[200:]))
	t.Logf("append 1 to 200: deleted=%d inserted=%d", counter.deleted, counter.inserted)
	require.Equal(t, int64(0), counter.deleted, "appending must not delete earlier rows")
	require.Equal(t, int64(1), counter.inserted, "appending must insert only the tail")

	// 重新加载后的历史与全量保存一致。
	require.NoError(t, Conversation().SaveMessages(ctx, 2, 0, makeMessages(full...)))
	require.Equal(t, loadedView(t, 2), loadedView(t, 1))
}

// TestConversationSvc_SaveMessages_ReplaceFromMiddle 守护 R17：从中间编辑重发时，
// 从该位置起替换，原来更靠后的消息被删掉（新列表比旧列表短）。
func TestConversationSvc_SaveMessages_ReplaceFromMiddle(t *testing.T) {
	counter := setupSQLiteConversation(t)
	ctx := t.Context()

	require.NoError(t, Conversation().SaveMessages(ctx, 1, 0, makeMessages(longHistory(10)...)))
	counter.reset()

	edited := append(longHistory(10)[:4], "edited-question")
	require.NoError(t, Conversation().SaveMessages(ctx, 1, 4, makeMessages(edited...)[4:]))
	require.Equal(t, int64(6), counter.deleted, "rows from index 4 on are replaced")
	require.Equal(t, int64(1), counter.inserted)

	require.NoError(t, Conversation().SaveMessages(ctx, 2, 0, makeMessages(edited...)))
	require.Equal(t, loadedView(t, 2), loadedView(t, 1))
	require.Len(t, loadedView(t, 1), 5)
}

// TestConversationSvc_SaveMessages_TailDoesNotTouchOtherConversations 尾部替换只作用于本会话。
func TestConversationSvc_SaveMessages_TailDoesNotTouchOtherConversations(t *testing.T) {
	setupSQLiteConversation(t)
	ctx := t.Context()

	require.NoError(t, Conversation().SaveMessages(ctx, 1, 0, makeMessages("a", "b", "c")))
	require.NoError(t, Conversation().SaveMessages(ctx, 2, 0, makeMessages("x", "y", "z")))
	require.NoError(t, Conversation().SaveMessages(ctx, 1, 1, makeMessages("a", "B")[1:]))

	require.Equal(t, []string{"0|user|x", "1|assistant|y", "2|user|z"}, loadedView(t, 2))
	require.Equal(t, []string{"0|user|a", "1|assistant|B"}, loadedView(t, 1))
}
