package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// classify is fixturedb's statement splitter: statements end at a semicolon
// outside single quotes, each one trimmed of surrounding space. The type is the
// first word uppercased (two words for DROP / CREATE / ALTER); UPDATE and DELETE
// without WHERE, and CALL, are dangerous. An unclosed quote cannot be split.
//
// A statement "VIOLATE <kind>" makes the answer break one rule of the contract,
// so tests see the host refuse it: lowercase (a lowercase type), reason (a danger
// reason that does not exist), range (an end past the input), overlap (a start
// inside the previous statement).
func classify(_ context.Context, sql string) ([]dbdriver.Statement, error) {
	var stmts []dbdriver.Statement
	start, quote := 0, -1
	for i := 0; i <= len(sql); i++ {
		if i < len(sql) && sql[i] == '\'' {
			if quote < 0 {
				quote = i
			} else {
				quote = -1
			}
		}
		if i < len(sql) && (quote >= 0 || sql[i] != ';') {
			continue
		}
		if quote >= 0 {
			return nil, fmt.Errorf("unterminated string literal at byte %d", quote)
		}
		piece := sql[start:i]
		text := strings.TrimSpace(piece)
		if text != "" {
			at := start + len(piece) - len(strings.TrimLeftFunc(piece, unicode.IsSpace))
			stmts = append(stmts, classifyOne(sql, at, text, stmts))
		}
		start = i + 1
	}
	return stmts, nil
}

// classifyOne classifies the statement text found at byte at of sql, after the
// statements before it.
func classifyOne(sql string, at int, text string, before []dbdriver.Statement) dbdriver.Statement {
	s := dbdriver.Statement{Start: at, End: at + len(text), Text: text}
	words := strings.Fields(strings.ToUpper(text))
	s.Type = words[0]
	arg := ""
	if len(words) > 1 {
		arg = words[1]
	}
	switch s.Type {
	case "DROP", "CREATE", "ALTER":
		s.Type = strings.TrimSpace(s.Type + " " + arg)
	case "UPDATE":
		if !slices.Contains(words, "WHERE") {
			s.Dangerous, s.Reason = true, dbdriver.ReasonNoWhereUpdate
		}
	case "DELETE":
		if !slices.Contains(words, "WHERE") {
			s.Dangerous, s.Reason = true, dbdriver.ReasonNoWhereDelete
		}
	case "CALL":
		s.Dangerous, s.Reason = true, dbdriver.ReasonCall
	case "VIOLATE":
		switch arg {
		case "LOWERCASE":
			s.Type = "violate"
		case "REASON":
			s.Dangerous, s.Reason = true, "risky"
		case "RANGE":
			s.End = len(sql) + 1
		case "OVERLAP":
			if len(before) > 0 {
				s.Start = before[len(before)-1].End - 1
			}
		}
	}
	return s
}
