package dbdrivertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opskat/opskat/pkg/dbdriver"
)

var (
	// ErrFakeSessionClosed：在已关闭的假会话上执行。
	ErrFakeSessionClosed = errors.New("fakedb session is closed")
	// ErrFakeReadOnly：在只读的假会话上执行写语句。只读由假驱动自己落实，共享代码不替它拦。
	ErrFakeReadOnly = errors.New("fakedb session is read-only")
)

// Open 打开一条假会话。没有声明 CapReadOnly 时拒绝只读配置。
func (f *Fake) Open(_ context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if f.OpenErr != nil {
		return nil, f.OpenErr
	}
	if req.Config.ReadOnly && !f.Desc.Supports(dbdriver.CapReadOnly) {
		return nil, fmt.Errorf("%w: %s has no connection-level read-only", dbdriver.ErrUnsupported, f.Desc.ID)
	}
	// 留一份打开那一刻的配置：调用方之后再改它的配置对象，不影响已经打开的会话。
	cfg := *req.Config
	req.Config = &cfg
	session := &FakeSession{Request: req, fake: f}
	f.mu.Lock()
	f.sessions = append(f.sessions, session)
	f.mu.Unlock()
	return session, nil
}

// SessionPerDatabase 照 SessionPerAsset 的声明回答。
func (f *Fake) SessionPerDatabase() bool { return !f.SessionPerAsset }

// Sessions 返回这个假驱动打开过的全部会话，按打开的先后。
func (f *Fake) Sessions() []*FakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sessions)
}

// FakeSessions 是假驱动给一致性测试用的会话工厂。
func FakeSessions(f *Fake) SessionFactory {
	return func(t *testing.T) dbdriver.Session {
		t.Helper()
		session, err := f.Open(context.Background(), dbdriver.OpenRequest{
			Config: &dbdriver.Config{Driver: f.Desc.ID, Host: "fake.internal", Port: f.Desc.DefaultPort},
		})
		if err != nil {
			t.Fatalf("open fake session: %v", err)
		}
		return session
	}
}

// FakeSession 是假驱动的会话：一张内存里的表，外加它被要求执行过的语句的记录。
type FakeSession struct {
	// Request 是打开这条会话的请求。
	Request dbdriver.OpenRequest

	fake       *Fake
	mu         sync.Mutex
	statements []string
	tableCalls []TableCall
	closed     bool
}

// Statements 返回这条会话执行过的语句，按先后。
func (s *FakeSession) Statements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.statements)
}

// Closed 报告会话是否已被关闭。
func (s *FakeSession) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Execute：SELECT 返回整张表，其余语句算作改动了表里的每一行。
func (s *FakeSession) Execute(_ context.Context, statement string) (*dbdriver.Result, error) {
	isQuery, err := s.record(statement)
	if err != nil {
		return nil, err
	}
	if !isQuery {
		return &dbdriver.Result{AffectedRows: int64(len(s.fake.Rows))}, nil
	}
	return &dbdriver.Result{Rows: &dbdriver.RowSet{Columns: s.fake.Columns, Rows: s.fake.Rows}}, nil
}

// ExecutePaged：SELECT 返回表里的一页和总行数，其余语句同 Execute。
func (s *FakeSession) ExecutePaged(_ context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	isQuery, err := s.record(statement)
	if err != nil {
		return nil, err
	}
	if !isQuery {
		return &dbdriver.Result{AffectedRows: int64(len(s.fake.Rows))}, nil
	}
	total := int64(len(s.fake.Rows))
	start := min(page.Index*page.Size, len(s.fake.Rows))
	end := min(start+page.Size, len(s.fake.Rows))
	return &dbdriver.Result{Rows: &dbdriver.RowSet{
		Columns: s.fake.Columns,
		Rows:    s.fake.Rows[start:end],
		Total:   &total,
	}}, nil
}

// 树的浏览直接取自假库里放着的内容，不经过 Execute：Statements 因此不会有它们的记录。

func (s *FakeSession) ListDatabases(context.Context) ([]string, error) {
	if err := s.browse(); err != nil {
		return nil, err
	}
	return s.fake.Databases, nil
}

func (s *FakeSession) ListTables(_ context.Context, database string) ([]string, error) {
	if err := s.browse(); err != nil {
		return nil, err
	}
	return s.fake.Tables[database], nil
}

func (s *FakeSession) TableMetadata(context.Context, string, string) (*dbdriver.TableMetadata, error) {
	if err := s.browse(); err != nil {
		return nil, err
	}
	return s.fake.Metadata, nil
}

func (s *FakeSession) ListObjects(context.Context, string) (*dbdriver.DatabaseObjects, error) {
	if err := s.browse(); err != nil {
		return nil, err
	}
	return s.fake.Objects, nil
}

func (s *FakeSession) ObjectSource(_ context.Context, _, objType, name, _ string) (string, error) {
	if err := s.fake.Desc.CheckObjectType(objType); err != nil {
		return "", err
	}
	if err := s.browse(); err != nil {
		return "", err
	}
	return s.fake.Sources[objType+":"+name], nil
}

// 表浏览同样直接取自内存里的那张表，不经过 Execute。假驱动不解释筛选与排序：它把收到的
// 结构化请求原样记下（TableCalls），证明到它手里的不是 SQL 文本。

// TableCall 是假会话收到的一次表浏览请求。
type TableCall struct {
	// Op 是 "open" / "rows" / "count" / "stream" / "ddl" / "starter" / "prepare_row_statements" / "row_statements" / "grid_edit" / "import" / "create_table" / "drop_table" / "truncate_table" / "create_database" / "alter_model" / "alter_table" / "batch"。
	Op       string
	Database string
	Table    string
	// Query 是 rows 的筛选与排序；count 只有其中的筛选。
	Query dbdriver.TableQuery
	// Page 是 open / rows 要的那一页。
	Page dbdriver.Page
	// RowStatements 是 row_statements 收到的请求。
	RowStatements *dbdriver.RowStatementsRequest
	// GridEdit 是 grid_edit 收到的请求。
	GridEdit *dbdriver.GridEditRequest
	// Import 是 import 收到的请求。
	Import *dbdriver.ImportRequest
	// CreateTable 是 create_table 收到的请求。
	CreateTable *dbdriver.CreateTableRequest
	// CreateDatabase 是 create_database 收到的请求。
	CreateDatabase *dbdriver.CreateDatabaseRequest
	// AlterTable 是 alter_table 收到的请求。
	AlterTable *dbdriver.AlterTableRequest
	// Batch 是 batch 收到的请求。
	Batch *dbdriver.BatchRequest
}

// TableCalls 返回这条会话收到的表浏览请求，按先后。
func (s *FakeSession) TableCalls() []TableCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tableCalls)
}

func (s *FakeSession) OpenTable(_ context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	page := dbdriver.Page{Index: 0, Size: pageSize}
	if err := s.tableCall(TableCall{Op: "open", Database: database, Table: table, Page: page}); err != nil {
		return nil, err
	}
	return &dbdriver.OpenedTable{
		ColumnTypes:      s.fake.ColumnTypes,
		ColumnCategories: s.fake.ColumnCategories,
		ColumnRules:      s.fake.ColumnRules,
		PrimaryKeys:      s.fake.PrimaryKeys,
		TotalCount:       int64(len(s.fake.Rows)),
		FirstPage:        s.page(page),
	}, nil
}

func (s *FakeSession) TableRows(_ context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	if err := s.tableCall(TableCall{Op: "rows", Database: database, Table: table, Query: query, Page: page}); err != nil {
		return nil, err
	}
	return s.page(page), nil
}

func (s *FakeSession) CountTableRows(_ context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	call := TableCall{Op: "count", Database: database, Table: table, Query: dbdriver.TableQuery{Filters: filters}}
	if err := s.tableCall(call); err != nil {
		return 0, err
	}
	return int64(len(s.fake.Rows)), nil
}

// StreamTableRows 记下收到的请求（Op "stream"），把整张表交给一个内存游标。
func (s *FakeSession) StreamTableRows(_ context.Context, database, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	if err := s.tableCall(TableCall{Op: "stream", Database: database, Table: table, Query: query}); err != nil {
		return nil, err
	}
	return &fakeStream{columns: s.fake.Columns, rows: s.fake.Rows, next: -1}, nil
}

type fakeStream struct {
	columns []string
	rows    [][]any
	next    int
}

func (f *fakeStream) Columns() []string { return f.columns }
func (f *fakeStream) Next() bool {
	f.next++
	return f.next < len(f.rows)
}
func (f *fakeStream) Row() []any   { return f.rows[f.next] }
func (f *fakeStream) Err() error   { return nil }
func (f *fakeStream) Close() error { return nil }

// TableDDL 记下收到的请求（Op "ddl"）。驱动没有声明 CapViewDDL 时以 ErrUnsupported 拒绝，
// 什么也不记、不执行。
func (s *FakeSession) TableDDL(_ context.Context, database, table string) (string, error) {
	if !s.fake.Desc.Supports(dbdriver.CapViewDDL) {
		return "", fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapViewDDL)
	}
	if err := s.tableCall(TableCall{Op: "ddl", Database: database, Table: table}); err != nil {
		return "", err
	}
	return s.fake.DDL, nil
}

// StarterQuery 记下收到的请求（Op "starter"，Page.Size 为 limit），给出一条一眼能与任何真实
// 方言区分的语句。
func (s *FakeSession) StarterQuery(_ context.Context, database, table string, limit int) (string, error) {
	if err := s.tableCall(TableCall{Op: "starter", Database: database, Table: table, Page: dbdriver.Page{Size: limit}}); err != nil {
		return "", err
	}
	return fmt.Sprintf("FAKE SELECT FIRST %d FROM %s.%s", limit, database, table), nil
}

// PrepareRowStatements 记下收到的请求（Op "prepare_row_statements"），交回一个 writer。驱动没有
// 声明导出 / 复制为 SQL 时以 ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) PrepareRowStatements(_ context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	if !s.fake.Desc.GeneratesRowStatements() {
		return nil, fmt.Errorf("%w: %s declares neither %s nor %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapExportSQL, dbdriver.CapCopySQL)
	}
	if err := s.tableCall(TableCall{Op: "prepare_row_statements", Database: database, Table: table}); err != nil {
		return nil, err
	}
	return fakeRowWriter{session: s, database: database, table: table}, nil
}

// fakeRowWriter 记下收到的请求（Op "row_statements"，记在备好它的会话上），每行给出一条一眼
// 能与任何真实方言区分的语句。它不碰会话：会话关了、TableErr 设了也照样生成。
type fakeRowWriter struct {
	session         *FakeSession
	database, table string
}

func (w fakeRowWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	w.session.mu.Lock()
	w.session.tableCalls = append(w.session.tableCalls, TableCall{Op: "row_statements", Database: w.database, Table: w.table, RowStatements: &req})
	w.session.mu.Unlock()
	statements := make([]string, 0, len(req.Rows))
	for i := range req.Rows {
		statements = append(statements, fmt.Sprintf("FAKE %s ROW %d OF %s.%s", strings.ToUpper(string(req.Kind)), i+1, w.database, w.table))
	}
	return statements, nil
}

// GridEditStatements 记下收到的请求（Op "grid_edit"），新增、改动、删除的每一行各给出一条一眼能
// 与任何真实方言区分的语句。驱动没有声明表格编辑时以 ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) GridEditStatements(_ context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	if !s.fake.Desc.Supports(dbdriver.CapGridEdit) {
		return nil, fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapGridEdit)
	}
	if err := s.tableCall(TableCall{Op: "grid_edit", Database: database, Table: table, GridEdit: &req}); err != nil {
		return nil, err
	}
	statements := func(kind string, n int) []string {
		out := make([]string, 0, n)
		for i := range n {
			out = append(out, fmt.Sprintf("FAKE %s ROW %d OF %s.%s", kind, i+1, database, table))
		}
		return out
	}
	return &dbdriver.GridEditStatements{
		Inserts: statements("INSERT", len(req.Inserts)),
		Updates: statements("UPDATE", len(req.Updates)),
		Deletes: statements("DELETE", len(req.Deletes)),
	}, nil
}

// ImportStatements 记下收到的请求（Op "import"），每行给出一条一眼能与任何真实方言区分的语句。
// 驱动没有声明导入时以 ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) ImportStatements(_ context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	if !s.fake.Desc.Supports(dbdriver.CapImport) {
		return nil, fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapImport)
	}
	if err := s.tableCall(TableCall{Op: "import", Database: database, Table: table, Import: &req}); err != nil {
		return nil, err
	}
	statements := make([]string, 0, len(req.Rows))
	for i := range req.Rows {
		statements = append(statements, fmt.Sprintf("FAKE IMPORT %s ROW %d OF %s.%s", strings.ToUpper(string(req.Mode)), i+1, database, table))
	}
	return statements, nil
}

// writeStatement 是建表、删表、清空、建库四个会话方法的共同做法：没有声明对应功能时以 ErrUnsupported
// 拒绝（什么也不记、不执行），否则记下请求（call），给出一条一眼能与任何真实方言区分的语句。
func (s *FakeSession) writeStatement(capability dbdriver.Capability, call TableCall, statement string) ([]string, error) {
	if !s.fake.Desc.Supports(capability) {
		return nil, fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, capability)
	}
	if err := s.tableCall(call); err != nil {
		return nil, err
	}
	return []string{statement}, nil
}

// CreateTableStatements 记下收到的请求（Op "create_table"）。
func (s *FakeSession) CreateTableStatements(_ context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	return s.writeStatement(dbdriver.CapCreateTable,
		TableCall{Op: "create_table", Database: database, Table: req.Name, CreateTable: &req},
		fmt.Sprintf("FAKE CREATE TABLE %s.%s", database, req.Name))
}

// DropTableStatements 记下收到的请求（Op "drop_table"）。
func (s *FakeSession) DropTableStatements(_ context.Context, database, table string) ([]string, error) {
	return s.writeStatement(dbdriver.CapDropTable,
		TableCall{Op: "drop_table", Database: database, Table: table},
		fmt.Sprintf("FAKE DROP TABLE %s.%s", database, table))
}

// TruncateTableStatements 记下收到的请求（Op "truncate_table"）。
func (s *FakeSession) TruncateTableStatements(_ context.Context, database, table string) ([]string, error) {
	return s.writeStatement(dbdriver.CapTruncateTable,
		TableCall{Op: "truncate_table", Database: database, Table: table},
		fmt.Sprintf("FAKE TRUNCATE TABLE %s.%s", database, table))
}

// CreateDatabaseStatements 记下收到的请求（Op "create_database"）。
func (s *FakeSession) CreateDatabaseStatements(_ context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	return s.writeStatement(dbdriver.CapCreateDatabase,
		TableCall{Op: "create_database", Table: req.Name, CreateDatabase: &req},
		fmt.Sprintf("FAKE CREATE DATABASE %s", req.Name))
}

// ExecuteBatch 记下收到的请求（Op "batch"），按次序执行每条非空白语句（记进 Statements，每条
// 写语句改动表里的每一行）；Fake.StatementErrors 里的语句失败。假库没有事务：Atomic 的批停在
// 失败的那条并报告回滚。驱动没有声明 CanDisableForeignKeyChecks 却被要求关闭外键检查时以
// ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) ExecuteBatch(_ context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	if req.DisableForeignKeyChecks && !s.fake.Desc.CanDisableForeignKeyChecks {
		return nil, fmt.Errorf("%w: %s does not declare disabling foreign key checks", dbdriver.ErrUnsupported, s.fake.Desc.ID)
	}
	if err := s.tableCall(TableCall{Op: "batch", Database: req.Database, Table: req.Table, Batch: &req}); err != nil {
		return nil, err
	}
	result := &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{}}
	for index, statement := range req.Statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		_, err := s.record(statement)
		if err == nil {
			err = s.fake.StatementErrors[statement]
		}
		if err != nil {
			result.Executed = append(result.Executed, dbdriver.BatchStatement{Index: index, Error: err.Error()})
			if req.Atomic {
				result.RolledBack = true
				break
			}
			if !req.ContinueOnError {
				break
			}
			continue
		}
		result.Executed = append(result.Executed, dbdriver.BatchStatement{Index: index, AffectedRows: int64(len(s.fake.Rows))})
	}
	return result, nil
}

func (s *FakeSession) page(page dbdriver.Page) *dbdriver.RowSet {
	start := min(page.Index*page.Size, len(s.fake.Rows))
	end := min(start+page.Size, len(s.fake.Rows))
	return &dbdriver.RowSet{Columns: s.fake.Columns, Rows: s.fake.Rows[start:end]}
}

func (s *FakeSession) tableCall(call TableCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrFakeSessionClosed
	}
	if s.fake.TableErr != nil {
		return s.fake.TableErr
	}
	s.tableCalls = append(s.tableCalls, call)
	return nil
}

func (s *FakeSession) browse() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrFakeSessionClosed
	}
	return s.fake.BrowseErr
}

func (s *FakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *FakeSession) record(statement string) (isQuery bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrFakeSessionClosed
	}
	isQuery = strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "SELECT")
	if !isQuery && s.Request.Config.ReadOnly {
		return false, ErrFakeReadOnly
	}
	s.statements = append(s.statements, statement)
	return isQuery, nil
}

// AlterTableModel 记下收到的请求（Op "alter_model"），把假库的表元数据（Metadata 的列）当作这张表给出，
// 表名原样、没有表注释。驱动没有声明改表时以 ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) AlterTableModel(_ context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	if !s.fake.Desc.Supports(dbdriver.CapAlterTable) {
		return nil, fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapAlterTable)
	}
	if err := s.tableCall(TableCall{Op: "alter_model", Database: database, Table: table}); err != nil {
		return nil, err
	}
	return s.alterModel(table), nil
}

func (s *FakeSession) alterModel(table string) *dbdriver.AlterTableModel {
	model := &dbdriver.AlterTableModel{Name: table, Columns: make([]dbdriver.ColumnDefinition, 0, len(s.fake.Metadata.Columns))}
	for _, column := range s.fake.Metadata.Columns {
		definition := dbdriver.ColumnDefinition{Name: column.Name, Type: column.Type, Nullable: column.Nullable, Comment: column.Comment}
		if column.Default != nil {
			definition.DefaultValue = *column.Default
		}
		model.Columns = append(model.Columns, definition)
	}
	return model
}

// AlterTableStatements 记下收到的请求（Op "alter_table"）：请求与 AlterTableModel 给出的样子相同时没有
// 语句，否则给出一条一眼能与任何真实方言区分的语句；改了名时表名是新名字。驱动没有声明改表时以
// ErrUnsupported 拒绝，什么也不记、不执行。
func (s *FakeSession) AlterTableStatements(_ context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	if !s.fake.Desc.Supports(dbdriver.CapAlterTable) {
		return nil, fmt.Errorf("%w: %s does not declare %s", dbdriver.ErrUnsupported, s.fake.Desc.ID, dbdriver.CapAlterTable)
	}
	if err := s.tableCall(TableCall{Op: "alter_table", Database: database, Table: table, AlterTable: &req}); err != nil {
		return nil, err
	}
	result := &dbdriver.AlterTableStatements{Statements: []string{}, Table: table}
	if req.Name != "" && req.Name != table {
		result.Table = req.Name
	}
	model := s.alterModel(table)
	unchanged := result.Table == table && req.Comment == model.Comment && len(req.Drops) == 0 && len(req.Columns) == len(model.Columns)
	for i := 0; unchanged && i < len(req.Columns); i++ {
		unchanged = req.Columns[i].OriginalName == model.Columns[i].Name && req.Columns[i].Definition() == model.Columns[i]
	}
	if !unchanged {
		result.Statements = []string{fmt.Sprintf("FAKE ALTER TABLE %s.%s", database, table)}
	}
	return result, nil
}
