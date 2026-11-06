package sqldb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 列定义里 DEFAULT 写在 NOT NULL 之前：Oracle 只接受这个次序（反过来报 ORA-00907），其余方言两种
// 次序都接受。
func TestColumnDefinitionWritesDefaultBeforeNotNull(t *testing.T) {
	session, _ := newTablesSession(t)
	statements, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "int", Nullable: false},
			{Name: "name", Type: " text ", Nullable: false, DefaultValue: "anon"},
			{Name: "memo", Type: "text", Nullable: true, DefaultValue: "x"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE <db>.<t> (\n" +
		"  <id> int NOT NULL,\n" +
		"  <name> text DEFAULT 'anon' NOT NULL,\n" +
		"  <memo> text DEFAULT 'x'\n)"}, statements)
}
