package extdriver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/extreg/extdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// Running SQL files and dumping them through host ABI 1.1, against the fixture
// driver (pkg/extension/testdata/fixture-dbdriver) built for it: the host reads
// the file, orders the run, reports progress and stops; the guest answers the
// hooks — splitting, a pinned connection executing batches, the dump dialect.

// fixtureDrivers loads a build of the database driver fixture and returns a
// driver factory over it: each driver has a far end of its own.
func fixtureDrivers(t *testing.T, ext *extension.Extension) func() *extensionDriver {
	t.Helper()
	drivers := extensiontest.DatabaseDrivers(t, ext.Manifest)
	require.Len(t, drivers, 1)
	desc := drivers[0]
	plugin := extdriver.New(ext.Plugin)
	return func() *extensionDriver {
		return &extensionDriver{
			NetworkRules: dbdriver.NetworkRules{DefaultPort: desc.DefaultPort},
			desc:         desc, plugin: plugin, server: extdriver.NewFixtureDBServer(t),
		}
	}
}

// scriptOf is a request to run text.
func scriptOf(text string) dbdriver.ScriptRequest {
	return dbdriver.ScriptRequest{Database: "app", Script: strings.NewReader(text), Size: int64(len(text))}
}

// inserts is a file of a SET and then n-1 INSERTs, one statement per line.
func inserts(n int) string {
	var b strings.Builder
	b.WriteString("SET MODE strict;\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "INSERT INTO [t] VALUES (%d);\n", i)
	}
	return b.String()
}

// execsFrom are the statements the far end was sent for execution from statement
// index from on.
func execsFrom(srv *extdriver.FixtureDBServer, from int) []extdriver.FixtureStatement {
	var execs []extdriver.FixtureStatement
	for _, st := range srv.SeenOn()[from:] {
		if strings.HasPrefix(st.Line, "EXEC ") {
			execs = append(execs, st)
		}
	}
	return execs
}

func lines(statements []extdriver.FixtureStatement) []string {
	out := make([]string, len(statements))
	for i, st := range statements {
		out[i] = st.Line
	}
	return out
}

func assertOneConnection(t *testing.T, statements []extdriver.FixtureStatement) {
	t.Helper()
	require.NotEmpty(t, statements)
	for _, st := range statements {
		require.Equal(t, statements[0].Conn, st.Conn, "statement %q ran on another connection", st.Line)
	}
}

func TestExtensionDriverRunsSQLFiles(t *testing.T) {
	ext := extensiontest.LoadDBDriverFixture(t)
	assert.Equal(t, "1.1", ext.Manifest.HostABI)
	newDriver := fixtureDrivers(t, ext)
	ctx := context.Background()

	t.Run("statements run in file order on one pinned connection, a batch per call", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())
		var progress []dbdriver.ScriptProgress
		req := scriptOf(inserts(450))
		req.Progress = func(p dbdriver.ScriptProgress) { progress = append(progress, p) }

		result, err := s.RunScript(ctx, req)
		require.NoError(t, err)
		assert.EqualValues(t, 450, result.Succeeded)
		assert.Zero(t, result.Failed)
		assert.False(t, result.Stopped)

		execs := execsFrom(d.server, before)
		require.Len(t, execs, 450)
		assert.Equal(t, "EXEC SET MODE strict", execs[0].Line)
		assert.Equal(t, "EXEC INSERT INTO [t] VALUES (449)", execs[449].Line)
		assertOneConnection(t, execs)
		// Progress is reported once per batch, which is one call into the guest.
		require.GreaterOrEqual(t, len(progress), 2, "450 statements take more than one batch")
		assert.Less(t, len(progress), 450)
		assert.Greater(t, progress[0].Succeeded, int64(1), "a call carries a batch of statements, not one")
	})

	t.Run("a failing statement stops the run there", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())

		result, err := s.RunScript(ctx, scriptOf("SET A;\nNOT A STATEMENT;\nSET B;\n"))
		require.NoError(t, err)
		require.NotNil(t, result.Failure)
		assert.EqualValues(t, 2, result.Failure.Number)
		assert.Equal(t, 2, result.Failure.Line)
		assert.Contains(t, result.Failure.Error, "syntax error")
		assert.Equal(t, "NOT A STATEMENT", result.Failure.Excerpt)
		assert.EqualValues(t, 1, result.Succeeded)
		assert.EqualValues(t, 1, result.Failed)
		assert.Equal(t, []string{"EXEC SET A", "EXEC NOT A STATEMENT"}, lines(execsFrom(d.server, before)))
	})

	t.Run("continue on error goes on past a failing statement and reports it", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())
		var failures []dbdriver.ScriptFailure
		req := scriptOf("SET A;\nNOT A STATEMENT;\nSET B;\n")
		req.ContinueOnError = true
		req.OnFailure = func(f dbdriver.ScriptFailure) { failures = append(failures, f) }

		result, err := s.RunScript(ctx, req)
		require.NoError(t, err)
		assert.Nil(t, result.Failure)
		assert.EqualValues(t, 2, result.Succeeded)
		assert.EqualValues(t, 1, result.Failed)
		require.Len(t, failures, 1)
		assert.EqualValues(t, 2, failures[0].Number)
		assert.Equal(t, []string{"EXEC SET A", "EXEC NOT A STATEMENT", "EXEC SET B"}, lines(execsFrom(d.server, before)))
	})

	// Like a built-in driver's: the statement running when stop is asked for runs to
	// its end, and none after it — not the rest of the batch the guest was handed.
	t.Run("stop takes effect after the statement in flight", func(t *testing.T) {
		d := newDriver()
		d.server.Hold = "EXEC INSERT INTO [t] VALUES (1)"
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())
		stop := make(chan struct{})
		req := scriptOf(inserts(450))
		req.Stop = stop
		var result *dbdriver.ScriptResult
		done := make(chan error, 1)
		go func() {
			var err error
			result, err = s.RunScript(ctx, req)
			done <- err
		}()
		held := d.server.WaitHeld(t)
		close(stop)
		held.Release()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("the stopped run did not come back")
		}
		assert.True(t, result.Stopped)
		assert.Equal(t, []string{"EXEC SET MODE strict", "EXEC INSERT INTO [t] VALUES (1)"}, lines(execsFrom(d.server, before)),
			"the held statement finishes; the rest of its batch does not run")
		assert.EqualValues(t, 2, result.Succeeded)
		assert.Zero(t, result.Failed)
	})

	t.Run("canceling the run ends the call and closes the run's connection", func(t *testing.T) {
		d := newDriver()
		d.server.Hold = "EXEC INSERT INTO [t] VALUES (2)"
		s := d.open(t)
		defer func() { _ = s.Close() }()
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := s.RunScript(runCtx, scriptOf(inserts(10)))
			done <- err
		}()
		held := d.server.WaitHeld(t)
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("the canceled run did not come back")
		}
		held.Release()
		require.Eventually(t, func() bool { return d.server.Ended(held.Conn) }, 5*time.Second, 10*time.Millisecond,
			"the run's connection is closed")
		_, err := s.Execute(ctx, "SELECT 1")
		require.NoError(t, err, "the session goes on on another instance")
	})

	t.Run("a guest panic fails the run and closes the run's connection", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())

		_, err := s.RunScript(ctx, scriptOf("SET A;\nSELECT PANIC;\nSET B;\n"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extension database driver fault")
		execs := execsFrom(d.server, before)
		require.Equal(t, []string{"EXEC SET A", "EXEC SELECT PANIC"}, lines(execs))
		require.Eventually(t, func() bool { return d.server.Ended(execs[0].Conn) }, 5*time.Second, 10*time.Millisecond,
			"the run's connection is closed")
		_, err = s.Execute(ctx, "SELECT 1")
		require.NoError(t, err, "the session goes on on another instance")
	})

	t.Run("the driver's own splitting carries its state across reads and stops at what it cannot run", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())
		req := scriptOf("DELIMITER $$\nSET A; still A$$\nDELIMITER ;\nSET B;\n\\connect other\nSET C;\n")
		req.ReadSize = 8

		_, err := s.RunScript(ctx, req)
		var unsupported *dbdriver.ScriptUnsupportedError
		require.ErrorAs(t, err, &unsupported)
		assert.Equal(t, dbdriver.ScriptUnsupportedError{Construct: `\connect other`, Tool: "fixturecli", Line: 5}, *unsupported)
		assert.Equal(t, []string{"EXEC SET A; still A", "EXEC SET B"}, lines(execsFrom(d.server, before)))
	})

	t.Run("a file that is not UTF-8 is refused before anything runs", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		before := len(d.server.SeenOn())

		_, err := s.RunScript(ctx, scriptOf("SET A;\n\xff\n"))
		var encoding *dbdriver.ScriptEncodingError
		require.ErrorAs(t, err, &encoding)
		assert.Equal(t, 2, encoding.Line)
		assert.Empty(t, execsFrom(d.server, before))
	})
}

func TestExtensionDriverDumps(t *testing.T) {
	ext := extensiontest.LoadDBDriverFixture(t)
	newDriver := fixtureDrivers(t, ext)
	ctx := context.Background()

	t.Run("a whole-database dump runs back through Run SQL File", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		path := filepath.Join(t.TempDir(), "app.sql")

		dumped, err := dbdriver.DumpToFile(ctx, s, dbdriver.DumpRequest{
			Database: "app", Whole: true, WithData: true, Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Drops: true},
			Header: dbdriver.DumpHeader{Version: "test", Asset: "fixture", Time: time.Unix(0, 0).UTC()},
		}, path)
		require.NoError(t, err)
		assert.Equal(t, 3, dumped.Objects)
		assert.EqualValues(t, 3+1000+1, dumped.Rows)
		file, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
		require.NoError(t, err)
		assert.Contains(t, string(file), "\nSET FIXTURE_CHECKS = 0;\n", "the dialect's opening is written")
		assert.True(t, strings.HasSuffix(string(file), "\nSET FIXTURE_CHECKS = 1;\n"), "the dialect's closing ends the file")

		f, err := os.Open(path) //nolint:gosec // the test's own temp file
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		before := len(d.server.SeenOn())
		run, err := s.RunScript(ctx, dbdriver.ScriptRequest{Database: "app", Script: f, Size: int64(len(file))})
		require.NoError(t, err)
		require.Nil(t, run.Failure)
		assert.Zero(t, run.Failed)

		execs := execsFrom(d.server, before)
		assert.EqualValues(t, len(execs), run.Succeeded)
		assertOneConnection(t, execs)
		ran := lines(execs)
		inserted := map[string]int{}
		for _, line := range ran {
			if rest, ok := strings.CutPrefix(line, "EXEC INSERT INTO ["); ok {
				table, _, _ := strings.Cut(rest, "]")
				inserted[table]++
			}
		}
		for table, rows := range map[string]int{"users": 3, "series": 1000, "blobs": 1} {
			ddl, err := s.TableDDL(ctx, "app", table)
			require.NoError(t, err)
			assert.Contains(t, ran, "EXEC DROP TABLE IF EXISTS ["+table+"]")
			assert.Contains(t, ran, "EXEC "+ddl, "the structure of %s is restored", table)
			assert.Equal(t, rows, inserted[table], "the rows of %s are restored", table)
		}
		assert.Contains(t, ran, "EXEC INSERT INTO [blobs] ([id], [data]) VALUES ('9007199254740993', X'00fffe6f6b')",
			"the big integer and the binary value survive the dump")
	})

	t.Run("the export options reach the guest", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()
		dump := func(options dbdriver.DumpOptions) string {
			var out strings.Builder
			_, err := s.Dump(ctx, dbdriver.DumpRequest{Database: "app", Tables: []string{"users"}, Options: options, Writer: &out})
			require.NoError(t, err)
			return out.String()
		}

		withIndexes := dump(dbdriver.DumpOptions{Indexes: true})
		assert.Contains(t, withIndexes, "\nCREATE INDEX [users_name] ON [users] ([name]);\n", "the guest writes the index it was asked for")
		assert.NotContains(t, withIndexes, "DROP")
		assert.NotContains(t, dump(dbdriver.DumpOptions{}), "CREATE INDEX", "the guest leaves the index out")
		assert.Contains(t, dump(dbdriver.DumpOptions{Drops: true}), "\nDROP TABLE IF EXISTS [users];\n")
	})

	t.Run("a canceled dump leaves the target as it was", func(t *testing.T) {
		d := newDriver()
		d.server.Hold = "QUERY SELECT * FROM [app].[series]"
		s := d.open(t)
		defer func() { _ = s.Close() }()
		dir := t.TempDir()
		path := filepath.Join(dir, "app.sql")
		require.NoError(t, os.WriteFile(path, []byte("previous"), 0o600))
		dumpCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := dbdriver.DumpToFile(dumpCtx, s, dbdriver.DumpRequest{Database: "app", Tables: []string{"users", "series"}, WithData: true}, path)
			done <- err
		}()
		held := d.server.WaitHeld(t)
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("the canceled dump did not come back")
		}
		held.Release()
		content, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
		require.NoError(t, err)
		assert.Equal(t, "previous", string(content))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no unfinished dump is left behind")
	})

	t.Run("binary values and big integers survive the writer and the cursor", func(t *testing.T) {
		d := newDriver()
		s := d.open(t)
		defer func() { _ = s.Close() }()

		w, err := s.PrepareRowStatements(ctx, "app", "blobs")
		require.NoError(t, err)
		statements, err := w.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: []string{"id", "data"}, Rows: []map[string]any{
			{"id": int64(9007199254740993), "data": []byte{0x00, 0xff, 0xfe}},
			{"id": json.Number("-9223372036854775808"), "data": nil},
		}})
		require.NoError(t, err)
		assert.Equal(t, []string{
			"INSERT INTO [app].[blobs] ([id], [data]) VALUES ('9007199254740993', X'00fffe');",
			"INSERT INTO [app].[blobs] ([id], [data]) VALUES ('-9223372036854775808', NULL);",
		}, statements)

		rows, err := s.StreamTableRows(ctx, "app", "blobs", dbdriver.TableQuery{})
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		require.True(t, rows.Next())
		assert.Equal(t, json.Number("9007199254740993"), rows.Row()[0])
		assert.Equal(t, string([]byte{0x00, 0xff, 0xfe, 'o', 'k'}), rows.Row()[1], "the raw bytes arrive as read")
		assert.False(t, rows.Next())
		require.NoError(t, rows.Err())
	})
}

// An extension built for hostABI 1.0 keeps working: its driver declares neither
// feature, so neither is offered, and the host refuses both without calling into
// the guest, which has no operations for them.
func TestHostABI10ExtensionDriverHasNoScriptFeatures(t *testing.T) {
	ext, err := extensiontest.LoadDBDriverFixtureBuild(t, extensiontest.DBDriverFixtureBuild{HostABI10: true}, extensiontest.AcceptingContributions())
	require.NoError(t, err)
	assert.Equal(t, "1.0", ext.Manifest.HostABI)
	newDriver := fixtureDrivers(t, ext)
	d := newDriver()
	assert.False(t, d.desc.Supports(dbdriver.CapRunSQLFile))
	assert.False(t, d.desc.Supports(dbdriver.CapDumpSQL))

	dbdrivertest.Register(t, d)
	sessions := func(t *testing.T) dbdriver.Session { return d.open(t) }
	dbdrivertest.RunConformance(t, d, sessions)

	s := d.open(t)
	defer func() { _ = s.Close() }()
	before := len(d.server.SeenOn())
	_, err = s.RunScript(context.Background(), scriptOf("SET A;\n"))
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	var out strings.Builder
	_, err = s.Dump(context.Background(), dbdriver.DumpRequest{Database: "app", Tables: []string{"users"}, Writer: &out})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, out.String())
	assert.Len(t, d.server.SeenOn(), before, "nothing reached the database")
}
