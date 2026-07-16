package aictx

import "testing"

func TestArgBool(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want bool
	}{
		{name: "native true", args: map[string]any{"flag": true}, want: true},
		{name: "native false", args: map[string]any{"flag": false}, want: false},
		{name: "trimmed string true", args: map[string]any{"flag": " true "}, want: true},
		{name: "case insensitive string true", args: map[string]any{"flag": "TRUE"}, want: true},
		{name: "string false", args: map[string]any{"flag": "FALSE"}, want: false},
		{name: "invalid string", args: map[string]any{"flag": "yes"}, want: false},
		{name: "wrong type", args: map[string]any{"flag": 1}, want: false},
		{name: "missing", args: map[string]any{}, want: false},
		{name: "nil args", args: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ArgBool(tt.args, "flag"); got != tt.want {
				t.Fatalf("ArgBool() = %v, want %v", got, tt.want)
			}
		})
	}
}
