package extdriver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
)

// driverInterruptBound is how long a canceled or timed-out database driver call
// may take to fail. The spec says "about a second"; the margin absorbs a loaded
// machine.
const driverInterruptBound = 3 * time.Second

// A database driver call blocked in a host read — the database not answering — is
// ended by the host closing that instance's connection: the extension's own timers
// do not fire while it is blocked.
func TestDriverCallCancelAndTimeout(t *testing.T) {
	t.Run("a canceled call fails promptly and only its instance is discarded", func(t *testing.T) {
		p := newDBDriverPlugin(t)
		srv := NewFixtureDBServer(t)
		srv.Hold = "QUERY SHOW DATABASES"
		s := openFixtureSession(t, p, srv)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		canceled := driverOp(listDatabases(ctx, s))
		hc := srv.WaitHeld(t)
		other := driverOp(listDatabases(context.Background(), s))
		ho := srv.WaitHeld(t)
		require.NotEqual(t, hc.Conn, ho.Conn)

		cancel()
		err := waitOp(t, canceled, driverInterruptBound)
		require.ErrorIs(t, err, context.Canceled)
		assertPending(t, other, "the other instance's operation")

		_, err = s.Execute(context.Background(), "SELECT 1")
		require.NoError(t, err, "the session stays usable")
		assert.Len(t, srv.Logins(), 3, "the canceled instance is gone: the next operation logs in again")

		ho.Release()
		require.NoError(t, waitOp(t, other, callBound), "the other instance's operation is not affected")
	})

	t.Run("a waiting operation goes on when a running one is canceled", func(t *testing.T) {
		p := newDBDriverPlugin(t)
		srv := NewFixtureDBServer(t)
		srv.Hold = "QUERY SHOW DATABASES"
		s := openFixtureSession(t, p, srv)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		canceled := driverOp(listDatabases(ctx, s))
		srv.WaitHeld(t)
		ops, held := holdAll(t, s, srv, 2)
		waiting := driverOp(execute(context.Background(), s, "SELECT 1"))
		waitQueued(t, s, 1)

		cancel()
		require.ErrorIs(t, waitOp(t, canceled, driverInterruptBound), context.Canceled)
		require.NoError(t, waitOp(t, waiting, callBound), "the waiting operation logs in an instance in place of the discarded one")
		assert.Len(t, srv.Logins(), 4)
		for i, h := range held {
			assertPending(t, ops[i], "an operation on another instance")
			h.Release()
			require.NoError(t, waitOp(t, ops[i], callBound))
		}
	})

	t.Run("a call past its deadline fails as a timeout", func(t *testing.T) {
		p := newDBDriverPlugin(t)
		srv := NewFixtureDBServer(t)
		srv.Hold = "QUERY SHOW DATABASES"
		s := openFixtureSession(t, p, srv)

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		err := waitOp(t, driverOp(listDatabases(ctx, s)), 300*time.Millisecond+driverInterruptBound)
		require.ErrorIs(t, err, context.DeadlineExceeded)

		_, err = s.Execute(context.Background(), "SELECT 1")
		require.NoError(t, err)
		assert.Len(t, srv.Logins(), 2, "the timed-out instance is discarded; the next operation logs in again")
	})

	t.Run("the tool timeout does not bound a driver call", func(t *testing.T) {
		const toolTimeout = 100 * time.Millisecond
		p := newDBDriverPlugin(t, extension.WithToolTimeout(toolTimeout))
		srv := NewFixtureDBServer(t)
		srv.Hold = "QUERY SHOW DATABASES"
		s := openFixtureSession(t, p, srv)

		start := time.Now()
		op := driverOp(listDatabases(context.Background(), s))
		h := srv.WaitHeld(t)
		release := time.AfterFunc(4*toolTimeout, h.Release)
		defer release.Stop()
		require.NoError(t, waitOp(t, op, callBound))
		assert.GreaterOrEqual(t, time.Since(start), 4*toolTimeout)
	})
}

// Closing a session closes its sessions in the guest, and a guest's close may talk
// to the database (a logoff). A database that never answers must not hold the
// close forever: closing runs when an extension is disabled, upgraded or removed,
// and when an idle instance is reaped.
func TestDriverSessionCloseIsBounded(t *testing.T) {
	p := newDBDriverPlugin(t)
	p.closeTimeout = 100 * time.Millisecond
	srv := NewFixtureDBServer(t)
	srv.Hold = "LOGOFF"
	s, err := p.OpenDriverSession(context.Background(), "fixturedb", dbdriver.OpenRequest{
		Config:        &dbdriver.Config{Driver: "fixturedb", Host: "db.internal", Port: 7357, Username: "alice", Database: "app"},
		Password:      "s3cret",
		DriverOptions: map[string]any{"logoff": true},
		Transport:     dbdriver.Transport{Dial: srv.Dial},
	})
	require.NoError(t, err)

	closed := driverOp(s.Close)
	srv.WaitHeld(t)
	require.ErrorIs(t, waitOp(t, closed, p.closeTimeout+driverInterruptBound), context.DeadlineExceeded,
		"the close fails as a timeout instead of waiting for the database")
}

// A session's idle instances close side by side: each close is bounded, but one
// after another a database that never answers would hold the close for that bound
// once per instance.
func TestDriverSessionClosesItsInstancesSideBySide(t *testing.T) {
	p := newDBDriverPlugin(t)
	p.closeTimeout = time.Minute
	srv := NewFixtureDBServer(t)
	srv.Hold = "LOGOFF"
	ctx := context.Background()
	s, err := p.OpenDriverSession(ctx, "fixturedb", dbdriver.OpenRequest{
		Config:        &dbdriver.Config{Driver: "fixturedb", Host: "db.internal", Port: 7357, Username: "alice", Database: "app"},
		Password:      "s3cret",
		DriverOptions: map[string]any{"logoff": true},
		Transport:     dbdriver.Transport{Dial: srv.Dial},
	})
	require.NoError(t, err)
	// A cursor holds the first instance, so the query logs in a second; both are
	// then idle.
	rows, err := s.StreamTableRows(ctx, "app", "users", dbdriver.TableQuery{})
	require.NoError(t, err)
	_, err = s.Execute(ctx, "SELECT 1")
	require.NoError(t, err)
	require.NoError(t, rows.Close())
	require.Len(t, srv.Logins(), 2)

	closed := driverOp(s.Close)
	first := srv.WaitHeld(t)
	second := srv.WaitHeld(t) // fails the test if it waits for the first close
	first.Release()
	second.Release()
	require.NoError(t, waitOp(t, closed, callBound))
}
