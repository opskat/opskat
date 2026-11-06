package query_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表元数据按结构体转换复制：驱动加上索引的 INCLUDE 列与外键的 ON UPDATE / ON DELETE 之后，
// 绑定返回的形状也一并带上（dbdriver 加字段必须在这里同步）。
func TestNewTableMetadataCarriesIndexIncludesAndForeignKeyActions(t *testing.T) {
	got := NewTableMetadata(&dbdriver.TableMetadata{
		Columns: []dbdriver.ColumnMeta{{Name: "id"}},
		Indexes: []dbdriver.IndexMeta{{
			Name: "ix", Columns: []string{"id"}, Unique: true, Include: []string{"name", "note"},
		}},
		ForeignKeys: []dbdriver.ForeignKeyMeta{{
			Name: "fk", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id",
			OnUpdate: "CASCADE", OnDelete: "SET NULL",
		}},
	})
	require.Len(t, got.Indexes, 1)
	assert.Equal(t, []string{"name", "note"}, got.Indexes[0].Include)
	require.Len(t, got.ForeignKeys, 1)
	assert.Equal(t, ForeignKeyMeta{
		Name: "fk", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id",
		OnUpdate: "CASCADE", OnDelete: "SET NULL",
	}, got.ForeignKeys[0])
}
