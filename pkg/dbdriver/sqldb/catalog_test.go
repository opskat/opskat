package sqldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
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
