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

// 表单把哨兵密码以明文单独传入：不得再按密文解密 configJSON 中的值。
func TestRedisTestConfigUsesPlainSentinelPassword(t *testing.T) {
	cfg, password, err := redisTestConfig(context.Background(),
		`{"mode":"sentinel","nodes":["s1:26379"],"master_name":"mymaster","sentinel_password":"not-ciphertext"}`,
		"data-pw", "sentinel-pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if password != "data-pw" || cfg.SentinelPassword != "sentinel-pw" {
		t.Fatalf("got password %q sentinel %q", password, cfg.SentinelPassword)
	}

	if _, _, err := redisTestConfig(context.Background(), "{not json", "", ""); err == nil {
		t.Fatal("expected parse error for malformed config JSON")
	}
}
