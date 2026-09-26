package helper

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// TestExecRedisOnAssetLiveCluster 在真实集群上验证 go-redis 集群路由实现（COMMAND GETKEYS
// 判定 key、按 slot 路由、按节点执行、缺节点报错）。
// OPSKAT_REDIS_CLUSTER_TEST_NODES：逗号分隔的种子节点；OPSKAT_REDIS_CLUSTER_TEST_PASSWORD：可选密码。
func TestExecRedisOnAssetLiveCluster(t *testing.T) {
	nodes := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_NODES"))
	if nodes == "" {
		t.Skip("set OPSKAT_REDIS_CLUSTER_TEST_NODES to run live Redis cluster test")
	}
	cfg := &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: strings.Split(nodes, ",")}
	if pw := os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_PASSWORD"); pw != "" {
		orig := credential_svc.Default()
		t.Cleanup(func() { credential_svc.SetDefault(orig) })
		svc := credential_svc.New("live-test", []byte("live-test-salt-0"))
		credential_svc.SetDefault(svc)
		enc, err := svc.Encrypt(pw)
		require.NoError(t, err)
		cfg.Password = enc
	}
	asset := redisTestAsset(t, cfg)
	cache := NewRedisClientCache()
	ctx := WithRedisCache(context.Background(), cache)
	defer func() { _ = cache.Close() }()

	exec := func(command, scope string) (map[string]any, error) {
		out, err := ExecRedisOnAsset(ctx, asset, command, scope)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &m), out)
		return m, nil
	}

	_, err := exec("DBSIZE", "")
	var nodeErr *RedisNodeRequiredError
	require.ErrorAs(t, err, &nodeErr)
	require.NotEmpty(t, nodeErr.Masters)
	t.Logf("masters: %v", nodeErr.Masters)

	for _, key := range []string{"opskat:live:a", "opskat:live:b", "opskat:live:c"} {
		res, err := exec("SET "+key+" 1", nodeErr.Masters[0])
		require.NoError(t, err)
		assert.Equal(t, "key", res["route"])
		t.Logf("%s -> %v", key, res["node"])
		res, err = exec("OBJECT ENCODING "+key, "")
		require.NoError(t, err)
		assert.Equal(t, "key", res["route"])
		assert.NotEmpty(t, res["node"])
		_, err = exec("DEL "+key, "")
		require.NoError(t, err)
	}

	res, err := exec("GET opskat:live:missing", "")
	require.NoError(t, err)
	assert.Equal(t, "nil", res["type"])
	assert.NotEmpty(t, res["node"])

	res, err = exec("DBSIZE", nodeErr.Masters[1])
	require.NoError(t, err)
	assert.Equal(t, nodeErr.Masters[1], res["node"])
	assert.Equal(t, "scope", res["route"])

	res, err = exec("PING", "")
	require.NoError(t, err)
	assert.Equal(t, "any", res["route"])

	_, err = exec("INFO keyspace", "0")
	require.ErrorAs(t, err, &nodeErr)

	_, err = exec("MSET opskat:live:a 1 opskat:live:b 2", "")
	require.Error(t, err)
	t.Logf("cross-slot: %v", err)
}
