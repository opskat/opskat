package kafka

import (
	"context"
	"testing"
)

func TestKafkaTesterBadJSON(t *testing.T) {
	if err := (&Kafka{}).testConnection(context.Background(), "{not json", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
