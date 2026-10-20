package dbdrivertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ConformanceStatement 是一致性测试在会话上执行的语句：先 Execute，再用 ExecutePaged 取
// 第一页（每页 ConformancePageSize 行）。会话工厂据此准备数据或桩。
const ConformanceStatement = "SELECT 1"

// ConformancePageSize 是一致性测试分页执行时的每页行数。
const ConformancePageSize = 10

// ConformanceFailingStatement 是一致性测试整批执行时用来模拟失败的语句：任何数据库都会拒绝它，
// 会话工厂给出的会话也必须拒绝它（假驱动经 Fake.StatementErrors）。
const ConformanceFailingStatement = "NOT A STATEMENT"

// SessionFactory 给一致性测试一条该驱动的会话，且不需要真的数据库：database/sql 之上的
// 驱动接一个桩连接，文件型驱动开一个临时文件。会话由一致性测试关闭。
type SessionFactory func(t *testing.T) dbdriver.Session

// RunConformance 是每个注册的驱动都必须通过的一致性测试。驱动须已注册。
//
//   - 描述：完整、自洽、只用共享代码与前端认识的 Tab / 字段 / 功能，注册表确实能按它声明
//     的 id 与别名找到它。
//   - 会话：SQL 执行与分页执行是每个驱动都要有的，必须可调用并返回成形的结构化结果；
//     没有声明的功能必须以 dbdriver.ErrUnsupported 拒绝：连接级只读，没有声明的对象类型
//     的浏览（ObjectSource），没有声明表格编辑时的表格编辑语句（GridEditStatements），没有声明
//     改表时的改表（AlterTableModel / AlterTableStatements），没有声明导入时的导入语句（ImportStatements），没有声明导出 / 复制为 SQL 时的行语句（PrepareRowStatements），没有声明建表、删表、清空、建库时的对应语句（声明了的
//     必须给出语句），以及
//     没有声明 CanDisableForeignKeyChecks 时整批执行里的关闭外键检查，且拒绝时不能执行任何语句。声明了的浏览操作、表浏览与行语句能不能调用，要有数据才能
//     验证，由 RunBrowseConformance / RunTableConformance 在能提供数据的会话上检查。
func RunConformance(t *testing.T, d dbdriver.Driver, newSession SessionFactory) {
	t.Helper()
	if err := d.Descriptor().Validate(); err != nil {
		t.Errorf("descriptor: %v", err)
	}
	for _, problem := range descriptorProblems(d.Descriptor()) {
		t.Errorf("descriptor: %s", problem)
	}
	for _, problem := range registrationProblems(d) {
		t.Errorf("registration: %s", problem)
	}
	for _, problem := range readOnlyProblems(d) {
		t.Errorf("read-only: %s", problem)
	}
	for _, problem := range sessionProblems(d.Descriptor(), newSession(t)) {
		t.Errorf("session: %s", problem)
	}
}

// readOnlyProblems 检查没有声明连接级只读的驱动是否拒绝只读配置。声明了的驱动怎么落实
// 只读各不相同，由各驱动自己的测试固定。
func readOnlyProblems(d dbdriver.Driver) []string {
	desc := d.Descriptor()
	if desc.Supports(dbdriver.CapReadOnly) {
		return nil
	}
	session, err := d.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{Driver: desc.ID, ReadOnly: true},
	})
	if err == nil {
		problem := fmt.Sprintf("the driver does not declare %q, yet opened a session for a read-only config", dbdriver.CapReadOnly)
		if closeErr := session.Close(); closeErr != nil {
			problem += fmt.Sprintf(" (and closing it failed: %v)", closeErr)
		}
		return []string{problem}
	}
	if !errors.Is(err, dbdriver.ErrUnsupported) {
		return []string{fmt.Sprintf("a read-only config must be refused with ErrUnsupported, got: %v", err)}
	}
	return nil
}

// sessionProblems 在一条会话上跑每个驱动都必须支持的操作，最后关闭它。
func sessionProblems(desc dbdriver.Descriptor, s dbdriver.Session) []string {
	ctx := context.Background()
	var problems []string

	// 拒绝要在任何语句之前：基于桩连接的会话在见到未预期的语句时会失败。
	problems = append(problems, undeclaredObjectProblems(ctx, desc, s)...)
	problems = append(problems, schemaWriteProblems(ctx, desc, s)...)

	res, err := s.Execute(ctx, ConformanceStatement)
	problems = append(problems, resultProblems("Execute", res, err)...)
	if err == nil && res != nil && res.Rows != nil && res.Rows.Total != nil {
		problems = append(problems, "Execute: an unpaged execution must not report a total")
	}

	res, err = s.ExecutePaged(ctx, ConformanceStatement, dbdriver.Page{Index: 0, Size: ConformancePageSize})
	problems = append(problems, resultProblems("ExecutePaged", res, err)...)
	if err == nil && res != nil && res.Rows != nil {
		switch rows := res.Rows; {
		case rows.Total == nil:
			problems = append(problems, "ExecutePaged: a paged query must report the total row count")
		case *rows.Total < int64(len(rows.Rows)):
			problems = append(problems, fmt.Sprintf("ExecutePaged: total %d is smaller than the %d rows of the page", *rows.Total, len(rows.Rows)))
		}
		if len(res.Rows.Rows) > ConformancePageSize {
			problems = append(problems, fmt.Sprintf("ExecutePaged: the page holds %d rows, more than the %d asked for", len(res.Rows.Rows), ConformancePageSize))
		}
	}

	if err := s.Close(); err != nil {
		problems = append(problems, fmt.Sprintf("Close: %v", err))
	}
	return problems
}

// schemaWriteProblems 检查建表、删表、清空、建库的语句生成（只生成、不执行，所以不需要数据）：
// 声明了的必须给出非空语句，没有声明的必须以 ErrUnsupported 拒绝；建库还要接受驱动声明的选项（取
// 默认值）、拒绝没有声明的选项。
func schemaWriteProblems(ctx context.Context, desc dbdriver.Descriptor, s dbdriver.Session) []string {
	var problems []string
	check := func(name string, capability dbdriver.Capability, statements []string, err error) {
		switch {
		case !desc.Supports(capability) && !errors.Is(err, dbdriver.ErrUnsupported):
			problems = append(problems, fmt.Sprintf("%s: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", name, capability, err))
		case !desc.Supports(capability):
		case errors.Is(err, dbdriver.ErrUnsupported):
			problems = append(problems, fmt.Sprintf("%s: declared %q but refused with ErrUnsupported: %v", name, capability, err))
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
		case len(statements) == 0:
			problems = append(problems, fmt.Sprintf("%s: returned no statements and no error", name))
		default:
			for i, statement := range statements {
				if strings.TrimSpace(statement) == "" {
					problems = append(problems, fmt.Sprintf("%s: statement %d is empty", name, i))
				}
			}
		}
	}
	statements, err := s.CreateTableStatements(ctx, "", dbdriver.CreateTableRequest{
		Name: "x", Columns: []dbdriver.NewColumn{{Name: "id", Type: "INT", Nullable: true}},
	})
	check("CreateTableStatements", dbdriver.CapCreateTable, statements, err)
	statements, err = s.DropTableStatements(ctx, "", "x")
	check("DropTableStatements", dbdriver.CapDropTable, statements, err)
	statements, err = s.TruncateTableStatements(ctx, "", "x")
	check("TruncateTableStatements", dbdriver.CapTruncateTable, statements, err)

	options := make(map[string]string, len(desc.CreateDatabaseOptions))
	for _, option := range desc.CreateDatabaseOptions {
		options[option.Key] = option.Default
	}
	statements, err = s.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "x", Options: options})
	check("CreateDatabaseStatements", dbdriver.CapCreateDatabase, statements, err)
	if desc.Supports(dbdriver.CapCreateDatabase) {
		if _, err := s.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "x", Options: map[string]string{"undeclared_option": "v"}}); err == nil {
			problems = append(problems, "CreateDatabaseStatements: an option the driver did not declare must be refused")
		}
	}
	return problems
}

// undeclaredObjectProblems 检查没有声明的功能都被以 ErrUnsupported 拒绝：对象类型的
// ObjectSource、查看建表语句、表格编辑、改表、导入与行语句，以及整批执行里的关闭外键检查。
func undeclaredObjectProblems(ctx context.Context, desc dbdriver.Descriptor, s dbdriver.Session) []string {
	var problems []string
	for _, objType := range objectTypes {
		capability, _ := dbdriver.ObjectCapability(objType)
		if desc.Supports(capability) {
			continue
		}
		if _, err := s.ObjectSource(ctx, "", objType, "x", ""); !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("ObjectSource(%q): the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", objType, capability, err))
		}
	}
	if !desc.Supports(dbdriver.CapViewDDL) {
		if _, err := s.TableDDL(ctx, "", "x"); !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("TableDDL: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", dbdriver.CapViewDDL, err))
		}
	}
	if !desc.CanDisableForeignKeyChecks {
		_, err := s.ExecuteBatch(ctx, dbdriver.BatchRequest{
			Statements: []string{ConformanceStatement}, DisableForeignKeyChecks: true, Table: "x",
		})
		if !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("ExecuteBatch: the driver does not declare canDisableForeignKeyChecks, so disabling them must be refused with ErrUnsupported, got: %v", err))
		}
	}
	if !desc.Supports(dbdriver.CapGridEdit) {
		_, err := s.GridEditStatements(ctx, "", "x", dbdriver.GridEditRequest{
			Columns: []string{"id"}, Inserts: []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{{Column: "id", Value: float64(1)}}}},
		})
		if !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("GridEditStatements: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", dbdriver.CapGridEdit, err))
		}
	}
	if !desc.Supports(dbdriver.CapAlterTable) {
		if _, err := s.AlterTableModel(ctx, "", "x"); !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("AlterTableModel: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", dbdriver.CapAlterTable, err))
		}
		_, err := s.AlterTableStatements(ctx, "", "x", dbdriver.AlterTableRequest{Name: "y"})
		if !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("AlterTableStatements: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", dbdriver.CapAlterTable, err))
		}
	}
	if !desc.Supports(dbdriver.CapImport) {
		_, err := s.ImportStatements(ctx, "", "x", dbdriver.ImportRequest{
			Mode: dbdriver.ImportAppend, Columns: []string{"id"}, Rows: [][]dbdriver.ImportValue{{{Kind: dbdriver.ImportText, Text: "1"}}},
		})
		if !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("ImportStatements: the driver does not declare %q, so it must be refused with ErrUnsupported, got: %v", dbdriver.CapImport, err))
		}
	}
	if !desc.GeneratesRowStatements() {
		_, err := s.PrepareRowStatements(ctx, "", "x")
		if !errors.Is(err, dbdriver.ErrUnsupported) {
			problems = append(problems, fmt.Sprintf("PrepareRowStatements: the driver declares neither %q nor %q, so it must be refused with ErrUnsupported, got: %v",
				dbdriver.CapExportSQL, dbdriver.CapCopySQL, err))
		}
	}
	return problems
}

// columnShapeProblems 检查序列化后的一列：前端读 comment（字符串，没有注释是空串）与
// default（字符串，没有默认值是 null）两个键，缺了或类型不对都不行。
func columnShapeProblems(raw []byte) []string {
	var column map[string]json.RawMessage
	if err := json.Unmarshal(raw, &column); err != nil {
		return []string{fmt.Sprintf("not a JSON object: %v", err)}
	}
	var problems []string
	var s string
	if v, ok := column["comment"]; !ok {
		problems = append(problems, `missing "comment" (a string, empty when the column has no comment)`)
	} else if json.Unmarshal(v, &s) != nil || string(v) == "null" {
		problems = append(problems, `"comment" must be a string, empty when the column has no comment`)
	}
	if v, ok := column["default"]; !ok {
		problems = append(problems, `missing "default" (a string, or null when the column has no default)`)
	} else if json.Unmarshal(v, &s) != nil {
		problems = append(problems, `"default" must be a string, or null when the column has no default`)
	}
	return problems
}

var objectTypes = []string{dbdriver.ObjectView, dbdriver.ObjectProcedure, dbdriver.ObjectFunction, dbdriver.ObjectTrigger}

// RunBrowseConformance 检查树的浏览：声明了的操作可以调用并给出成形的结果，没声明的对象
// 类型不出现在对象列表里、查源码被 ErrUnsupported 拒绝。会话必须是能回答这些操作的（有库、
// 有表可列），由测试关闭。
func RunBrowseConformance(t *testing.T, d dbdriver.Driver, newSession SessionFactory) {
	t.Helper()
	for _, problem := range browseProblems(d.Descriptor(), newSession(t)) {
		t.Errorf("browse: %s", problem)
	}
}

func browseProblems(desc dbdriver.Descriptor, s dbdriver.Session) []string {
	ctx := context.Background()
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	databases, err := s.ListDatabases(ctx)
	if err != nil {
		report("ListDatabases: %v", err)
	}
	database := ""
	if len(databases) > 0 {
		database = databases[0]
	}
	tables, err := s.ListTables(ctx, database)
	if err != nil {
		report("ListTables(%q): %v", database, err)
	}
	if len(tables) > 0 {
		meta, err := s.TableMetadata(ctx, database, tables[0])
		switch {
		case err != nil:
			report("TableMetadata(%q): %v", tables[0], err)
		case meta == nil:
			report("TableMetadata(%q): returned no metadata and no error", tables[0])
		case meta.Columns == nil || meta.Indexes == nil || meta.ForeignKeys == nil:
			report("TableMetadata(%q): columns, indexes and foreign keys must be empty lists, not nil (they serialize to null)", tables[0])
		default:
			for _, column := range meta.Columns {
				raw, err := json.Marshal(column)
				if err != nil {
					report("TableMetadata(%q): column %q does not serialize: %v", tables[0], column.Name, err)
					continue
				}
				for _, problem := range columnShapeProblems(raw) {
					report("TableMetadata(%q): column %q: %s", tables[0], column.Name, problem)
				}
			}
		}
	}

	objects, err := s.ListObjects(ctx, database)
	if err != nil {
		report("ListObjects: %v", err)
	} else if objects == nil {
		report("ListObjects: returned no objects and no error")
	} else {
		groups := map[string][]dbdriver.ObjectMeta{
			dbdriver.ObjectView: objects.Views, dbdriver.ObjectProcedure: objects.Procedures,
			dbdriver.ObjectFunction: objects.Functions, dbdriver.ObjectTrigger: objects.Triggers,
		}
		for _, objType := range objectTypes {
			if capability, _ := dbdriver.ObjectCapability(objType); !desc.Supports(capability) && len(groups[objType]) > 0 {
				report("ListObjects lists %d %s objects, but the driver does not declare %q", len(groups[objType]), objType, capability)
			}
		}
	}

	// 查看建表语句同样单独声明：声明了的可以调用（表存在，所以有内容）；没声明的被拒绝由
	// sessionProblems 在任何语句之前检查。
	if desc.Supports(dbdriver.CapViewDDL) {
		ddlTable := "x"
		if len(tables) > 0 {
			ddlTable = tables[0]
		}
		ddl, err := s.TableDDL(ctx, database, ddlTable)
		switch {
		case errors.Is(err, dbdriver.ErrUnsupported):
			report("TableDDL(%q): declared %q but refused with ErrUnsupported: %v", ddlTable, dbdriver.CapViewDDL, err)
		case err != nil:
			report("TableDDL(%q): %v", ddlTable, err)
		case len(tables) > 0 && ddl == "":
			report("TableDDL(%q): returned no statement for a table that exists", ddlTable)
		}
	}

	for _, objType := range objectTypes {
		capability, _ := dbdriver.ObjectCapability(objType)
		_, err := s.ObjectSource(ctx, database, objType, "x", "")
		switch declared := desc.Supports(capability); {
		case declared && errors.Is(err, dbdriver.ErrUnsupported):
			report("ObjectSource(%q): declared %q but refused with ErrUnsupported: %v", objType, capability, err)
		case !declared && !errors.Is(err, dbdriver.ErrUnsupported):
			report("ObjectSource(%q): not declared %q, so it must be refused with ErrUnsupported, got: %v", objType, capability, err)
		}
	}

	if err := s.Close(); err != nil {
		report("Close: %v", err)
	}
	return problems
}

// RunTableConformance 检查表浏览：打开表、取一页、计数都可以调用，给出成形且彼此一致的结果；
// 声明了导出 / 复制为 SQL 的驱动还要能为表里的行生成语句，声明了改表的驱动要能读出这张表、原样交回时
// 不生成语句（见 alterProblems）；整批执行对失败的语句停下并回滚或
// 继续，声明了能关闭外键检查的驱动关得掉也恢复得了（见 batchProblems）。会话必须是能回答这些
// 操作的（database 里有 table 这张表，最好有行），由测试关闭。筛选条件与排序项的语义、语句的写法
// 随驱动的方言而定，由各驱动自己的测试固定。
func RunTableConformance(t *testing.T, d dbdriver.Driver, newSession SessionFactory, database, table string) {
	t.Helper()
	for _, problem := range tableProblems(d.Descriptor(), newSession(t), database, table) {
		t.Errorf("table: %s", problem)
	}
}

func tableProblems(desc dbdriver.Descriptor, s dbdriver.Session, database, table string) []string {
	ctx := context.Background()
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	page := dbdriver.Page{Index: 0, Size: ConformancePageSize}

	opened, err := s.OpenTable(ctx, database, table, page.Size)
	switch {
	case err != nil:
		report("OpenTable(%q): %v", table, err)
	case opened == nil:
		report("OpenTable(%q): returned no result and no error", table)
	case opened.FirstPage == nil:
		report("OpenTable(%q): the result has no first page", table)
	default:
		first := opened.FirstPage
		problems = append(problems, rowSetProblems("OpenTable", first)...)
		if len(first.Rows) > page.Size {
			report("OpenTable: the first page holds %d rows, more than the %d asked for", len(first.Rows), page.Size)
		}
		if opened.TotalCount < int64(len(first.Rows)) {
			report("OpenTable: total %d is smaller than the %d rows of the first page", opened.TotalCount, len(first.Rows))
		}
		// 类型、约束、主键说的都必须是表格里有的列：前端按列名把它们对到表格上。
		for name := range opened.ColumnTypes {
			if !slices.Contains(first.Columns, name) {
				report("OpenTable: column types name %q, which is not a column of the first page", name)
			}
		}
		for _, rule := range opened.ColumnRules {
			if !slices.Contains(first.Columns, rule.Name) {
				report("OpenTable: column rules name %q, which is not a column of the first page", rule.Name)
			}
		}
		for name, category := range opened.ColumnCategories {
			if !slices.Contains(first.Columns, name) {
				report("OpenTable: column categories name %q, which is not a column of the first page", name)
			}
			if !slices.Contains(dbdriver.ColumnCategories(), category) {
				report("OpenTable: column %q has category %q, which is not one of %v", name, category, dbdriver.ColumnCategories())
			}
		}
		for _, name := range opened.PrimaryKeys {
			if !slices.Contains(first.Columns, name) {
				report("OpenTable: primary keys name %q, which is not a column of the first page", name)
			}
		}
	}

	rows, err := s.TableRows(ctx, database, table, dbdriver.TableQuery{}, page)
	switch {
	case err != nil:
		report("TableRows(%q): %v", table, err)
	case rows == nil:
		report("TableRows(%q): returned no row set and no error", table)
	default:
		problems = append(problems, rowSetProblems("TableRows", rows)...)
		if len(rows.Rows) > page.Size {
			report("TableRows: the page holds %d rows, more than the %d asked for", len(rows.Rows), page.Size)
		}
		if rows.Total != nil {
			report("TableRows: a table page must not report a total; CountTableRows does")
		}
		if opened != nil && opened.FirstPage != nil {
			if !slices.Equal(rows.Columns, opened.FirstPage.Columns) {
				report("TableRows: columns %v differ from the %v OpenTable gave", rows.Columns, opened.FirstPage.Columns)
			}
			if len(rows.Rows) != len(opened.FirstPage.Rows) {
				report("TableRows: the unfiltered first page holds %d rows, OpenTable's first page %d", len(rows.Rows), len(opened.FirstPage.Rows))
			}
		}
	}

	total, err := s.CountTableRows(ctx, database, table, nil)
	switch {
	case err != nil:
		report("CountTableRows(%q): %v", table, err)
	case opened != nil && total != opened.TotalCount:
		report("CountTableRows: the unfiltered count is %d, but OpenTable reported a total of %d", total, opened.TotalCount)
	}

	stream, err := s.StreamTableRows(ctx, database, table, dbdriver.TableQuery{})
	switch {
	case err != nil:
		report("StreamTableRows(%q): %v", table, err)
	case stream == nil:
		report("StreamTableRows(%q): returned no stream and no error", table)
	default:
		streamed := int64(0)
		for stream.Next() {
			if len(stream.Row()) != len(stream.Columns()) {
				report("StreamTableRows: row %d has %d values for %d columns", streamed, len(stream.Row()), len(stream.Columns()))
			}
			streamed++
		}
		if err := stream.Err(); err != nil {
			report("StreamTableRows: reading rows: %v", err)
		}
		if err := stream.Close(); err != nil {
			report("StreamTableRows: close: %v", err)
		}
		if opened != nil && streamed != opened.TotalCount {
			report("StreamTableRows: streamed %d rows, but OpenTable reported a total of %d", streamed, opened.TotalCount)
		}
	}

	var writer dbdriver.RowStatementWriter
	if desc.GeneratesRowStatements() && opened != nil && opened.FirstPage != nil {
		var rowProblems []string
		writer, rowProblems = rowStatementProblems(ctx, s, database, table, opened)
		problems = append(problems, rowProblems...)
	}
	if desc.Supports(dbdriver.CapGridEdit) && opened != nil && opened.FirstPage != nil {
		problems = append(problems, gridEditProblems(ctx, s, database, table, opened)...)
	}
	if desc.Supports(dbdriver.CapImport) && opened != nil && opened.FirstPage != nil {
		problems = append(problems, importProblems(ctx, s, database, table, opened.FirstPage)...)
	}
	if desc.Supports(dbdriver.CapAlterTable) && opened != nil && opened.FirstPage != nil {
		problems = append(problems, alterProblems(ctx, s, database, table, opened.FirstPage.Columns)...)
	}

	problems = append(problems, batchProblems(ctx, desc, s, database, table)...)

	starter, err := s.StarterQuery(ctx, database, table, ConformancePageSize)
	switch {
	case err != nil:
		report("StarterQuery(%q): %v", table, err)
	case !strings.Contains(starter, table):
		report("StarterQuery(%q): the statement %q does not reference the table", table, starter)
	}

	if err := s.Close(); err != nil {
		report("Close: %v", err)
	}
	// 备好的 writer 不再碰数据库：会话关了也照样生成（整表导出为 SQL 在读游标占着会话的连接时用它）。
	if writer != nil {
		if _, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: dbdriver.RowInsert, Columns: opened.FirstPage.Columns, Rows: firstPageRecords(opened.FirstPage),
		}); err != nil {
			report("RowStatements after the session was closed: %v; a prepared writer must not touch the database", err)
		}
	}
	return problems
}

// rowStatementProblems 用打开表时的第一页检查行语句：每行一条 INSERT、每行一条 UPDATE，没有行
// 时是空列表（序列化成 []，不是 null）。语句只生成，这里不执行。备好的 writer 一并交回。
func rowStatementProblems(ctx context.Context, s dbdriver.Session, database, table string, opened *dbdriver.OpenedTable) (dbdriver.RowStatementWriter, []string) {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	writer, err := s.PrepareRowStatements(ctx, database, table)
	switch {
	case errors.Is(err, dbdriver.ErrUnsupported):
		report("PrepareRowStatements: declared %q or %q but refused with ErrUnsupported: %v", dbdriver.CapExportSQL, dbdriver.CapCopySQL, err)
		return nil, problems
	case err != nil:
		report("PrepareRowStatements: %v", err)
		return nil, problems
	}
	first := opened.FirstPage
	rows := firstPageRecords(first)
	for _, kind := range []dbdriver.RowStatementKind{dbdriver.RowInsert, dbdriver.RowUpdate} {
		statements, err := writer.RowStatements(dbdriver.RowStatementsRequest{
			Kind: kind, Columns: first.Columns, Rows: rows, PrimaryKeys: opened.PrimaryKeys,
		})
		switch {
		case err != nil:
			report("RowStatements(%s): %v", kind, err)
		case len(statements) != len(rows):
			report("RowStatements(%s): %d statement(s) for %d row(s); there must be one per row", kind, len(statements), len(rows))
		default:
			for i, statement := range statements {
				if strings.TrimSpace(statement) == "" {
					report("RowStatements(%s): statement %d is empty", kind, i)
				}
			}
		}
	}
	statements, err := writer.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: first.Columns})
	if err == nil && statements == nil {
		report("RowStatements without rows: returned null; it must be an empty list")
	}
	return writer, problems
}

// importProblems 用打开表时的第一页检查导入语句：把每行当作要追加的一行（值都是 NULL，不依赖列的
// 类型），每行一条 INSERT；没有行时是空列表（序列化成 []，不是 null）。语句只生成，这里不执行。
func importProblems(ctx context.Context, s dbdriver.Session, database, table string, first *dbdriver.RowSet) []string {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	req := dbdriver.ImportRequest{Mode: dbdriver.ImportAppend, Columns: first.Columns}
	for range first.Rows {
		row := make([]dbdriver.ImportValue, len(first.Columns))
		for i := range row {
			row[i] = dbdriver.ImportValue{Kind: dbdriver.ImportNull}
		}
		req.Rows = append(req.Rows, row)
	}
	statements, err := s.ImportStatements(ctx, database, table, req)
	switch {
	case errors.Is(err, dbdriver.ErrUnsupported):
		report("ImportStatements: declared %q but refused with ErrUnsupported: %v", dbdriver.CapImport, err)
	case err != nil:
		report("ImportStatements: %v", err)
	case len(statements) != len(req.Rows):
		report("ImportStatements: %d statement(s) to append %d row(s) one by one; there must be one per row", len(statements), len(req.Rows))
	default:
		for i, statement := range statements {
			if strings.TrimSpace(statement) == "" {
				report("ImportStatements: statement %d is empty", i)
			}
		}
	}
	req.Rows = nil
	statements, err = s.ImportStatements(ctx, database, table, req)
	if err == nil && statements == nil {
		report("ImportStatements without rows: returned null; it must be an empty list")
	}
	return problems
}

// firstPageRecords 把一页的行转成按列名取值的记录，就像前端带回的表格行。
func firstPageRecords(page *dbdriver.RowSet) []map[string]any {
	records := make([]map[string]any, 0, len(page.Rows))
	for _, row := range page.Rows {
		record := make(map[string]any, len(page.Columns))
		for i, column := range page.Columns {
			if i < len(row) {
				record[column] = row[i]
			}
		}
		records = append(records, record)
	}
	return records
}

// gridEditProblems 用打开表时的第一页检查表格编辑的语句：每行当作一个新增行（全部列）、一个改动
// 的行（第一列设回原值）与一个删除的行，外加一个整行取默认值的新增行；三组各自与请求一一对应。
// 没有任何行时三组都是空列表（序列化成 []，不是 null）。语句只生成，这里不执行。
func gridEditProblems(ctx context.Context, s dbdriver.Session, database, table string, opened *dbdriver.OpenedTable) []string {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	first := opened.FirstPage
	req := dbdriver.GridEditRequest{Columns: first.Columns, PrimaryKeys: opened.PrimaryKeys, Inserts: []dbdriver.GridInsert{{}}}
	for _, row := range firstPageRecords(first) {
		values := make([]dbdriver.ColumnValue, 0, len(first.Columns))
		for _, column := range first.Columns {
			values = append(values, dbdriver.ColumnValue{Column: column, Value: row[column]})
		}
		req.Inserts = append(req.Inserts, dbdriver.GridInsert{Values: values})
		req.Updates = append(req.Updates, dbdriver.GridUpdate{Row: row, Set: values[:1]})
		req.Deletes = append(req.Deletes, row)
	}

	statements, err := s.GridEditStatements(ctx, database, table, req)
	switch {
	case errors.Is(err, dbdriver.ErrUnsupported):
		report("GridEditStatements: declared %q but refused with ErrUnsupported: %v", dbdriver.CapGridEdit, err)
	case err != nil:
		report("GridEditStatements: %v", err)
	case statements == nil:
		report("GridEditStatements: returned no statements and no error")
	default:
		for _, group := range []struct {
			name       string
			statements []string
			want       int
		}{
			{"inserts", statements.Inserts, len(req.Inserts)},
			{"updates", statements.Updates, len(req.Updates)},
			{"deletes", statements.Deletes, len(req.Deletes)},
		} {
			if len(group.statements) != group.want {
				report("GridEditStatements: %d %s for %d row(s); there must be one per row", len(group.statements), group.name, group.want)
			}
			for i, statement := range group.statements {
				if strings.TrimSpace(statement) == "" {
					report("GridEditStatements: %s statement %d is empty", group.name, i)
				}
			}
		}
	}

	empty, err := s.GridEditStatements(ctx, database, table, dbdriver.GridEditRequest{Columns: first.Columns})
	if err == nil && (empty == nil || empty.Inserts == nil || empty.Updates == nil || empty.Deletes == nil) {
		report("GridEditStatements without rows: returned null; each group must be an empty list")
	}
	return problems
}

// alterProblems 检查改表：读出的表名是列表名去掉 schema 的那部分，列都是表格里有的列；把读出的样子
// 原样交回去，不能生成任何语句（序列化成 []，不是 null），表名不变。语句只生成，这里不执行。
func alterProblems(ctx context.Context, s dbdriver.Session, database, table string, columns []string) []string {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	model, err := s.AlterTableModel(ctx, database, table)
	switch {
	case errors.Is(err, dbdriver.ErrUnsupported):
		report("AlterTableModel: declared %q but refused with ErrUnsupported: %v", dbdriver.CapAlterTable, err)
		return problems
	case err != nil:
		report("AlterTableModel(%q): %v", table, err)
		return problems
	case model == nil || len(model.Columns) == 0:
		report("AlterTableModel(%q): returned no columns and no error", table)
		return problems
	}
	if model.Name == "" || !strings.HasSuffix(table, model.Name) {
		report("AlterTableModel: name %q is not the end of the table's name %q", model.Name, table)
	}
	req := dbdriver.AlterTableRequest{Name: model.Name, Comment: model.Comment, Drops: []string{}}
	for _, column := range model.Columns {
		if !slices.Contains(columns, column.Name) {
			report("AlterTableModel: column %q is not a column of the first page", column.Name)
		}
		req.Columns = append(req.Columns, dbdriver.AlterColumn{
			OriginalName: column.Name, Name: column.Name, Type: column.Type, Nullable: column.Nullable,
			DefaultValue: column.DefaultValue, Comment: column.Comment,
		})
	}
	result, err := s.AlterTableStatements(ctx, database, table, req)
	switch {
	case err != nil:
		report("AlterTableStatements with the table as it is: %v", err)
	case result == nil || result.Statements == nil:
		report("AlterTableStatements with the table as it is: returned null; it must be an empty list")
	case len(result.Statements) > 0:
		report("AlterTableStatements with the table as it is: generated %q; nothing changed, so there must be no statements", result.Statements)
	case result.Table != table:
		report("AlterTableStatements with the table as it is: the table is now named %q, not %q", result.Table, table)
	}
	return problems
}

// batchProblems 用必然失败的 ConformanceFailingStatement 检查整批执行（声明了能关闭外键检查的
// 驱动同时关闭它，最后必须恢复得了）：整批的那次停在第一条失败的语句并回滚，出错继续的那次逐条
// 执行；空白语句跳过，下标指向请求里的位置。失败的语句不改动任何数据，这里不检查表的内容。
func batchProblems(ctx context.Context, desc dbdriver.Descriptor, s dbdriver.Session, database, table string) []string {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	failing := []string{" ", ConformanceFailingStatement, ConformanceFailingStatement}

	for _, atomic := range []bool{true, false} {
		result, err := s.ExecuteBatch(ctx, dbdriver.BatchRequest{
			Statements: failing, Atomic: atomic, ContinueOnError: true,
			DisableForeignKeyChecks: desc.CanDisableForeignKeyChecks, Database: database, Table: table,
		})
		label := fmt.Sprintf("ExecuteBatch(atomic=%t, disableForeignKeyChecks=%t)", atomic, desc.CanDisableForeignKeyChecks)
		switch {
		case errors.Is(err, dbdriver.ErrUnsupported):
			report("%s: refused with ErrUnsupported: %v", label, err)
			continue
		case err != nil:
			report("%s: %v", label, err)
			continue
		case result == nil:
			report("%s: returned no result and no error", label)
			continue
		}
		wantIndexes := []int{1, 2}
		if atomic {
			wantIndexes = []int{1}
		}
		indexes := make([]int, 0, len(result.Executed))
		for _, executed := range result.Executed {
			indexes = append(indexes, executed.Index)
			if executed.Error == "" {
				report("%s: statement %d %q did not report its failure", label, executed.Index, ConformanceFailingStatement)
			}
		}
		if !slices.Equal(indexes, wantIndexes) {
			if atomic {
				report("%s: executed statements %v; the batch must skip the blank one and stop at the first failure %v", label, indexes, wantIndexes)
			} else {
				report("%s: executed statements %v; with continueOnError the batch must skip the blank one and continue past failures %v", label, indexes, wantIndexes)
			}
		}
		if result.RolledBack != atomic {
			report("%s: rolledBack is %t; a failed atomic batch must roll back, a non-atomic one has nothing to roll back", label, result.RolledBack)
		}
	}
	return problems
}

// resultProblems 检查 ConformanceStatement 的执行结果：必须成功，并返回成形的结果集。
func resultProblems(op string, res *dbdriver.Result, err error) []string {
	if errors.Is(err, dbdriver.ErrUnsupported) {
		return []string{fmt.Sprintf("%s: every driver must execute SQL, got: %v", op, err)}
	}
	if err != nil {
		return []string{fmt.Sprintf("%s(%q): %v", op, ConformanceStatement, err)}
	}
	if res == nil {
		return []string{fmt.Sprintf("%s: returned no result and no error", op)}
	}
	if res.Rows == nil {
		return []string{fmt.Sprintf("%s(%q): a query must return a row set", op, ConformanceStatement)}
	}
	return rowSetProblems(op, res.Rows)
}

// rowSetProblems 检查一个结果集成形：有列，每行的值与列一一对应，且能序列化成 JSON。
func rowSetProblems(op string, rows *dbdriver.RowSet) []string {
	var problems []string
	if len(rows.Columns) == 0 {
		problems = append(problems, fmt.Sprintf("%s: the row set has no columns", op))
	}
	for i, row := range rows.Rows {
		if len(row) != len(rows.Columns) {
			problems = append(problems, fmt.Sprintf("%s: row %d has %d values for %d columns", op, i, len(row), len(rows.Columns)))
		}
		if _, jsonErr := json.Marshal(row); jsonErr != nil {
			problems = append(problems, fmt.Sprintf("%s: row %d cannot be serialized to JSON: %v", op, i, jsonErr))
		}
	}
	return problems
}

// 名字会出现在命令行（--type <name>）与存储的配置里，只允许小写单词。
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func descriptorProblems(desc dbdriver.Descriptor) []string {
	var problems []string
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if !namePattern.MatchString(string(desc.ID)) {
		report("id %q must match %s", desc.ID, namePattern)
	}
	if desc.Name == "" {
		report("display name is empty")
	}
	if desc.Icon == "" {
		report("icon is empty")
	}
	// 展示顺序必须由驱动自己声明：留着零值的驱动会不声不响地排到所有驱动前面。
	if desc.Order <= 0 {
		report("order %d is not declared; it must be positive", desc.Order)
	}

	// 切片必须非 nil：描述原样序列化给前端，nil 会变成 null 而不是空数组。
	if desc.Aliases == nil {
		report("aliases is nil; declare an empty list when there are none")
	}
	seenNames := map[string]bool{string(desc.ID): true}
	for _, alias := range desc.Aliases {
		if !namePattern.MatchString(alias) {
			report("alias %q must match %s", alias, namePattern)
		}
		if seenNames[alias] {
			report("alias %q repeats the id or another alias", alias)
		}
		seenNames[alias] = true
	}

	if len(desc.Form) == 0 {
		report("form declares no tabs")
	}
	seenTabs := map[dbdriver.FormTab]bool{}
	seenFields := map[dbdriver.FormField]bool{}
	for _, tab := range desc.Form {
		if !slices.Contains(dbdriver.FormTabs(), tab.Tab) {
			report("form tab %q is not a known tab", tab.Tab)
		}
		if seenTabs[tab.Tab] {
			report("form tab %q is listed more than once", tab.Tab)
		}
		seenTabs[tab.Tab] = true
		if len(tab.Fields) == 0 {
			report("form tab %q declares no fields", tab.Tab)
		}
		for _, field := range tab.Fields {
			if !slices.Contains(dbdriver.FormFields(), field) {
				report("form field %q is not a known field", field)
			}
			if seenFields[field] {
				report("form field %q is used more than once", field)
			}
			seenFields[field] = true
		}
	}

	// 默认端口与端口字段必须一起出现：表单切到这个驱动时用它预填端口。
	if desc.UsesField(dbdriver.FieldPort) {
		if desc.DefaultPort < 1 || desc.DefaultPort > 65535 {
			report("default port %d is not a valid port, but the form uses the port field", desc.DefaultPort)
		}
	} else if desc.DefaultPort != 0 {
		report("default port %d is declared, but the form has no port field", desc.DefaultPort)
	}

	if desc.Capabilities == nil {
		report("capabilities is nil; declare an empty list when there are none")
	}
	seenCaps := map[dbdriver.Capability]bool{}
	for _, c := range desc.Capabilities {
		if !slices.Contains(dbdriver.Capabilities(), c) {
			report("capability %q is not a known capability", c)
		}
		if seenCaps[c] {
			report("capability %q is declared more than once", c)
		}
		seenCaps[c] = true
	}

	// 没有声明的功能不能有入口：表单上的只读开关就是连接级只读的入口，会话打开时没声明
	// CapReadOnly 的驱动会拒绝只读配置。
	if desc.UsesField(dbdriver.FieldReadOnly) && !desc.Supports(dbdriver.CapReadOnly) {
		report("form field %q is declared without capability %q; the switch would offer what Open refuses",
			dbdriver.FieldReadOnly, dbdriver.CapReadOnly)
	}
	// 细项声明只在声明了所属的功能时有意义。
	if desc.CanDisableForeignKeyChecks && !desc.Supports(dbdriver.CapImport) {
		report("canDisableForeignKeyChecks is declared without capability %q, the only feature that uses it", dbdriver.CapImport)
	}
	if desc.LocatesRowWithoutPrimaryKey && !desc.Supports(dbdriver.CapGridEdit) {
		report("locatesRowWithoutPrimaryKey is declared without capability %q, the only feature that uses it", dbdriver.CapGridEdit)
	}
	for _, option := range desc.CreateDatabaseOptions {
		if option.Key != dbdriver.OptionCharset && option.Key != dbdriver.OptionCollation {
			report("createDatabaseOptions has the key %q, which the create database dialog has no field text for", option.Key)
		}
	}
	if len(desc.CreateDatabaseOptions) > 0 && !desc.Supports(dbdriver.CapCreateDatabase) {
		report("createDatabaseOptions are declared without capability %q, the only feature that uses them", dbdriver.CapCreateDatabase)
	}
	seenAttributes := map[dbdriver.AlterAttribute]bool{}
	for _, attribute := range desc.AlterTableAttributes {
		if !slices.Contains(dbdriver.AlterAttributes(), attribute) {
			report("alterTableAttributes has %q, which the alter table dialog has no input for", attribute)
		}
		if seenAttributes[attribute] {
			report("alter table attribute %q is declared more than once", attribute)
		}
		seenAttributes[attribute] = true
	}
	if len(desc.AlterTableAttributes) > 0 && !desc.Supports(dbdriver.CapAlterTable) {
		report("alterTableAttributes are declared without capability %q, the only feature that uses them", dbdriver.CapAlterTable)
	}
	return problems
}

func registrationProblems(d dbdriver.Driver) []string {
	var problems []string
	desc := d.Descriptor()
	if got, err := dbregistry.Lookup(desc.ID); err != nil {
		problems = append(problems, fmt.Sprintf("Lookup(%q): %v", desc.ID, err))
	} else if got != d {
		problems = append(problems, fmt.Sprintf("Lookup(%q) returns a different driver", desc.ID))
	}
	for _, name := range desc.Names() {
		if got, ok := dbregistry.ByName(name); !ok || got != d {
			problems = append(problems, fmt.Sprintf("ByName(%q) does not resolve to this driver", name))
		}
	}
	return problems
}

// RowStatements 备好 writer 再生成一次行语句：驱动测试检查一个请求生成的文本时用。
func RowStatements(ctx context.Context, s dbdriver.Session, database, table string, req dbdriver.RowStatementsRequest) ([]string, error) {
	writer, err := s.PrepareRowStatements(ctx, database, table)
	if err != nil {
		return nil, err
	}
	return writer.RowStatements(req)
}
