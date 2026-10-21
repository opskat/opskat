package main

import (
	"bufio"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"github.com/opskat/opskat/pkg/dbdriver"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// memdb is the fixture's database: a fixed, read-only catalog held in guest
// memory, behind a real database/sql driver so the SDK's sqldb session runs on it
// unchanged.
//
// A connection logs in over a network connection opened through the reach path
// ("LOGIN <user> <password>", answered "OK" or "ERR <message>") and announces each
// statement on it before serving it from memory ("QUERY ..." / "EXEC ..." /
// "PING", answered "OK"). The far end — the test — can therefore count logins,
// see each statement arrive, and refuse a login with the error the driver then
// reports.

// defaultDatabase is the database a bare [table] reference names.
const defaultDatabase = "app"

type memColumn struct {
	name, typ  string
	nullable   bool
	primaryKey bool
	def        *string
	comment    string
}

type memTable struct {
	columns []memColumn
	size    int
	row     func(i int) []driver.Value
	// ddl is what SHOW CREATE TABLE answers.
	ddl string
}

var anon = "anon"

var users = [][]driver.Value{{int64(1), "alice"}, {int64(2), "bob"}, {int64(3), "carol"}}

// seriesSize is large enough that a whole-table read spans several host calls.
const seriesSize = 1000

var tables = map[string]map[string]*memTable{
	defaultDatabase: {
		"users": {
			columns: []memColumn{
				{name: "id", typ: "int", primaryKey: true, comment: "primary key"},
				{name: "name", typ: "text", nullable: true, def: &anon},
			},
			size: len(users),
			row:  func(i int) []driver.Value { return users[i] },
			ddl:  "CREATE TABLE [app].[users] ([id] int PRIMARY KEY, [name] text DEFAULT 'anon')",
		},
		"series": {
			columns: []memColumn{{name: "n", typ: "int", primaryKey: true}},
			size:    seriesSize,
			row:     func(i int) []driver.Value { return []driver.Value{int64(i + 1)} },
			ddl:     "CREATE TABLE [app].[series] ([n] int PRIMARY KEY)",
		},
	},
}

// tableOrder is the order SHOW TABLES lists them in.
var tableOrder = []string{"users", "series"}

var views = map[string]string{"v_users": "SELECT [id] FROM [app].[users]"}

// seriesProduced counts the rows the most recent read of series has produced so
// far; SHOW STREAM PROGRESS answers it.
var seriesProduced int

type connector struct {
	dial     dbdriver.DialFunc
	addr     string
	user     string
	password string
	config   dbdriver.Config
	options  map[string]any
	tls      *dbdriver.TLSMaterial
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	nc, err := c.dial(ctx, "tcp", c.addr)
	if err != nil {
		return nil, err
	}
	cn := &conn{nc: nc, r: bufio.NewReader(nc), config: c.config, options: c.options, tls: c.tls}
	if err := cn.roundTrip("LOGIN " + c.user + " " + c.password); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return cn, nil
}

func (c *connector) Driver() driver.Driver { return memDriver{} }

type memDriver struct{}

func (memDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fixturedb: connections are opened through the connector")
}

type conn struct {
	nc     net.Conn
	r      *bufio.Reader
	config dbdriver.Config
	// options are the driver options the session was opened with.
	options map[string]any
	// tls is the TLS material the session was opened with, nil for none. memdb
	// speaks plaintext: the material is only held for SHOW OPEN TLS to answer.
	tls *dbdriver.TLSMaterial
}

func (c *conn) roundTrip(line string) error {
	if _, err := io.WriteString(c.nc, strings.ReplaceAll(line, "\n", " ")+"\n"); err != nil {
		return err
	}
	reply, err := c.r.ReadString('\n')
	if err != nil {
		return err
	}
	if msg, failed := strings.CutPrefix(strings.TrimSpace(reply), "ERR "); failed {
		return errors.New(msg)
	}
	return nil
}

func (c *conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fixturedb: prepared statements are not supported")
}

// Close says goodbye first ("LOGOFF", awaiting the answer) when the session was
// opened with the driver option "logoff": true, as a real database's logoff does.
func (c *conn) Close() error {
	if c.options["logoff"] == true {
		_ = c.roundTrip("LOGOFF")
	}
	return c.nc.Close()
}

func (c *conn) Begin() (driver.Tx, error) { return tx{}, nil }

func (c *conn) Ping(context.Context) error { return c.roundTrip("PING") }

func (c *conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return nil, errors.New("fixturedb: query arguments are not supported")
	}
	if err := c.roundTrip("QUERY " + query); err != nil {
		return nil, err
	}
	return c.query(strings.TrimSpace(query))
}

func (c *conn) ExecContext(_ context.Context, statement string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		return nil, errors.New("fixturedb: statement arguments are not supported")
	}
	if err := c.roundTrip("EXEC " + statement); err != nil {
		return nil, err
	}
	// The catalog is read-only: every statement that is not a query fails, which is
	// also what the conformance suite's failing batch statement needs.
	return nil, fmt.Errorf("fixturedb: syntax error near %q", statement)
}

type tx struct{}

func (tx) Commit() error   { return nil }
func (tx) Rollback() error { return nil }

var (
	countOf   = regexp.MustCompile(`^COUNT OF (.+)$`)
	pageOf    = regexp.MustCompile(`^PAGE (\d+) (\d+) OF (.+)$`)
	selectAll = regexp.MustCompile(`^SELECT \* FROM (\S+)(?: LIMIT (\d+)(?: OFFSET (\d+))?)?$`)
	countAll  = regexp.MustCompile(`^SELECT COUNT\(\*\) FROM (\S+)$`)
	showFrom  = regexp.MustCompile(`^SHOW (TABLES|VIEWS) FROM \[([^\]]*)\]$`)
	showHost  = regexp.MustCompile(`^SHOW HOST (\w+)(?: (\S+))?$`)
	showOf    = regexp.MustCompile(`^SHOW (COLUMNS FROM|PRIMARY KEY FROM|CREATE TABLE|VIEW SOURCE) (\S+)$`)
	tableRef  = regexp.MustCompile(`^(?:\[([^\]]*)\]\.)?\[([^\]]+)\]$`)
)

func (c *conn) query(q string) (driver.Rows, error) {
	switch {
	case q == "SELECT 1":
		return values([]string{"1"}, []driver.Value{int64(1)}), nil
	case q == "SHOW DATABASES":
		return values([]string{"name"}, []driver.Value{defaultDatabase}), nil
	case q == "SHOW STREAM PROGRESS":
		return values([]string{"produced"}, []driver.Value{int64(seriesProduced)}), nil
	case q == "SHOW OPEN CONFIG":
		raw, err := json.Marshal(c.config)
		if err != nil {
			return nil, err
		}
		return values([]string{"config"}, []driver.Value{string(raw)}), nil
	case q == "SHOW OPEN OPTIONS":
		raw, err := json.Marshal(c.options)
		if err != nil {
			return nil, err
		}
		return values([]string{"options"}, []driver.Value{string(raw)}), nil
	case q == "SHOW OPEN TLS":
		// The test compares the material inside itself; nothing here logs it.
		raw, err := json.Marshal(c.tls)
		if err != nil {
			return nil, err
		}
		return values([]string{"tls"}, []driver.Value{string(raw)}), nil
	case q == "SELECT PANIC", q == "SELECT TRAP", q == "SELECT OOM":
		fault(q)
	}
	if m := showHost.FindStringSubmatch(q); m != nil {
		return hostProbe(m[1], m[2]), nil
	}
	if m := countOf.FindStringSubmatch(q); m != nil {
		return c.count(m[1])
	}
	if m := pageOf.FindStringSubmatch(q); m != nil {
		return c.page(m[3], atoi(m[1]), atoi(m[2]))
	}
	if m := countAll.FindStringSubmatch(q); m != nil {
		t, err := lookup(m[1])
		if err != nil {
			return nil, err
		}
		return values([]string{"count"}, []driver.Value{int64(t.size)}), nil
	}
	if m := selectAll.FindStringSubmatch(q); m != nil {
		return selectTable(m[1], m[2], m[3])
	}
	if m := showFrom.FindStringSubmatch(q); m != nil {
		return showNames(m[1], m[2])
	}
	if m := showOf.FindStringSubmatch(q); m != nil {
		return show(m[1], m[2])
	}
	return nil, fmt.Errorf("fixturedb: syntax error near %q", q)
}

func (c *conn) count(inner string) (driver.Rows, error) {
	rows, err := c.query(inner)
	if err != nil {
		return nil, err
	}
	n := 0
	dest := make([]driver.Value, len(rows.Columns()))
	for rows.Next(dest) == nil {
		n++
	}
	return values([]string{"count"}, []driver.Value{int64(n)}), nil
}

func (c *conn) page(inner string, offset, limit int) (driver.Rows, error) {
	rows, err := c.query(inner)
	if err != nil {
		return nil, err
	}
	columns := rows.Columns()
	var all [][]driver.Value
	for {
		dest := make([]driver.Value, len(columns))
		if rows.Next(dest) != nil {
			break
		}
		all = append(all, dest)
	}
	end := min(offset+limit, len(all))
	offset = min(offset, end)
	return values(columns, all[offset:end]...), nil
}

func selectTable(ref, limit, offset string) (driver.Rows, error) {
	t, err := lookup(ref)
	if err != nil {
		return nil, err
	}
	start, end := 0, t.size
	if limit != "" {
		start = min(atoi(offset), t.size)
		end = min(start+atoi(limit), t.size)
	}
	columns := make([]string, len(t.columns))
	for i, column := range t.columns {
		columns[i] = column.name
	}
	r := &memRows{columns: columns, next: start, end: end, row: t.row}
	if t == tables[defaultDatabase]["series"] {
		seriesProduced = 0
		r.produced = &seriesProduced
	}
	return r, nil
}

func showNames(what, database string) (driver.Rows, error) {
	if database != defaultDatabase {
		return nil, fmt.Errorf("fixturedb: unknown database %q", database)
	}
	var names [][]driver.Value
	if what == "TABLES" {
		for _, name := range tableOrder {
			names = append(names, []driver.Value{name})
		}
	} else {
		for name := range views {
			names = append(names, []driver.Value{name})
		}
	}
	return values([]string{"name"}, names...), nil
}

func show(what, ref string) (driver.Rows, error) {
	if what == "VIEW SOURCE" {
		_, name, err := splitRef(ref)
		if err != nil {
			return nil, err
		}
		source, ok := views[name]
		if !ok {
			return nil, fmt.Errorf("fixturedb: no view %s", ref)
		}
		return values([]string{"source"}, []driver.Value{source}), nil
	}
	t, err := lookup(ref)
	if err != nil {
		return nil, err
	}
	switch what {
	case "CREATE TABLE":
		return values([]string{"ddl"}, []driver.Value{t.ddl}), nil
	case "PRIMARY KEY FROM":
		var keys [][]driver.Value
		for _, column := range t.columns {
			if column.primaryKey {
				keys = append(keys, []driver.Value{column.name})
			}
		}
		return values([]string{"column_name"}, keys...), nil
	default: // COLUMNS FROM
		rows := make([][]driver.Value, 0, len(t.columns))
		for _, column := range t.columns {
			nullable := "NO"
			if column.nullable {
				nullable = "YES"
			}
			var def driver.Value
			if column.def != nil {
				def = *column.def
			}
			rows = append(rows, []driver.Value{column.name, column.typ, nullable, def, column.comment})
		}
		return values([]string{"column_name", "data_type", "is_nullable", "column_default", "column_comment"}, rows...), nil
	}
}

func splitRef(ref string) (database, table string, err error) {
	m := tableRef.FindStringSubmatch(ref)
	if m == nil {
		return "", "", fmt.Errorf("fixturedb: bad table reference %q", ref)
	}
	database = m[1]
	if database == "" {
		database = defaultDatabase
	}
	return database, m[2], nil
}

func lookup(ref string) (*memTable, error) {
	database, name, err := splitRef(ref)
	if err != nil {
		return nil, err
	}
	t, ok := tables[database][name]
	if !ok {
		return nil, fmt.Errorf("fixturedb: no table %s", ref)
	}
	return t, nil
}

// The ways an extension driver can fail inside a call, for the host's fault
// isolation tests: a Go panic (the SDK recovers it into an error reply), a WASM
// trap (a read past the end of guest memory) and running out of guest memory
// (the Go runtime dies once the host's memory limit refuses to grow it).
var (
	faultSink  byte
	faultHoard [][]byte
)

func fault(q string) {
	switch q {
	case "SELECT PANIC":
		panic("fixturedb: panic requested by the test")
	case "SELECT TRAP":
		faultSink = *(*byte)(unsafe.Pointer(uintptr(0xFFFF_FFF0)))
	case "SELECT OOM":
		for {
			faultHoard = append(faultHoard, make([]byte, 8<<20))
		}
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// values is a result set held in full.
func values(columns []string, rows ...[]driver.Value) driver.Rows {
	return &memRows{columns: columns, end: len(rows), row: func(i int) []driver.Value { return rows[i] }}
}

// memRows produces rows on demand, so a cursor that has been read halfway has
// produced only half of them.
type memRows struct {
	columns   []string
	next, end int
	row       func(i int) []driver.Value
	produced  *int
}

func (r *memRows) Columns() []string { return r.columns }

func (r *memRows) Close() error { return nil }

func (r *memRows) Next(dest []driver.Value) error {
	if r.next >= r.end {
		return io.EOF
	}
	copy(dest, r.row(r.next))
	r.next++
	if r.produced != nil {
		*r.produced++
	}
	return nil
}

// hostProbe makes the host call the test names from inside a driver call
// ("SHOW HOST <what> [target]") and answers the error it got, empty for none: what
// a driver call may and may not ask of the host.
func hostProbe(what, target string) driver.Rows {
	var err error
	switch what {
	case "LOG":
		opskat.Log("info", "fixturedb host probe")
	case "KV":
		_, err = opskat.KVGet("probe")
	case "KVSET":
		err = opskat.KVSet("probe", []byte("x"))
	case "FILE_DIALOG":
		_, err = opskat.FileDialog("open", opskat.DialogOptions{})
	case "ASSET_CONFIG":
		_, err = (&opskat.ActionContext{Asset: opskat.Asset{ID: 1}}).AssetConfig()
	case "HTTP":
		var resp *http.Response
		if resp, err = (&http.Client{Transport: opskat.NewHTTPTransport()}).Get(target); err == nil {
			_ = resp.Body.Close()
		}
	case "DIAL":
		var nc net.Conn
		if nc, err = opskat.Dial("tcp", target); err == nil {
			_ = nc.Close()
		}
	default:
		err = fmt.Errorf("fixturedb: unknown host probe %q", what)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return values([]string{"error"}, []driver.Value{msg})
}
