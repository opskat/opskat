package etcd

import (
	"context"
	"testing"
)

func TestEtcdTesterBadJSON(t *testing.T) {
	if err := (&Etcd{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
