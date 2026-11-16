//go:build integration

package postgresql

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

// 需要一个可随意建库的 PostgreSQL：E2E_POSTGRES_HOST / PORT / USER / PASSWORD / DATABASE。
func openDDLDatabase(t *testing.T, database string) *sql.DB {
	t.Helper()
	host := os.Getenv("E2E_POSTGRES_HOST")
	if host == "" {
		t.Skip("E2E_POSTGRES_HOST 未设置，跳过集成测试")
	}
	port, err := strconv.Atoi(os.Getenv("E2E_POSTGRES_PORT"))
	require.NoError(t, err)
	cfg := &dbdriver.Config{Host: host, Port: port, Username: os.Getenv("E2E_POSTGRES_USER"), Database: database}
	db, err := sql.Open("pgx", dsn(cfg, os.Getenv("E2E_POSTGRES_PASSWORD")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createScratchDatabase(t *testing.T, admin *sql.DB, name string) {
	t.Helper()
	_, err := admin.Exec("CREATE DATABASE " + quoteIdent(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE " + quoteIdent(name))
		assert.NoError(t, err)
	})
}

// 源表覆盖建表语句要还原的各项：serial 拥有的序列、带参数的身份列、生成列、长度 / 精度、默认值、非空、
// 主键、唯一、CHECK、表达式与部分索引、表与列注释（含单引号）。
const ddlSourceTable = `CREATE TABLE reporting.events (
  id serial PRIMARY KEY,
  ident bigint GENERATED ALWAYS AS IDENTITY (START WITH 10 INCREMENT BY 5),
  "Mixed Code" varchar(20) NOT NULL UNIQUE,
  price numeric(12, 2) DEFAULT 0 CHECK (price >= 0),
  total numeric GENERATED ALWAYS AS (price * 2) STORED,
  happened_at timestamp(3) with time zone DEFAULT now(),
  note text,
  CONSTRAINT events_code_price UNIQUE ("Mixed Code", price)
);
CREATE INDEX events_note_lower ON reporting.events (lower(note)) WHERE note IS NOT NULL;
CREATE INDEX events_happened ON reporting.events USING btree (happened_at DESC);
COMMENT ON TABLE reporting.events IS 'it''s events';
COMMENT ON COLUMN reporting.events."Mixed Code" IS 'the code';`

// fingerprint 从系统目录读出这张表的结构（不经 TableDDL），两个库的指纹相同就说明结构被还原了。
const fingerprintQuery = `SELECT 'column ' || ROW(column_name, data_type, character_maximum_length, numeric_precision,
    numeric_scale, datetime_precision, is_nullable, column_default, is_identity, identity_generation, identity_start,
    identity_increment, identity_minimum, identity_maximum, identity_cycle, is_generated, generation_expression,
    col_description('reporting.events'::regclass, ordinal_position::int))::text
  FROM information_schema.columns WHERE table_schema = 'reporting' AND table_name = 'events'
UNION ALL SELECT 'generated ' || a.attname || ' ' || coalesce(to_jsonb(a) ->> 'attgenerated', '')
  FROM pg_attribute a WHERE a.attrelid = 'reporting.events'::regclass AND a.attnum > 0
UNION ALL SELECT 'constraint ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE conrelid = 'reporting.events'::regclass AND contype <> 'n'
UNION ALL SELECT 'index ' || pg_get_indexdef(indexrelid) FROM pg_index WHERE indrelid = 'reporting.events'::regclass
UNION ALL SELECT 'sequence ' || ROW(sequence_schema, sequence_name, data_type, start_value, minimum_value,
    maximum_value, increment, cycle_option)::text FROM information_schema.sequences
UNION ALL SELECT 'owned ' || coalesce(pg_get_serial_sequence('reporting.events', 'id'), '')
UNION ALL SELECT 'comment ' || coalesce(obj_description('reporting.events'::regclass, 'pg_class'), '')
ORDER BY 1`

func fingerprint(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(fingerprintQuery)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())
	return lines
}

// 在一个临时库里建表，取回 TableDDL，到另一个空的临时库里原样运行：两边的表结构（从系统目录读）一致，
// 并且序列、身份列、唯一与 CHECK 在新库里照样生效。
func TestIntegrationTableDDLRestoresOnAnEmptyDatabase(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_ddl_src_%d", suffix), fmt.Sprintf("opskat_ddl_dst_%d", suffix)
	createScratchDatabase(t, admin, source)
	createScratchDatabase(t, admin, target)

	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	for _, db := range []*sql.DB{sourceDB, targetDB} {
		_, err := db.Exec("CREATE SCHEMA reporting")
		require.NoError(t, err)
	}
	_, err := sourceDB.Exec(ddlSourceTable)
	require.NoError(t, err)
	var version int
	require.NoError(t, sourceDB.QueryRow("SELECT current_setting('server_version_num')::int").Scan(&version))
	if version >= 180000 { // 虚拟生成列从 PostgreSQL 18 起才有
		_, err = sourceDB.Exec("ALTER TABLE reporting.events ADD COLUMN doubled int GENERATED ALWAYS AS (id * 2) VIRTUAL")
		require.NoError(t, err)
	}

	ctx := context.Background()
	ddl, err := newSession(sourceDB).TableDDL(ctx, source, "reporting.events")
	require.NoError(t, err)
	t.Log(ddl)

	_, err = targetDB.Exec(ddl)
	require.NoError(t, err, "the DDL must run on an empty database")
	assert.Equal(t, fingerprint(t, sourceDB), fingerprint(t, targetDB))

	restored, err := newSession(targetDB).TableDDL(ctx, target, "reporting.events")
	require.NoError(t, err)
	assert.Equal(t, ddl, restored)

	var id, ident int64
	require.NoError(t, targetDB.QueryRow(`INSERT INTO reporting.events ("Mixed Code", price) VALUES ('a', 1) `+
		"RETURNING id, ident").Scan(&id, &ident))
	assert.Equal(t, []int64{1, 10}, []int64{id, ident})
	_, err = targetDB.Exec(`INSERT INTO reporting.events ("Mixed Code", price) VALUES ('a', 2)`)
	assert.ErrorContains(t, err, "events_Mixed Code_key")
	_, err = targetDB.Exec(`INSERT INTO reporting.events ("Mixed Code", price) VALUES ('b', -1)`)
	assert.ErrorContains(t, err, "events_price_check")
}
