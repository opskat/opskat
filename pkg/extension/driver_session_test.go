package extension_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// extensionDriver is a dbdriver.Driver whose sessions live in the fixture
// extension: what the host registry will hold for an extension's driver. Its
// reach path leads to server (see extension.FixtureDBServer).
type extensionDriver struct {
	dbdriver.NetworkRules
	desc   dbdriver.Descriptor
	plugin *extension.Plugin
	server *extension.FixtureDBServer
}

func (d *extensionDriver) Descriptor() dbdriver.Descriptor { return d.desc }

func (d *extensionDriver) SessionPerDatabase() bool { return true }

func (d *extensionDriver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	req.Transport.Dial = d.server.Dial
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
			desc:         desc, plugin: ext.Plugin, server: extension.NewFixtureDBServer(t),
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

		assert.Equal(t, []string{"s3cret"}, d.server.Logins(), "one login, with the password the host handed to open")
		assert.Equal(t, []string{"db.internal:7357"}, d.server.Dialed(), "the driver dials the asset's host:port through the reach path")
		assert.Contains(t, d.server.Seen(), "QUERY SELECT 1")
		assert.Contains(t, d.server.Seen(), "QUERY SELECT COUNT(*) FROM [app].[users]")
	})

	t.Run("a whole-table read is a cursor read over several calls", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { assert.NoError(t, s.Close()) }()

		// The cursor holds the session's one instance until it is closed; the
		// progress read then runs on that same instance, the only one logged in.
		first, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		require.True(t, first.Next())
		assert.Equal(t, "1", fmt.Sprint(first.Row()[0]))
		require.NoError(t, first.Close())
		progress, err := s.Execute(ctx, "SHOW STREAM PROGRESS")
		require.NoError(t, err)
		require.Len(t, d.server.Logins(), 1)
		produced, err := strconv.Atoi(fmt.Sprint(progress.Rows.Rows[0][0]))
		require.NoError(t, err)
		assert.True(t, produced >= 1 && produced < 1000,
			"the guest cursor must not have been drained by the first read (produced %d)", produced)

		stream, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		read := 0
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
		d.server.Refuse = "access denied for user alice"
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
		assert.Equal(t, []string{"s3cret"}, d.server.Logins())
	})

	// A database config's ssh_asset_id is the SSH tunnel the host resolves into the
	// session's reach path (opsctl --ssh-asset / AI ssh_asset_id store it there); the
	// tunnel is host-owned and stays out of the config the extension sees.
	t.Run("the config the driver is handed carries no SSH tunnel asset", func(t *testing.T) {
		d := newDriver()
		cfg := &dbdriver.Config{
			Driver: d.desc.ID, Host: "db.internal", Port: d.desc.DefaultPort, Username: "alice", Database: "app",
			SSHAssetID: 42,
		}
		s, err := d.Open(ctx, dbdriver.OpenRequest{Config: cfg, Password: "s3cret"})
		require.NoError(t, err)
		defer func() { assert.NoError(t, s.Close()) }()

		res, err := s.Execute(ctx, "SHOW OPEN CONFIG")
		require.NoError(t, err)
		var seen map[string]any
		require.NoError(t, json.Unmarshal([]byte(res.Rows.Rows[0][0].(string)), &seen))
		assert.Equal(t, "db.internal", seen["host"])
		assert.NotContains(t, seen, "ssh_asset_id")
		assert.Equal(t, int64(42), cfg.SSHAssetID, "the caller's config is left as it was")
	})

	t.Run("every instance of a session is handed the driver options when it logs in, and they are not logged", func(t *testing.T) {
		const secret = "opt-s3cret"
		logs := observeLogs(t)
		d := newDriver()
		s, err := d.Open(ctx, dbdriver.OpenRequest{
			Config:        &dbdriver.Config{Driver: desc.ID, Host: "db.internal", Port: desc.DefaultPort, Username: "alice", Database: "app"},
			Password:      "s3cret",
			DriverOptions: map[string]any{"tenant": "acme", "token": secret},
		})
		require.NoError(t, err)
		defer func() { assert.NoError(t, s.Close()) }()
		want := map[string]any{"tenant": "acme", "token": secret}

		assert.Equal(t, want, openOptions(t, s), "the first instance")

		// A cursor holds the first instance, so the next operation logs in a second one.
		cursor, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		require.True(t, cursor.Next())
		assert.Equal(t, want, openOptions(t, s), "a second instance of the same session")
		require.NoError(t, cursor.Close())
		require.Len(t, d.server.Logins(), 2)

		for _, entry := range logs.All() {
			assert.NotContains(t, fmt.Sprint(entry.Message, entry.ContextMap()), secret)
		}
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
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session is closed")
	})
}

// openOptions asks the fixture's instance that runs the statement which driver
// options its driver was opened with.
func openOptions(t *testing.T, s dbdriver.Session) map[string]any {
	t.Helper()
	res, err := s.Execute(context.Background(), "SHOW OPEN OPTIONS")
	require.NoError(t, err)
	var seen map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.Rows.Rows[0][0].(string)), &seen))
	return seen
}

// observeLogs routes the app logger into memory for the test.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	orig := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(orig) })
	return logs
}
