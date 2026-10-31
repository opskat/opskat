package extdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	cagologger "github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extwire"
)

func TestDriverSessionRunsUpToThreeInstances(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	require.Equal(t, []string{"s3cret"}, srv.Logins(), "opening logs in once")

	ctx := context.Background()
	var ops []<-chan error
	var held []*HeldStatement
	for i := 0; i < 3; i++ {
		ops = append(ops, driverOp(listDatabases(ctx, s)))
		held = append(held, srv.WaitHeld(t))
	}
	conns := map[int]bool{}
	for _, h := range held {
		conns[h.Conn] = true
	}
	assert.Len(t, conns, 3, "three operations run at once, each on a connection of its own")
	assert.Len(t, srv.Logins(), 3, "the two extra instances are created on demand, each one login")

	for i, h := range held {
		h.Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
	for i := 0; i < 5; i++ {
		_, err := s.Execute(ctx, "SELECT 1")
		require.NoError(t, err)
	}
	assert.Len(t, srv.Logins(), 3, "later operations reuse the instances already logged in")
}

func TestDriverCursorIsPinnedToOneInstance(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ctx := context.Background()

	stream, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
	require.NoError(t, err)
	require.True(t, stream.Next())
	read := 1

	_, err = s.Execute(ctx, "SELECT 1")
	require.NoError(t, err)
	assert.Len(t, srv.Logins(), 2, "the open cursor keeps its instance: another operation needs one of its own")

	// Bring up the third instance and leave the other two idle, then go on
	// reading: every read must land on the instance holding the cursor.
	a := driverOp(listDatabases(ctx, s))
	ha := srv.WaitHeld(t)
	b := driverOp(listDatabases(ctx, s))
	hb := srv.WaitHeld(t)
	ha.Release()
	hb.Release()
	require.NoError(t, waitOp(t, a, callBound))
	require.NoError(t, waitOp(t, b, callBound))
	require.Len(t, srv.Logins(), 3)

	batch, err := s.ExecuteBatch(ctx, dbdriver.BatchRequest{Statements: []string{"DELETE 1", "DELETE 2", "DELETE 3"}, ContinueOnError: true})
	require.NoError(t, err, "a batch that goes on after errors reports them per statement")
	require.Len(t, batch.Executed, 3)
	batchConns := map[int]bool{}
	for _, st := range srv.SeenOn() {
		if strings.HasPrefix(st.Line, "EXEC DELETE") {
			batchConns[st.Conn] = true
		}
	}
	assert.Len(t, batchConns, 1, "a batch runs from start to end on one instance")

	for stream.Next() {
		read++
		require.Equal(t, fmt.Sprint(read), fmt.Sprint(stream.Row()[0]))
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	assert.Equal(t, 1000, read)
}

func TestDriverFaultFailsOnlyTheOperation(t *testing.T) {
	for _, trigger := range []string{"SELECT PANIC", "SELECT TRAP", "SELECT OOM"} {
		t.Run(trigger, func(t *testing.T) {
			core, logs := observer.New(zap.ErrorLevel)
			prev := cagologger.Default()
			cagologger.SetLogger(zap.New(core))
			defer cagologger.SetLogger(prev)

			p := newDBDriverPlugin(t)
			srv := NewFixtureDBServer(t)
			s := openFixtureSession(t, p, srv)
			ctx := context.Background()

			_, err := s.Execute(ctx, trigger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "extension database driver fault", "the error says the extension's driver failed")
			assert.NotEmpty(t, logs.FilterMessage("extension database driver fault").All(), "the fault is in the app log")

			res, err := s.Execute(ctx, "SELECT 1")
			require.NoError(t, err, "the session stays usable")
			assert.Equal(t, "1", fmt.Sprint(res.Rows.Rows[0][0]))
			assert.Len(t, srv.Logins(), 2, "the failed instance is discarded; the next operation logs in again")
		})
	}
}

// waitQueued waits until n operations wait for an instance of s: the point a test
// can act on knowing they arrived in that order.
func waitQueued(t *testing.T, s dbdriver.Session, n int) {
	t.Helper()
	ds := s.(*driverSession)
	require.Eventually(t, func() bool {
		ds.mu.Lock()
		defer ds.mu.Unlock()
		return len(ds.queue) == n
	}, callBound, time.Millisecond, "waiting for %d queued operation(s)", n)
}

// holdAll starts n held operations and waits until each reaches the database.
func holdAll(t *testing.T, s dbdriver.Session, srv *FixtureDBServer, n int) ([]<-chan error, []*HeldStatement) {
	t.Helper()
	var ops []<-chan error
	var held []*HeldStatement
	for i := 0; i < n; i++ {
		ops = append(ops, driverOp(listDatabases(context.Background(), s)))
		held = append(held, srv.WaitHeld(t))
	}
	return ops, held
}

func TestDriverSessionQueuesInArrivalOrder(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)

	ctx := context.Background()
	statements := []string{"SELECT 1", "SHOW STREAM PROGRESS", "SHOW OPEN CONFIG"}
	queued := make([]<-chan error, 0, len(statements))
	for i, statement := range statements {
		queued = append(queued, driverOp(execute(ctx, s, statement)))
		waitQueued(t, s, i+1)
	}
	assert.Len(t, srv.Logins(), 3, "a fourth operation waits instead of logging in a fourth instance")

	held[0].Release()
	require.NoError(t, waitOp(t, ops[0], callBound))
	for _, op := range queued {
		require.NoError(t, waitOp(t, op, callBound))
	}
	var order []string
	for _, st := range srv.SeenOn() {
		for _, statement := range statements {
			if st.Line == "QUERY "+statement {
				order = append(order, statement)
				assert.Equal(t, held[0].Conn, st.Conn, "the waiting operations run on the instance that was released")
			}
		}
	}
	assert.Equal(t, statements, order, "waiting operations run in arrival order")
	assert.Len(t, srv.Logins(), 3)

	for i := 1; i < 3; i++ {
		held[i].Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
}

func TestDriverCancelingAQueuedOperationOnlyDequeuesIt(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := driverOp(execute(ctx, s, "SHOW OPEN CONFIG"))
	waitQueued(t, s, 1)
	next := driverOp(execute(context.Background(), s, "SELECT 1"))
	waitQueued(t, s, 2)

	cancel()
	require.ErrorIs(t, waitOp(t, canceled, driverInterruptBound), context.Canceled)
	assert.Equal(t, 3, srv.OpenConns(), "no instance was touched")
	for _, op := range ops {
		assertPending(t, op, "a running operation")
	}

	held[0].Release()
	require.NoError(t, waitOp(t, ops[0], callBound))
	require.NoError(t, waitOp(t, next, callBound), "the operation behind it gets the released instance")
	assert.NotContains(t, srv.Seen(), "QUERY SHOW OPEN CONFIG", "the canceled operation never ran")
	assert.Len(t, srv.Logins(), 3)

	for i := 1; i < 3; i++ {
		held[i].Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}
}

func TestDriverIdleExtraInstancesClose(t *testing.T) {
	p := newDBDriverPlugin(t)
	p.idleTimeout = 50 * time.Millisecond
	srv := NewFixtureDBServer(t)
	srv.Hold = "QUERY SHOW DATABASES"
	s := openFixtureSession(t, p, srv)
	ops, held := holdAll(t, s, srv, 3)
	for i, h := range held {
		h.Release()
		require.NoError(t, waitOp(t, ops[i], callBound))
	}

	require.Eventually(t, func() bool { return srv.OpenConns() == 1 }, callBound, 5*time.Millisecond,
		"the extra instances close once idle, and with them their connections")
	assert.Never(t, func() bool { return srv.OpenConns() == 0 }, 10*p.idleTimeout, 5*time.Millisecond,
		"the session keeps its last instance")

	_, err := s.Execute(context.Background(), "SELECT 1")
	require.NoError(t, err)
	assert.Len(t, srv.Logins(), 3, "the instance kept is still logged in")
}

// A row statement writer the host no longer holds is forgotten in the guest too,
// with the next call on its instance: one writer per "copy as SQL" does not grow
// the memory of an instance that lives as long as its session.
func TestDriverRowWriterIsReleasedInTheGuest(t *testing.T) {
	p := newDBDriverPlugin(t)
	srv := NewFixtureDBServer(t)
	s := openFixtureSession(t, p, srv)
	ctx := context.Background()

	inst, id := prepareAndDropWriter(t, s)
	forgotten := func() bool {
		raw, _ := json.Marshal(dbdriver.RowStatementsRequest{})
		err := inst.call(ctx, extwire.DriverCall{Op: extwire.DriverOpRowStatements, Args: extwire.DriverArgs{Writer: id, Request: raw}}, nil)
		return err != nil && strings.Contains(err.Error(), "no database driver row statement writer")
	}
	require.Eventually(t, func() bool {
		runtime.GC()
		if _, err := s.Execute(ctx, "SELECT 1"); err != nil {
			return false
		}
		return forgotten()
	}, callBound, 10*time.Millisecond, "the writer is gone from the guest once the host let go of it")
}

// prepareAndDropWriter prepares a writer on s and lets go of it, returning the
// instance it lives on and its id in the guest.
func prepareAndDropWriter(t *testing.T, s dbdriver.Session) (*driverInstance, uint64) {
	t.Helper()
	w, err := s.PrepareRowStatements(context.Background(), "app", "users")
	require.NoError(t, err)
	dw := w.(*driverRowWriter)
	return dw.inst, dw.id
}
