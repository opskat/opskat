package extwire

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRowPartsBoundEachGuestCallBySize(t *testing.T) {
	row := func(size int) map[string]any { return map[string]any{"body": strings.Repeat("x", size)} }
	sizes := func(parts [][]map[string]any) [][]int {
		out := make([][]int, len(parts))
		for i, part := range parts {
			out[i] = []int{}
			for _, r := range part {
				out[i] = append(out[i], len(r["body"].(string)))
			}
		}
		return out
	}
	half := DriverRowsBytes / 2

	t.Run("no rows are one empty part, still one call", func(t *testing.T) {
		assert.Equal(t, [][]int{{}}, sizes(RowParts(nil)))
	})
	t.Run("rows under the size stay in one part", func(t *testing.T) {
		assert.Equal(t, [][]int{{10, 10, 10}}, sizes(RowParts([]map[string]any{row(10), row(10), row(10)})))
	})
	t.Run("a part ends once it reaches the size", func(t *testing.T) {
		parts := RowParts([]map[string]any{row(half), row(half), row(half), row(half), row(1)})
		assert.Equal(t, [][]int{{half, half}, {half, half}, {1}}, sizes(parts))
	})
	t.Run("a row larger than the size goes alone, and the rows around it in their own parts", func(t *testing.T) {
		parts := RowParts([]map[string]any{row(10), row(3 * half), row(2 * DriverRowsBytes), row(10)})
		assert.Equal(t, [][]int{{10, 3 * half}, {2 * DriverRowsBytes}, {10}}, sizes(parts))
	})
	t.Run("the parts are the rows, in order, each once", func(t *testing.T) {
		rows := make([]map[string]any, 40)
		for i := range rows {
			rows[i] = map[string]any{"id": int64(i), "body": strings.Repeat("y", half/3)}
		}
		var ids []int64
		parts := RowParts(rows)
		assert.Greater(t, len(parts), 1)
		for _, part := range parts {
			for _, r := range part {
				ids = append(ids, r["id"].(int64))
			}
		}
		want := make([]int64, 40)
		for i := range want {
			want[i] = int64(i)
		}
		assert.Equal(t, want, ids)
	})
}
