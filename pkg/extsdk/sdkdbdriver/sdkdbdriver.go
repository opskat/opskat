// Package sdkdbdriver is the extension SDK's database driver entry: an extension
// that provides a SQL database driver registers it here from init(), next to its
// pkg/extsdk registrations. It is a package of its own because serving a driver
// links database/sql and pkg/dbdriver/sqldb (and what they import), which an
// extension without a driver must not carry; it hooks into the SDK through
// opskat.RegisterDatabaseDriver.
package sdkdbdriver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/opskat/opskat/pkg/extension/driverwire"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// DriverOpen opens the database connection of one session and returns it with
// the driver's dialect; the SDK assembles the session from them with
// pkg/dbdriver/sqldb, the same shared session the built-in drivers use.
//
// It is called once per instance of a session: the host runs a session on up to
// three module instances of its own — operations that overlap get one each — and
// logs in a new one in place of one it discarded, so open runs again there and
// connection state (a current schema, a temporary table) is not shared between
// them. It must return only once the database is reachable (sqldb.Establish pings it), and
// a failure is returned as the driver's own error: the host shows it to the user
// as is. req carries the connection config, the decrypted password — handed over
// in this call only — and the reach path: every network connection must be made
// through req.Transport.Dial, which dials through the host along the asset's
// tunnel / proxy chain. A read-only config only reaches open when the descriptor
// declares dbdriver.CapReadOnly; open must then honor it.
type DriverOpen func(ctx context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error)

type driverEntry struct {
	desc dbdriver.Descriptor
	open DriverOpen
}

// DatabaseDriver declares a SQL database driver this extension provides: the
// descriptor describe() reports for it, and how a session of it is opened. The
// driver id must also be listed in the manifest's capabilities.databaseDrivers,
// which needs hostABI 2.3; the host refuses to load an extension whose two lists
// differ. The driver's icon is the extension's (Meta.Icon), not the descriptor's.
//
// The dialect decides which session operations exist (see sqldb.NewSession and
// the optional dialect interfaces); declare in the descriptor exactly the
// capabilities the dialect implements.
func DatabaseDriver(d dbdriver.Descriptor, open DriverOpen) {
	if open == nil {
		panic(fmt.Sprintf("opskat: database driver %q has no open function", d.ID))
	}
	if slices.ContainsFunc(drivers, func(e *driverEntry) bool { return e.desc.ID == d.ID }) {
		panic(fmt.Sprintf("opskat: database driver %q is already registered", d.ID))
	}
	drivers = append(drivers, &driverEntry{desc: d, open: open})
	opskat.RegisterDatabaseDriver(d, serve)
}

// drivers are the drivers this extension registered.
var drivers []*driverEntry

// driverHandleTable holds what the host refers to by id in this instance: open
// sessions, open cursors (with the session they read from) and prepared row
// statement writers, which outlive their session until the host releases them
// (driverwire.Call.ReleaseWriters).
type driverHandleTable struct {
	next     uint64
	sessions map[uint64]dbdriver.Session
	streams  map[uint64]driverStream
	writers  map[uint64]dbdriver.RowStatementWriter
}

type driverStream struct {
	session uint64
	rows    dbdriver.RowStream
}

func newDriverHandleTable() *driverHandleTable {
	return &driverHandleTable{
		sessions: map[uint64]dbdriver.Session{},
		streams:  map[uint64]driverStream{},
		writers:  map[uint64]dbdriver.RowStatementWriter{},
	}
}

func (t *driverHandleTable) id() uint64 {
	t.next++
	return t.next
}

var driverHandles = newDriverHandleTable()

// serve serves one driverwire.Call. A failure of the operation is part of the
// reply (driverwire.Error, coded when the host must recognize it); only a request
// that cannot be read is an error of the call itself.
func serve(input []byte) (json.RawMessage, error) {
	var call driverwire.Call
	if err := json.Unmarshal(input, &call); err != nil {
		return nil, fmt.Errorf("parse database driver call: %w", err)
	}
	for _, id := range call.ReleaseWriters {
		delete(driverHandles.writers, id)
	}
	result, opErr := runDriverOp(context.Background(), call)
	var reply driverwire.Reply
	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode %s result: %w", call.Op, err)
		}
		if string(raw) != "null" {
			reply.Result = raw
		}
	}
	if opErr != nil {
		reply.Error = &driverwire.Error{Message: opErr.Error()}
		if errors.Is(opErr, dbdriver.ErrUnsupported) {
			reply.Error.Code = driverwire.CodeUnsupported
		}
	}
	return json.Marshal(reply)
}

func runDriverOp(ctx context.Context, call driverwire.Call) (any, error) {
	a := call.Args
	switch call.Op {
	case driverwire.OpOpen:
		return openDriverSession(ctx, a)
	case driverwire.OpStreamNext:
		return streamNext(a)
	case driverwire.OpStreamClose:
		stream, ok := driverHandles.streams[a.Stream]
		if !ok {
			return nil, fmt.Errorf("no database driver cursor %d", a.Stream)
		}
		delete(driverHandles.streams, a.Stream)
		return nil, stream.rows.Close()
	case driverwire.OpRowStatements:
		writer, ok := driverHandles.writers[a.Writer]
		if !ok {
			return nil, fmt.Errorf("no database driver row statement writer %d", a.Writer)
		}
		req, err := decodeDriverRequest[dbdriver.RowStatementsRequest](a.Request)
		if err != nil {
			return nil, err
		}
		return writer.RowStatements(req)
	}

	s, ok := driverHandles.sessions[call.Session]
	if !ok {
		return nil, fmt.Errorf("no database driver session %d", call.Session)
	}
	switch call.Op {
	case driverwire.OpClose:
		return nil, closeDriverSession(call.Session, s)
	case driverwire.OpExecute:
		return s.Execute(ctx, a.Statement)
	case driverwire.OpExecutePaged:
		return s.ExecutePaged(ctx, a.Statement, a.Page)
	case driverwire.OpListDatabases:
		return s.ListDatabases(ctx)
	case driverwire.OpListTables:
		return s.ListTables(ctx, a.Database)
	case driverwire.OpTableMetadata:
		return s.TableMetadata(ctx, a.Database, a.Table)
	case driverwire.OpListObjects:
		return s.ListObjects(ctx, a.Database)
	case driverwire.OpObjectSource:
		return s.ObjectSource(ctx, a.Database, a.ObjectType, a.Name, a.ObjectTable)
	case driverwire.OpOpenTable:
		return s.OpenTable(ctx, a.Database, a.Table, a.PageSize)
	case driverwire.OpTableRows:
		return s.TableRows(ctx, a.Database, a.Table, a.Query, a.Page)
	case driverwire.OpCountTableRows:
		return s.CountTableRows(ctx, a.Database, a.Table, a.Filters)
	case driverwire.OpStreamTableRows:
		rows, err := s.StreamTableRows(ctx, a.Database, a.Table, a.Query)
		if err != nil {
			return nil, err
		}
		id := driverHandles.id()
		driverHandles.streams[id] = driverStream{session: call.Session, rows: rows}
		return driverwire.StreamOpened{Stream: id, Columns: rows.Columns()}, nil
	case driverwire.OpTableDDL:
		return s.TableDDL(ctx, a.Database, a.Table)
	case driverwire.OpStarterQuery:
		return s.StarterQuery(ctx, a.Database, a.Table, a.Limit)
	case driverwire.OpPrepareRowStatements:
		writer, err := s.PrepareRowStatements(ctx, a.Database, a.Table)
		if err != nil {
			return nil, err
		}
		id := driverHandles.id()
		driverHandles.writers[id] = writer
		return driverwire.WriterPrepared{Writer: id}, nil
	case driverwire.OpDropTableStatements:
		return s.DropTableStatements(ctx, a.Database, a.Table)
	case driverwire.OpTruncateTableStatements:
		return s.TruncateTableStatements(ctx, a.Database, a.Table)
	case driverwire.OpAlterTableModel:
		return s.AlterTableModel(ctx, a.Database, a.Table)
	}
	return runDriverRequestOp(ctx, s, call.Op, a)
}

// runDriverRequestOp runs the operations whose input is a request type.
func runDriverRequestOp(ctx context.Context, s dbdriver.Session, op string, a driverwire.Args) (any, error) {
	switch op {
	case driverwire.OpGridEditStatements:
		return withDriverRequest(a.Request, func(req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
			return s.GridEditStatements(ctx, a.Database, a.Table, req)
		})
	case driverwire.OpCreateTableStatements:
		return withDriverRequest(a.Request, func(req dbdriver.CreateTableRequest) ([]string, error) {
			return s.CreateTableStatements(ctx, a.Database, req)
		})
	case driverwire.OpCreateDatabaseStatements:
		return withDriverRequest(a.Request, func(req dbdriver.CreateDatabaseRequest) ([]string, error) {
			return s.CreateDatabaseStatements(ctx, req)
		})
	case driverwire.OpAlterTableStatements:
		return withDriverRequest(a.Request, func(req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
			return s.AlterTableStatements(ctx, a.Database, a.Table, req)
		})
	case driverwire.OpImportStatements:
		return withDriverRequest(a.Request, func(req dbdriver.ImportRequest) ([]string, error) {
			return s.ImportStatements(ctx, a.Database, a.Table, req)
		})
	case driverwire.OpExecuteBatch:
		return withDriverRequest(a.Request, func(req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
			return s.ExecuteBatch(ctx, req)
		})
	default:
		// An operation of a host newer than this SDK: refused as unsupported, so the host
		// treats the feature as one the driver does not have rather than as a failure.
		return nil, fmt.Errorf("%w: unknown database driver operation %q", dbdriver.ErrUnsupported, op)
	}
}

func decodeDriverRequest[T any](raw json.RawMessage) (T, error) {
	var req T
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, fmt.Errorf("parse %T: %w", req, err)
	}
	return req, nil
}

func withDriverRequest[T, R any](raw json.RawMessage, run func(T) (R, error)) (any, error) {
	req, err := decodeDriverRequest[T](raw)
	if err != nil {
		return nil, err
	}
	return run(req)
}

func openDriverSession(ctx context.Context, a driverwire.Args) (any, error) {
	i := slices.IndexFunc(drivers, func(e *driverEntry) bool { return e.desc.ID == a.Driver })
	if i < 0 {
		return nil, fmt.Errorf("this extension provides no database driver %q", a.Driver)
	}
	drv := drivers[i]
	if a.Config == nil {
		return nil, fmt.Errorf("open %s: no connection config", a.Driver)
	}
	if a.Config.ReadOnly && !drv.desc.Supports(dbdriver.CapReadOnly) {
		return nil, fmt.Errorf("%w: %s does not provide read-only connections", dbdriver.ErrUnsupported, a.Driver)
	}
	db, dialect, err := drv.open(ctx, dbdriver.OpenRequest{
		Config:        a.Config,
		Password:      a.Password,
		DriverOptions: a.DriverOptions,
		TLS:           a.TLS,
		Transport:     dbdriver.Transport{Dial: opskat.DialContext},
	})
	if err != nil {
		return nil, err
	}
	id := driverHandles.id()
	driverHandles.sessions[id] = sqldb.NewSession(db, dialect)
	return driverwire.Opened{Session: id}, nil
}

// closeDriverSession closes the session's cursors, then the session. Its prepared
// writers stay: they no longer touch the database.
func closeDriverSession(id uint64, s dbdriver.Session) error {
	var errs []error
	for streamID, stream := range driverHandles.streams {
		if stream.session == id {
			errs = append(errs, stream.rows.Close())
			delete(driverHandles.streams, streamID)
		}
	}
	delete(driverHandles.sessions, id)
	errs = append(errs, s.Close())
	return errors.Join(errs...)
}

// streamNext reads up to a.Max rows; fewer means the cursor is done. A read error
// is answered together with the rows read before it.
func streamNext(a driverwire.Args) (any, error) {
	stream, ok := driverHandles.streams[a.Stream]
	if !ok {
		return nil, fmt.Errorf("no database driver cursor %d", a.Stream)
	}
	if a.Max <= 0 {
		return nil, fmt.Errorf("read cursor %d: max must be positive, got %d", a.Stream, a.Max)
	}
	page := driverwire.StreamPage{Rows: make([][]any, 0, a.Max)}
	for len(page.Rows) < a.Max && stream.rows.Next() {
		page.Rows = append(page.Rows, stream.rows.Row())
	}
	page.Done = len(page.Rows) < a.Max
	if page.Done {
		return page, stream.rows.Err()
	}
	return page, nil
}
