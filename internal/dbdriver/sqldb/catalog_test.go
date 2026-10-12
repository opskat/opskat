package sqldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
)

// 方言没有提供目录查询时，浏览操作明确报「不支持」，且不碰数据库（mock 没有预期任何语句）。
func TestBrowsingWithoutACatalogIsUnsupported(t *testing.T) {
	session, mock := newMockSession(t)
	ctx := context.Background()

	_, err := session.ListDatabases(ctx)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ListTables(ctx, "db")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.TableMetadata(ctx, "db", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ListObjects(ctx, "db")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.ObjectSource(ctx, "db", "view", "v", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSplitSchemaTable(t *testing.T) {
	for table, want := range map[string][2]string{
		"events":           {"public", "events"},
		"reporting.events": {"reporting", "events"},
		".events":          {"public", "events"},
		"a.b.events":       {"a", "events"},
		"":                 {"public", ""},
	} {
		schema, name := SplitSchemaTable(table, "public")
		assert.Equal(t, want, [2]string{schema, name}, "table %q", table)
	}
}
