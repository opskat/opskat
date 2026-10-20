package opskat

import (
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// A database driver is provided through pkg/extsdk/sdkdbdriver, not this
// package: serving a driver links database/sql and pkg/dbdriver/sqldb (and what
// they import), which an extension without a driver must not carry. This file is
// the seam that package registers through — describe() reports the descriptors,
// and dispatch hands the driver calls to the one serve function.

// DatabaseDriverServe serves one call of the guest function driverwire.Function.
type DatabaseDriverServe func(input []byte) (json.RawMessage, error)

// databaseDriverServe answers the driver calls; the descriptors are in
// databaseDrivers (registry.go).
var databaseDriverServe DatabaseDriverServe

// RegisterDatabaseDriver is how pkg/extsdk/sdkdbdriver declares a driver: d is
// reported by describe(), and serve answers every driver call (it is the same
// function for every driver the extension provides). Extensions call
// sdkdbdriver.DatabaseDriver instead.
func RegisterDatabaseDriver(d dbdriver.Descriptor, serve DatabaseDriverServe) {
	if serve == nil {
		panic(fmt.Sprintf("opskat: database driver %q has nothing to serve its calls", d.ID))
	}
	databaseDrivers = append(databaseDrivers, d)
	databaseDriverServe = serve
}

func dispatchDatabaseDriver(input []byte) (json.RawMessage, error) {
	if databaseDriverServe == nil {
		return nil, fmt.Errorf("this extension provides no database driver")
	}
	return databaseDriverServe(input)
}
