package extension_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// fixtureServer is the far end of the fixture driver's reach path: it answers the
// logins and statement announcements memdb sends (see testdata/fixture-dbdriver),
// and records them.
type fixtureServer struct {
	mu         sync.Mutex
	addrs      []string
	passwords  []string
	statements []string
	// refuse, when set, is the error every login gets.
	refuse string
}

func (s *fixtureServer) dial(_ context.Context, _, addr string) (net.Conn, error) {
	s.mu.Lock()
	s.addrs = append(s.addrs, addr)
	s.mu.Unlock()
	client, server := net.Pipe()
	go s.serve(server)
	return client, nil
}

func (s *fixtureServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
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
			if s.refuse != "" {
				reply = "ERR " + s.refuse + "\n"
			} else {
				_, password, _ := strings.Cut(login, " ")
				s.passwords = append(s.passwords, password)
			}
		} else {
			s.statements = append(s.statements, line)
		}
		s.mu.Unlock()
		if _, err := c.Write([]byte(reply)); err != nil {
			return
		}
	}
}

func (s *fixtureServer) logins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.passwords...)
}

func (s *fixtureServer) dialed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.addrs...)
}

func (s *fixtureServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.statements...)
}

// extensionDriver is a dbdriver.Driver whose sessions live in the fixture
// extension: what the host registry will hold for an extension's driver.
type extensionDriver struct {
	dbdriver.NetworkRules
	desc   dbdriver.Descriptor
	plugin *extension.Plugin
	server *fixtureServer
}

func (d *extensionDriver) Descriptor() dbdriver.Descriptor { return d.desc }

func (d *extensionDriver) SessionPerDatabase() bool { return true }

func (d *extensionDriver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	req.Transport.Dial = d.server.dial
	return d.plugin.OpenDriverSession(ctx, string(d.desc.ID), req)
}

func (d *extensionDriver) open(t *testing.T) dbdriver.Session {
	t.Helper()
	s, err := d.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{
			Driver: d.desc.ID, Host: "db.internal", Port: d.desc.DefaultPort, Username: "alice", Database: "app",
		},
		Password: "s3cret",
	})
	require.NoError(t, err)
	return s
}

func TestExtensionDatabaseDriverSessions(t *testing.T) {
	ext := extensiontest.LoadDBDriverFixture(t)
	require.Len(t, ext.Manifest.DatabaseDrivers, 1)
	desc := ext.Manifest.DatabaseDrivers[0]
	require.Equal(t, dbdriver.DriverID(extensiontest.DBDriverFixtureDriver), desc.ID)
	newDriver := func() *extensionDriver {
		return &extensionDriver{
			NetworkRules: dbdriver.NetworkRules{DefaultPort: desc.DefaultPort},
			desc:         desc, plugin: ext.Plugin, server: &fixtureServer{},
		}
	}
	ctx := context.Background()

	t.Run("the forwarded session meets the driver contract", func(t *testing.T) {
		d := newDriver()
		dbdrivertest.Register(t, d)
		sessions := func(t *testing.T) dbdriver.Session { return d.open(t) }
		dbdrivertest.RunConformance(t, d, sessions)
		dbdrivertest.RunBrowseConformance(t, d, sessions)
		dbdrivertest.RunTableConformance(t, d, sessions, "app", "users")
	})

	t.Run("a session logs in once through the reach path and keeps its connection across calls", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { assert.NoError(t, s.Close()) }()

		_, err := s.Execute(ctx, "SELECT 1")
		require.NoError(t, err)
		_, err = s.ListTables(ctx, "app")
		require.NoError(t, err)
		_, err = s.OpenTable(ctx, "app", "users", 10)
		require.NoError(t, err)
		_, err = s.TableRows(ctx, "app", "users", dbdriver.TableQuery{}, dbdriver.Page{Size: 2})
		require.NoError(t, err)
		_, err = s.CountTableRows(ctx, "app", "users", nil)
		require.NoError(t, err)

		assert.Equal(t, []string{"s3cret"}, d.server.logins(), "one login, with the password the host handed to open")
		assert.Equal(t, []string{"db.internal:7357"}, d.server.dialed(), "the driver dials the asset's host:port through the reach path")
		assert.Contains(t, d.server.seen(), "QUERY SELECT 1")
		assert.Contains(t, d.server.seen(), "QUERY SELECT COUNT(*) FROM [app].[users]")
	})

	t.Run("a whole-table read is a cursor read over several calls", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { assert.NoError(t, s.Close()) }()

		stream, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		require.True(t, stream.Next())
		assert.Equal(t, "1", fmt.Sprint(stream.Row()[0]))

		progress, err := s.Execute(ctx, "SHOW STREAM PROGRESS")
		require.NoError(t, err)
		produced := fmt.Sprint(progress.Rows.Rows[0][0])
		assert.NotEqual(t, "1000", produced, "the guest cursor must not have been drained by the first read")

		read := 1
		for stream.Next() {
			read++
			require.Equal(t, fmt.Sprint(read), fmt.Sprint(stream.Row()[0]), "rows arrive in order, numbers exactly")
		}
		require.NoError(t, stream.Err())
		require.NoError(t, stream.Close())
		assert.Equal(t, 1000, read)
	})

	t.Run("an open that cannot log in fails with the driver's error", func(t *testing.T) {
		d := newDriver()
		d.server.refuse = "access denied for user alice"
		_, err := d.Open(ctx, dbdriver.OpenRequest{
			Config:   &dbdriver.Config{Driver: desc.ID, Host: "db.internal", Port: 7357, Username: "alice"},
			Password: "wrong",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied for user alice")
	})

	t.Run("the driver is handed the connection config, and the password only to open", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { assert.NoError(t, s.Close()) }()

		res, err := s.Execute(ctx, "SHOW OPEN CONFIG")
		require.NoError(t, err)
		var seen map[string]any
		require.NoError(t, json.Unmarshal([]byte(res.Rows.Rows[0][0].(string)), &seen))
		assert.Equal(t, "db.internal", seen["host"])
		assert.Equal(t, "app", seen["database"])
		assert.Equal(t, "alice", seen["username"])
		assert.NotContains(t, seen, "password")
		assert.Equal(t, []string{"s3cret"}, d.server.logins())
	})

	t.Run("a driver the extension does not provide is refused", func(t *testing.T) {
		_, err := ext.Plugin.OpenDriverSession(ctx, "otherdb", dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "otherdb"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "otherdb")
	})

	t.Run("a closed session refuses further work", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		require.NoError(t, s.Close())
		_, err := s.Execute(ctx, "SELECT 1")
		assert.Error(t, err)
	})
}
