package extdriver

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// callBound is how long a timed-out or canceled call may take to come back. It
// is far above the deadlines the tests set and far below "never", which is what
// a guest blocked inside a host IO call used to mean.
const callBound = 5 * time.Second

// The database driver fixture (pkg/extension/testdata/fixture-dbdriver) logs in
// to, and announces every statement to, whatever its reach path leads to.
// FixtureDBServer is that far end. It is exported so the external driver tests
// (driver_session_test.go) use the same one.

var (
	dbDriverFixtureOnce     sync.Once
	dbDriverFixtureManifest *extension.Manifest
	dbDriverFixtureWASM     []byte
	dbDriverFixtureErr      error
	// dbDriverCompiled spares each test compiling the module again, which takes
	// tens of seconds under -race.
	dbDriverCompiled = wazero.NewCompilationCache()
)

// dbDriverFixture builds the database driver fixture (extensiontest), once per
// test binary, and returns its manifest and module.
func dbDriverFixture(t *testing.T) (*extension.Manifest, []byte) {
	t.Helper()
	dbDriverFixtureOnce.Do(func() {
		root, err := os.MkdirTemp("", "opskat-fixture-dbdriver-*")
		if err != nil {
			dbDriverFixtureErr = err
			return
		}
		defer func() { _ = os.RemoveAll(root) }()
		dir := extensiontest.InstallDBDriverFixture(t, root)
		data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // path built above
		if err != nil {
			dbDriverFixtureErr = err
			return
		}
		if dbDriverFixtureManifest, err = extension.ParseManifest(data); err != nil {
			dbDriverFixtureErr = err
			return
		}
		dbDriverFixtureWASM, dbDriverFixtureErr = os.ReadFile(filepath.Join(dir, "main.wasm")) //nolint:gosec // path built above
	})
	if dbDriverFixtureErr != nil {
		t.Fatal(dbDriverFixtureErr)
	}
	return dbDriverFixtureManifest, dbDriverFixtureWASM
}

// newDBDriverPlugin loads the database driver fixture the way Manager.LoadExtension
// does and returns the drivers it provides.
func newDBDriverPlugin(t *testing.T, opts ...extension.PluginOption) *Drivers {
	t.Helper()
	manifest, wasm := dbDriverFixture(t)
	host := extension.NewCapabilityHost(extension.NewDefaultHostProvider(extension.DefaultHostConfig{}), manifest, t.TempDir())
	p, err := extension.LoadPlugin(context.Background(), manifest, wasm, host, dbDriverCompiled, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, p.Close(context.Background())) })
	return New(p)
}

// openFixtureSession opens a fixturedb session whose reach path leads to srv.
func openFixtureSession(t *testing.T, p *Drivers, srv *FixtureDBServer) dbdriver.Session {
	t.Helper()
	s, err := p.OpenDriverSession(context.Background(), "fixturedb", dbdriver.OpenRequest{
		Config:    &dbdriver.Config{Driver: "fixturedb", Host: "db.internal", Port: 7357, Username: "alice", Database: "app"},
		Password:  "s3cret",
		Transport: dbdriver.Transport{Dial: srv.Dial},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// FixtureStatement is one line the fixture sent after logging in, and the
// connection (numbered from 1 in dial order) it came on.
type FixtureStatement struct {
	Conn int
	Line string
}

// HeldStatement is a statement whose answer the server holds back until the test
// releases it, keeping the driver call blocked in a host read.
type HeldStatement struct {
	FixtureStatement
	release chan struct{}
	once    sync.Once
}

// Release lets the server answer the statement.
func (h *HeldStatement) Release() { h.once.Do(func() { close(h.release) }) }

// FixtureDBServer answers the fixture driver's logins and statement announcements
// and records them.
type FixtureDBServer struct {
	// Refuse, when set, is the error every login gets.
	Refuse string
	// Hold, when set, is a line (e.g. "QUERY SHOW DATABASES") whose answer is
	// held: each one is handed to the test (WaitHeld) and answered once released.
	Hold string

	mu         sync.Mutex
	addrs      []string
	passwords  []string
	statements []FixtureStatement
	conns      int
	open       int
	held       chan *HeldStatement
	done       chan struct{}
}

// NewFixtureDBServer starts a server; whatever it still holds is dropped when the
// test ends.
func NewFixtureDBServer(t testing.TB) *FixtureDBServer {
	s := &FixtureDBServer{held: make(chan *HeldStatement, 16), done: make(chan struct{})}
	t.Cleanup(func() { close(s.done) })
	return s
}

// Dial is the reach path's dial.
func (s *FixtureDBServer) Dial(_ context.Context, _, addr string) (net.Conn, error) {
	s.mu.Lock()
	s.addrs = append(s.addrs, addr)
	s.conns++
	s.open++
	id := s.conns
	s.mu.Unlock()
	client, server := net.Pipe()
	go s.serve(id, server)
	return client, nil
}

func (s *FixtureDBServer) serve(id int, c net.Conn) {
	defer func() {
		_ = c.Close()
		s.mu.Lock()
		s.open--
		s.mu.Unlock()
	}()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		reply := "OK\n"
		s.mu.Lock()
		if login, ok := strings.CutPrefix(line, "LOGIN "); ok {
			if s.Refuse != "" {
				reply = "ERR " + s.Refuse + "\n"
			} else {
				_, password, _ := strings.Cut(login, " ")
				s.passwords = append(s.passwords, password)
			}
		} else {
			s.statements = append(s.statements, FixtureStatement{Conn: id, Line: line})
		}
		hold := s.Hold != "" && line == s.Hold
		s.mu.Unlock()
		if hold {
			h := &HeldStatement{FixtureStatement: FixtureStatement{Conn: id, Line: line}, release: make(chan struct{})}
			s.held <- h
			select {
			case <-h.release:
			case <-s.done:
				return
			}
		}
		if _, err := c.Write([]byte(reply)); err != nil {
			return
		}
	}
}

// WaitHeld returns the next held statement.
func (s *FixtureDBServer) WaitHeld(t testing.TB) *HeldStatement {
	t.Helper()
	select {
	case h := <-s.held:
		return h
	case <-time.After(callBound):
		t.Fatalf("no held statement reached the database within %s", callBound)
		return nil
	}
}

// Logins are the passwords of the successful logins, in order.
func (s *FixtureDBServer) Logins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwords...)
}

// Dialed are the addresses dialed, in order.
func (s *FixtureDBServer) Dialed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.addrs...)
}

// Seen are the statement lines received, in order.
func (s *FixtureDBServer) Seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := make([]string, len(s.statements))
	for i, st := range s.statements {
		lines[i] = st.Line
	}
	return lines
}

// SeenOn are the statements received, with the connection each came on.
func (s *FixtureDBServer) SeenOn() []FixtureStatement {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]FixtureStatement(nil), s.statements...)
}

// OpenConns is how many connections are open. One whose statement is held counts
// until the test ends: the server is not reading it.
func (s *FixtureDBServer) OpenConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// driverOp runs op in the background; the channel yields its error.
func driverOp(op func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- op() }()
	return done
}

// waitOp waits for an operation to come back, within bound.
func waitOp(t *testing.T, done <-chan error, bound time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(bound):
		t.Fatalf("the driver call did not come back within %s", bound)
		return nil
	}
}

// assertPending checks an operation has not come back.
func assertPending(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s came back (err=%v) while it should still be running", what, err)
	default:
	}
}

func listDatabases(ctx context.Context, s dbdriver.Session) func() error {
	return func() error {
		_, err := s.ListDatabases(ctx)
		return err
	}
}

func execute(ctx context.Context, s dbdriver.Session, statement string) func() error {
	return func() error {
		_, err := s.Execute(ctx, statement)
		return err
	}
}
