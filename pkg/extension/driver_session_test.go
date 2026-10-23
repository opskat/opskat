package extension_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
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

	t.Run("every instance of a session is handed the TLS material when it logs in, and it is not logged", func(t *testing.T) {
		material := dbdriver.TLSMaterial{
			Insecure: true, ServerName: "db.example.com",
			CA: []byte("ca-pem"), Cert: []byte("cert-pem"), Key: []byte("key-pem-s3cret"),
		}
		logs := observeLogs(t)
		d := newDriver()
		s, err := d.Open(ctx, dbdriver.OpenRequest{
			Config:   &dbdriver.Config{Driver: desc.ID, Host: "db.internal", Port: desc.DefaultPort, Username: "alice", Database: "app"},
			Password: "s3cret",
			TLS:      &material,
		})
		require.NoError(t, err)
		defer func() { assert.NoError(t, s.Close()) }()

		assert.Equal(t, &material, openTLS(t, s), "the first instance")

		// A cursor holds the first instance, so the next operation logs in a second one.
		cursor, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		require.True(t, cursor.Next())
		assert.Equal(t, &material, openTLS(t, s), "a second instance of the same session")
		require.NoError(t, cursor.Close())
		require.Len(t, d.server.Logins(), 2)

		for _, entry := range logs.All() {
			line := fmt.Sprint(entry.Message, entry.ContextMap())
			assert.NotContains(t, line, "key-pem-s3cret")
			assert.NotContains(t, line, "cert-pem")
		}
	})

	t.Run("a session opened without TLS material hands the driver none", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { assert.NoError(t, s.Close()) }()
		assert.Nil(t, openTLS(t, s))
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

// openTLS asks the fixture's instance that runs the statement which TLS material
// its driver was opened with (nil for none).
func openTLS(t *testing.T, s dbdriver.Session) *dbdriver.TLSMaterial {
	t.Helper()
	res, err := s.Execute(context.Background(), "SHOW OPEN TLS")
	require.NoError(t, err)
	var seen *dbdriver.TLSMaterial
	require.NoError(t, json.Unmarshal([]byte(res.Rows.Rows[0][0].(string)), &seen))
	return seen
}

// The certificate field group is a host ABI 2.4 capability: the fixture driver
// declares it, so the same extension on hostABI 2.3 is refused at load, naming
// the driver and the reason.
func TestExtensionDriverCertificateGroupNeedsHostABI24(t *testing.T) {
	root := t.TempDir()
	dir := extensiontest.InstallDBDriverFixture(t, root)
	path := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(path) //nolint:gosec // this test's own temp dir
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["hostABI"] = "2.3"
	delete(m["capabilities"].(map[string]any), "databaseKerberos")
	raw, err = json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })
	_, err = mgr.LoadExtension(context.Background(), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), extensiontest.DBDriverFixtureDriver)
	assert.Contains(t, err.Error(), string(dbdriver.FieldTLSCert))
	assert.Contains(t, err.Error(), extension.DatabaseExtrasMinHostABI)
}

// fakeTickets is a Kerberos ticket source as the caller of Open supplies it: it
// answers "ticket:<spn>" for every SPN, or fail, and records the SPNs asked.
type fakeTickets struct {
	mu    sync.Mutex
	asked []string
	fail  error
}

func (f *fakeTickets) ticket(_ context.Context, spn string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, spn)
	if f.fail != nil {
		return nil, f.fail
	}
	return []byte("ticket:" + spn), nil
}

func (f *fakeTickets) spns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

// krbLogin is the login line's password field of a fixture login with ticket.
func krbLogin(ticket string) string { return "krb:" + hex.EncodeToString([]byte(ticket)) }

// openAuth asks the fixture's instance what it was told about authentication
// when it logged in: whether the asset uses Kerberos, and the password.
func openAuth(t *testing.T, s dbdriver.Session) (kerberos bool, password string) {
	t.Helper()
	res, err := s.Execute(context.Background(), "SHOW OPEN AUTH")
	require.NoError(t, err)
	var seen struct {
		Kerberos bool   `json:"kerberos"`
		Password string `json:"password"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Rows.Rows[0][0].(string)), &seen))
	return seen.Kerberos, seen.Password
}

// askTicket runs the fixture's ticket probe inside a driver call: the guest asks
// the host for the ticket of service on host, as during a login handshake. It
// answers the ticket, or the error the guest got.
func askTicket(t *testing.T, s dbdriver.Session, service, host string) (ticket, errMsg string) {
	t.Helper()
	res, err := s.Execute(context.Background(), "SHOW KERBEROS TICKET "+service+" "+host)
	require.NoError(t, err)
	raw, err := hex.DecodeString(res.Rows.Rows[0][0].(string))
	require.NoError(t, err)
	return string(raw), res.Rows.Rows[0][1].(string)
}

// ticketLogs counts, by SPN, the Info entries that record a session's first
// ticket for it, checking each names the session and carries no secret.
func ticketLogs(t *testing.T, logs *observer.ObservedLogs, secrets ...string) map[string]int {
	t.Helper()
	seen := map[string]int{}
	for _, e := range logs.All() {
		fields := e.ContextMap()
		line := fmt.Sprint(e.Message, fields)
		for _, secret := range secrets {
			assert.NotContains(t, line, secret)
		}
		if e.Level != zap.InfoLevel || fields["spn"] == nil {
			continue
		}
		assert.Equal(t, "fixture-dbdriver", fields["extension"])
		assert.Equal(t, extensiontest.DBDriverFixtureDriver, fields["driver"])
		assert.EqualValues(t, 42, fields["assetID"])
		assert.Equal(t, "alice@EXAMPLE.COM", fields["principal"])
		seen[fields["spn"].(string)]++
	}
	return seen
}

// A driver session of a Kerberos asset gets its tickets from the host: the
// fixture driver asks for one in its login handshake (service "fixturedb" on the
// asset's host) and logs in with it, and its ticket probe asks again from inside
// a later call.
func TestExtensionDriverKerberosTickets(t *testing.T) {
	ext := extensiontest.LoadDBDriverFixture(t)
	desc := ext.Manifest.DatabaseDrivers[0]
	require.True(t, desc.UsesField(dbdriver.FieldKerberos), "the fixture declares the Kerberos field group")
	ctx := context.Background()
	open := func(t *testing.T, server *extension.FixtureDBServer, krb *dbdriver.Kerberos) (dbdriver.Session, error) {
		t.Helper()
		s, err := ext.Plugin.OpenDriverSession(ctx, string(desc.ID), dbdriver.OpenRequest{
			Config:    &dbdriver.Config{Driver: desc.ID, Host: "db.internal", Port: desc.DefaultPort, Username: "alice", Database: "app"},
			Password:  "s3cret",
			Transport: dbdriver.Transport{Dial: server.Dial},
			AssetID:   42,
			Kerberos:  krb,
		})
		if err == nil {
			t.Cleanup(func() { assert.NoError(t, s.Close()) })
		}
		return s, err
	}

	t.Run("the driver logs in with the ticket for the SPN its server named, learning only that the asset uses Kerberos", func(t *testing.T) {
		server, tickets := extension.NewFixtureDBServer(t), &fakeTickets{}
		s, err := open(t, server, &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", Ticket: tickets.ticket})
		require.NoError(t, err)

		assert.Equal(t, []string{"fixturedb/db.internal"}, tickets.spns(), "the SPN is service/host as the handshake named them")
		assert.Equal(t, []string{krbLogin("ticket:fixturedb/db.internal")}, server.Logins(), "the AP-REQ bytes reach the database as they were")
		kerberos, password := openAuth(t, s)
		assert.True(t, kerberos)
		assert.Empty(t, password, "a Kerberos asset's password is not handed to the extension")
		res, err := s.Execute(ctx, "SHOW OPEN CONFIG")
		require.NoError(t, err)
		assert.Contains(t, res.Rows.Rows[0][0], `"username":"alice"`, "the username still is")
	})

	t.Run("an asset without Kerberos hands the driver no Kerberos, and its ticket calls are refused saying why", func(t *testing.T) {
		server := extension.NewFixtureDBServer(t)
		s, err := open(t, server, nil)
		require.NoError(t, err)
		kerberos, password := openAuth(t, s)
		assert.False(t, kerberos)
		assert.Equal(t, "s3cret", password)

		ticket, msg := askTicket(t, s, "fixturedb", "db.internal")
		assert.Empty(t, ticket)
		assert.Contains(t, msg, "does not log in with Kerberos")
	})

	t.Run("a pinned SPN refuses any other, naming both, without asking the source", func(t *testing.T) {
		server, tickets := extension.NewFixtureDBServer(t), &fakeTickets{}
		s, err := open(t, server, &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", SPN: "fixturedb/db.internal", Ticket: tickets.ticket})
		require.NoError(t, err)

		ticket, msg := askTicket(t, s, "oracle", "db2.internal")
		assert.Empty(t, ticket)
		assert.Contains(t, msg, "oracle/db2.internal")
		assert.Contains(t, msg, "fixturedb/db.internal")
		assert.Equal(t, []string{"fixturedb/db.internal"}, tickets.spns(), "only the pinned SPN was fetched, at login")

		ticket, msg = askTicket(t, s, "fixturedb", "db.internal")
		assert.Empty(t, msg)
		assert.Equal(t, "ticket:fixturedb/db.internal", ticket)
	})

	t.Run("names that would spell another principal or realm are refused", func(t *testing.T) {
		server, tickets := extension.NewFixtureDBServer(t), &fakeTickets{}
		s, err := open(t, server, &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", Ticket: tickets.ticket})
		require.NoError(t, err)
		for _, bad := range [][2]string{{"fixturedb", "db.internal@EVIL.REALM"}, {"fixturedb/x", "db.internal"}} {
			ticket, msg := askTicket(t, s, bad[0], bad[1])
			assert.Empty(t, ticket, bad)
			assert.Contains(t, msg, "Kerberos", bad)
		}
		assert.Equal(t, []string{"fixturedb/db.internal"}, tickets.spns())
	})

	t.Run("each session logs its first ticket for each SPN once, with no secret in any log", func(t *testing.T) {
		logs := observeLogs(t)
		server, tickets := extension.NewFixtureDBServer(t), &fakeTickets{}
		s, err := open(t, server, &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", Ticket: tickets.ticket})
		require.NoError(t, err)

		for _, name := range [][2]string{{"fixturedb", "db.internal"}, {"fixturedb", "db.internal"}, {"other", "node2.internal"}} {
			_, msg := askTicket(t, s, name[0], name[1])
			require.Empty(t, msg)
		}
		// A cursor holds the first instance, so the next operation logs in a second
		// one, asking for the login ticket again.
		cursor, err := s.StreamTableRows(ctx, "app", "series", dbdriver.TableQuery{})
		require.NoError(t, err)
		require.True(t, cursor.Next())
		_, msg := askTicket(t, s, "fixturedb", "db.internal")
		require.Empty(t, msg)
		require.NoError(t, cursor.Close())
		require.Len(t, server.Logins(), 2)

		secrets := []string{"s3cret", "ticket:", hex.EncodeToString([]byte("ticket:"))}
		assert.Equal(t, map[string]int{"fixturedb/db.internal": 1, "other/node2.internal": 1}, ticketLogs(t, logs, secrets...))

		_, err = open(t, extension.NewFixtureDBServer(t), &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", Ticket: tickets.ticket})
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"fixturedb/db.internal": 2, "other/node2.internal": 1}, ticketLogs(t, logs, secrets...),
			"another session logs its own first ticket")
	})

	t.Run("a source that cannot issue fails the login with its reason, never falling back to the password", func(t *testing.T) {
		server := extension.NewFixtureDBServer(t)
		tickets := &fakeTickets{fail: errors.New("KDC refused: KDC_ERR_PREAUTH_FAILED (wrong password or keytab)")}
		_, err := open(t, server, &dbdriver.Kerberos{Principal: "alice@EXAMPLE.COM", Ticket: tickets.ticket})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "KDC_ERR_PREAUTH_FAILED")
		assert.Empty(t, server.Logins(), "no login was attempted")
	})
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
