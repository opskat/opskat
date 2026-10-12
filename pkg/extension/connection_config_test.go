package extension

import (
	"encoding/json"
	"reflect"
	"testing"
)

func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("parse got %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("parse want %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestStripHostConnectionConfig(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "removes the reserved key alongside guest fields",
			in:   `{"endpoint":"es.internal:9200","` + HostConnectionConfigKey + `":{"tls":{"enabled":true,"caCert":"ca-pem"}}}`,
			want: `{"endpoint":"es.internal:9200"}`,
		},
		{
			name: "no-ops when the key is absent",
			in:   `{"endpoint":"es.internal:9200"}`,
			want: `{"endpoint":"es.internal:9200"}`,
		},
		{
			name: "no-ops on an empty config",
			in:   ``,
			want: ``,
		},
		{
			name: "no-ops on the default empty object",
			in:   `{}`,
			want: `{}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := StripHostConnectionConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotStr := string(got)
			if tc.in == "" {
				if gotStr != "" {
					t.Fatalf("want empty, got %q", gotStr)
				}
				return
			}
			// Compare as parsed JSON so key order doesn't matter.
			assertJSONEqual(t, gotStr, tc.want)
		})
	}
}

func TestStripHostConnectionConfigRejectsMalformedJSON(t *testing.T) {
	_, err := StripHostConnectionConfig([]byte(`{not json`))
	if err == nil {
		t.Fatal("want error for malformed config, got nil")
	}
}
