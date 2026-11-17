//go:build integration

package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// partitionSourceSQL 是分区表与继承表的样例库：readings 按日期分区（readings_2025；readings_2026 再按地区分区，有
// eu 与默认分区；默认分区 readings_other 在另一个 schema 里），父表上定义了主键、唯一索引、普通索引、CHECK 与外键，
// readings_2025 上有一个只建在它上面的索引，readings_2026 上有一个只建在它与它的分区上的索引；a_readers 的外键引用
// 分区表（数据库为每个分区各建一份）。notes 有两个继承的子表：a_notes_archive 多一列，notes_draft 没有自己的列。
// 分区与子表的名字有排在父表前面的，每张表（含父表）都有自己的行。
const partitionSourceSQL = `CREATE SCHEMA archive;
CREATE TABLE public.lookup (id integer PRIMARY KEY, label text);
INSERT INTO public.lookup VALUES (1, 'one'), (2, 'two');
CREATE TABLE public.readings (
  id integer NOT NULL,
  created date NOT NULL,
  region text NOT NULL,
  code text,
  lookup_id integer REFERENCES public.lookup (id),
  PRIMARY KEY (id, created, region),
  CONSTRAINT readings_id_check CHECK (id > 0)
) PARTITION BY RANGE (created);
CREATE UNIQUE INDEX readings_code ON public.readings (code, created, region);
CREATE INDEX readings_region ON public.readings (region);
CREATE TABLE public.readings_2025 PARTITION OF public.readings FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
CREATE TABLE public.readings_2026 PARTITION OF public.readings FOR VALUES FROM ('2026-01-01') TO ('2027-01-01') PARTITION BY LIST (region);
CREATE TABLE public.readings_2026_eu PARTITION OF public.readings_2026 FOR VALUES IN ('eu');
CREATE TABLE public.readings_2026_rest PARTITION OF public.readings_2026 DEFAULT;
CREATE TABLE archive.readings_other PARTITION OF public.readings DEFAULT;
CREATE INDEX readings_2025_lookup_only ON public.readings_2025 (lookup_id);
CREATE INDEX readings_2026_code_lower ON public.readings_2026 (lower(code));
CREATE TABLE public.a_readers (id integer, created date, region text,
  CONSTRAINT a_readers_reading_fk FOREIGN KEY (id, created, region) REFERENCES public.readings (id, created, region));
INSERT INTO public.readings VALUES
  (1, '2025-03-01', 'eu', 'a', 1), (2, '2025-04-01', 'us', 'b', NULL),
  (3, '2026-02-01', 'eu', 'c', 2), (4, '2026-02-02', 'eu', NULL, NULL), (5, '2026-05-01', 'us', 'e', 1),
  (6, '2031-01-01', 'eu', 'f', NULL);
INSERT INTO public.a_readers VALUES (1, '2025-03-01', 'eu'), (5, '2026-05-01', 'us');
CREATE TABLE public.notes (id serial PRIMARY KEY, body text NOT NULL, CONSTRAINT notes_body_check CHECK (body <> ''));
CREATE INDEX notes_body ON public.notes (body);
CREATE TABLE public.a_notes_archive (archived_at date) INHERITS (public.notes);
CREATE TABLE public.notes_draft () INHERITS (public.notes);
INSERT INTO public.notes (body) VALUES ('kept'), ('also kept');
INSERT INTO public.a_notes_archive (body, archived_at) VALUES ('old', '2026-01-01');
INSERT INTO public.notes_draft (body) VALUES ('draft one'), ('draft two'), ('draft three');`

// familyStructureQuery 读分区与继承的结构：pg_inherits 里每一对（表与索引都有）、分区的分区范围，以及分区表的分区键。
const familyStructureQuery = `SELECT 'inherits ' || h.inhrelid::regclass::text || ' -> ' || h.inhparent::regclass::text || ' ' ||
    c.relkind::text || ' ' || coalesce(pg_get_expr(c.relpartbound, c.oid), '')
  FROM pg_inherits h JOIN pg_class c ON c.oid = h.inhrelid
UNION ALL SELECT 'partition key ' || c.oid::regclass::text || ' ' || pg_get_partkeydef(c.oid) FROM pg_class c WHERE c.relkind = 'p'
ORDER BY 1`

// ownRows 是每张表自己的行数（ONLY，不含子表的行）与连子表一起的行数。
func ownRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	tables := queryLines(t, db, "SELECT c.oid::regclass::text FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace "+
		"WHERE c.relkind IN ('r', 'p') AND n.nspname "+userSchemas+" ORDER BY 1")
	lines := make([]string, 0, len(tables))
	for _, table := range tables {
		var own, all int
		require.NoError(t, db.QueryRow("SELECT (SELECT count(*) FROM ONLY "+table+"), (SELECT count(*) FROM "+table+")").Scan(&own, &all))
		lines = append(lines, fmt.Sprintf("%s own %d all %d", table, own, all))
	}
	return lines
}

// partitionSnapshot 是比对还原结果时看的全部：结构（列、约束、索引、序列、注释）、分区与继承的结构、每张表自己的行数
// 与连子表的行数、每张表（连子表）的行数与数据校验和。
func partitionSnapshot(t *testing.T, db *sql.DB) map[string][]string {
	t.Helper()
	return map[string][]string{
		"structure": queryLines(t, db, structureQuery),
		"family":    queryLines(t, db, familyStructureQuery),
		"rows":      ownRows(t, db),
		"data":      tableData(t, db),
	}
}

func dumpPartitionSource(t *testing.T, db *sql.DB, req dbdriver.DumpRequest) (string, *dbdriver.DumpResult) {
	t.Helper()
	var out strings.Builder
	req.Writer = &out
	req.Header = dbdriver.DumpHeader{Version: "test", Asset: "pg", Time: time.Now()}
	result, err := newSession(db).Dump(context.Background(), req)
	require.NoError(t, err)
	t.Log(out.String())
	return out.String(), result
}

// 分区表与继承表整库转储，运行到另一个名字的空库：每条语句都成功；分区结构（含多级分区、默认分区、另一个 schema
// 里的分区）、继承关系、约束与索引（父表上定义的随之建到分区上，只建在分区上的照样在）、每张表自己的行数与总行数、
// 数据都与源库相同，没有重复的行；勾了「写入删除语句」时，文件运行回源库本身也成功，结果不变。
func TestIntegrationDumpRestoresPartitionedAndInheritedTables(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_dump_part_src_%d", suffix), fmt.Sprintf("opskat_dump_part_dst_%d", suffix)
	createScratchDatabase(t, admin, source)
	createScratchDatabase(t, admin, target)
	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	_, err := sourceDB.Exec(partitionSourceSQL)
	require.NoError(t, err)
	want := partitionSnapshot(t, sourceDB)
	require.Contains(t, want["rows"], "readings own 0 all 6")
	require.Contains(t, want["rows"], "notes own 2 all 6")

	t.Run("into an empty database", func(t *testing.T) {
		script, dumped := dumpPartitionSource(t, sourceDB, dbdriver.DumpRequest{
			Database: source, Whole: true, WithData: true, Options: structureOptions,
		})
		assert.EqualValues(t, 2+6+2+6, dumped.Rows, "every row is written once")
		assert.NotContains(t, script, "ON ONLY", "indexes on partitioned tables are created on their partitions too")
		runDump(t, targetDB, target, script)
		assert.Equal(t, want, partitionSnapshot(t, targetDB))
		assert.Equal(t, nextValues(t, sourceDB), nextValues(t, targetDB))
	})
	t.Run("over the original database", func(t *testing.T) {
		before := partitionSnapshot(t, sourceDB) // 上一步取下一个值时推进了序列
		options := structureOptions
		options.Drops = true
		script, _ := dumpPartitionSource(t, sourceDB, dbdriver.DumpRequest{Database: source, Whole: true, WithData: true, Options: options})
		runDump(t, sourceDB, source, script)
		assert.Equal(t, before, partitionSnapshot(t, sourceDB))
	})
}

// 只转储选中的分区表父表：它的全部分区（一路下去，含另一个 schema 里的默认分区）随它写出，文件在空库上每条语句都
// 成功，得出同样的分区结构与每个分区的行；继承的子表不随父表带上。单独选中一个分区或继承的子表时写成 PARTITION OF /
// INHERITS，目标库里有父表才建得出。
func TestIntegrationDumpOfASelectedPartitionedTableBringsItsPartitions(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_dump_psel_src_%d", suffix), fmt.Sprintf("opskat_dump_psel_dst_%d", suffix)
	createScratchDatabase(t, admin, source)
	createScratchDatabase(t, admin, target)
	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	_, err := sourceDB.Exec(partitionSourceSQL)
	require.NoError(t, err)
	options := dbdriver.DumpOptions{Indexes: true}

	script, dumped := dumpPartitionSource(t, sourceDB, dbdriver.DumpRequest{
		Database: source, Tables: []string{"public.readings", "public.notes"}, WithData: true, Options: options,
	})
	assert.Equal(t, 2+5, dumped.Objects, "readings brings its five partitions; notes brings no inherited table")
	runDump(t, targetDB, target, script)
	readingsFamily := func(db *sql.DB) []string {
		var lines []string
		for _, line := range queryLines(t, db, familyStructureQuery) {
			if strings.Contains(line, "readings") {
				lines = append(lines, line)
			}
		}
		return lines
	}
	assert.Equal(t, readingsFamily(sourceDB), readingsFamily(targetDB))
	assert.Equal(t, []string{
		"archive.readings_other own 1 all 1", "notes own 2 all 2", "readings own 0 all 6", "readings_2025 own 2 all 2",
		"readings_2026 own 0 all 3", "readings_2026_eu own 2 all 2", "readings_2026_rest own 1 all 1",
	}, ownRows(t, targetDB))

	t.Run("a single partition and a single inherited table", func(t *testing.T) {
		script, _ := dumpPartitionSource(t, sourceDB, dbdriver.DumpRequest{
			Database: source, Tables: []string{"public.readings_2026_eu", "public.a_notes_archive"}, Options: options,
		})
		assert.Contains(t, script, `CREATE TABLE "public"."readings_2026_eu" PARTITION OF "public"."readings_2026" FOR VALUES IN ('eu');`)
		assert.Contains(t, script, `CREATE TABLE "public"."a_notes_archive" (`+"\n"+`  "archived_at" date`+"\n"+`) INHERITS ("public"."notes");`)
		assert.NotContains(t, script, `CREATE TABLE "public"."readings" `)
	})
}

// 分区表有一个外部表分区（postgres_fdw 分片的写法）：外部表不在转储里（ListTables 不列出它），分区表照样转储——整库与
// 只选中父表时都成功，本地分区与它们的行写出、运行到空库每条语句都成功，外部表分区不读、不写。这里的外部数据包装器
// 没有处理函数，读外部表就会出错。
func TestIntegrationDumpSkipsAForeignTablePartition(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	suffix := time.Now().UnixNano()
	source, target := fmt.Sprintf("opskat_dump_pfdw_src_%d", suffix), fmt.Sprintf("opskat_dump_pfdw_dst_%d", suffix)
	createScratchDatabase(t, admin, source)
	createScratchDatabase(t, admin, target)
	sourceDB, targetDB := openDDLDatabase(t, source), openDDLDatabase(t, target)
	_, err := sourceDB.Exec(`CREATE FOREIGN DATA WRAPPER opskat_no_handler;
CREATE SERVER opskat_remote FOREIGN DATA WRAPPER opskat_no_handler;
CREATE TABLE public.metrics (id integer NOT NULL, at date NOT NULL) PARTITION BY RANGE (at);
CREATE TABLE public.metrics_local PARTITION OF public.metrics FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');
CREATE FOREIGN TABLE public.metrics_remote PARTITION OF public.metrics FOR VALUES FROM ('2025-01-01') TO ('2026-01-01') SERVER opskat_remote;
INSERT INTO public.metrics_local VALUES (1, '2026-02-01'), (2, '2026-03-01');`)
	require.NoError(t, err)

	for _, req := range []dbdriver.DumpRequest{
		{Database: source, Whole: true, WithData: true, Options: structureOptions},
		{Database: source, Tables: []string{"public.metrics"}, WithData: true, Options: structureOptions},
	} {
		script, dumped := dumpPartitionSource(t, sourceDB, req)
		assert.Equal(t, 2, dumped.Objects, "the parent and its local partition")
		assert.EqualValues(t, 2, dumped.Rows)
		assert.NotContains(t, script, "metrics_remote")
	}
	script, _ := dumpPartitionSource(t, sourceDB, dbdriver.DumpRequest{Database: source, Whole: true, WithData: true, Options: structureOptions})
	runDump(t, targetDB, target, script)
	assert.Equal(t, []string{"metrics own 0 all 2", "metrics_local own 2 all 2"}, ownRows(t, targetDB))
}
