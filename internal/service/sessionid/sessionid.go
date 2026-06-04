// Package sessionid mints terminal session IDs with a random instance segment.
//
// Session IDs are persisted by the frontend (they become tab IDs in the tab
// store), so an ID must stay unique across app restarts — not just within a
// single Manager's lifetime. A plain incrementing counter resets to 0 on every
// launch, which makes a fresh process re-mint IDs an earlier run already
// persisted, producing two tabs that share one ID (issue #141).
//
// Each Generator carries a random per-instance segment, so IDs minted by
// different processes (or different Managers) are extremely unlikely to collide
// even when their counters match. The ID format is "<kind>-<instance>-<n>"; the
// kind prefix is preserved because the frontend infers the transport from it
// (inferTransportFromSessionId keys off "ssh-" / "serial-" / "local-").
package sessionid

import (
	"fmt"
	"math/rand/v2"
	"sync/atomic"
)

// Generator produces unique session IDs of the form "<kind>-<instance>-<n>".
// It is safe for concurrent use. Must be used by pointer (atomic counter).
type Generator struct {
	kind     string
	instance uint64
	counter  atomic.Int64
}

// NewGenerator returns a Generator whose IDs are prefixed with kind (e.g.
// "ssh"). The random instance segment is drawn once per Generator, giving each
// generator an ID namespace with negligible collision risk.
func NewGenerator(kind string) *Generator {
	return &Generator{kind: kind, instance: rand.Uint64()}
}

// Next returns the next unique session ID.
func (g *Generator) Next() string {
	return fmt.Sprintf("%s-%x-%d", g.kind, g.instance, g.counter.Add(1))
}
