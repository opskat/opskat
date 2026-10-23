package dbdriver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classifyingDriver is a driver whose classifier answers canned output.
type classifyingDriver struct {
	Driver // only Descriptor and ClassifyStatements are called
	desc   Descriptor
	out    []Statement
	err    error
	calls  int
}

func (d *classifyingDriver) Descriptor() Descriptor { return d.desc }

func (d *classifyingDriver) ClassifyStatements(context.Context, string) ([]Statement, error) {
	d.calls++
	return d.out, d.err
}

func classifier(out []Statement, err error) *classifyingDriver {
	return &classifyingDriver{
		desc: Descriptor{ID: "splitdb", Capabilities: []Capability{CapClassifyStatements}},
		out:  out, err: err,
	}
}

// plainDriver declares nothing and implements no classifier.
type plainDriver struct{ Driver }

func (plainDriver) Descriptor() Descriptor { return Descriptor{ID: "plaindb"} }

func TestClassifyStatementsAnswersTheDriversStatements(t *testing.T) {
	const sql = "SELECT 1; UPDATE t SET a = 1;\nMERGE INTO t USING s ON (1=1)"
	want := []Statement{
		{Start: 0, End: 8, Text: "SELECT 1", Type: "SELECT"},
		{Start: 10, End: 28, Text: "UPDATE t SET a = 1", Type: "UPDATE", Dangerous: true, Reason: ReasonNoWhereUpdate},
		{Start: 30, End: len(sql), Text: "MERGE INTO t USING s ON (1=1)", Type: "MERGE"},
	}
	got, err := ClassifyStatements(context.Background(), classifier(want, nil), sql)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	got, err = ClassifyStatements(context.Background(), classifier(nil, nil), "  ")
	require.NoError(t, err, "text with no statement splits into none")
	assert.Empty(t, got)
}

func TestClassifyStatementsRefusesOutputViolatingTheContract(t *testing.T) {
	const sql = "SELECT 1; DELETE FROM t; é" // é is two bytes: 25..27
	sel := Statement{Start: 0, End: 8, Text: "SELECT 1", Type: "SELECT"}
	del := Statement{Start: 10, End: 23, Text: "DELETE FROM t", Type: "DELETE", Dangerous: true, Reason: ReasonNoWhereDelete}
	cases := []struct {
		name string
		out  []Statement
		want string // what the error must name besides the driver
	}{
		{"lowercase type", []Statement{{Start: 0, End: 8, Type: "select"}}, `"select" is not uppercase`},
		{"mixed-case type", []Statement{{Start: 0, End: 8, Type: "Select"}}, `"Select" is not uppercase`},
		{"empty type", []Statement{{Start: 0, End: 8}}, "no type"},
		{"unknown danger reason", []Statement{{Start: 0, End: 8, Type: "DROP TABLE", Dangerous: true, Reason: "drop"}}, `"drop"`},
		{"dangerous without a reason", []Statement{{Start: 0, End: 8, Type: "UPDATE", Dangerous: true}}, `reason ""`},
		{"reason without danger", []Statement{{Start: 0, End: 8, Type: "CALL", Reason: ReasonCall}}, "not marked dangerous"},
		{"negative start", []Statement{{Start: -1, End: 8, Type: "SELECT"}}, "[-1, 8)"},
		{"end past the input", []Statement{{Start: 0, End: len(sql) + 1, Type: "SELECT"}}, "[0, 28)"},
		{"empty range", []Statement{{Start: 3, End: 3, Type: "SELECT"}}, "[3, 3)"},
		{"reversed range", []Statement{{Start: 8, End: 0, Type: "SELECT"}}, "[8, 0)"},
		{"start inside a character", []Statement{{Start: 26, End: 27, Type: "OTHER"}}, "byte 26"},
		{"end inside a character", []Statement{{Start: 24, End: 26, Type: "OTHER"}}, "byte 26"},
		{"out of order", []Statement{del, sel}, "statement 2"},
		{"overlapping", []Statement{sel, {Start: 7, End: 23, Type: "DELETE", Dangerous: true, Reason: ReasonNoWhereDelete}}, "statement 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ClassifyStatements(context.Background(), classifier(tc.out, nil), sql)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrCannotSplit)
			assert.Contains(t, err.Error(), `"splitdb"`, "the error names the driver")
			assert.Contains(t, err.Error(), tc.want, "the error names the violated constraint")
		})
	}
}

func TestClassifyStatementsPassesOnTheDriversError(t *testing.T) {
	reason := errors.New("unterminated string literal at byte 7")
	_, err := ClassifyStatements(context.Background(), classifier(nil, reason), "SELECT 'x")
	require.Error(t, err)
	assert.ErrorIs(t, err, reason)
	assert.Contains(t, err.Error(), `"splitdb"`)
	assert.Contains(t, err.Error(), "unterminated string literal at byte 7")
}

func TestClassifyStatementsRefusesADriverThatDoesNotDeclareIt(t *testing.T) {
	_, err := ClassifyStatements(context.Background(), plainDriver{}, "SELECT 1")
	require.ErrorIs(t, err, ErrUnsupported)
	assert.Contains(t, err.Error(), `"plaindb"`)

	undeclared := classifier([]Statement{{Start: 0, End: 8, Type: "SELECT"}}, nil)
	undeclared.desc.Capabilities = nil
	_, err = ClassifyStatements(context.Background(), undeclared, "SELECT 1")
	require.ErrorIs(t, err, ErrUnsupported, "implementing the classifier is not declaring it")
	assert.Zero(t, undeclared.calls, "an undeclared classifier is not asked")
}
