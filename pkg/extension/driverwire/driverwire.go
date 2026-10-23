// Package driverwire is the JSON a host and an extension exchange to run a
// database driver the extension provides: the host side is
// pkg/extension.(*Plugin).OpenDriverSession (and ClassifyStatements, which
// needs no session), the guest side the SDK's
// DatabaseDriver (pkg/extsdk). Every call is the guest function Function with a
// Call as input, answered by a Reply.
//
// The JSON is the contract, not these types: an extension is built against one
// version of them and keeps running against newer hosts. Fields may only be
// added, and a side that receives a field it does not know ignores it (plain
// encoding/json decoding). The package depends on nothing but pkg/dbdriver so it
// compiles into the guest.
package driverwire

import (
	"encoding/json"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// Function is the guest function every driver call goes through.
const Function = "database_driver"

// Operations. OpOpen creates a session and answers Opened; the session
// operations are the dbdriver.Session methods of the same name; the cursor and
// writer operations work on the StreamOpened / WriterPrepared they answered.
const (
	OpOpen  = "open"
	OpClose = "close"

	OpExecute                  = "execute"
	OpExecutePaged             = "executePaged"
	OpListDatabases            = "listDatabases"
	OpListTables               = "listTables"
	OpTableMetadata            = "tableMetadata"
	OpListObjects              = "listObjects"
	OpObjectSource             = "objectSource"
	OpOpenTable                = "openTable"
	OpTableRows                = "tableRows"
	OpCountTableRows           = "countTableRows"
	OpStreamTableRows          = "streamTableRows"
	OpTableDDL                 = "tableDDL"
	OpStarterQuery             = "starterQuery"
	OpPrepareRowStatements     = "prepareRowStatements"
	OpGridEditStatements       = "gridEditStatements"
	OpCreateTableStatements    = "createTableStatements"
	OpDropTableStatements      = "dropTableStatements"
	OpTruncateTableStatements  = "truncateTableStatements"
	OpCreateDatabaseStatements = "createDatabaseStatements"
	OpAlterTableModel          = "alterTableModel"
	OpAlterTableStatements     = "alterTableStatements"
	OpImportStatements         = "importStatements"
	OpExecuteBatch             = "executeBatch"

	// OpStreamNext reads up to Args.Max rows of a cursor; OpStreamClose closes it.
	OpStreamNext  = "streamNext"
	OpStreamClose = "streamClose"
	// OpRowStatements generates statements with a prepared writer. A writer
	// outlives the session it was prepared on: it no longer touches the database.
	OpRowStatements = "rowStatements"

	// OpClassifyStatements splits and classifies Args.Statement with driver
	// Args.Driver (dbdriver.StatementClassifier), answering []dbdriver.Statement.
	// It runs on no session: the driver does not touch the database to answer.
	OpClassifyStatements = "classifyStatements"
)

// Call is the input of Function.
type Call struct {
	Op string `json:"op"`
	// Session is the session the operation runs on (every operation but OpOpen,
	// OpClassifyStatements and the cursor / writer ones).
	Session uint64 `json:"session,omitempty"`
	Args    Args   `json:"args"`
	// ReleaseWriters are row statement writers of this instance the host no longer
	// holds. A writer outlives its session, so the host hands their release along
	// with the next call on their instance; the guest forgets them before it runs
	// the operation.
	ReleaseWriters []uint64 `json:"releaseWriters,omitempty"`
}

// Args are an operation's arguments, named as the dbdriver.Session parameters
// they carry. Each operation reads the ones it needs.
type Args struct {
	// OpOpen: the driver, the config it is handed (the host-owned parts of an
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

	// Statement is the SQL of OpExecute / OpExecutePaged; with Driver, the SQL
	// text OpClassifyStatements splits.
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

	Stream uint64 `json:"stream,omitempty"`
	Writer uint64 `json:"writer,omitempty"`
	// Max is how many rows OpStreamNext may answer.
	Max int `json:"max,omitempty"`
}

// Reply is the output of Function. Result and Error may both be set: a batch
// that failed part way answers what it executed and the failure.
type Reply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is an operation's failure: the driver's own message, and a code for
// failures the host must tell apart.
type Error struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// CodeUnsupported marks an error that wraps dbdriver.ErrUnsupported.
const CodeUnsupported = "unsupported"

// Opened answers OpOpen.
type Opened struct {
	Session uint64 `json:"session"`
}

// StreamOpened answers OpStreamTableRows.
type StreamOpened struct {
	Stream  uint64   `json:"stream"`
	Columns []string `json:"columns"`
}

// StreamPage answers OpStreamNext: the next rows, and whether the cursor has no
// more after them.
type StreamPage struct {
	Rows [][]any `json:"rows"`
	Done bool    `json:"done"`
}

// WriterPrepared answers OpPrepareRowStatements.
type WriterPrepared struct {
	Writer uint64 `json:"writer"`
}
