// Package sdkdbdriver is the extension SDK's database driver entry: an extension
// that provides a SQL database driver registers it here from init(), next to its
// pkg/extsdk registrations. It is a package of its own because serving a driver
// links database/sql and pkg/dbdriver/sqldb (and what they import), which an
// extension without a driver must not carry; it hooks into the SDK through
// opskat.RegisterDatabaseDriver.
package sdkdbdriver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	opskat "github.com/opskat/opskat/pkg/extsdk"
	"github.com/opskat/opskat/pkg/extsdk/internal/registration"
	"github.com/opskat/opskat/pkg/extwire"
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
// declares dbdriver.CapReadOnly; open must then honor it. When the asset logs in
// with Kerberos (the descriptor declares dbdriver.FieldKerberos) req.Kerberos is
// set and req.Password empty: in the login handshake the driver asks
// req.Kerberos.Ticket for dbdriver.KerberosSPN(service, host) of the names the
// database sent, and writes the bytes it gets into the handshake; the Kerberos
// credentials never reach the extension.
type DriverOpen func(ctx context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error)

// StatementClassifier splits a SQL text into statements and classifies each,
// without touching the database (dbdriver.StatementClassifier): the host asks it
// for the SQL the AI or opsctl runs on an asset of the driver and for the query
// panel's current statement, outside any session. The host checks what it
// answers (dbdriver.Statement documents the rules) and treats an answer breaking
// them, like an error, as text the driver cannot split.
type StatementClassifier func(ctx context.Context, sql string) ([]dbdriver.Statement, error)

// DriverOption is an optional part of a database driver.
type DriverOption func(*driverEntry)

// WithStatementClassifier gives the driver its own statement splitting and
// classification. The descriptor must declare dbdriver.CapClassifyStatements,
// and a descriptor that declares it must come with this option.
func WithStatementClassifier(classify StatementClassifier) DriverOption {
	return func(e *driverEntry) { e.classify = classify }
}

type driverEntry struct {
	desc     dbdriver.Descriptor
	open     DriverOpen
	classify StatementClassifier
}

// DatabaseDriver declares a SQL database driver this extension provides: the
// descriptor describe() reports for it, and how a session of it is opened. The
// driver id must also be listed in the manifest's contributes.databaseDrivers;
// the host refuses to load an extension whose two lists
// differ. The driver's icon is the extension's (Meta.Icon), not the descriptor's.
//
// The dialect decides which session operations exist (see sqldb.NewSession and
// the optional dialect interfaces); declare in the descriptor exactly the
// capabilities the dialect implements. Parts that are not session operations
// come as options (WithStatementClassifier), each matching a capability of its own.
//
// Running SQL files and dumping them (the database tree's "Run SQL File…" and
// "Dump SQL File" menus) need an extension built for hostABI 1.1 — manifest.json
// "hostABI": "1.1"; the host refuses to load an extension built for 1.0 whose
// driver declares either capability. The host does everything users see the
// same as for a built-in driver: it reads and writes the file, checks it is UTF-8,
// orders the objects, reports progress, stops and cancels. The dialect only
// answers the hooks:
//
//   - Run SQL File, declared with dbdriver.CapRunSQLFile: the dialect splits the
//     file into statements, either by describing its lexical syntax
//     (sqldb.ScriptDialect: quotes, comments, DELIMITER, GO batches, ...) or, when a
//     syntax cannot say it — Oracle's PL/SQL blocks ending in a line holding only
//     "/" — by splitting itself (dbdriver.ScriptSplitter, which then takes the place
//     of a ScriptSyntax). The host hands the splitter one chunk of the file at a time
//     (dbdriver.ScriptChunk): it answers the complete statements, how much text it
//     used and a state string of its own choosing that comes back with the next
//     chunk (the current delimiter, say); a construct the file cannot be run with
//     here is answered as ScriptSplit.Unsupported, naming the tool to use instead.
//     The statements of one run go, in batches, to a single database connection the
//     SDK takes from the session for the length of the run, so a setting a
//     statement makes holds for the ones after it; each statement commits on its
//     own. The host asks for a stop between batches, and closes the connection's
//     instance when the run is canceled or the guest fails.
//   - Dump, declared with dbdriver.CapDumpSQL together with dbdriver.CapViewDDL
//     (the definitions are what TableDDL and ObjectSource give) and, for structure
//     and data, a capability that generates row statements (dbdriver.CapExportSQL):
//     the dialect implements sqldb.DumpDialect, whose DumpSyntax is what every file
//     starts and ends with; sqldb.DumpFileWriter and sqldb.DumpObjectWriter
//     optionally change the file's and each object's statements on a connection
//     (drop statements for a database without IF EXISTS, wrapping a definition in
//     "/", index and foreign key statements, the columns an INSERT writes, ...). The
//     rows are read with the session's cursor and written with its row statements,
//     the INSERTs "copy as INSERT" makes, binary values as bytes.
func DatabaseDriver(d dbdriver.Descriptor, open DriverOpen, opts ...DriverOption) {
	entry := &driverEntry{desc: d, open: open}
	for _, opt := range opts {
		opt(entry)
	}
	if err := checkDriver(entry); err != nil {
		// A registration mistake fails describe, naming the driver — not a panic
		// in init, which the host sees only as a wasm trap.
		registration.Fail(err)
		return
	}
	drivers = append(drivers, entry)
	opskat.RegisterDatabaseDriver(d, serve)
}

// checkDriver is the registration rule for one driver against those already
// registered.
func checkDriver(entry *driverEntry) error {
	d := entry.desc
	if entry.open == nil {
		return fmt.Errorf("opskat: database driver %q has no open function", d.ID)
	}
	if slices.ContainsFunc(drivers, func(e *driverEntry) bool { return e.desc.ID == d.ID }) {
		return fmt.Errorf("opskat: database driver %q is already registered", d.ID)
	}
	if declared := d.Supports(dbdriver.CapClassifyStatements); declared != (entry.classify != nil) {
		return fmt.Errorf("opskat: database driver %q must declare capability %q exactly when it is given WithStatementClassifier", d.ID, dbdriver.CapClassifyStatements)
	}
	return nil
}

// drivers are the drivers this extension registered.
var drivers []*driverEntry

// driverHandleTable holds what the host refers to by id in this instance: open
// sessions, open cursors and SQL file executors (with the session they belong
// to) and prepared row statement writers, which outlive their session until the
// host releases them (extwire.DriverCall.ReleaseWriters).
type driverHandleTable struct {
	next      uint64
	sessions  map[uint64]*sqldb.Session
	streams   map[uint64]driverStream
	executors map[uint64]driverExecutor
	writers   map[uint64]dbdriver.RowStatementWriter
}

type driverStream struct {
	session uint64
	rows    dbdriver.RowStream
}

// driverExecutor is the connection a SQL file runs on (extwire.DriverOpOpenScriptExecutor).
type driverExecutor struct {
	session  uint64
	executor dbdriver.ScriptExecutor
}

func newDriverHandleTable() *driverHandleTable {
	return &driverHandleTable{
		sessions:  map[uint64]*sqldb.Session{},
		streams:   map[uint64]driverStream{},
		executors: map[uint64]driverExecutor{},
		writers:   map[uint64]dbdriver.RowStatementWriter{},
	}
}

func (t *driverHandleTable) id() uint64 {
	t.next++
	return t.next
}

var driverHandles = newDriverHandleTable()

// serve serves one extwire.DriverCall. A failure of the operation is part of the
// reply (extwire.DriverError, coded when the host must recognize it); only a request
// that cannot be read is an error of the call itself.
func serve(input []byte) (json.RawMessage, error) {
	var call extwire.DriverCall
	if err := json.Unmarshal(input, &call); err != nil {
		return nil, fmt.Errorf("parse database driver call: %w", err)
	}
	for _, id := range call.ReleaseWriters {
		delete(driverHandles.writers, id)
	}
	result, opErr := runDriverOp(context.Background(), call)
	var reply extwire.DriverReply
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
		reply.Error = &extwire.DriverError{Message: opErr.Error()}
		if errors.Is(opErr, dbdriver.ErrUnsupported) {
			reply.Error.Code = extwire.DriverCodeUnsupported
		}
	}
	return json.Marshal(reply)
}

func runDriverOp(ctx context.Context, call extwire.DriverCall) (any, error) {
	a := call.Args
	switch call.Op {
	case extwire.DriverOpOpen:
		return openDriverSession(ctx, a)
	case extwire.DriverOpClassifyStatements:
		return classifyStatements(ctx, a)
	case extwire.DriverOpStreamNext:
		return streamNext(a)
	case extwire.DriverOpStreamClose:
		stream, ok := driverHandles.streams[a.Stream]
		if !ok {
			return nil, fmt.Errorf("no database driver cursor %d", a.Stream)
		}
		delete(driverHandles.streams, a.Stream)
		return nil, stream.rows.Close()
	case extwire.DriverOpRowStatements:
		writer, ok := driverHandles.writers[a.Writer]
		if !ok {
			return nil, fmt.Errorf("no database driver row statement writer %d", a.Writer)
		}
		req, err := decodeRowStatementsRequest(a)
		if err != nil {
			return nil, err
		}
		return writer.RowStatements(req)
	case extwire.DriverOpExecuteScriptBatch:
		executor, ok := driverHandles.executors[a.Executor]
		if !ok {
			return nil, fmt.Errorf("no database driver SQL file executor %d", a.Executor)
		}
		return withDriverRequest(a.Request, func(batch dbdriver.ScriptBatch) (*dbdriver.BatchResult, error) {
			return executor.executor.ExecuteScriptBatch(ctx, batch)
		})
	case extwire.DriverOpCloseScriptExecutor:
		executor, ok := driverHandles.executors[a.Executor]
		if !ok {
			return nil, fmt.Errorf("no database driver SQL file executor %d", a.Executor)
		}
		delete(driverHandles.executors, a.Executor)
		return nil, executor.executor.Close()
	}

	s, ok := driverHandles.sessions[call.Session]
	if !ok {
		return nil, fmt.Errorf("no database driver session %d", call.Session)
	}
	switch call.Op {
	case extwire.DriverOpClose:
		return nil, closeDriverSession(call.Session, s)
	case extwire.DriverOpExecute:
		return s.Execute(ctx, a.Statement)
	case extwire.DriverOpExecutePaged:
		return s.ExecutePaged(ctx, a.Statement, a.Page)
	case extwire.DriverOpListDatabases:
		return s.ListDatabases(ctx)
	case extwire.DriverOpListTables:
		return s.ListTables(ctx, a.Database)
	case extwire.DriverOpTableMetadata:
		return s.TableMetadata(ctx, a.Database, a.Table)
	case extwire.DriverOpListObjects:
		return s.ListObjects(ctx, a.Database)
	case extwire.DriverOpObjectSource:
		return s.ObjectSource(ctx, a.Database, a.ObjectType, a.Name, a.ObjectTable)
	case extwire.DriverOpOpenTable:
		return s.OpenTable(ctx, a.Database, a.Table, a.PageSize)
	case extwire.DriverOpTableRows:
		return s.TableRows(ctx, a.Database, a.Table, a.Query, a.Page)
	case extwire.DriverOpCountTableRows:
		return s.CountTableRows(ctx, a.Database, a.Table, a.Filters)
	case extwire.DriverOpStreamTableRows:
		rows, err := s.StreamTableRows(ctx, a.Database, a.Table, a.Query)
		if err != nil {
			return nil, err
		}
		id := driverHandles.id()
		driverHandles.streams[id] = driverStream{session: call.Session, rows: rows}
		return extwire.DriverStreamOpened{Stream: id, Columns: rows.Columns()}, nil
	case extwire.DriverOpTableDDL:
		return s.TableDDL(ctx, a.Database, a.Table)
	case extwire.DriverOpStarterQuery:
		return s.StarterQuery(ctx, a.Database, a.Table, a.Limit)
	case extwire.DriverOpPrepareRowStatements:
		writer, err := s.PrepareRowStatements(ctx, a.Database, a.Table)
		if err != nil {
			return nil, err
		}
		id := driverHandles.id()
		driverHandles.writers[id] = writer
		return extwire.DriverWriterPrepared{Writer: id}, nil
	case extwire.DriverOpDropTableStatements:
		return s.DropTableStatements(ctx, a.Database, a.Table)
	case extwire.DriverOpTruncateTableStatements:
		return s.TruncateTableStatements(ctx, a.Database, a.Table)
	case extwire.DriverOpAlterTableModel:
		return s.AlterTableModel(ctx, a.Database, a.Table)
	case extwire.DriverOpOpenScriptExecutor:
		runner, err := s.ScriptRunner()
		if err != nil {
			return nil, err
		}
		executor, err := runner.OpenScriptExecutor(ctx, a.Database)
		if err != nil {
			return nil, err
		}
		id := driverHandles.id()
		driverHandles.executors[id] = driverExecutor{session: call.Session, executor: executor}
		return extwire.DriverScriptExecutorOpened{Executor: id}, nil
	}
	return runDriverRequestOp(ctx, s, call.Op, a)
}

// runDriverRequestOp runs the operations whose input is a request type.
func runDriverRequestOp(ctx context.Context, s *sqldb.Session, op string, a extwire.DriverArgs) (any, error) {
	switch op {
	case extwire.DriverOpSplitScript:
		runner, err := s.ScriptRunner()
		if err != nil {
			return nil, err
		}
		return withDriverRequest(a.Request, func(chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
			return runner.SplitScript(ctx, chunk)
		})
	case extwire.DriverOpDumpFile:
		dialect, err := s.DumpDialect()
		if err != nil {
			return nil, err
		}
		return withDriverRequest(a.Request, func(scope dbdriver.DumpScope) (*dbdriver.DumpFileScript, error) {
			return dialect.DumpFile(ctx, scope)
		})
	case extwire.DriverOpDumpObject:
		dialect, err := s.DumpDialect()
		if err != nil {
			return nil, err
		}
		return withDriverRequest(a.Request, func(object dbdriver.DumpObject) (*dbdriver.DumpObjectScript, error) {
			return dialect.DumpObject(ctx, object)
		})
	case extwire.DriverOpGridEditStatements:
		return withDriverRequest(a.Request, func(req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
			return s.GridEditStatements(ctx, a.Database, a.Table, req)
		})
	case extwire.DriverOpCreateTableStatements:
		return withDriverRequest(a.Request, func(req dbdriver.CreateTableRequest) ([]string, error) {
			return s.CreateTableStatements(ctx, a.Database, req)
		})
	case extwire.DriverOpCreateDatabaseStatements:
		return withDriverRequest(a.Request, func(req dbdriver.CreateDatabaseRequest) ([]string, error) {
			return s.CreateDatabaseStatements(ctx, req)
		})
	case extwire.DriverOpAlterTableStatements:
		return withDriverRequest(a.Request, func(req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
			return s.AlterTableStatements(ctx, a.Database, a.Table, req)
		})
	case extwire.DriverOpImportStatements:
		return withDriverRequest(a.Request, func(req dbdriver.ImportRequest) ([]string, error) {
			return s.ImportStatements(ctx, a.Database, a.Table, req)
		})
	case extwire.DriverOpExecuteBatch:
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

// driverByID is the registered driver id names.
func driverByID(id dbdriver.DriverID) (*driverEntry, error) {
	i := slices.IndexFunc(drivers, func(e *driverEntry) bool { return e.desc.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("this extension provides no database driver %q", id)
	}
	return drivers[i], nil
}

func classifyStatements(ctx context.Context, a extwire.DriverArgs) (any, error) {
	drv, err := driverByID(a.Driver)
	if err != nil {
		return nil, err
	}
	if drv.classify == nil {
		return nil, fmt.Errorf("%w: %s does not split and classify statements", dbdriver.ErrUnsupported, a.Driver)
	}
	return drv.classify(ctx, a.Statement)
}

func openDriverSession(ctx context.Context, a extwire.DriverArgs) (any, error) {
	drv, err := driverByID(a.Driver)
	if err != nil {
		return nil, err
	}
	if a.Config == nil {
		return nil, fmt.Errorf("open %s: no connection config", a.Driver)
	}
	if a.Config.ReadOnly && !drv.desc.Supports(dbdriver.CapReadOnly) {
		return nil, fmt.Errorf("%w: %s does not provide read-only connections", dbdriver.ErrUnsupported, a.Driver)
	}
	req := dbdriver.OpenRequest{
		Config:        a.Config,
		Password:      a.Password,
		DriverOptions: a.DriverOptions,
		TLS:           a.TLS,
		Transport:     dbdriver.Transport{Dial: opskat.DialContext},
	}
	if a.Kerberos {
		req.Kerberos = &dbdriver.Kerberos{Ticket: hostKerberosTicket}
	}
	db, dialect, err := drv.open(ctx, req)
	if err != nil {
		return nil, err
	}
	id := driverHandles.id()
	driverHandles.sessions[id] = sqldb.NewSession(db, dialect)
	return extwire.DriverOpened{Session: id}, nil
}

// hostKerberosTicket is OpenRequest.Kerberos.Ticket in the guest: the host's
// ticket for spn. The host bounds the request by the call it runs in.
func hostKerberosTicket(_ context.Context, spn string) ([]byte, error) {
	return opskat.KerberosTicket(spn)
}

// closeDriverSession closes the session's cursors and SQL file executors, then the
// session. Its prepared writers stay: they no longer touch the database.
func closeDriverSession(id uint64, s dbdriver.Session) error {
	var errs []error
	for streamID, stream := range driverHandles.streams {
		if stream.session == id {
			errs = append(errs, stream.rows.Close())
			delete(driverHandles.streams, streamID)
		}
	}
	for executorID, executor := range driverHandles.executors {
		if executor.session == id {
			errs = append(errs, executor.executor.Close())
			delete(driverHandles.executors, executorID)
		}
	}
	delete(driverHandles.sessions, id)
	errs = append(errs, s.Close())
	return errors.Join(errs...)
}

// streamNext reads up to a.Max rows; fewer means the cursor is done. A read error
// is answered together with the rows read before it.
func streamNext(a extwire.DriverArgs) (any, error) {
	stream, ok := driverHandles.streams[a.Stream]
	if !ok {
		return nil, fmt.Errorf("no database driver cursor %d", a.Stream)
	}
	if a.Max <= 0 {
		return nil, fmt.Errorf("read cursor %d: max must be positive, got %d", a.Stream, a.Max)
	}
	page := extwire.DriverStreamPage{Rows: make([][]any, 0, a.Max)}
	for len(page.Rows) < a.Max && stream.rows.Next() {
		row := stream.rows.Row()
		if a.AcceptBytes {
			for column, value := range row {
				// sqldb reads a binary column into a string of its bytes; JSON would
				// replace the ones that are not UTF-8.
				if text, ok := value.(string); ok && !utf8.ValidString(text) {
					row[column] = []byte(text)
					page.Bytes = append(page.Bytes, extwire.DriverCell{Row: len(page.Rows), Column: column})
				}
			}
		}
		page.Rows = append(page.Rows, row)
	}
	page.Done = len(page.Rows) < a.Max
	if page.Done {
		return page, stream.rows.Err()
	}
	return page, nil
}

// decodeRowStatementsRequest reads a DriverOpRowStatements request as the host
// sent it: the values a.RowBytes names back into bytes, and numbers as written —
// an integer as int64, so one past 2^53 keeps its digits, any other number as
// float64 — which is how sqldb's literals take them.
func decodeRowStatementsRequest(a extwire.DriverArgs) (dbdriver.RowStatementsRequest, error) {
	var req dbdriver.RowStatementsRequest
	dec := json.NewDecoder(bytes.NewReader(a.Request))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("parse %T: %w", req, err)
	}
	for _, row := range req.Rows {
		for column, value := range row {
			number, err := exactNumber(value)
			if err != nil {
				return req, fmt.Errorf("parse %T: column %q: %w", req, column, err)
			}
			row[column] = number
		}
	}
	for _, ref := range a.RowBytes {
		if ref.Row < 0 || ref.Row >= len(req.Rows) {
			return req, fmt.Errorf("parse %T: binary value of row %d of %d", req, ref.Row, len(req.Rows))
		}
		text, ok := req.Rows[ref.Row][ref.Column].(string)
		if !ok {
			return req, fmt.Errorf("parse %T: binary value of row %d column %q is not base64 text", req, ref.Row, ref.Column)
		}
		raw, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return req, fmt.Errorf("parse %T: binary value of row %d column %q: %w", req, ref.Row, ref.Column, err)
		}
		req.Rows[ref.Row][ref.Column] = raw
	}
	return req, nil
}

// exactNumber is value with each json.Number in it (in a list too) an int64 when it
// is an integer that fits, otherwise a float64.
func exactNumber(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		return v.Float64()
	case []any:
		for i, element := range v {
			var err error
			if v[i], err = exactNumber(element); err != nil {
				return nil, err
			}
		}
	}
	return value, nil
}
