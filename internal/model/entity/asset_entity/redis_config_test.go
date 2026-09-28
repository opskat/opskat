package asset_entity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func redisAsset(t *testing.T, cfg *RedisConfig) *Asset {
	t.Helper()
	a := &Asset{Name: "cache", Type: AssetTypeRedis}
	require.NoError(t, a.SetRedisConfig(cfg))
	return a
}

func TestRedisConfigModeJSONContract(t *testing.T) {
	cfg := &RedisConfig{
		Mode:             RedisModeSentinel,
		Nodes:            []string{"10.0.0.1:26379"},
		MasterName:       "mymaster",
		SentinelUsername: "sentinel-user",
		SentinelPassword: "encrypted",
		NodeAddressMap:   map[string]string{"172.18.0.2:6379": "10.0.0.2:16379"},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "sentinel", raw["mode"])
	assert.Equal(t, []any{"10.0.0.1:26379"}, raw["nodes"])
	assert.Equal(t, "mymaster", raw["master_name"])
	assert.Equal(t, "sentinel-user", raw["sentinel_username"])
	assert.Equal(t, "encrypted", raw["sentinel_password"])
	assert.Equal(t, map[string]any{"172.18.0.2:6379": "10.0.0.2:16379"}, raw["node_address_map"])

	// 旧资产没有 mode 字段:按单机处理,且序列化时不写出新字段。
	legacy, err := json.Marshal(&RedisConfig{Host: "h", Port: 6379})
	require.NoError(t, err)
	assert.JSONEq(t, `{"host":"h","port":6379}`, string(legacy))
	assert.Equal(t, RedisModeStandalone, (&RedisConfig{}).EffectiveMode())

	// 集群 / 哨兵不用 host/port:清空后的零值不应出现在存储的 JSON 里(raw 来自上面的哨兵 cfg)。
	_, hasHost := raw["host"]
	_, hasPort := raw["port"]
	assert.False(t, hasHost, "cluster/sentinel stored config must not carry an empty host key")
	assert.False(t, hasPort, "cluster/sentinel stored config must not carry a zero port key")
}

func TestValidateRedisModes(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *RedisConfig
		wantErr []string // 全部子串都需出现;nil 表示应通过
	}{
		{name: "legacy standalone without mode", cfg: &RedisConfig{Host: "h", Port: 6379}},
		{name: "explicit standalone", cfg: &RedisConfig{Mode: RedisModeStandalone, Host: "h", Port: 6379, Database: 3}},
		{name: "standalone still requires host", cfg: &RedisConfig{Mode: RedisModeStandalone, Port: 6379}, wantErr: []string{"主机"}},
		{name: "unknown mode", cfg: &RedisConfig{Mode: "replica", Host: "h", Port: 6379}, wantErr: []string{"mode", "replica"}},
		{name: "cluster seeds only, no host", cfg: &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001", "[::1]:7002"}}},
		{name: "cluster without nodes", cfg: &RedisConfig{Mode: RedisModeCluster, Host: "h", Port: 6379}, wantErr: []string{"nodes"}},
		{name: "cluster names bad line", cfg: &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001", "10.0.0.2"}}, wantErr: []string{"nodes", "第 2 行", "10.0.0.2"}},
		{name: "cluster rejects bad port", cfg: &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:70010"}}, wantErr: []string{"第 1 行"}},
		{name: "cluster rejects blank line", cfg: &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001", " "}}, wantErr: []string{"第 2 行"}},
		{name: "cluster only has db0", cfg: &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}, Database: 2}, wantErr: []string{"database", "db0"}},
		{
			name: "sentinel valid",
			cfg: &RedisConfig{
				Mode: RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster", Database: 4,
				SentinelPassword: "enc", NodeAddressMap: map[string]string{"172.18.0.2:6379": "10.0.0.2:16379"},
			},
		},
		{name: "sentinel requires master name", cfg: &RedisConfig{Mode: RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "  "}, wantErr: []string{"master_name"}},
		{name: "sentinel requires nodes", cfg: &RedisConfig{Mode: RedisModeSentinel, MasterName: "mymaster"}, wantErr: []string{"nodes"}},
		{
			name:    "address map bad announced address",
			cfg:     &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}, NodeAddressMap: map[string]string{"172.18.0.2": "10.0.0.2:7001"}},
			wantErr: []string{"node_address_map", "172.18.0.2"},
		},
		{
			name:    "address map bad actual address",
			cfg:     &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}, NodeAddressMap: map[string]string{"172.18.0.2:7001": ""}},
			wantErr: []string{"node_address_map", "172.18.0.2:7001"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := redisAsset(t, tc.cfg).Validate()
			if tc.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, s := range tc.wantErr {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
}

func TestRedisCanConnectByMode(t *testing.T) {
	cluster := redisAsset(t, &RedisConfig{Mode: RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}})
	cluster.Status = StatusActive
	assert.True(t, cluster.CanConnect())

	sentinel := redisAsset(t, &RedisConfig{Mode: RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}})
	sentinel.Status = StatusActive
	assert.False(t, sentinel.CanConnect(), "sentinel without master name cannot connect")

	legacy := redisAsset(t, &RedisConfig{Host: "h", Port: 6379})
	legacy.Status = StatusActive
	assert.True(t, legacy.CanConnect())
}
