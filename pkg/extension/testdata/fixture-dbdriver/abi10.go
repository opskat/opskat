//go:build fixture_hostabi10

package main

import "github.com/opskat/opskat/pkg/dbdriver"

// abiCapabilities: built as an extension for hostABI 1.0 is, the driver declares
// neither running SQL files nor dumping them, and its dialect has neither hook.
var abiCapabilities []dbdriver.Capability
