package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension/driverwire"
)

// A database driver an extension provides runs in the guest: the SDK assembles
// its session there (pkg/extsdk DatabaseDriver), and the host holds a
// dbdriver.Session that forwards every operation across the boundary as JSON
// (pkg/extension/driverwire). To shared code it is a session like any built-in
// driver's.
//
// A session lives in a module instance of its own, outside the plugin's call
// pool: the guest keeps the session, its connections and its cursors in that
// instance's memory between calls, and the instance's invocation — with its IO
// handle table, which holds the database connections — lasts as long as the
// session instead of one call.

// driverStreamRows is how many rows one cursor read brings over.
const driverStreamRows = 256

// OpenDriverSession opens a session of database driver id, which this extension
// must declare in capabilities.databaseDrivers. req is handed to the driver as
// is — its config, the decrypted password (this call only) and the reach path,
// which every TCP connection the driver opens takes. A driver that cannot
// connect fails with its own error.
func (p *Plugin) OpenDriverSession(ctx context.Context, id string, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("plugin closed")
	}
	if !slices.Contains(p.manifest.Capabilities.DatabaseDrivers, id) {
		return nil, fmt.Errorf("extension %q does not provide database driver %q", p.manifest.Name, id)
	}
	inst, err := p.newInstance(ctx)
	if err != nil {
		return nil, err
	}
	inv := newInvocation(p.nextInvocationID(), nil)
	inv.reach = &req.Transport
	conn := &driverInstance{inst: inst, inv: inv, refs: 1}

	var opened driverwire.Opened
	err = conn.call(ctx, driverwire.Call{Op: driverwire.OpOpen, Args: driverwire.Args{
		Driver: dbdriver.DriverID(id), Config: req.Config, Password: req.Password,
	}}, &opened)
	if err != nil {
		conn.inv.close()
		conn.release()
		return nil, err
	}
	return &driverSession{conn: conn, id: opened.Session}, nil
}

// driverInstance is the module instance a session lives in. Calls into it are
// serialized: an instance runs one call at a time.
//
// It is reference counted because a prepared row statement writer outlives its
// session (dbdriver.Session.PrepareRowStatements) and generates in the guest: the
// instance is closed once the session is closed and every writer prepared on it
// has been collected.
type driverInstance struct {
	inst *instance
	inv  *invocation

	mu   sync.Mutex
	refs int
	// broken is the failure that left the instance in an unknown state (a trap, a
	// canceled call); every later call fails with it.
	broken error
}

func (d *driverInstance) retain() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refs++
}

func (d *driverInstance) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refs--
	if d.refs > 0 {
		return
	}
	if err := d.inst.mod.Close(context.Background()); err != nil {
		logger.Default().Warn("close database driver instance", zap.Error(err))
	}
}

// call runs one operation and decodes its result into out (when the reply has
// one). The operation's own failure is returned as a driverError, after the
// result: a batch that failed part way answers both.
func (d *driverInstance) call(ctx context.Context, call driverwire.Call, out any) error {
	input, err := json.Marshal(call)
	if err != nil {
		return fmt.Errorf("marshal database driver %s: %w", call.Op, err)
	}
	req, err := callRequest(driverwire.Function, input)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.broken != nil {
		return fmt.Errorf("database driver session is unusable after an earlier failure: %w", d.broken)
	}
	raw, err := d.inst.invoke(withInvocation(ctx, d.inv), req)
	if err != nil {
		d.broken = err
		return fmt.Errorf("database driver %s: %w", call.Op, err)
	}

	var reply driverwire.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return fmt.Errorf("parse database driver %s reply: %w", call.Op, err)
	}
	if len(reply.Result) > 0 && out != nil {
		// Numbers stay as written (json.Number): a BIGINT read as float64 would
		// lose digits on its way to the grid, an export or the model.
		dec := json.NewDecoder(bytes.NewReader(reply.Result))
		dec.UseNumber()
		if err := dec.Decode(out); err != nil {
			return fmt.Errorf("parse database driver %s result: %w", call.Op, err)
		}
	}
	if reply.Error != nil {
		return &driverError{message: reply.Error.Message, code: reply.Error.Code}
	}
	return nil
}

// driverError is a driver operation's failure, with the driver's own message.
type driverError struct {
	message string
	code    string
}

func (e *driverError) Error() string { return e.message }

// Is lets callers recognize a refusal with errors.Is(err, dbdriver.ErrUnsupported),
// as they do for a built-in driver.
func (e *driverError) Is(target error) bool {
	return target == dbdriver.ErrUnsupported && e.code == driverwire.CodeUnsupported
}

// driverSession is the host's handle on a session in the guest.
type driverSession struct {
	conn   *driverInstance
	id     uint64
	closed atomic.Bool
}

var _ dbdriver.Session = (*driverSession)(nil)

func (s *driverSession) do(ctx context.Context, op string, args driverwire.Args, out any) error {
	if s.closed.Load() {
		return fmt.Errorf("database driver session is closed")
	}
	return s.conn.call(ctx, driverwire.Call{Op: op, Session: s.id, Args: args}, out)
}

// forward runs op and returns its decoded result.
func forward[T any](ctx context.Context, s *driverSession, op string, args driverwire.Args) (T, error) {
	var out T
	err := s.do(ctx, op, args, &out)
	return out, err
}

// forwardRequest runs op with req as its request.
func forwardRequest[T any](ctx context.Context, s *driverSession, op string, args driverwire.Args, req any) (T, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("marshal database driver %s request: %w", op, err)
	}
	args.Request = raw
	return forward[T](ctx, s, op, args)
}

func (s *driverSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, driverwire.OpExecute, driverwire.Args{Statement: statement})
}

func (s *driverSession) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	return forward[*dbdriver.Result](ctx, s, driverwire.OpExecutePaged, driverwire.Args{Statement: statement, Page: page})
}

func (s *driverSession) ListDatabases(ctx context.Context) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpListDatabases, driverwire.Args{})
}

func (s *driverSession) ListTables(ctx context.Context, database string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpListTables, driverwire.Args{Database: database})
}

func (s *driverSession) TableMetadata(ctx context.Context, database, table string) (*dbdriver.TableMetadata, error) {
	return forward[*dbdriver.TableMetadata](ctx, s, driverwire.OpTableMetadata, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	return forward[*dbdriver.DatabaseObjects](ctx, s, driverwire.OpListObjects, driverwire.Args{Database: database})
}

func (s *driverSession) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	return forward[string](ctx, s, driverwire.OpObjectSource, driverwire.Args{
		Database: database, ObjectType: objType, Name: name, ObjectTable: table,
	})
}

func (s *driverSession) OpenTable(ctx context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	return forward[*dbdriver.OpenedTable](ctx, s, driverwire.OpOpenTable, driverwire.Args{Database: database, Table: table, PageSize: pageSize})
}

func (s *driverSession) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	return forward[*dbdriver.RowSet](ctx, s, driverwire.OpTableRows, driverwire.Args{Database: database, Table: table, Query: query, Page: page})
}

func (s *driverSession) CountTableRows(ctx context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	return forward[int64](ctx, s, driverwire.OpCountTableRows, driverwire.Args{Database: database, Table: table, Filters: filters})
}

// StreamTableRows opens the cursor in the guest and reads it over as many calls
// as it takes, driverStreamRows rows at a time, so the table is never held whole
// on either side. ctx bounds every one of those reads, as it would bound reading
// a database/sql cursor.
func (s *driverSession) StreamTableRows(ctx context.Context, database, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	opened, err := forward[driverwire.StreamOpened](ctx, s, driverwire.OpStreamTableRows, driverwire.Args{Database: database, Table: table, Query: query})
	if err != nil {
		return nil, err
	}
	return &driverStream{ctx: ctx, conn: s.conn, id: opened.Stream, columns: opened.Columns}, nil
}

func (s *driverSession) TableDDL(ctx context.Context, database, table string) (string, error) {
	return forward[string](ctx, s, driverwire.OpTableDDL, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) StarterQuery(ctx context.Context, database, table string, limit int) (string, error) {
	return forward[string](ctx, s, driverwire.OpStarterQuery, driverwire.Args{Database: database, Table: table, Limit: limit})
}

// PrepareRowStatements prepares the writer in the guest. The writer keeps the
// instance alive after the session closes, until it is collected.
func (s *driverSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	prepared, err := forward[driverwire.WriterPrepared](ctx, s, driverwire.OpPrepareRowStatements, driverwire.Args{Database: database, Table: table})
	if err != nil {
		return nil, err
	}
	s.conn.retain()
	w := &driverRowWriter{conn: s.conn, id: prepared.Writer}
	runtime.AddCleanup(w, (*driverInstance).release, s.conn)
	return w, nil
}

func (s *driverSession) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	return forwardRequest[*dbdriver.GridEditStatements](ctx, s, driverwire.OpGridEditStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) CreateTableStatements(ctx context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpCreateTableStatements, driverwire.Args{Database: database}, req)
}

func (s *driverSession) DropTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpDropTableStatements, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) TruncateTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return forward[[]string](ctx, s, driverwire.OpTruncateTableStatements, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) CreateDatabaseStatements(ctx context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpCreateDatabaseStatements, driverwire.Args{}, req)
}

func (s *driverSession) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	return forward[*dbdriver.AlterTableModel](ctx, s, driverwire.OpAlterTableModel, driverwire.Args{Database: database, Table: table})
}

func (s *driverSession) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	return forwardRequest[*dbdriver.AlterTableStatements](ctx, s, driverwire.OpAlterTableStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	return forwardRequest[[]string](ctx, s, driverwire.OpImportStatements, driverwire.Args{Database: database, Table: table}, req)
}

func (s *driverSession) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	return forwardRequest[*dbdriver.BatchResult](ctx, s, driverwire.OpExecuteBatch, driverwire.Args{}, req)
}

// Close closes the session in the guest, then whatever connection it left open,
// and gives up the session's hold on the instance. Closing twice is a no-op.
func (s *driverSession) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	err := s.conn.call(context.Background(), driverwire.Call{Op: driverwire.OpClose, Session: s.id}, nil)
	s.conn.inv.close()
	s.conn.release()
	return err
}

// driverStream is a cursor in the guest, read over in batches.
type driverStream struct {
	ctx     context.Context
	conn    *driverInstance
	id      uint64
	columns []string

	rows   [][]any
	row    []any
	done   bool
	err    error
	closed bool
}

func (r *driverStream) Columns() []string { return r.columns }

func (r *driverStream) Next() bool {
	for len(r.rows) == 0 {
		if r.done || r.err != nil {
			return false
		}
		var page driverwire.StreamPage
		r.err = r.conn.call(r.ctx, driverwire.Call{Op: driverwire.OpStreamNext, Args: driverwire.Args{
			Stream: r.id, Max: driverStreamRows,
		}}, &page)
		r.rows, r.done = page.Rows, page.Done
	}
	r.row, r.rows = r.rows[0], r.rows[1:]
	return true
}

func (r *driverStream) Row() []any { return r.row }

func (r *driverStream) Err() error { return r.err }

func (r *driverStream) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.conn.call(r.ctx, driverwire.Call{Op: driverwire.OpStreamClose, Args: driverwire.Args{Stream: r.id}}, nil)
}

// driverRowWriter is a row statement writer prepared in the guest.
type driverRowWriter struct {
	conn *driverInstance
	id   uint64
}

func (w *driverRowWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal row statements request: %w", err)
	}
	var out []string
	err = w.conn.call(context.Background(), driverwire.Call{Op: driverwire.OpRowStatements, Args: driverwire.Args{
		Writer: w.id, Request: raw,
	}}, &out)
	return out, err
}
