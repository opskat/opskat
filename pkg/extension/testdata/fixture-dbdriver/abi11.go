//go:build !fixture_hostabi10

package main

import (
	"context"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// abiCapabilities are what host ABI 1.1 lets an extension driver declare: running
// SQL files and dumping them. The dialect implements both: its own splitting
// (SplitScript) and the dump writing (DumpSyntax).
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
