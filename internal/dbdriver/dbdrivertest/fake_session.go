package dbdrivertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
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
			Config: &asset_entity.DatabaseConfig{Driver: f.Desc.ID, Host: "fake.internal", Port: f.Desc.DefaultPort},
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
