package dbdrivertest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func openFake(t *testing.T, fake *Fake, cfg *dbdriver.Config) *FakeSession {
	t.Helper()
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: cfg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session.(*FakeSession)
}

// 假驱动的会话不是 database/sql 的句柄：它只有驱动契约上的方法，结果直接来自内存里的表。
func TestFakeSessionExecutesWithoutADatabaseHandle(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h", Database: "app"})
	ctx := context.Background()

	res, err := session.Execute(ctx, "SELECT * FROM t")
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Equal(t, fake.Columns, res.Rows.Columns)
	assert.Equal(t, fake.Rows, res.Rows.Rows)
	assert.Nil(t, res.Rows.Total, "an unpaged execution reports no total")

	res, err = session.Execute(ctx, "DELETE FROM t")
	require.NoError(t, err)
	assert.Nil(t, res.Rows)
	assert.Equal(t, int64(len(fake.Rows)), res.AffectedRows)

	assert.Equal(t, []string{"SELECT * FROM t", "DELETE FROM t"}, session.Statements())
	assert.Equal(t, "app", session.Request.Config.Database, "the session sees the config it was opened with")
}

func TestFakeSessionPagesItsRows(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	require.Len(t, fake.Rows, 3)

	res, err := session.ExecutePaged(context.Background(), "SELECT * FROM t", dbdriver.Page{Index: 1, Size: 2})
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Equal(t, fake.Rows[2:], res.Rows.Rows, "the second page of two holds the third row")
	require.NotNil(t, res.Rows.Total)
	assert.Equal(t, int64(3), *res.Rows.Total)

	res, err = session.ExecutePaged(context.Background(), "SELECT * FROM t", dbdriver.Page{Index: 5, Size: 2})
	require.NoError(t, err)
	assert.Empty(t, res.Rows.Rows, "a page past the end is empty, not an error")
	assert.Equal(t, int64(3), *res.Rows.Total)
}

// 连接级只读由驱动落实：假驱动的只读会话拒绝写语句。
func TestFakeSessionEnforcesReadOnlyItself(t *testing.T) {
	session := openFake(t, NewFake(), &dbdriver.Config{Driver: "fakedb", Host: "h", ReadOnly: true})

	_, err := session.Execute(context.Background(), "DELETE FROM t")
	require.ErrorIs(t, err, ErrFakeReadOnly)
	_, err = session.Execute(context.Background(), "SELECT * FROM t")
	require.NoError(t, err)
}

func TestFakeSessionRefusesWorkAfterClose(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	require.NoError(t, session.Close())

	assert.True(t, session.Closed())
	_, err := session.Execute(context.Background(), "SELECT * FROM t")
	require.ErrorIs(t, err, ErrFakeSessionClosed)
	assert.Equal(t, []*FakeSession{session}, fake.Sessions())
}

func TestFakeOpenReturnsItsConnectionError(t *testing.T) {
	fake := NewFake()
	fake.OpenErr = errors.New("fakedb: connection refused")

	_, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "fakedb"}})
	require.ErrorIs(t, err, fake.OpenErr)
	assert.Empty(t, fake.Sessions())
}

// 没声明连接级只读的驱动必须拒绝只读配置，而不是悄悄打开可写的会话。
func TestFakeWithoutReadOnlyRefusesReadOnlyConfig(t *testing.T) {
	fake := NewFake()
	fake.Desc.Capabilities = []dbdriver.Capability{dbdriver.CapViews}
	// 不支持连接级只读，表单上也就没有只读开关：只留连接 Tab。
	fake.Desc.Form = fake.Desc.Form[:1]

	_, err := fake.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{Driver: "fakedb", Host: "h", ReadOnly: true},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, fake.Sessions())

	Register(t, fake)
	RunConformance(t, fake, FakeSessions(fake))
}

// --- 一致性测试自己也得能失败 ---

type alterResult func(*dbdriver.Result, error) (*dbdriver.Result, error)

// brokenSession 在假会话之上按需改坏一处。
type brokenSession struct {
	dbdriver.Session
	execute      alterResult
	executePaged alterResult
	closeErr     error
	// generates 为真时不管声明与否都给出行语句；gridEdits 为真时不管声明与否都给出表格编辑语句。
	generates bool
	gridEdits bool
	// schemaWrites 为真时不管声明与否都给出建表、删表、清空、建库的语句。
	schemaWrites bool
	// imports 为真时不管声明与否都给出导入语句。
	imports bool
}

func (b brokenSession) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	if b.imports {
		return []string{"INSERT"}, nil
	}
	return b.Session.ImportStatements(ctx, database, table, req)
}

func (b brokenSession) CreateTableStatements(ctx context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	if b.schemaWrites {
		return []string{"CREATE TABLE"}, nil
	}
	return b.Session.CreateTableStatements(ctx, database, req)
}

func (b brokenSession) DropTableStatements(ctx context.Context, database, table string) ([]string, error) {
	if b.schemaWrites {
		return []string{"DROP TABLE"}, nil
	}
	return b.Session.DropTableStatements(ctx, database, table)
}

func (b brokenSession) TruncateTableStatements(ctx context.Context, database, table string) ([]string, error) {
	if b.schemaWrites {
		return []string{"TRUNCATE TABLE"}, nil
	}
	return b.Session.TruncateTableStatements(ctx, database, table)
}

func (b brokenSession) CreateDatabaseStatements(ctx context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	if b.schemaWrites {
		return []string{"CREATE DATABASE"}, nil
	}
	return b.Session.CreateDatabaseStatements(ctx, req)
}

func (b brokenSession) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	if b.gridEdits {
		return &dbdriver.GridEditStatements{Inserts: []string{"INSERT"}}, nil
	}
	return b.Session.GridEditStatements(ctx, database, table, req)
}

func (b brokenSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	if b.generates {
		return fixedRowWriter{"INSERT"}, nil
	}
	return b.Session.PrepareRowStatements(ctx, database, table)
}

// fixedRowWriter 不管请求，总给出同样的语句。
type fixedRowWriter []string

func (w fixedRowWriter) RowStatements(dbdriver.RowStatementsRequest) ([]string, error) { return w, nil }

func (b brokenSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	res, err := b.Session.Execute(ctx, statement)
	if b.execute != nil {
		return b.execute(res, err)
	}
	return res, err
}

func (b brokenSession) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	res, err := b.Session.ExecutePaged(ctx, statement, page)
	if b.executePaged != nil {
		return b.executePaged(res, err)
	}
	return res, err
}

func (b brokenSession) Close() error {
	if err := b.Session.Close(); err != nil {
		return err
	}
	return b.closeErr
}

func TestConformanceReportsBrokenSessions(t *testing.T) {
	unsupported := func(*dbdriver.Result, error) (*dbdriver.Result, error) {
		return nil, fmt.Errorf("%w: execute", dbdriver.ErrUnsupported)
	}
	withTotal := func(total int64) alterResult {
		return func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			res.Rows.Total = &total
			return res, err
		}
	}
	for name, tt := range map[string]struct {
		broken brokenSession
		want   string
	}{
		"execute refused as unsupported":       {brokenSession{execute: unsupported}, "Execute"},
		"paged execute refused as unsupported": {brokenSession{executePaged: unsupported}, "ExecutePaged"},
		"execute fails": {brokenSession{execute: func(*dbdriver.Result, error) (*dbdriver.Result, error) {
			return nil, errors.New("boom")
		}}, "boom"},
		"no result": {brokenSession{execute: func(*dbdriver.Result, error) (*dbdriver.Result, error) {
			return nil, nil
		}}, "no result"},
		"query without a row set": {brokenSession{execute: func(*dbdriver.Result, error) (*dbdriver.Result, error) {
			return &dbdriver.Result{AffectedRows: 1}, nil
		}}, "row set"},
		"row set without columns": {brokenSession{execute: func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			res.Rows.Columns = nil
			return res, err
		}}, "columns"},
		"row shorter than the columns": {brokenSession{execute: func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			res.Rows.Rows = [][]any{{1}}
			return res, err
		}}, "values"},
		"value that cannot be serialized": {brokenSession{execute: func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			res.Rows.Rows = [][]any{{1, make(chan int)}}
			return res, err
		}}, "JSON"},
		"unpaged execute reports a total": {brokenSession{execute: withTotal(3)}, "total"},
		"paged execute without a total": {brokenSession{executePaged: func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			res.Rows.Total = nil
			return res, err
		}}, "total"},
		"total smaller than the page": {brokenSession{executePaged: withTotal(1)}, "total"},
		"page larger than requested": {brokenSession{executePaged: func(res *dbdriver.Result, err error) (*dbdriver.Result, error) {
			for len(res.Rows.Rows) <= ConformancePageSize {
				res.Rows.Rows = append(res.Rows.Rows, res.Rows.Rows[0])
			}
			return withTotal(int64(len(res.Rows.Rows)))(res, err)
		}}, "page"},
		"close fails":                          {brokenSession{closeErr: errors.New("still busy")}, "still busy"},
		"undeclared row statements generated":  {brokenSession{generates: true}, "PrepareRowStatements"},
		"undeclared grid edit generated":       {brokenSession{gridEdits: true}, "GridEditStatements"},
		"undeclared import generated":          {brokenSession{imports: true}, "ImportStatements"},
		"undeclared create table generated":    {brokenSession{schemaWrites: true}, "CreateTableStatements"},
		"undeclared drop table generated":      {brokenSession{schemaWrites: true}, "DropTableStatements"},
		"undeclared truncate generated":        {brokenSession{schemaWrites: true}, "TruncateTableStatements"},
		"undeclared create database generated": {brokenSession{schemaWrites: true}, "CreateDatabaseStatements"},
	} {
		t.Run(name, func(t *testing.T) {
			tt.broken.Session = openFake(t, NewFake(), &dbdriver.Config{Driver: "fakedb", Host: "h"})
			problems := sessionProblems(NewFake().Desc, tt.broken)
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

// 没声明只读却照样打开只读配置的驱动。
type ignoresReadOnly struct{ *Fake }

func (d ignoresReadOnly) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	cfg := *req.Config
	cfg.ReadOnly = false
	req.Config = &cfg
	return d.Fake.Open(ctx, req)
}

// 没声明只读、遇到只读配置时报的却不是「不支持」的驱动。
type misreportsReadOnly struct{ *Fake }

func (d misreportsReadOnly) Open(context.Context, dbdriver.OpenRequest) (dbdriver.Session, error) {
	return nil, errors.New("connection refused")
}

func TestConformanceReportsDriversThatMishandleUndeclaredReadOnly(t *testing.T) {
	fake := NewFake()
	fake.Desc.Capabilities = []dbdriver.Capability{}

	assert.Empty(t, readOnlyProblems(fake))

	problems := readOnlyProblems(ignoresReadOnly{fake})
	require.NotEmpty(t, problems)
	assert.Contains(t, problems[0], "read_only")
	require.NotEmpty(t, fake.Sessions())
	for _, s := range fake.Sessions() {
		assert.True(t, s.Closed(), "the conformance suite closes a session it should never have been given")
	}

	problems = readOnlyProblems(misreportsReadOnly{fake})
	require.NotEmpty(t, problems)
	assert.Contains(t, problems[0], "connection refused")

	// 声明了只读的驱动不在这一项的检查范围内：它怎么落实只读由各驱动自己的测试固定。
	assert.Empty(t, readOnlyProblems(misreportsReadOnly{NewFake()}))
}

// 声明了建表、删表、清空、建库却被会话拒绝；声明了建库选项却没有声明建库。
func TestConformanceReportsDeclaredSchemaWritesBeingRefused(t *testing.T) {
	desc := NewFake().Desc
	desc.Capabilities = append(desc.Capabilities,
		dbdriver.CapCreateTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable, dbdriver.CapCreateDatabase)
	session := openFake(t, NewFake(), &dbdriver.Config{Driver: "fakedb", Host: "h"})

	joined := strings.Join(schemaWriteProblems(context.Background(), desc, session), "\n")
	for _, name := range []string{"CreateTableStatements", "DropTableStatements", "TruncateTableStatements", "CreateDatabaseStatements"} {
		assert.Contains(t, joined, name+": declared")
	}

	desc = NewFake().Desc
	desc.CreateDatabaseOptions = []dbdriver.DatabaseOption{{Key: dbdriver.OptionCollation}}
	assert.Contains(t, strings.Join(descriptorProblems(desc), "\n"), "createDatabaseOptions")
}
