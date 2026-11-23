//go:build !fixture_hostabi10

package main

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// abiCapabilities are what host ABI 1.1 lets an extension driver declare: running
// SQL files and dumping them. The dialect implements both: its own splitting
// (SplitScript) and the dump writing (DumpSyntax, DumpObject).
var abiCapabilities = []dbdriver.Capability{dbdriver.CapRunSQLFile, dbdriver.CapDumpSQL}

// scriptSyntax is fixturedb's SQL file syntax: single-quoted strings, -- and /* */
// comments, and the client's DELIMITER command — the current delimiter is the
// state a split carries over to the next read of the file.
var scriptSyntax = sqldb.ScriptSyntax{Quotes: []sqldb.ScriptQuote{{Open: '\'', Close: '\''}}, Delimiter: true}

// fixtureCLI is the command-line tool a file with client commands has to be run with.
const fixtureCLI = "fixturecli"

// SplitScript is the dialect's own splitting (dbdriver.ScriptSplitter, which sqldb
// uses in place of a ScriptSyntax): scriptSyntax's statements, except that a
// statement starting with a backslash is a client command Run SQL File cannot
// run — the split stops there, naming the command's line and fixtureCLI.
func (dialect) SplitScript(_ context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	split, err := sqldb.SplitScript(scriptSyntax, chunk)
	if err != nil {
		return nil, err
	}
	for i, statement := range split.Statements {
		if strings.HasPrefix(statement.Text, `\`) {
			command, _, _ := strings.Cut(statement.Text, "\n")
			split.Statements = split.Statements[:i]
			split.Unsupported = &dbdriver.ScriptUnsupported{Construct: command, Tool: fixtureCLI, Offset: statement.Offset}
			return split, nil
		}
	}
	return split, nil
}

// DumpSyntax turns fixturedb's checks off for the length of a dump file. Every
// object is written the sqldb default way.
func (dialect) DumpSyntax() sqldb.DumpSyntax {
	return sqldb.DumpSyntax{Opening: []string{"SET FIXTURE_CHECKS = 0;"}, Closing: []string{"SET FIXTURE_CHECKS = 1;"}}
}

var _ sqldb.DumpObjectWriter = dialect{}

// usersIndex is fixturedb's one secondary index, on users.name. The catalog does
// not list it; the dump writes it after the table.
const usersIndex = "CREATE INDEX [users_name] ON [users] ([name]);"

// DumpObject writes usersIndex after the users table only when the dump includes
// indexes (dbdriver.DumpOptions.Indexes): the export options the host was given
// reach the guest's dump dialect with each object. The function users_count says
// it depends on the view v_users, so the host writes the view first and drops it
// last, against the order it uses for objects without dependencies. The
// partitioned table events names its partition events_2026 (Partitions), which a
// dump of events alone brings along and which says it depends on events, so the
// host writes it after events although SHOW TABLES lists it first.
func (dialect) DumpObject(_ context.Context, _ *sql.Conn, object dbdriver.DumpObject, script *dbdriver.DumpObjectScript) error {
	switch {
	case object.Kind == dbdriver.DumpTable && object.Name == "users" && object.Options.Indexes:
		script.Definition = append(script.Definition, usersIndex)
	case object.Kind == dbdriver.DumpTable && object.Name == "events":
		script.Partitions = []string{"events_2026"}
	case object.Kind == dbdriver.DumpTable && object.Name == "events_2026":
		script.Dependencies = []dbdriver.DumpDependency{{Kind: dbdriver.DumpTable, Name: "events"}}
	case object.Kind == dbdriver.ObjectFunction && object.Name == "users_count":
		script.Dependencies = []dbdriver.DumpDependency{{Kind: dbdriver.ObjectView, Name: "v_users"}}
	}
	return nil
}
