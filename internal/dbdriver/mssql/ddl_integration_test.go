//go:build integration

package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 需要一个可随意建库的 SQL Server：E2E_MSSQL_HOST / PORT / USER / PASSWORD。
func openDDLDatabase(t *testing.T, database string) *sql.DB {
	t.Helper()
	host := os.Getenv("E2E_MSSQL_HOST")
	if host == "" {
		t.Skip("E2E_MSSQL_HOST 未设置，跳过集成测试")
	}
	port, err := strconv.Atoi(os.Getenv("E2E_MSSQL_PORT"))
	require.NoError(t, err)
	cfg := &dbdriver.Config{Host: host, Port: port, Username: os.Getenv("E2E_MSSQL_USER"), Database: database}
	db, err := open(dsn(cfg, os.Getenv("E2E_MSSQL_PASSWORD")), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createScratchDatabase(t *testing.T, master *sql.DB, name string) {
	t.Helper()
	_, err := master.Exec("CREATE DATABASE " + quoteIdent(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = master.Exec("ALTER DATABASE " + quoteIdent(name) + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE")
		_, err := master.Exec("DROP DATABASE " + quoteIdent(name))
		assert.NoError(t, err)
	})
}

const ddlSourceTable = `CREATE TABLE [sales].[orders] (
  [id] int IDENTITY(100, 5) NOT NULL,
  [code] nvarchar(20) NOT NULL CONSTRAINT [DF_code] DEFAULT (N'N/A'),
  [note] nvarchar(max) NULL,
  [raw] varbinary(16) NULL,
  [price] decimal(18, 4) NOT NULL DEFAULT ((0)),
  [at] datetime2(3) NULL,
  [ratio] float(24) NULL,
  CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id]),
  CONSTRAINT [UQ_orders_code] UNIQUE NONCLUSTERED ([code], [price] DESC),
  CONSTRAINT [CK_orders_price] CHECK ([price] >= 0)
);
CREATE NONCLUSTERED INDEX [IX_orders_at] ON [sales].[orders] ([at] DESC) INCLUDE ([price]) WHERE ([at] IS NOT NULL);`

// 在一个临时库里建表，取回 TableDDL，到另一个空的临时库里原样运行，两边的 TableDDL 必须一致，
// 并且列精度、IDENTITY、约束与索引确实被还原。
func TestIntegrationTableDDLRestoresOnAnEmptyDatabase(t *testing.T) {
	master := openDDLDatabase(t, "master")
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_ddl_src_%d", suffix), fmt.Sprintf("opskat_ddl_dst_%d", suffix)
	createScratchDatabase(t, master, source)
	createScratchDatabase(t, master, target)

	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	for _, db := range []*sql.DB{sourceDB, targetDB} {
		_, err := db.Exec("CREATE SCHEMA [sales]")
		require.NoError(t, err)
	}
	_, err := sourceDB.Exec(ddlSourceTable)
	require.NoError(t, err)

	ctx := context.Background()
	ddl, err := newSession(sourceDB).TableDDL(ctx, source, "sales.orders")
	require.NoError(t, err)
	_, err = targetDB.Exec(ddl)
	require.NoError(t, err, "TableDDL output must run on an empty database:\n%s", ddl)

	restored, err := newSession(targetDB).TableDDL(ctx, target, "sales.orders")
	require.NoError(t, err)
	assert.Equal(t, ddl, restored)

	assert.Contains(t, ddl, "[price] decimal(18, 4) NOT NULL DEFAULT ((0))")
	assert.Contains(t, ddl, "[id] int IDENTITY(100, 5) NOT NULL")
	assert.Contains(t, ddl, "[note] nvarchar(max) NULL")
	assert.Contains(t, ddl, "CONSTRAINT [PK_orders] PRIMARY KEY CLUSTERED ([id])")
	assert.Contains(t, ddl, "CONSTRAINT [UQ_orders_code] UNIQUE NONCLUSTERED ([code], [price] DESC)")
	assert.Contains(t, ddl, "CONSTRAINT [CK_orders_price] CHECK")
	assert.Contains(t, ddl, "INCLUDE ([price]) WHERE ([at] IS NOT NULL);")
}
