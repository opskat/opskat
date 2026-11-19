package extwire

// The database driver calls: the JSON a host and an extension exchange to run a
// database driver the extension provides. The host side is
// internal/extreg/extdriver.(*Drivers).OpenDriverSession (and ClassifyStatements, which
// needs no session), the guest side the SDK's DatabaseDriver (pkg/extsdk).
// Every call is the guest function DriverFunction with a DriverCall as input,
// answered by a DriverReply.
//
// The JSON is the contract, not these types: an extension is built against one
// version of them and keeps running against newer hosts. Fields may only be
// added, and a side that receives a field it does not know ignores it (plain
// encoding/json decoding).

import (
	"encoding/json"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// DriverFunction is the guest function every driver call goes through.
const DriverFunction = "database_driver"

// Operations. DriverOpOpen creates a session and answers DriverOpened; the session
// operations are the dbdriver.Session methods of the same name; the cursor and
// writer operations work on the DriverStreamOpened / DriverWriterPrepared they answered.
const (
	DriverOpOpen  = "open"
	DriverOpClose = "close"

	DriverOpExecute                  = "execute"
	DriverOpExecutePaged             = "executePaged"
	DriverOpListDatabases            = "listDatabases"
	DriverOpListTables               = "listTables"
	DriverOpTableMetadata            = "tableMetadata"
	DriverOpListObjects              = "listObjects"
	DriverOpObjectSource             = "objectSource"
	DriverOpOpenTable                = "openTable"
	DriverOpTableRows                = "tableRows"
	DriverOpCountTableRows           = "countTableRows"
	DriverOpStreamTableRows          = "streamTableRows"
	DriverOpTableDDL                 = "tableDDL"
	DriverOpStarterQuery             = "starterQuery"
	DriverOpPrepareRowStatements     = "prepareRowStatements"
	DriverOpGridEditStatements       = "gridEditStatements"
	DriverOpCreateTableStatements    = "createTableStatements"
	DriverOpDropTableStatements      = "dropTableStatements"
	DriverOpTruncateTableStatements  = "truncateTableStatements"
	DriverOpCreateDatabaseStatements = "createDatabaseStatements"
	DriverOpAlterTableModel          = "alterTableModel"
	DriverOpAlterTableStatements     = "alterTableStatements"
	DriverOpImportStatements         = "importStatements"
	DriverOpExecuteBatch             = "executeBatch"

	// DriverOpStreamNext reads up to DriverArgs.Max rows of a cursor — fewer when they
	// are large (see DriverStreamPage); DriverOpStreamClose closes it.
	DriverOpStreamNext  = "streamNext"
	DriverOpStreamClose = "streamClose"
	// DriverOpRowStatements generates statements with a prepared writer, one per row
	// in order; the host sends a large request's rows over several calls
	// (DriverRowsBytes). A writer outlives the session it was prepared on: it no
	// longer touches the database.
	DriverOpRowStatements = "rowStatements"

	// DriverOpClassifyStatements splits and classifies DriverArgs.Statement with driver
	// DriverArgs.Driver (dbdriver.StatementClassifier), answering []dbdriver.Statement.
	// It runs on no session: the driver does not touch the database to answer.
	DriverOpClassifyStatements = "classifyStatements"
)

// DriverScriptHostABI is the hostABI the operations below arrived in: the hooks of
// running a SQL file (dbdriver.ScriptRunner) and of dumping one
// (dbdriver.DumpDialect). The host reads, orders, reports progress, stops and
// writes the file; the guest only answers the hooks. The host sends them only to
// a driver declaring dbdriver.CapRunSQLFile / dbdriver.CapDumpSQL, which only an
// extension built for this hostABI or a later one may declare.
const DriverScriptHostABI = "1.1"

// Operations of DriverScriptHostABI. Each request is the dbdriver type named,
// whole, in DriverArgs.Request, and each answer the dbdriver type named, so a field
// either side adds to them travels without a change here.
const (
	// DriverOpSplitScript splits a chunk of a SQL file (session operation;
	// dbdriver.ScriptChunk → dbdriver.ScriptSplit). The state the split carries over
	// is the guest's own and comes back as is.
	DriverOpSplitScript = "splitScript"
	// DriverOpOpenScriptExecutor takes one database connection of the session for a
	// run, on DriverArgs.Database (session operation; answers
	// DriverScriptExecutorOpened). The host keeps the run on this instance until
	// it closes the executor, so every statement of the file runs on that one
	// connection.
	DriverOpOpenScriptExecutor = "openScriptExecutor"
	// DriverOpExecuteScriptBatch runs a batch of statements in order on executor
	// DriverArgs.Executor (dbdriver.ScriptBatch → dbdriver.BatchResult; a failing
	// connection answers both what ran and the error). A batch is one call: the
	// host sends none once the run is stopped, and during the call answers the
	// stop check (tool.should_stop) true once it is, which the guest asks before
	// each statement — the run stops after the statement in flight.
	DriverOpExecuteScriptBatch = "executeScriptBatch"
	// DriverOpCloseScriptExecutor gives executor DriverArgs.Executor's connection
	// back. Closing the session closes its executors too.
	DriverOpCloseScriptExecutor = "closeScriptExecutor"
	// DriverOpDumpFile answers the parts of a dump written once per file (session
	// operation; dbdriver.DumpScope → dbdriver.DumpFileScript).
	DriverOpDumpFile = "dumpFile"
	// DriverOpDumpObject answers the statements of one object of a dump (session
	// operation; dbdriver.DumpObject → dbdriver.DumpObjectScript).
	DriverOpDumpObject = "dumpObject"
)

// DriverCall is the input of DriverFunction.
type DriverCall struct {
	Op string `json:"op"`
	// Session is the session the operation runs on (every operation but DriverOpOpen,
	// DriverOpClassifyStatements and the cursor / writer / SQL file executor ones).
	Session uint64     `json:"session,omitempty"`
	Args    DriverArgs `json:"args"`
	// ReleaseWriters are row statement writers of this instance the host no longer
	// holds. A writer outlives its session, so the host hands their release along
	// with the next call on their instance; the guest forgets them before it runs
	// the operation.
	ReleaseWriters []uint64 `json:"releaseWriters,omitempty"`
}

// DriverArgs are an operation's arguments, named as the dbdriver.Session parameters
// they carry. Each operation reads the ones it needs.
type DriverArgs struct {
	// DriverOpOpen: the driver, the config it is handed (the host-owned parts of an
	// asset — tunnel, proxies, credential — are not in dbdriver.Config), the
	// decrypted password, the driver options (password-type ones decrypted) and,
	// for a driver declaring the certificate field group on an asset with TLS on,
	// the TLS material (key decrypted), which no other call carries. A guest built
	// before TLS was added ignores it. Kerberos says the asset logs in with Kerberos
	// (the driver then asks the host for tickets in its login handshake); the
	// Kerberos credentials stay in the host and Password is empty.
	Driver        dbdriver.DriverID     `json:"driver,omitempty"`
	Config        *dbdriver.Config      `json:"config,omitempty"`
	Password      string                `json:"password,omitempty"`
	DriverOptions map[string]any        `json:"driverOptions,omitempty"`
	TLS           *dbdriver.TLSMaterial `json:"tls,omitempty"`
	Kerberos      bool                  `json:"kerberos,omitempty"`

	// Statement is the SQL of DriverOpExecute / DriverOpExecutePaged; with Driver, the SQL
	// text DriverOpClassifyStatements splits.
	Statement   string                `json:"statement,omitempty"`
	Page        dbdriver.Page         `json:"page"`
	Database    string                `json:"database,omitempty"`
	Table       string                `json:"table,omitempty"`
	ObjectType  string                `json:"objectType,omitempty"`
	Name        string                `json:"name,omitempty"`
	ObjectTable string                `json:"objectTable,omitempty"`
	PageSize    int                   `json:"pageSize,omitempty"`
	Limit       int                   `json:"limit,omitempty"`
	Query       dbdriver.TableQuery   `json:"query"`
	Filters     []dbdriver.FilterItem `json:"filters,omitempty"`
	// Request is the operation's request type (dbdriver.GridEditRequest,
	// BatchRequest, RowStatementsRequest, ...).
	Request json.RawMessage `json:"request,omitempty"`

	Stream   uint64 `json:"stream,omitempty"`
	Writer   uint64 `json:"writer,omitempty"`
	Executor uint64 `json:"executor,omitempty"`
	// Max is how many rows DriverOpStreamNext may answer at most. The guest may
	// answer fewer; only DriverStreamPage.Done says the cursor has ended.
	Max int `json:"max,omitempty"`
	// AcceptBytes tells DriverOpStreamNext the host reads DriverStreamPage.Bytes: only
	// then does the guest send raw byte values as bytes. A host that does not set it
	// gets them as before.
	AcceptBytes bool `json:"acceptBytes,omitempty"`
	// RowBytes are the values of the DriverOpRowStatements request's rows that are
	// bytes ([]byte, such as a dump's binary column values). JSON carries them as
	// base64 strings; the guest decodes each back into the bytes.
	RowBytes []DriverRowValue `json:"rowBytes,omitempty"`
}

// DriverRowValue names the value of column Column in row Row of a request's rows.
type DriverRowValue struct {
	Row    int    `json:"row"`
	Column string `json:"column"`
}

// DriverCell names the value at Rows[Row][Column] of a DriverStreamPage.
type DriverCell struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

// DriverReply is the output of DriverFunction. Result and Error may both be set: a batch
// that failed part way answers what it executed and the failure.
type DriverReply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *DriverError    `json:"error,omitempty"`
}

// DriverError is an operation's failure: the driver's own message, and a code for
// failures the host must tell apart.
type DriverError struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// DriverCodeUnsupported marks an error that wraps dbdriver.ErrUnsupported.
const DriverCodeUnsupported = "unsupported"

// DriverOpened answers DriverOpOpen.
type DriverOpened struct {
	Session uint64 `json:"session"`
}

// DriverStreamOpened answers DriverOpStreamTableRows.
type DriverStreamOpened struct {
	Stream  uint64   `json:"stream"`
	Columns []string `json:"columns"`
}

// DriverRowsBytes is about how many bytes of row values one driver call carries:
// the rows, their JSON and what the guest makes of them are all in the instance's
// memory at once, so a batch of large rows crosses in several calls of about this
// much, each with at least one row. A guest built with the current SDK ends a
// cursor page here (DriverStreamPage); the host splits a DriverOpRowStatements
// request here. Sizes are estimated with ValueBytes.
const DriverRowsBytes = 4 << 20

// ValueBytes estimates the bytes a row value takes: the length of text and bytes,
// which is where a large row's size is, and a few bytes for any other value.
func ValueBytes(value any) int {
	switch v := value.(type) {
	case string:
		return len(v)
	case []byte:
		return len(v)
	default:
		return 8
	}
}

// DriverStreamPage answers DriverOpStreamNext: the next rows, and whether the cursor has no
// more after them. A guest built with the current SDK also ends a page once its
// values take about DriverRowsBytes, with at least one row, so a page with fewer
// rows than DriverArgs.Max is not the end unless Done says so; one built with an
// earlier SDK answers Max rows, or fewer only at the end, and sets Done then.
type DriverStreamPage struct {
	Rows [][]any `json:"rows"`
	Done bool    `json:"done"`
	// Bytes are the cells holding raw bytes — a string that is not valid UTF-8,
	// which is how the database/sql drivers read a binary column — sent as bytes
	// (base64 in JSON) when the host asked with DriverArgs.AcceptBytes. JSON would
	// otherwise replace their invalid bytes; the host turns each back into the
	// string of those bytes.
	Bytes []DriverCell `json:"bytes,omitempty"`
}

// DriverWriterPrepared answers DriverOpPrepareRowStatements.
type DriverWriterPrepared struct {
	Writer uint64 `json:"writer"`
}

// DriverScriptExecutorOpened answers DriverOpOpenScriptExecutor.
type DriverScriptExecutorOpened struct {
	Executor uint64 `json:"executor"`
}
