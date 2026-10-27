package extwire

// The database driver calls: the JSON a host and an extension exchange to run a
// database driver the extension provides. The host side is
// pkg/extension.(*Plugin).OpenDriverSession (and ClassifyStatements, which
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

	// DriverOpStreamNext reads up to DriverArgs.Max rows of a cursor; DriverOpStreamClose closes it.
	DriverOpStreamNext  = "streamNext"
	DriverOpStreamClose = "streamClose"
	// DriverOpRowStatements generates statements with a prepared writer. A writer
	// outlives the session it was prepared on: it no longer touches the database.
	DriverOpRowStatements = "rowStatements"

	// DriverOpClassifyStatements splits and classifies DriverArgs.Statement with driver
	// DriverArgs.Driver (dbdriver.StatementClassifier), answering []dbdriver.Statement.
	// It runs on no session: the driver does not touch the database to answer.
	DriverOpClassifyStatements = "classifyStatements"
)

// DriverCall is the input of DriverFunction.
type DriverCall struct {
	Op string `json:"op"`
	// Session is the session the operation runs on (every operation but DriverOpOpen,
	// DriverOpClassifyStatements and the cursor / writer ones).
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

	Stream uint64 `json:"stream,omitempty"`
	Writer uint64 `json:"writer,omitempty"`
	// Max is how many rows DriverOpStreamNext may answer.
	Max int `json:"max,omitempty"`
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

// DriverStreamPage answers DriverOpStreamNext: the next rows, and whether the cursor has no
// more after them.
type DriverStreamPage struct {
	Rows [][]any `json:"rows"`
	Done bool    `json:"done"`
}

// DriverWriterPrepared answers DriverOpPrepareRowStatements.
type DriverWriterPrepared struct {
	Writer uint64 `json:"writer"`
}
