// Command fixture-dbdriver is the WASM extension pkg/extension's database driver
// tests run against: one SQL database driver, "fixturedb", whose session the SDK
// assembles from pkg/dbdriver/sqldb. Its database is memdb (memdb.go), an
// in-memory database/sql driver that serves a fixed catalog but logs in to — and
// announces every statement to — whatever the host's reach path leads to, so a
// test can count logins and see the connection survive between calls.
//
// It is built on demand by the tests (extensiontest.InstallDBDriverFixture), never
// shipped. Like every reactor guest it registers from init(); func main() is never
// called.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	opskat "github.com/opskat/opskat/pkg/extsdk"
	"github.com/opskat/opskat/pkg/extsdk/sdkdbdriver"
)

func main() {}

// descriptor declares a few capabilities whose sqldb pieces are small (views,
// DDL, create / drop / truncate table, export as SQL); everything else stays
// undeclared, so the conformance suites also see the refusals cross the boundary.
// It declares the certificate field group, so the host hands it the asset's TLS
// material (SHOW OPEN TLS answers what it got), and the Kerberos field group,
// so a Kerberos asset's session logs in with a host ticket (SHOW OPEN AUTH and
// SHOW KERBEROS TICKET probe it); its manifest declares the host ABI and the
// capability both need. It splits and classifies statements itself (split.go),
// which needs that host ABI too.
var descriptor = dbdriver.Descriptor{
	ID:          "fixturedb",
	Name:        "FixtureDB",
	Order:       900,
	Aliases:     []string{},
	DefaultPort: 7357,
	Form: []dbdriver.FormTabFields{
		{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldKerberos,
			dbdriver.FieldDatabase,
		}},
		{Tab: dbdriver.TabTLS, Fields: []dbdriver.FormField{dbdriver.FieldTLSCert}},
	},
	Capabilities: []dbdriver.Capability{
		dbdriver.CapViews, dbdriver.CapViewDDL, dbdriver.CapCreateTable, dbdriver.CapDropTable,
		dbdriver.CapTruncateTable, dbdriver.CapExportSQL, dbdriver.CapClassifyStatements,
	},
}

func init() {
	opskat.Extension(opskat.Meta{Icon: "database", DisplayName: "Fixture DB", Description: "Test fixture database driver"})
	sdkdbdriver.DatabaseDriver(descriptor, open, sdkdbdriver.WithStatementClassifier(classify))
}

// open logs in through the reach path the host handed over and confirms the
// database answers, as a built-in driver's Open does.
func open(ctx context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) {
	cfg := *req.Config
	db := sql.OpenDB(&connector{
		dial:     req.Transport.Dial,
		addr:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		user:     cfg.Username,
		password: req.Password,
		config:   cfg,
		options:  req.DriverOptions,
		tls:      req.TLS,
		kerberos: req.Kerberos,
	})
	if err := sqldb.Establish(ctx, db, nil); err != nil {
		return nil, nil, err
	}
	return db, dialect{}, nil
}

// dialect is fixturedb's SQL: [name] quoting, "[db].[table]" references, and
// paging / counting written as "PAGE <offset> <limit> OF <query>" and
// "COUNT OF <query>", which memdb evaluates.
type dialect struct{ sqldb.LimitOffset }

func (dialect) CountSQL(query string) string { return "COUNT OF " + query }

func (dialect) PagedSQL(query string, offset, limit int) string {
	return fmt.Sprintf("PAGE %d %d OF %s", offset, limit, query)
}

func (dialect) QuoteIdent(name string) string { return "[" + name + "]" }

func (d dialect) TableRef(database, table string) string {
	if database == "" {
		return d.QuoteIdent(table)
	}
	return d.QuoteIdent(database) + "." + d.QuoteIdent(table)
}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor }

func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SHOW DATABASES")
}

func (d dialect) ListTables(ctx context.Context, conn *sql.Conn, database string) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SHOW TABLES FROM "+d.QuoteIdent(database))
}

func (d dialect) TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error) {
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("primary keys", err)
	}
	return sqldb.AssembleMetadata(sqldb.ColumnMetas(columns), primaryKeys, []dbdriver.IndexMeta{}, []dbdriver.ForeignKeyMeta{}), nil
}

func (d dialect) ListObjects(ctx context.Context, conn *sql.Conn, database string) (*dbdriver.DatabaseObjects, error) {
	objects := &dbdriver.DatabaseObjects{
		Views: []dbdriver.ObjectMeta{}, Procedures: []dbdriver.ObjectMeta{},
		Functions: []dbdriver.ObjectMeta{}, Triggers: []dbdriver.ObjectMeta{},
	}
	if err := sqldb.CollectObjects(ctx, conn, "SHOW VIEWS FROM "+d.QuoteIdent(database), dbdriver.ObjectView, &objects.Views); err != nil {
		return nil, err
	}
	return objects, nil
}

func (d dialect) ObjectSource(ctx context.Context, conn *sql.Conn, database, _, name, _ string) (string, error) {
	var source string
	err := conn.QueryRowContext(ctx, "SHOW VIEW SOURCE "+d.TableRef(database, name)).Scan(&source)
	return source, err
}

func (d dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]string, error) {
	return sqldb.QueryPrimaryKeys(ctx, conn, "column_name", "SHOW PRIMARY KEY FROM "+d.TableRef(database, table))
}

func (d dialect) TableColumns(ctx context.Context, conn *sql.Conn, database, table string) ([]sqldb.TableColumn, error) {
	return sqldb.QueryTableColumns(ctx, conn, "SHOW COLUMNS FROM "+d.TableRef(database, table))
}

func (d dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	var ddl string
	err := conn.QueryRowContext(ctx, "SHOW CREATE TABLE "+d.TableRef(database, table)).Scan(&ddl)
	return ddl, err
}

func (dialect) Literals(context.Context, *sql.Conn) (sqldb.Literals, error) {
	return sqldb.Literals{}, nil
}

func (dialect) UpdateRowSQL(tableRef, set, where string) string {
	return "UPDATE " + tableRef + " SET " + set + " WHERE " + where + ";"
}

func (dialect) TruncateTableSQL(tableRef string) string { return "TRUNCATE TABLE " + tableRef }
