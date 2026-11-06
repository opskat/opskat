// Command fixture-badkinds is the WASM extension tests use to observe how an SDK
// registration mistake reaches the host: it declares a tool that is both host-only
// and page-only, which the SDK must report from describe() — naming the tool and
// the reason — instead of panicking inside init. Built on demand by
// extensiontest.InstallBadKindsFixture, never shipped.
package main

import (
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

func init() {
	opskat.Extension(opskat.Meta{DisplayName: "Bad Kinds Fixture"})

	opskat.Tool("bad_tool", func(_ *opskat.ToolContext, _ struct{}) (any, error) {
		return nil, nil
	}).HostOnly().PageOnly()
}
