package query

import (
	"context"
	"testing"
)

func TestQueryTestersBadJSON(t *testing.T) {
	q := &Query{}
	for _, fn := range []func(context.Context, string, string) error{
		q.testDatabaseConnection, q.testRedisConnection, q.testMongoConnection,
	} {
		if err := fn(context.Background(), "{not json", ""); err == nil {
			t.Fatal("expected parse error for malformed config JSON")
		}
	}
}
