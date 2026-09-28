package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// fakeRESPServer 是一个最小 RESP 服务端：记录每条连接 SELECT 到的库，
// 对 DBSIZE 回复该连接当前的库号，用来观察连接是否按 scope 区分。
type fakeRESPServer struct {
	ln    net.Listener
	mu    sync.Mutex
	conns int
}

func startFakeRESPServer(t *testing.T) *fakeRESPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &fakeRESPServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeRESPServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeRESPServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	db := 0
	for {
		args, err := readRESPArray(r)
		if err != nil {
			return
		}
		var reply string
		switch strings.ToUpper(args[0]) {
		case "HELLO":
			reply = "-ERR unknown command 'HELLO'\r\n"
		case "PING":
			reply = "+PONG\r\n"
		case "SELECT":
			db, _ = strconv.Atoi(args[1])
			reply = "+OK\r\n"
		case "DBSIZE":
			reply = fmt.Sprintf(":%d\r\n", db)
		default:
			reply = "+OK\r\n"
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

func readRESPArray(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if _, err := r.ReadString('\n'); err != nil { // $len
			return nil, err
		}
		v, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		args = append(args, strings.TrimRight(v, "\r\n"))
	}
	return args, nil
}

func redisTestAsset(t *testing.T, cfg *asset_entity.RedisConfig) *asset_entity.Asset {
	t.Helper()
	asset := &asset_entity.Asset{ID: 7, Type: asset_entity.AssetTypeRedis}
	require.NoError(t, asset.SetRedisConfig(cfg))
	return asset
}

func redisResultValue(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &m), out)
	return m
}

func TestExecRedisOnAsset_StandaloneScopeSelectsDB(t *testing.T) {
	srv := startFakeRESPServer(t)
	asset := redisTestAsset(t, &asset_entity.RedisConfig{Host: "127.0.0.1", Port: srv.port(), Database: 2})
	ctx := WithRedisCache(context.Background(), NewRedisClientCache())
	defer func() { _ = getRedisCache(ctx).Close() }()

	out, err := ExecRedisOnAsset(ctx, asset, "DBSIZE", "")
	require.NoError(t, err)
	assert.EqualValues(t, 2, redisResultValue(t, out)["value"], "empty scope uses the asset's default db")

	_, err = ExecRedisOnAsset(ctx, asset, "DBSIZE", "abc")
	require.Error(t, err)
}

// Problem 6：同一轮对话里对同一资产用两个不同 scope，第二次不能复用第一次那个库的连接。
func TestExecRedisOnAsset_CacheSeparatesDBScopes(t *testing.T) {
	srv := startFakeRESPServer(t)
	asset := redisTestAsset(t, &asset_entity.RedisConfig{Host: "127.0.0.1", Port: srv.port()})
	ctx := WithRedisCache(context.Background(), NewRedisClientCache())
	defer func() { _ = getRedisCache(ctx).Close() }()

	out, err := ExecRedisOnAsset(ctx, asset, "DBSIZE", "1")
	require.NoError(t, err)
	assert.EqualValues(t, 1, redisResultValue(t, out)["value"])

	out, err = ExecRedisOnAsset(ctx, asset, "DBSIZE", "3")
	require.NoError(t, err)
	assert.EqualValues(t, 3, redisResultValue(t, out)["value"])

	out, err = ExecRedisOnAsset(ctx, asset, "DBSIZE", "1")
	require.NoError(t, err)
	assert.EqualValues(t, 1, redisResultValue(t, out)["value"])
}

func TestRedisConnCacheKey_ClusterSharesOneClientPerAsset(t *testing.T) {
	cluster := &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}}
	a := redisConnCacheKey(7, cluster)
	require.NoError(t, ApplyRedisScope(cluster, "10.0.0.2:7002"))
	assert.Equal(t, a, redisConnCacheKey(7, cluster), "cluster scope names a node, not a connection")
	assert.NotEqual(t, a, redisConnCacheKey(8, cluster))
}

// --- 集群路由 ---

type fakeClusterNode struct {
	addr   string
	master bool
	down   bool
}

type fakeCall struct {
	route string // "key" | "node"
	addr  string
	args  []string
}

// fakeCluster 模拟集群路由器：keys 表示 COMMAND GETKEYS 的结果（按命令名），
// slotOwner 表示 key 所在主节点。
type fakeCluster struct {
	nodes     []fakeClusterNode
	keys      map[string][]string
	slotOwner map[string]string
	calls     []fakeCall
}

func (f *fakeCluster) CommandKeys(_ context.Context, args []string) ([]string, error) {
	return f.keys[strings.ToUpper(args[0])], nil
}

func (f *fakeCluster) DoByKey(_ context.Context, args []string, keyPos int) (any, string, error) {
	addr := f.slotOwner[args[keyPos]]
	f.calls = append(f.calls, fakeCall{route: "key", addr: addr, args: args})
	return "v", addr, nil
}

func (f *fakeCluster) Masters(_ context.Context) ([]string, error) {
	var out []string
	for _, n := range f.nodes {
		if n.master {
			out = append(out, n.addr)
		}
	}
	return out, nil
}

func (f *fakeCluster) DoOnNode(_ context.Context, addr string, args []string) (any, bool, error) {
	for _, n := range f.nodes {
		if n.addr != addr {
			continue
		}
		f.calls = append(f.calls, fakeCall{route: "node", addr: addr, args: args})
		if n.down {
			return nil, true, errors.New("dial tcp " + addr + ": connection refused")
		}
		return "PONG", true, nil
	}
	return nil, false, nil
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{
		nodes: []fakeClusterNode{
			{addr: "10.0.0.1:7001", master: true},
			{addr: "10.0.0.2:7002", master: true},
			{addr: "10.0.0.3:7003", master: true},
			{addr: "10.0.0.4:7004"},
		},
		keys:      map[string][]string{"GET": {"user:1"}, "OBJECT": {"user:1"}},
		slotOwner: map[string]string{"user:1": "10.0.0.2:7002"},
	}
}

func TestExecClusterArgs_KeyedCommandRoutesBySlotAndIgnoresScope(t *testing.T) {
	fc := newFakeCluster()
	out, err := execClusterArgs(context.Background(), fc, []string{"OBJECT", "ENCODING", "user:1"}, "10.0.0.1:7001")
	require.NoError(t, err)
	res := redisResultValue(t, out)
	assert.Equal(t, "10.0.0.2:7002", res["node"])
	assert.Equal(t, "key", res["route"])
	require.Len(t, fc.calls, 1)
	assert.Equal(t, "key", fc.calls[0].route)
	assert.Equal(t, "user:1", fc.calls[0].args[2])
}

func TestExecClusterArgs_NodeIndependentCommandRunsAnywhere(t *testing.T) {
	for _, cmd := range [][]string{{"PING"}, {"echo", "hi"}, {"TIME"}, {"COMMAND", "COUNT"}, {"CLUSTER", "INFO"}} {
		fc := newFakeCluster()
		fc.nodes[0].down = true // 第一个主节点不可达时换下一个
		out, err := execClusterArgs(context.Background(), fc, cmd, "")
		require.NoError(t, err, cmd)
		assert.Equal(t, "10.0.0.2:7002", redisResultValue(t, out)["node"], cmd)
	}
}

// CLUSTER 子命令多数与节点相关（COUNTKEYSINSLOT 只在 slot 所在节点有数、RESET/FORGET/FAILOVER
// 只作用于收到它的节点），不能交给任一主节点执行。
func TestExecClusterArgs_NodeDependentClusterSubcommandNeedsScope(t *testing.T) {
	for _, cmd := range [][]string{{"CLUSTER"}, {"CLUSTER", "COUNTKEYSINSLOT", "42"}, {"cluster", "reset"}, {"CLUSTER", "FORGET", "abc"}} {
		fc := newFakeCluster()
		_, err := execClusterArgs(context.Background(), fc, cmd, "")
		var nodeErr *RedisNodeRequiredError
		require.ErrorAs(t, err, &nodeErr, cmd)
		assert.Empty(t, fc.calls, "nothing may execute on a random node: %v", cmd)
	}
}

func TestExecClusterArgs_NodeIndependentCommandHonoursScope(t *testing.T) {
	fc := newFakeCluster()
	out, err := execClusterArgs(context.Background(), fc, []string{"CLUSTER", "NODES"}, "10.0.0.3:7003")
	require.NoError(t, err)
	res := redisResultValue(t, out)
	assert.Equal(t, "10.0.0.3:7003", res["node"])
	assert.Equal(t, "scope", res["route"])
}

func TestExecClusterArgs_KeylessCommandNeedsNodeScope(t *testing.T) {
	for _, scope := range []string{"", "0", "10.9.9.9:7009"} {
		fc := newFakeCluster()
		_, err := execClusterArgs(context.Background(), fc, []string{"DBSIZE"}, scope)
		var nodeErr *RedisNodeRequiredError
		require.ErrorAs(t, err, &nodeErr, "scope %q", scope)
		assert.Equal(t, []string{"10.0.0.1:7001", "10.0.0.2:7002", "10.0.0.3:7003"}, nodeErr.Masters)
		for _, m := range nodeErr.Masters {
			assert.Contains(t, err.Error(), m)
		}
		assert.Empty(t, fc.calls, "nothing may execute on a random node")
	}
}

func TestExecClusterArgs_KeylessCommandOnScopedNode(t *testing.T) {
	fc := newFakeCluster()
	out, err := execClusterArgs(context.Background(), fc, []string{"INFO", "memory"}, "10.0.0.4:7004")
	require.NoError(t, err, "a replica may be targeted for read-only diagnostics")
	res := redisResultValue(t, out)
	assert.Equal(t, "10.0.0.4:7004", res["node"])
	assert.Equal(t, "scope", res["route"])
}

func TestExecClusterArgs_RejectsSelect(t *testing.T) {
	fc := newFakeCluster()
	_, err := execClusterArgs(context.Background(), fc, []string{"select", "1"}, "10.0.0.1:7001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db0")
	assert.Empty(t, fc.calls)
}

func TestExecClusterArgs_NilReplyNamesNode(t *testing.T) {
	fc := &nilCluster{fakeCluster: newFakeCluster()}
	out, err := execClusterArgs(context.Background(), fc, []string{"GET", "user:1"}, "")
	require.NoError(t, err)
	res := redisResultValue(t, out)
	assert.Equal(t, "nil", res["type"])
	assert.Equal(t, "10.0.0.2:7002", res["node"])
}

type nilCluster struct{ *fakeCluster }

func (n *nilCluster) DoByKey(ctx context.Context, args []string, keyPos int) (any, string, error) {
	_, addr, _ := n.fakeCluster.DoByKey(ctx, args, keyPos)
	return nil, addr, redis.Nil
}
