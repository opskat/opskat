package query

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
)

// 测试连接经配置的驱动建立会话，测完即关；假驱动的会话不是 *sql.DB。
func TestDatabaseConnectionTestOpensAndClosesTheDriversSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := &Query{}

	err := q.testDatabaseConnection(context.Background(),
		`{"driver":"fakedb","host":"h","port":7777,"database":"app","read_only":true}`, "form-password")
	require.NoError(t, err)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, "form-password", sessions[0].Request.Password, "the unsaved form password is used as is")
	assert.Equal(t, "app", sessions[0].Request.Config.Database)
	assert.True(t, sessions[0].Request.Config.ReadOnly)
	assert.True(t, sessions[0].Closed())
}

// 连不上时返回的就是驱动给的错误。
func TestDatabaseConnectionTestReturnsTheDriversError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.OpenErr = assert.AnError
	dbdrivertest.Register(t, fake)
	q := &Query{}

	err := q.testDatabaseConnection(context.Background(), `{"driver":"fakedb","host":"h","port":7777}`, "pw")
	assert.Equal(t, assert.AnError, err)
}

// 配置里的驱动没注册：测试连接必须报「驱动不可用」并点名驱动，而不是把它当成别的驱动去连。
func TestDatabaseConnectionTestRejectsUnregisteredDriver(t *testing.T) {
	q := &Query{}
	err := q.testDatabaseConnection(context.Background(),
		`{"driver":"oracle","host":"127.0.0.1","port":1521,"username":"u"}`, "pw")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

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
