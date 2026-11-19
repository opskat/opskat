package sdkdbdriver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	opskat "github.com/opskat/opskat/pkg/extsdk"
	"github.com/opskat/opskat/pkg/extsdk/internal/registration"
	"github.com/opskat/opskat/pkg/extwire"
)

var demoDriver = dbdriver.Descriptor{
	ID:      "demodb",
	Name:    "DemoDB",
	Order:   100,
	Aliases: []string{},
	Form: []dbdriver.FormTabFields{{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
		dbdriver.FieldHost, dbdriver.FieldPassword,
	}}},
	Capabilities: []dbdriver.Capability{},
}

// driverReply is the database_driver reply as the host reads it.
type driverReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func callDriver(t *testing.T, input string) driverReply {
	t.Helper()
	raw, err := serve([]byte(input))
	So(err, ShouldBeNil)
	var reply driverReply
	So(json.Unmarshal(raw, &reply), ShouldBeNil)
	return reply
}

// resetDrivers clears this package's registrations and handles, and the SDK's
// record of invalid registrations (the SDK's own registry keeps the descriptors,
// which these tests do not read).
func resetDrivers() {
	drivers = nil
	driverHandles = newDriverHandleTable()
	registration.Reset()
}

func TestDatabaseDriverRegistrationRejectsBrokenDeclarations(t *testing.T) {
	open := func(context.Context, dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) { return nil, nil, nil }

	// registration.Err is what the SDK answers describe (and every other call) with.
	Convey("a driver without an opener fails describe, naming the driver", t, func() {
		resetDrivers()
		DatabaseDriver(demoDriver, nil)
		So(drivers, ShouldBeEmpty)
		So(registration.Err(), ShouldBeError, `opskat: database driver "demodb" has no open function`)
	})

	Convey("a driver registered twice fails describe, naming the driver", t, func() {
		resetDrivers()
		DatabaseDriver(demoDriver, open)
		So(registration.Err(), ShouldBeNil)
		DatabaseDriver(demoDriver, open)
		So(drivers, ShouldHaveLength, 1)
		So(registration.Err(), ShouldBeError, `opskat: database driver "demodb" is already registered`)
	})
}

func TestDatabaseDriverStatementClassifierMatchesItsDeclaration(t *testing.T) {
	open := func(context.Context, dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) { return nil, nil, nil }
	classify := func(_ context.Context, sql string) ([]dbdriver.Statement, error) {
		return []dbdriver.Statement{{Start: 0, End: len(sql), Text: sql, Type: "SELECT"}}, nil
	}
	splitting := demoDriver
	splitting.Capabilities = []dbdriver.Capability{dbdriver.CapClassifyStatements}

	Convey("a classifier and the capability declaring it come together, or describe fails naming the driver", t, func() {
		mismatch := `opskat: database driver "demodb" must declare capability "classify_statements" exactly when it is given WithStatementClassifier`
		for _, register := range []func(){
			func() { DatabaseDriver(splitting, open) },
			func() { DatabaseDriver(demoDriver, open, WithStatementClassifier(classify)) },
			func() { DatabaseDriver(splitting, open, WithStatementClassifier(nil)) },
		} {
			resetDrivers()
			register()
			So(drivers, ShouldBeEmpty)
			So(registration.Err(), ShouldBeError, mismatch)
		}
	})

	Convey("the host asks the driver to split SQL without a session", t, func() {
		resetDrivers()
		DatabaseDriver(splitting, open, WithStatementClassifier(classify))

		reply := callDriver(t, `{"op":"classifyStatements","args":{"driver":"demodb","statement":"SELECT 1"}}`)
		So(reply.Error, ShouldBeNil)
		var stmts []dbdriver.Statement
		So(json.Unmarshal(reply.Result, &stmts), ShouldBeNil)
		So(stmts, ShouldResemble, []dbdriver.Statement{{Start: 0, End: 8, Text: "SELECT 1", Type: "SELECT"}})

		Convey("a driver without a classifier refuses as unsupported", func() {
			DatabaseDriver(dbdriver.Descriptor{ID: "plaindb"}, open)
			reply := callDriver(t, `{"op":"classifyStatements","args":{"driver":"plaindb","statement":"SELECT 1"}}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Code, ShouldEqual, "unsupported")
			So(reply.Error.Message, ShouldContainSubstring, "plaindb")
		})
		Convey("a driver the extension does not register is an error", func() {
			reply := callDriver(t, `{"op":"classifyStatements","args":{"driver":"otherdb","statement":"SELECT 1"}}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Message, ShouldContainSubstring, "otherdb")
		})
	})
}

func TestDatabaseDriverServesSessionsOverJSON(t *testing.T) {
	Convey("the host opens a session and runs operations on it as JSON", t, func() {
		resetDrivers()
		db, mock, err := sqlmock.New()
		So(err, ShouldBeNil)
		var opened dbdriver.OpenRequest
		DatabaseDriver(demoDriver, func(_ context.Context, req dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) {
			opened = req
			return db, sqldb.LimitOffset{}, nil
		})

		Convey("fields the extension does not know are ignored, in the call and in its arguments", func() {
			reply := callDriver(t, `{"op":"open","fromTheFuture":1,"args":{"driver":"demodb","config":{"driver":"demodb","host":"db.internal","newKey":true},"password":"s3cret","driverOptions":{"tenant":"acme","token":"opt-s3cret"},"alsoNew":"x"}}`)
			So(reply.Error, ShouldBeNil)
			So(opened.Config.Host, ShouldEqual, "db.internal")
			So(opened.Password, ShouldEqual, "s3cret")
			So(opened.DriverOptions, ShouldResemble, map[string]any{"tenant": "acme", "token": "opt-s3cret"})
			So(opened.Transport.Dial, ShouldNotBeNil)
			var session struct {
				Session uint64 `json:"session"`
			}
			So(json.Unmarshal(reply.Result, &session), ShouldBeNil)

			mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
			reply = callDriver(t, `{"op":"execute","session":`+jsonNumber(session.Session)+`,"args":{"statement":"SELECT 1","notYet":[1,2]}}`)
			So(reply.Error, ShouldBeNil)
			var result dbdriver.Result
			So(json.Unmarshal(reply.Result, &result), ShouldBeNil)
			So(result.Rows.Columns, ShouldResemble, []string{"1"})
			So(len(result.Rows.Rows), ShouldEqual, 1)
			So(mock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("a paged execute the host asks to skip the total fetches only the page", func() {
			reply := callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb"}}}`)
			var session struct {
				Session uint64 `json:"session"`
			}
			So(json.Unmarshal(reply.Result, &session), ShouldBeNil)

			// The regexp matcher only accepts the page statement: a COUNT sent first fails the call.
			mock.ExpectQuery(`LIMIT 10 OFFSET 10$`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
			reply = callDriver(t, `{"op":"executePaged","session":`+jsonNumber(session.Session)+
				`,"args":{"statement":"SELECT id FROM t","page":{"Index":1,"Size":10,"SkipTotal":true}}}`)
			So(reply.Error, ShouldBeNil)
			var result dbdriver.Result
			So(json.Unmarshal(reply.Result, &result), ShouldBeNil)
			So(len(result.Rows.Rows), ShouldEqual, 1)
			So(result.Rows.Total, ShouldBeNil)
			So(mock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("a refusal the session reports as ErrUnsupported crosses as code unsupported", func() {
			reply := callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb"}}}`)
			var session struct {
				Session uint64 `json:"session"`
			}
			So(json.Unmarshal(reply.Result, &session), ShouldBeNil)

			reply = callDriver(t, `{"op":"tableDDL","session":`+jsonNumber(session.Session)+`,"args":{"table":"x"}}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Code, ShouldEqual, "unsupported")
			So(reply.Error.Message, ShouldContainSubstring, dbdriver.ErrUnsupported.Error())
		})

		Convey("a Kerberos asset's open carries a ticket source that asks the host, and a password asset's none", func() {
			host := opskat.NewTestHost(opskat.WithKerberosTickets(func(spn string) ([]byte, error) {
				return []byte("ticket:" + spn), nil
			}))
			defer host.Close()

			reply := callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb","host":"db.internal"},"kerberos":true}}`)
			So(reply.Error, ShouldBeNil)
			So(opened.Kerberos, ShouldNotBeNil)
			So(opened.Kerberos.Principal, ShouldBeEmpty)
			ticket, err := opened.Kerberos.Ticket(context.Background(), dbdriver.KerberosSPN("demodb", "db.internal"))
			So(err, ShouldBeNil)
			So(string(ticket), ShouldEqual, "ticket:demodb/db.internal")

			reply = callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb"},"password":"s3cret"}}`)
			So(reply.Error, ShouldBeNil)
			So(opened.Kerberos, ShouldBeNil)
		})

		Convey("a read-only config is refused before the opener runs when the driver does not declare it", func() {
			opened = dbdriver.OpenRequest{}
			reply := callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb","read_only":true}}}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Code, ShouldEqual, "unsupported")
			So(opened.Config, ShouldBeNil)
		})

		Convey("an operation of a newer host is refused as unsupported, so the host treats the feature as absent", func() {
			reply := callDriver(t, `{"op":"open","args":{"driver":"demodb","config":{"driver":"demodb"}}}`)
			var session struct {
				Session uint64 `json:"session"`
			}
			So(json.Unmarshal(reply.Result, &session), ShouldBeNil)

			reply = callDriver(t, `{"op":"opFromTheFuture","session":`+jsonNumber(session.Session)+`}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Code, ShouldEqual, "unsupported")
			So(reply.Error.Message, ShouldContainSubstring, "opFromTheFuture")
		})

		Convey("a driver the extension does not register is an error", func() {
			reply := callDriver(t, `{"op":"open","args":{"driver":"otherdb","config":{"driver":"otherdb"}}}`)
			So(reply.Error, ShouldNotBeNil)
			So(reply.Error.Message, ShouldContainSubstring, "otherdb")
		})
	})
}

func jsonNumber(n uint64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

// listRows is a cursor over rows, failing with err once they are read.
type listRows struct {
	rows [][]any
	err  error
	row  []any
}

func (r *listRows) Columns() []string { return []string{"body"} }

func (r *listRows) Next() bool {
	if len(r.rows) == 0 {
		return false
	}
	r.row, r.rows = r.rows[0], r.rows[1:]
	return true
}

func (r *listRows) Row() []any { return r.row }

func (r *listRows) Err() error {
	if len(r.rows) == 0 {
		return r.err
	}
	return nil
}

func (r *listRows) Close() error { return nil }

// streamPage is a streamNext answer as the host reads it.
type streamPage struct {
	Rows [][]any `json:"rows"`
	Done bool    `json:"done"`
}

func TestStreamNextBoundsPagesBySize(t *testing.T) {
	text := func(size int) []any { return []any{strings.Repeat("x", size)} }
	// pages reads the cursor the way the host does, max rows at a time, until a
	// page says it is done or fails, and answers each page's row sizes.
	pages := func(rows *listRows) (sizes [][]int, done bool, lastErr string) {
		resetDrivers()
		driverHandles.streams[1] = driverStream{rows: rows}
		for i := 0; i < 10 && !done && lastErr == ""; i++ {
			reply := callDriver(t, `{"op":"streamNext","args":{"stream":1,"max":256}}`)
			if reply.Error != nil {
				lastErr = reply.Error.Message
			}
			var page streamPage
			So(json.Unmarshal(reply.Result, &page), ShouldBeNil)
			pageSizes := make([]int, 0, len(page.Rows))
			for _, row := range page.Rows {
				pageSizes = append(pageSizes, len(row[0].(string)))
			}
			sizes, done = append(sizes, pageSizes), page.Done
		}
		return sizes, done, lastErr
	}

	Convey("a cursor page holds rows until about DriverRowsBytes, at least one, and is done only when the cursor is", t, func() {
		Convey("an empty cursor answers one empty page that is done", func() {
			sizes, done, err := pages(&listRows{})
			So(sizes, ShouldResemble, [][]int{{}})
			So(done, ShouldBeTrue)
			So(err, ShouldBeEmpty)
		})

		Convey("a page that reaches the size stops there short of max, and is not done", func() {
			half := extwire.DriverRowsBytes / 2
			sizes, done, err := pages(&listRows{rows: [][]any{text(half), text(half), text(half)}})
			So(sizes, ShouldResemble, [][]int{{half, half}, {half}})
			So(done, ShouldBeTrue)
			So(err, ShouldBeEmpty)
		})

		Convey("a row larger than a page comes alone, and the rows after it follow", func() {
			sizes, done, err := pages(&listRows{rows: [][]any{text(2 * extwire.DriverRowsBytes), text(10), text(10)}})
			So(sizes, ShouldResemble, [][]int{{2 * extwire.DriverRowsBytes}, {10, 10}})
			So(done, ShouldBeTrue)
			So(err, ShouldBeEmpty)
		})

		Convey("a read error mid-page answers the rows read before it with the error, and the page is done", func() {
			sizes, done, err := pages(&listRows{rows: [][]any{text(10), text(10)}, err: errors.New("connection reset")})
			So(sizes, ShouldResemble, [][]int{{10, 10}})
			So(done, ShouldBeTrue)
			So(err, ShouldContainSubstring, "connection reset")
		})
	})
}
