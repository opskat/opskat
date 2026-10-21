package sdkdbdriver

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
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

// resetDrivers clears this package's registrations and handles (the SDK's own
// registry keeps the descriptors, which these tests do not read).
func resetDrivers() {
	drivers = nil
	driverHandles = newDriverHandleTable()
}

func TestDatabaseDriverRegistrationRejectsBrokenDeclarations(t *testing.T) {
	Convey("a driver without an opener, or registered twice, fails at init", t, func() {
		resetDrivers()
		So(func() { DatabaseDriver(demoDriver, nil) }, ShouldPanic)

		open := func(context.Context, dbdriver.OpenRequest) (*sql.DB, sqldb.Dialect, error) { return nil, nil, nil }
		DatabaseDriver(demoDriver, open)
		So(func() { DatabaseDriver(demoDriver, open) }, ShouldPanic)
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
