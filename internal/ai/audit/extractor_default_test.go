package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecEtcdExtractor_PrefixVariants(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "prefix true (bool)",
			args: map[string]any{"op": "get", "key": "/cfg", "prefix": true},
			want: "get /cfg --prefix",
		},
		{
			name: "prefix true (string)",
			args: map[string]any{"op": "get", "key": "/cfg", "prefix": "true"},
			want: "get /cfg --prefix",
		},
		{
			name: "prefix TRUE (case-insensitive string)",
			args: map[string]any{"op": "get", "key": "/cfg", "prefix": "TRUE"},
			want: "get /cfg --prefix",
		},
		{
			name: "prefix false (bool)",
			args: map[string]any{"op": "get", "key": "/cfg", "prefix": false},
			want: "get /cfg",
		},
		{
			name: "prefix omitted",
			args: map[string]any{"op": "get", "key": "/cfg"},
			want: "get /cfg",
		},
		{
			name: "compound op normalized",
			args: map[string]any{"op": "member_list"},
			want: "member list",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractCommandForAudit("exec_etcd", tc.args)
			assert.Equal(t, tc.want, got)
		})
	}
}
