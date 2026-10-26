package opskat

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestDescribeReportsDatabaseDrivers(t *testing.T) {
	Convey("a driver registered through the seam is reported by describe() and served its calls", t, func() {
		resetRegistries()
		Extension(Meta{Icon: "database"})
		var served []byte
		RegisterDatabaseDriver(dbdriver.Descriptor{ID: "demodb", Name: "DemoDB"}, func(input []byte) (json.RawMessage, error) {
			served = input
			return json.RawMessage(`{"result":{"session":1}}`), nil
		})

		drivers := decodeDescribe(t)["databaseDrivers"].([]any)
		So(len(drivers), ShouldEqual, 1)
		reported := drivers[0].(map[string]any)
		So(reported["id"], ShouldEqual, "demodb")
		So(reported["name"], ShouldEqual, "DemoDB")

		_, err := dispatch("database_driver", []byte(`{"op":"open"}`))
		So(err, ShouldBeNil)
		So(string(served), ShouldEqual, `{"op":"open"}`)
	})
}

func TestDatabaseDriverCallsWithoutADriver(t *testing.T) {
	Convey("an extension that registered no driver reports so instead of serving the call", t, func() {
		resetRegistries()
		_, err := dispatch("database_driver", []byte(`{"op":"open"}`))
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "no database driver")
		So(func() { RegisterDatabaseDriver(dbdriver.Descriptor{ID: "demodb"}, nil) }, ShouldPanic)
	})
}
