package ssh

import (
	"context"
	"testing"
)

func TestSSHTesterBadJSON(t *testing.T) {
	if err := (&SSH{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
