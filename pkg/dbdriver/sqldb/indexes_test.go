package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// indexerDialect 在 designerDialect 之上开启索引编辑：索引写成一眼能与真实方言区分的语句，收到的改动留在
// changes 里。declared 非 nil 时用它顶替默认的声明，好试未声明的能力。
type indexerDialect struct {
	designerDialect
	declared *dbdriver.IndexEditing
	changes  []TableChange
}

// indexEditingDeclaration 是测试方言的索引编辑声明：主键、唯一、普通三个类型，主键可改、可降序、可前缀、
// 可注释，名称范围是 schema，方法枚举与聚集开关两个额外选项。
func indexEditingDeclaration() *dbdriver.IndexEditing {
	return &dbdriver.IndexEditing{
		Types: []dbdriver.IndexTypeDecl{
			{ID: dbdriver.IndexTypePrimary, Label: "indexType.primary"},
			{ID: dbdriver.IndexTypeUnique, Label: "indexType.unique"},
			{ID: dbdriver.IndexTypeNormal, Label: "indexType.normal"},
		},
		AlterPrimaryKey: true, Descending: true, PrefixLength: true, Comments: true,
		NameScope: dbdriver.IndexScopeSchema,
		Options: []dbdriver.IndexOptionDecl{
			{Key: "method", Label: "indexOption.method", Type: dbdriver.FieldTypeEnum, Enum: []string{"btree", "hash"}},
			{Key: "clustered", Label: "indexOption.clustered", Type: dbdriver.FieldTypeSwitch},
		},
	}
}

func (d *indexerDialect) IndexEditing() *dbdriver.IndexEditing {
	if d.declared != nil {
		return d.declared
	}
	return indexEditingDeclaration()
}

func indexColumnsOf(index dbdriver.IndexDefinition) string {
	columns := make([]string, len(index.Columns))
	for i, column := range index.Columns {
		name := column.Name
		if !column.Expression {
			name = angle(name)
		}
		if column.Descending {
			name += " DESC"
		}
		if column.PrefixLength > 0 {
			name += fmt.Sprintf("(%d)", column.PrefixLength)
		}
		columns[i] = name
	}
	return strings.Join(columns, ", ")
}

func (indexerDialect) IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error) {
	return "CREATE " + strings.ToUpper(string(index.Type)) + " INDEX " + angle(index.Name) + " ON " + tableRef +
		" (" + indexColumnsOf(index) + ")", nil
}

func (indexerDialect) DropIndexSQL(tableRef, name string) (string, error) {
	return "DROP INDEX " + angle(name) + " ON " + tableRef, nil
}

// 建表之后的注释也由方言写：表注释一条，列注释每列一条。
func (indexerDialect) TableCommentSQL(tableRef, comment string) (string, error) {
	return "COMMENT TABLE " + tableRef + " = " + comment, nil
}

func (indexerDialect) ColumnCommentSQL(tableRef string, column dbdriver.ColumnDefinition) (string, error) {
	return "COMMENT COLUMN " + tableRef + "." + angle(column.Name) + " = " + column.Comment, nil
}

func (d *indexerDialect) AlterAttributes() []dbdriver.AlterAttribute {
	return []dbdriver.AlterAttribute{dbdriver.AlterTableComment, dbdriver.AlterColumnType, dbdriver.AlterColumnComment}
}

// indexTable 是索引编辑测试里读出的表：列同 alterTable，另有一份各不相同的索引与范围内已占用的名字。
func indexTable() *AlterableTable {
	table := alterTable()
	table.Indexes = []dbdriver.IndexDefinition{
		{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
		{Name: "ux_t_name", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "name"}}},
		{Name: "ix_t_at", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "at", Descending: true}},
			Options: map[string]any{"method": "btree"}},
		{Name: "ix_t_note", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "note"}},
			ReadOnlyReason: "partial index"},
		{Name: "ux_t_auto", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Undroppable: true},
	}
	table.TakenIndexNames = []string{"ix_other_table_name"}
	return table
}

func (d *indexerDialect) AlterableTable(context.Context, *sql.Conn, string, string) (*AlterableTable, error) {
	return indexTable(), nil
}

// AlterTableSQL 照方言的写法：先改表名，再删索引，然后删列与新增列，最后建索引。
func (d *indexerDialect) AlterTableSQL(_ context.Context, _ *sql.Conn, change TableChange) ([]string, error) {
	d.changes = append(d.changes, change)
	var statements []string
	if change.Rename != "" {
		statements = append(statements, "RENAME "+change.Target)
	}
	drops, err := IndexDropStatements(d, change.TargetRef, change)
	if err != nil {
		return nil, err
	}
	statements = append(statements, drops...)
	for _, drop := range change.Drops {
		statements = append(statements, "DROP COLUMN "+drop)
	}
	for _, column := range change.Columns {
		if column.IsNew() {
			statements = append(statements, "ADD "+column.Column.Name)
		}
	}
	creates, err := IndexCreateStatements(d, change.TargetRef, change)
	if err != nil {
		return nil, err
	}
	return append(statements, creates...), nil
}

// creatorDialect 是另能整条写建表语句的方言。
type creatorDialect struct {
	indexerDialect
	createRequests []dbdriver.CreateTableRequest
}

func (d *creatorDialect) CreateTableSQL(_ Tables, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	d.createRequests = append(d.createRequests, req)
	statements := []string{"CREATOR " + angle(database+"."+req.Name)}
	for _, index := range req.Indexes {
		statement, err := d.IndexSQL("ref", index)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

// plainIndexDialect 开启索引编辑，但不会写注释：它用共用的建表写法。
type plainIndexDialect struct {
	designerDialect
	declared *dbdriver.IndexEditing
}

func (d plainIndexDialect) IndexEditing() *dbdriver.IndexEditing {
	if d.declared != nil {
		return d.declared
	}
	return indexEditingDeclaration()
}

func (plainIndexDialect) IndexSQL(tableRef string, index dbdriver.IndexDefinition) (string, error) {
	return "CREATE " + strings.ToUpper(string(index.Type)) + " INDEX " + angle(index.Name) + " ON " + tableRef +
		" (" + indexColumnsOf(index) + ")", nil
}

func (plainIndexDialect) DropIndexSQL(tableRef, name string) (string, error) {
	return "DROP INDEX " + angle(name) + " ON " + tableRef, nil
}

func newIndexSession(t *testing.T, dialect Dialect) *Session {
	t.Helper()
	db, _, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, dialect)
}

// indexModelRequest 把读出的表（列与索引）原样交回。
func indexModelRequest(model *dbdriver.AlterTableModel) dbdriver.AlterTableRequest {
	req := modelRequest(model)
	for _, index := range model.Indexes {
		req.Indexes = append(req.Indexes, dbdriver.AlterIndex{OriginalName: index.Name, IndexDefinition: index})
	}
	return req
}

// 索引编辑的声明与实现只有一个来源：方言实现了 TableIndexer 才有声明，没实现的方言没有。
func TestIndexEditingComesFromTheDialect(t *testing.T) {
	assert.False(t, IndexesTables(tablesDialect{}))
	assert.Nil(t, IndexEditing(tablesDialect{}))
	assert.True(t, IndexesTables(&indexerDialect{}))
	assert.Equal(t, indexEditingDeclaration(), IndexEditing(&indexerDialect{}))
}

// 没开启索引编辑的方言：带索引的建表请求在生成任何语句之前以 ErrUnsupported 拒绝，不碰数据库。
func TestCreateTableRefusesUndeclaredIndexes(t *testing.T) {
	session, mock := newTablesSession(t)
	index := dbdriver.IndexDefinition{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}}
	_, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "int"}}, Indexes: []dbdriver.IndexDefinition{index},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "pk_t")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 声明了索引编辑、但请求里的索引用到没声明的能力（这里：没声明的类型）时，同样在生成任何语句之前拒绝。
func TestCreateTableRefusesIndexFeaturesTheDialectLacks(t *testing.T) {
	undeclared := indexEditingDeclaration()
	undeclared.Types = []dbdriver.IndexTypeDecl{{ID: dbdriver.IndexTypeNormal, Label: "indexType.normal"}}
	session, mock := newTablesSession(t)
	session.dialect = plainIndexDialect{declared: undeclared}
	_, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "int"}},
		Indexes: []dbdriver.IndexDefinition{{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "pk_t")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 方言写不了建表时的注释（没有 SchemaCommenter，也不能整条写建表语句）时，带注释的请求以 ErrUnsupported
// 拒绝，而不是悄悄丢掉。
func TestCreateTableRefusesCommentsTheDialectCannotWrite(t *testing.T) {
	session, mock := newTablesSession(t)
	session.dialect = plainIndexDialect{}
	column := dbdriver.NewColumn{Name: "id", Type: "int"}

	_, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{column}, Comment: "people",
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "table comment")

	column.Comment = "the id"
	_, err = session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{Name: "t", Columns: []dbdriver.NewColumn{column}})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "comment")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 方言能整条写建表语句：列、索引与注释一并交给它，请求不碰数据库。
func TestCreateTableHandsTheWholeRequestToTheDialect(t *testing.T) {
	dialect := &creatorDialect{}
	statements, err := NewSession(nil, dialect).CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Comment: "people",
		Columns: []dbdriver.NewColumn{{Name: "id", Type: "int", Comment: "the id"}, {Name: "name", Type: "text", Nullable: true}},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "ux_t_name", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "name"}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATOR <db.t>",
		"CREATE PRIMARY INDEX <pk_t> ON ref (<id>)",
		"CREATE UNIQUE INDEX <ux_t_name> ON ref (<name>)",
	}, statements)
	require.Len(t, dialect.createRequests, 1)
	assert.Equal(t, "people", dialect.createRequests[0].Comment)
	assert.Equal(t, "the id", dialect.createRequests[0].Columns[0].Comment, "列注释由方言自己写")
}

// 方言不能整条写建表语句时用共用的写法：CREATE TABLE，随后按请求的次序建索引，再写注释（表注释在前，
// 列注释按列的次序）。
func TestCreateTableWritesIndexesAndCommentsAfterTheTable(t *testing.T) {
	session, mock := newTablesSession(t)
	session.dialect = &indexerDialect{designerDialect: designerDialect{tablesDialect{}}}
	statements, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name: "t", Comment: "people",
		Columns: []dbdriver.NewColumn{{Name: "id", Type: "int", Comment: "the id"}, {Name: "name", Type: "text", Nullable: true}},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "ux_t_name", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "name", PrefixLength: 8}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE TABLE <db>.<t> (\n  <id> int NOT NULL,\n  <name> text\n)",
		"CREATE PRIMARY INDEX <pk_t> ON <db>.<t> (<id>)",
		"CREATE UNIQUE INDEX <ux_t_name> ON <db>.<t> (<name>(8))",
		"COMMENT TABLE <db>.<t> = people",
		"COMMENT COLUMN <db>.<t>.<id> = the id",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 只开索引编辑、没有注释能力的方言：语句到建索引为止。
func TestCreateTableWritesIndexesWithoutComments(t *testing.T) {
	session, mock := newTablesSession(t)
	session.dialect = plainIndexDialect{}
	statements, err := session.CreateTableStatements(context.Background(), "db", dbdriver.CreateTableRequest{
		Name:    "t",
		Columns: []dbdriver.NewColumn{{Name: "id", Type: "int"}, {Name: "name", Type: "text", Nullable: true}},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "pk_t", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "ux_t_name", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "name", PrefixLength: 8}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE TABLE <db>.<t> (\n  <id> int NOT NULL,\n  <name> text\n)",
		"CREATE PRIMARY INDEX <pk_t> ON <db>.<t> (<id>)",
		"CREATE UNIQUE INDEX <ux_t_name> ON <db>.<t> (<name>(8))",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改表读出的模型：声明了索引编辑的方言给出索引与范围内其他表的索引名，没声明的一个都不带。
func TestAlterTableModelCarriesIndexesOnlyWhenDeclared(t *testing.T) {
	ctx := context.Background()
	model, err := newIndexSession(t, &indexerDialect{}).AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	assert.Equal(t, indexTable().Indexes, model.Indexes)
	assert.Equal(t, []string{"ix_other_table_name"}, model.TakenIndexNames)

	reads := 0
	plain, err := newAlterSession(t, plainAlterDialect{alterer: alterer{reads: &reads}}).AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	assert.Empty(t, plain.Indexes)
	assert.Empty(t, plain.TakenIndexNames)
}

// 原样交回的索引不产生任何语句，方言根本不被调用；改动的索引是删除后重建，按请求的次序。
func TestAlterTableIndexesUnchangedAndRebuilt(t *testing.T) {
	ctx := context.Background()
	dialect := &indexerDialect{}
	session := newIndexSession(t, dialect)

	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	result, err := session.AlterTableStatements(ctx, "db", "t", indexModelRequest(model))
	require.NoError(t, err)
	assert.Empty(t, result.Statements, "原样交回没有改动")
	assert.Empty(t, dialect.changes, "没有改动时方言根本不被调用")

	req := indexModelRequest(model)
	req.Indexes[1].Columns = append(req.Indexes[1].Columns, dbdriver.IndexColumn{Name: "at"}) // 唯一索引加一列
	req.Indexes[2].Name = "ix_t_at_desc"                                                      // 普通索引改名
	result, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX <ux_t_name> ON <db>.<t>",
		"DROP INDEX <ix_t_at> ON <db>.<t>",
		"CREATE UNIQUE INDEX <ux_t_name> ON <db>.<t> (<name>, <at>)",
		"CREATE NORMAL INDEX <ix_t_at_desc> ON <db>.<t> (<at> DESC)",
	}, result.Statements)
}

// 只读的索引只能删除：改它以 ErrUnsupported 拒绝（点名原因），删掉它可以；不可删除的索引连删都不行。
func TestAlterTableRefusesChangingReadOnlyAndDroppingUndroppableIndexes(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &indexerDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Indexes[3].Comment = "why"
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "ix_t_note")
	assert.Contains(t, err.Error(), "partial index")

	req = indexModelRequest(model)
	req.Indexes = append(req.Indexes[:3], req.Indexes[4:]...)
	req.IndexDrops = []string{"ix_t_note"}
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Contains(t, result.Statements, "DROP INDEX <ix_t_note> ON <db>.<t>")

	req = indexModelRequest(model)
	req.Indexes[4].Comment = "why"
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported, "不可删除的索引也改不了")

	req = indexModelRequest(model)
	req.Indexes = req.Indexes[:4]
	req.IndexDrops = []string{"ux_t_auto"}
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "ux_t_auto")
}

// 删列时引用它的索引随列一起删、排在删列之前；请求已经列出的不重复删；不可删除的留给数据库随列消失。
func TestAlterTableDropsIndexesOfADroppedColumnFirst(t *testing.T) {
	ctx := context.Background()
	dialect := &indexerDialect{}
	session := newIndexSession(t, dialect)
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Columns = req.Columns[:3] // 删 note 列
	req.Indexes = req.Indexes[:3] // 请求没有列出 ix_t_note 的删除
	req.Drops = []string{"note"}
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX <ix_t_note> ON <db>.<t>",
		"DROP COLUMN note",
	}, result.Statements, "引用了删掉的列的索引先删")
	require.Len(t, dialect.changes, 1)
	assert.Equal(t, []string{"note"}, dialect.changes[0].Drops)
	assert.Equal(t, []dbdriver.IndexDefinition{{
		Name: "ix_t_note", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "note"}},
		ReadOnlyReason: "partial index",
	}}, dialect.changes[0].IndexDrops)

	// 删 id 列：ux_t_auto 不可删除（数据库随列一起丢掉它），请求没有列出也不报错。
	req = indexModelRequest(model)
	req.Columns = req.Columns[1:]
	req.Indexes = req.Indexes[1:4]
	req.Drops = []string{"id"}
	result, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.NotContains(t, result.Statements, "DROP INDEX <ux_t_auto> ON <db>.<t>")
	assert.Contains(t, result.Statements, "DROP INDEX <pk_t> ON <db>.<t>")
}

// 索引的列按改动之后的表解析：改名的列按新名字写，交回的是现在的名字也不算改动；不在表里的列报错。
func TestAlterTableResolvesIndexColumns(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &indexerDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	// at 改名为 created_at，请求里的索引仍写现在的名字：索引不必重建（列改了名，索引跟着列）。
	req := indexModelRequest(model)
	req.Columns[2].Name = "created_at"
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Empty(t, result.Statements, "列改名后索引跟着列，不必重建")

	// 同时给它加一列：重建的语句写新名字。
	req = indexModelRequest(model)
	req.Columns[2].Name = "created_at"
	req.Indexes[2].Columns = append(req.Indexes[2].Columns, dbdriver.IndexColumn{Name: "id"})
	result, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX <ix_t_at> ON <db>.<t>",
		"CREATE NORMAL INDEX <ix_t_at> ON <db>.<t> (<created_at> DESC, <id>)",
	}, result.Statements)
	req = indexModelRequest(model)
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{
		IndexDefinition: dbdriver.IndexDefinition{Name: "ix_t_ghost", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "ghost"}}},
	})
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost")
}

// 一张表最多一个主键；改 / 删已有主键要声明了主键可改，没声明时任何改动都拒绝，但原样交回不算改动。
func TestAlterTablePrimaryKeyRules(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &indexerDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{
		IndexDefinition: dbdriver.IndexDefinition{Name: "pk_t2", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "name"}}},
	})
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "primary key")

	req = indexModelRequest(model)
	req.Indexes[0].Columns = append(req.Indexes[0].Columns, dbdriver.IndexColumn{Name: "name"})
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX <pk_t> ON <db>.<t>",
		"CREATE PRIMARY INDEX <pk_t> ON <db>.<t> (<id>, <name>)",
	}, result.Statements)

	locked := indexEditingDeclaration()
	locked.AlterPrimaryKey = false
	session = newIndexSession(t, &indexerDialect{declared: locked})

	req = indexModelRequest(model)
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err, "原样交回的主键不算改动")

	req = indexModelRequest(model)
	req.Indexes[0].Columns = append(req.Indexes[0].Columns, dbdriver.IndexColumn{Name: "name"})
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "primary key")

	req = indexModelRequest(model)
	req.Indexes = req.Indexes[1:]
	req.IndexDrops = []string{"pk_t"}
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "primary key")
}

// 请求里用到方言没声明的能力，在读表之前以 ErrUnsupported 拒绝（新索引的类型、降序、前缀、注释、选项、
// 新主键）；没声明索引编辑的方言拒绝任何索引改动。改动已有索引的越界在读了表之后才看得出，同样拒绝。
func TestAlterTableRefusesUndeclaredIndexFeatures(t *testing.T) {
	ctx := context.Background()
	locked := indexEditingDeclaration()
	locked.Descending, locked.PrefixLength, locked.Comments, locked.AlterPrimaryKey = false, false, false, false
	locked.Options = nil
	locked.Types = []dbdriver.IndexTypeDecl{{ID: dbdriver.IndexTypeNormal, Label: "indexType.normal"}}

	for name, tc := range map[string]struct {
		index   dbdriver.IndexDefinition
		wantErr string
	}{
		"undeclared type":   {dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "id"}}}, "unique"},
		"descending":        {dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "id", Descending: true}}}, "descending"},
		"prefix":            {dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "name", PrefixLength: 4}}}, "prefix"},
		"comment":           {dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Comment: "why"}, "comment"},
		"option":            {dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: map[string]any{"method": "hash"}}, "option"},
		"primary key added": {dbdriver.IndexDefinition{Name: "pk", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}}, "primary key"},
	} {
		session := newIndexSession(t, &indexerDialect{declared: locked})
		_, err := session.AlterTableStatements(ctx, "db", "t", dbdriver.AlterTableRequest{
			Indexes: []dbdriver.AlterIndex{{IndexDefinition: tc.index}},
		})
		require.ErrorIs(t, err, dbdriver.ErrUnsupported, name)
		assert.Contains(t, err.Error(), tc.wantErr, name)
		assert.Contains(t, err.Error(), tc.index.Name, name)
	}

	// 没声明索引编辑的方言：任何索引改动与索引删除都拒绝，原样交回也不行（它读不出索引）。
	reads := 0
	session := newAlterSession(t, plainAlterDialect{alterer: alterer{reads: &reads}})
	_, err := session.AlterTableStatements(ctx, "db", "t", dbdriver.AlterTableRequest{
		Indexes: []dbdriver.AlterIndex{{IndexDefinition: dbdriver.IndexDefinition{Name: "ix", Type: dbdriver.IndexTypeNormal}}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.AlterTableStatements(ctx, "db", "t", dbdriver.AlterTableRequest{IndexDrops: []string{"ix"}})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Zero(t, reads, "拒绝在读表之前")
	// 改动已有的索引，改出来的样子用到没声明的能力：读了表之后拒绝。
	session = newIndexSession(t, &indexerDialect{declared: locked})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)
	req := indexModelRequest(model)
	req.Indexes[1].Comment = "why"
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "ux_t_name")
}

// 索引语句分两半：删除（请求删掉的与改动了的现有索引）写在列改动之前，新建（改动的与新增的）写在列改动之后。
func TestIndexStatementsSplitIntoDropsAndCreates(t *testing.T) {
	change := TableChange{
		IndexDrops: []dbdriver.IndexDefinition{{Name: "gone", Type: dbdriver.IndexTypeNormal}},
		Indexes: []IndexChange{
			{Current: &dbdriver.IndexDefinition{Name: "old", Type: dbdriver.IndexTypeNormal},
				Index: dbdriver.IndexDefinition{Name: "old", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "id"}}}},
			{Index: dbdriver.IndexDefinition{Name: "new", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "id"}}}},
		},
	}
	drops, err := IndexDropStatements(&indexerDialect{}, "<db>.<t>", change)
	require.NoError(t, err)
	assert.Equal(t, []string{"DROP INDEX <gone> ON <db>.<t>", "DROP INDEX <old> ON <db>.<t>"}, drops)

	creates, err := IndexCreateStatements(&indexerDialect{}, "<db>.<t>", change)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE UNIQUE INDEX <old> ON <db>.<t> (<id>)",
		"CREATE NORMAL INDEX <new> ON <db>.<t> (<id>)",
	}, creates)
}

// 同一次保存里新增一列并在它上面建索引：建索引必须排在加列之后，否则数据库找不到这一列。
func TestAlterTableCreatesAnIndexOnANewColumnAfterAddingIt(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &indexerDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "email", Type: "TEXT", Nullable: true})
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "ix_t_email", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "email"}},
	}})
	statements, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	add := slices.Index(statements.Statements, "ADD email")
	create := slices.IndexFunc(statements.Statements, func(s string) bool { return strings.Contains(s, "<ix_t_email>") })
	require.GreaterOrEqual(t, add, 0, statements.Statements)
	require.GreaterOrEqual(t, create, 0, statements.Statements)
	assert.Less(t, add, create, "index on a new column must be created after the column is added: %v", statements.Statements)
}

// 请求改完之后仍有一个索引指向被删掉的列：矛盾，报错而不是写一条数据库会拒的语句。
func TestAlterTableRefusesAnIndexNamingADroppedColumn(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &indexerDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Columns = req.Columns[:2] // at 这一列不在请求里（不改它）……
	req.Indexes[2].Columns = []dbdriver.IndexColumn{{Name: "at"}}
	req.Drops = []string{"at"} // ……同时删掉 at
	_, err = session.AlterTableStatements(ctx, "db", "t", req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ix_t_at")
	assert.Contains(t, err.Error(), `"at"`)
}

// 声明为独占的开关（如 SQL Server 的聚集）一张表至多一个索引打开：改完之后的表上算——留下的、改动的与新增的。
func TestAlterTableRefusesTwoIndexesWithAnExclusiveOption(t *testing.T) {
	ctx := context.Background()
	declared := indexEditingDeclaration()
	declared.Options[1].Exclusive = true
	session := newIndexSession(t, &indexerDialect{declared: declared})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	clustered := func(name, column string) dbdriver.AlterIndex {
		return dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
			Name: name, Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: column}},
			Options: map[string]any{"clustered": true},
		}}
	}
	one := indexModelRequest(model)
	one.Indexes = append(one.Indexes, clustered("ix_c1", "name"))
	_, err = session.AlterTableStatements(ctx, "db", "t", one)
	require.NoError(t, err, "一个打开没有问题")

	two := indexModelRequest(model)
	two.Indexes = append(two.Indexes, clustered("ix_c1", "name"), clustered("ix_c2", "at"))
	_, err = session.AlterTableStatements(ctx, "db", "t", two)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clustered")
	assert.Contains(t, err.Error(), "ix_c1")
	assert.Contains(t, err.Error(), "ix_c2")

	// 关掉的开关（false）不算。
	off := indexModelRequest(model)
	second := clustered("ix_c2", "at")
	second.Options = map[string]any{"clustered": false}
	off.Indexes = append(off.Indexes, clustered("ix_c1", "name"), second)
	_, err = session.AlterTableStatements(ctx, "db", "t", off)
	require.NoError(t, err)
}

// 建表时同样至多一个索引打开独占开关，在交给方言（含整条写建表语句的方言）之前拒绝。
func TestCreateTableRefusesTwoIndexesWithAnExclusiveOption(t *testing.T) {
	declared := indexEditingDeclaration()
	declared.Options[1].Exclusive = true
	session := newIndexSession(t, &indexerDialect{declared: declared})
	clustered := func(name, column string) dbdriver.IndexDefinition {
		return dbdriver.IndexDefinition{Name: name, Type: dbdriver.IndexTypeNormal,
			Columns: []dbdriver.IndexColumn{{Name: column}}, Options: map[string]any{"clustered": true}}
	}
	req := dbdriver.CreateTableRequest{Name: "t", Columns: []dbdriver.NewColumn{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}},
		Indexes: []dbdriver.IndexDefinition{clustered("ix_a", "a")}}
	_, err := session.CreateTableStatements(context.Background(), "db", req)
	require.NoError(t, err)

	req.Indexes = append(req.Indexes, clustered("ix_b", "b"))
	_, err = session.CreateTableStatements(context.Background(), "db", req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clustered")
	assert.Contains(t, err.Error(), "ix_b")
}

// includeDialect 的表另有一个只在 INCLUDE 里用到 note 的索引。
type includeDialect struct{ indexerDialect }

func (d *includeDialect) AlterableTable(context.Context, *sql.Conn, string, string) (*AlterableTable, error) {
	table := indexTable()
	table.Indexes = append(table.Indexes, dbdriver.IndexDefinition{
		Name: "ix_t_name_cover", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "name"}},
		Include: []string{"note"}, ReadOnlyReason: "INCLUDE columns",
	})
	return table, nil
}

// 只在 INCLUDE 里引用被删列的索引同样随列一起删（SQL Server 不先删它就删不掉这一列，PostgreSQL 会悄悄删掉它），
// 删除警告才能列出它。
func TestAlterTableDropsAnIndexIncludingADroppedColumn(t *testing.T) {
	ctx := context.Background()
	dialect := &includeDialect{}
	session := newIndexSession(t, dialect)
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := modelRequest(model)
	req.Columns = req.Columns[:3]
	req.Drops = []string{"note"}
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DROP INDEX <ix_t_note> ON <db>.<t>",
		"DROP INDEX <ix_t_name_cover> ON <db>.<t>",
		"DROP COLUMN note",
	}, result.Statements)

	// 请求原样留下这个索引、却删掉它 INCLUDE 的列：矛盾，报错。
	kept := indexModelRequest(model)
	kept.Columns = kept.Columns[:3]
	kept.Indexes = slices.DeleteFunc(kept.Indexes, func(i dbdriver.AlterIndex) bool { return i.OriginalName == "ix_t_note" })
	kept.Drops = []string{"note"}
	_, err = session.AlterTableStatements(ctx, "db", "t", kept)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ix_t_name_cover")
}

// 改名的列在 INCLUDE 里同样跟着改名：原样交回不重建。
func TestAlterTableRenamesIncludedColumnsWithoutRebuilding(t *testing.T) {
	ctx := context.Background()
	session := newIndexSession(t, &includeDialect{})
	model, err := session.AlterTableModel(ctx, "db", "t")
	require.NoError(t, err)

	req := indexModelRequest(model)
	req.Columns[3].Name = "remark" // note → remark
	result, err := session.AlterTableStatements(ctx, "db", "t", req)
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(result.Statements, "\n"), "ix_t_name_cover", "INCLUDE 列改名不重建索引")
}
