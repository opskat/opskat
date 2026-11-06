package outputcap

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapReturnsUnderLimitVerbatim(t *testing.T) {
	s := strings.Repeat("line\n", MaxLines)
	assert.Equal(t, s, Cap(s))
	assert.Equal(t, "", Cap(""))
}

func TestCapKeepsHeadAndTailOfManyLines(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&b, "row %d\n", i)
	}
	out := Cap(b.String())
	assert.True(t, strings.HasPrefix(out, "row 1\nrow 2\n"))
	assert.True(t, strings.HasSuffix(out, "row 4999\nrow 5000\n"))
	assert.Contains(t, out, "omitted")
	assert.LessOrEqual(t, strings.Count(out, "\n"), MaxLines+2)
	assert.LessOrEqual(t, len(out), MaxBytes)
	assert.Regexp(t, `\[\.\.\. \d+ lines / \d+\.\d KiB omitted \.\.\.\]`, out)
}

func TestCapBoundsASingleHugeMultibyteLineWithoutSplittingRunes(t *testing.T) {
	s := strings.Repeat("中", 100*1024) // 300 KiB, no newline
	out := Cap(s)
	require.True(t, utf8.ValidString(out))
	assert.LessOrEqual(t, len(out), MaxBytes)
	assert.Contains(t, out, "KiB omitted")
	assert.True(t, strings.HasPrefix(out, "中"))
	assert.True(t, strings.HasSuffix(out, "中"))
}

func TestCapNoteStatesOmittedBytes(t *testing.T) {
	s := strings.Repeat("x", 200*1024)
	out := Cap(s)
	assert.LessOrEqual(t, len(out), MaxBytes)
	assert.Regexp(t, `\[\.\.\. 1 lines / 1\d\d\.\d KiB omitted \.\.\.\]`, out)
}
