package redis_svc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClusterNode 模拟单个节点对一条命令的回复。
type fakeClusterNode func(args []any) (any, error)

// fakeCluster 实现 clusterExecutor：Do 走 routed（按 slot 路由的命令），
// DoOnNode 按地址分发到 nodes，nodes 之外的地址视为用例错误。
type fakeCluster struct {
	mu        sync.Mutex
	shards    []string
	nodes     map[string]fakeClusterNode
	routed    fakeClusterNode
	keyMaster map[string]string
	calls     []string
}

func (f *fakeCluster) record(prefix string, args []any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, prefix+" "+joinArgs(args))
}

func joinArgs(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	return strings.Join(parts, " ")
}

func (f *fakeCluster) Do(_ context.Context, args ...any) (any, error) {
	f.record("routed:", args)
	if f.routed == nil {
		return nil, errors.New("unexpected routed command")
	}
	return f.routed(args)
}

func (f *fakeCluster) DoOnNode(_ context.Context, addr string, args ...any) (any, error) {
	f.record(addr+":", args)
	node, ok := f.nodes[addr]
	if !ok {
		return nil, fmt.Errorf("fake cluster: unexpected node %s", addr)
	}
	return node(args)
}

func (f *fakeCluster) ShardAddrs(context.Context) ([]string, error) {
	return f.shards, nil
}

func (f *fakeCluster) MasterForKey(_ context.Context, key string) (string, error) {
	addr, ok := f.keyMaster[key]
	if !ok {
		return "", errors.New("no master for key")
	}
	return addr, nil
}

func (f *fakeCluster) callsTo(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

const (
	m1 = "10.0.0.1:7001"
	m2 = "10.0.0.2:7002"
	m3 = "10.0.0.3:7003"
	r1 = "10.0.0.4:7004"
	r2 = "10.0.0.5:7005"
)

const testClusterNodes = "" +
	"id3333 10.0.0.3:7003@17003 master - 0 1700 3 connected 10923-16383\n" +
	"id1111 10.0.0.1:7001@17001,host-a myself,master - 0 0 1 connected 0-5460\n" +
	"id4444 10.0.0.4:7004@17004 slave id1111 0 1700 1 connected\n" +
	"id2222 10.0.0.2:7002@17002 master - 0 1700 2 connected 5461-10922 [5461->-id3333]\n" +
	"id5555 10.0.0.5:7005@17005 slave,fail id2222 0 1700 2 disconnected\n"

// scanNode 返回一个节点：CLUSTER NODES 回拓扑，PING 回 PONG，SCAN 按 cursor 查表。
func scanNode(pages map[string][]any) fakeClusterNode {
	return func(args []any) (any, error) {
		switch strings.ToUpper(fmt.Sprint(args[0])) {
		case "CLUSTER":
			return testClusterNodes, nil
		case "PING":
			return "PONG", nil
		case "SCAN":
			page, ok := pages[fmt.Sprint(args[1])]
			if !ok {
				return nil, fmt.Errorf("unexpected cursor %v", args[1])
			}
			return page, nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
}

func downNode(args []any) (any, error) {
	return nil, errors.New("dial tcp: connection refused")
}

func TestParseClusterNodes(t *testing.T) {
	nodes := parseClusterNodes(testClusterNodes)
	require.Len(t, nodes, 5)
	byAddr := map[string]clusterNodeInfo{}
	for _, n := range nodes {
		byAddr[n.Addr] = n
	}
	assert.Equal(t, "id1111", byAddr[m1].ID)
	assert.True(t, byAddr[m1].isMaster())
	assert.Equal(t, []slotRange{{0, 5460}}, byAddr[m1].Slots)
	assert.Equal(t, []slotRange{{5461, 10922}}, byAddr[m2].Slots, "migrating markers are not slots")
	assert.Equal(t, "id2222", byAddr[r2].MasterID)
	assert.False(t, byAddr[r2].isMaster())
	assert.Contains(t, byAddr[r2].Flags, "fail")
	assert.Equal(t, "disconnected", byAddr[r2].LinkState)
	assert.Equal(t, "0-5460", formatSlotRanges(byAddr[m1].Slots))
	assert.Equal(t, "0-5460,5462,5470-5471", formatSlotRanges([]slotRange{{0, 5460}, {5462, 5462}, {5470, 5471}}))
}

func TestScanClusterKeys(t *testing.T) {
	newCluster := func() *fakeCluster {
		return &fakeCluster{
			shards: []string{m3, m1, m2},
			nodes: map[string]fakeClusterNode{
				m1: scanNode(map[string][]any{"0": {"0", []any{"a", "b"}}}),
				m2: scanNode(map[string][]any{"0": {"9", []any{"c"}}, "9": {"0", []any{"d"}}}),
				m3: scanNode(map[string][]any{"0": {"0", []any{"e"}}}),
			},
		}
	}

	t.Run("merges masters and continues across nodes with a composite cursor", func(t *testing.T) {
		c := newCluster()
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 3})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, got.Keys)
		assert.True(t, got.HasMore)
		assert.NotEqual(t, "0", got.Cursor)
		assert.Equal(t, 1, got.ScannedMasters)
		assert.Equal(t, 3, got.TotalMasters)
		assert.Empty(t, got.Unreachable)
		assert.Empty(t, c.callsTo(m3+": SCAN"), "later master not scanned once the page is full")

		c2 := newCluster()
		next, err := scanKeys(context.Background(), c2, RedisScanRequest{Count: 3, Cursor: got.Cursor})
		require.NoError(t, err)
		assert.Equal(t, []string{"d", "e"}, next.Keys)
		assert.False(t, next.HasMore)
		assert.Equal(t, "0", next.Cursor)
		assert.Equal(t, 3, next.ScannedMasters)
		assert.Equal(t, 3, next.TotalMasters)
		assert.Empty(t, c2.callsTo(m1+": SCAN"), "finished master is not rescanned")
		assert.Equal(t, []string{m2 + ": SCAN 9 MATCH * COUNT 3"}, c2.callsTo(m2+": SCAN"))
	})

	t.Run("passes match and type to every master", func(t *testing.T) {
		c := &fakeCluster{shards: []string{m1}, nodes: map[string]fakeClusterNode{
			m1: scanNode(map[string][]any{"0": {"0", []any{"u:1"}}}),
		}}
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 10, Match: "u:*", Type: "hash"})
		require.NoError(t, err)
		assert.Equal(t, []string{"u:1"}, got.Keys)
		assert.Equal(t, []string{m1 + ": SCAN 0 MATCH u:* COUNT 10 TYPE hash"}, c.callsTo(m1+": SCAN"))
	})

	t.Run("skips unreachable master and reports it with its slots", func(t *testing.T) {
		c := newCluster()
		c.nodes[m2] = downNode
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 100})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "e"}, got.Keys)
		assert.False(t, got.HasMore)
		assert.Equal(t, 2, got.ScannedMasters)
		assert.Equal(t, 3, got.TotalMasters)
		assert.Equal(t, []RedisClusterNodeRef{{Addr: m2, Slots: "5461-10922"}}, got.Unreachable)
	})

	t.Run("master failing mid-scan is reported unreachable, keys so far kept", func(t *testing.T) {
		c := newCluster()
		calls := 0
		c.nodes[m2] = func(args []any) (any, error) {
			if fmt.Sprint(args[0]) == "SCAN" {
				calls++
				if calls > 1 {
					return nil, errors.New("i/o timeout")
				}
				return []any{"9", []any{"c"}}, nil
			}
			return scanNode(nil)(args)
		}
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 100})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c", "e"}, got.Keys)
		assert.Equal(t, 2, got.ScannedMasters)
		assert.Equal(t, []RedisClusterNodeRef{{Addr: m2, Slots: "5461-10922"}}, got.Unreachable)
	})

	t.Run("scope limits the scan to one master", func(t *testing.T) {
		c := newCluster()
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 100, Node: m3})
		require.NoError(t, err)
		assert.Equal(t, []string{"e"}, got.Keys)
		assert.Equal(t, 1, got.ScannedMasters)
		assert.Equal(t, 1, got.TotalMasters)
		assert.Empty(t, c.callsTo(m1+": SCAN"))
		assert.Empty(t, c.callsTo(m2+": SCAN"))
	})

	t.Run("scope that is not a master is rejected", func(t *testing.T) {
		c := newCluster()
		_, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 100, Node: r1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), r1)
	})

	t.Run("exact key goes straight to its slot", func(t *testing.T) {
		c := newCluster()
		c.routed = func(args []any) (any, error) { return int64(1), nil }
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Match: "user:1", Exact: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"user:1"}, got.Keys)
		assert.Equal(t, []string{"routed: EXISTS user:1"}, c.callsTo("routed:"))
		assert.Empty(t, c.callsTo(m1+": SCAN"))
	})

	t.Run("falls back to another shard for topology when one is down", func(t *testing.T) {
		c := newCluster()
		c.shards = []string{m2, m1}
		c.nodes[m2] = downNode
		got, err := scanKeys(context.Background(), c, RedisScanRequest{Count: 100, Node: m1})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got.Keys)
	})
}

func TestListClusterDatabases(t *testing.T) {
	dbsize := func(n int64) fakeClusterNode {
		return func(args []any) (any, error) {
			switch strings.ToUpper(fmt.Sprint(args[0])) {
			case "CLUSTER":
				return testClusterNodes, nil
			case "DBSIZE":
				return n, nil
			}
			return nil, fmt.Errorf("unexpected %v", args)
		}
	}
	c := &fakeCluster{shards: []string{m1, m2, m3}, nodes: map[string]fakeClusterNode{
		m1: dbsize(70), m2: downNode, m3: dbsize(65),
	}}

	got, err := listDatabases(context.Background(), c)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].DB)
	assert.Equal(t, int64(135), got[0].Keys)
	assert.Equal(t, []RedisClusterMasterKeys{
		{Addr: m1, Slots: "0-5460", Keys: 70, Reachable: true},
		{Addr: m2, Slots: "5461-10922", Keys: -1, Reachable: false},
		{Addr: m3, Slots: "10923-16383", Keys: 65, Reachable: true},
	}, got[0].Masters)
}

func TestClusterKeyDetailHasSlotAndNode(t *testing.T) {
	c := &fakeCluster{
		routed: func(args []any) (any, error) {
			switch strings.ToUpper(fmt.Sprint(args[0])) {
			case "TYPE":
				return "string", nil
			case "GET":
				return "v", nil
			}
			return int64(-1), nil
		},
		keyMaster: map[string]string{"{foo}bar": m3},
	}

	got, err := getKeyDetail(context.Background(), c, RedisKeyRequest{Key: "{foo}bar"})

	require.NoError(t, err)
	require.NotNil(t, got.Slot)
	assert.Equal(t, 12182, *got.Slot, "hash tag {foo} hashes like foo")
	assert.Equal(t, m3, got.Node)
	assert.Equal(t, "v", got.Value)
}

func TestKeySlot(t *testing.T) {
	assert.Equal(t, 12182, keySlot("foo"))
	assert.Equal(t, 866, keySlot("hello"))
	assert.Equal(t, 12182, keySlot("{foo}.x"))
	assert.Equal(t, keySlot("{}x"), keySlot("{}x"))
	assert.NotEqual(t, keySlot("x"), keySlot("{}x"), "empty hash tag hashes the whole key")
}

func TestStandaloneKeyDetailHasNoSlot(t *testing.T) {
	exec := &fakeRedisExecutor{results: []any{"string", int64(-1), int64(10), "v"}}
	got, err := getKeyDetail(context.Background(), exec, RedisKeyRequest{Key: "k"})
	require.NoError(t, err)
	assert.Nil(t, got.Slot)
	assert.Empty(t, got.Node)
}

func TestDeleteKeys(t *testing.T) {
	t.Run("standalone deletes with one DEL", func(t *testing.T) {
		exec := &fakeRedisExecutor{results: []any{int64(2)}}
		got, err := deleteKeys(context.Background(), exec, []string{"a", "b", "c"})
		require.NoError(t, err)
		assert.Equal(t, []any{"DEL", "a", "b", "c"}, exec.calls[0])
		assert.Equal(t, RedisDeleteResult{Deleted: 2}, got)
	})

	t.Run("standalone DEL error is returned", func(t *testing.T) {
		exec := &fakeRedisExecutor{errs: []error{errors.New("NOPERM")}}
		_, err := deleteKeys(context.Background(), exec, []string{"a"})
		require.ErrorContains(t, err, "NOPERM")
	})

	t.Run("cluster deletes key by key and reports partial failure", func(t *testing.T) {
		c := &fakeCluster{routed: func(args []any) (any, error) {
			switch args[1] {
			case "b":
				return nil, errors.New("CLUSTERDOWN The cluster is down")
			case "c":
				return int64(0), nil
			}
			return int64(1), nil
		}}
		got, err := deleteKeys(context.Background(), c, []string{"a", "b", "c", "d"})
		require.NoError(t, err)
		assert.Equal(t, []string{"routed: DEL a", "routed: DEL b", "routed: DEL c", "routed: DEL d"}, c.callsTo("routed:"))
		assert.Equal(t, int64(2), got.Deleted)
		assert.Equal(t, []RedisKeyFailure{{Key: "b", Error: "CLUSTERDOWN The cluster is down"}}, got.Failed)
	})
}

func TestValidateDBForMode(t *testing.T) {
	assert.NoError(t, validateDBForMode("cluster", -1))
	assert.NoError(t, validateDBForMode("cluster", 0))
	assert.Error(t, validateDBForMode("cluster", 3))
	assert.NoError(t, validateDBForMode("standalone", 3))
	assert.NoError(t, validateDBForMode("sentinel", 3))
}

// startPongServer 起一个对任何命令都回 +PONG 的 RESP 假节点（HELLO 回错误，客户端退回 RESP2）。
func startPongServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				for {
					header, err := r.ReadString('\n')
					if err != nil {
						return
					}
					n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "*")))
					args := make([]string, n)
					for i := range args {
						_, _ = r.ReadString('\n')
						v, _ := r.ReadString('\n')
						args[i] = strings.TrimSpace(v)
					}
					reply := "+PONG\r\n"
					if len(args) > 0 && strings.EqualFold(args[0], "HELLO") {
						reply = "-ERR unknown command\r\n"
					}
					if _, err := conn.Write([]byte(reply)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// CLUSTER NODES 中存在、但客户端拓扑（CLUSTER SLOTS）不含的节点（失去 slot 的故障主、宕机的从）
// 也要经集群的传输（拨号器：地址映射 / 隧道 / 代理）连接：可达的照常执行，不可达的返回真实拨号错误。
func TestGoRedisClusterExecutorDoOnNodeOutsideSlots(t *testing.T) {
	const liveAnnounced, deadAnnounced = "10.255.0.7:7007", "10.255.0.8:7008"
	deadLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadLocal := deadLn.Addr().String()
	require.NoError(t, deadLn.Close())
	mapped := map[string]string{liveAnnounced: startPongServer(t), deadAnnounced: deadLocal}

	client := redis.NewClusterClient(&redis.ClusterOptions{
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			return []redis.ClusterSlot{{Start: 0, End: 16383, Nodes: []redis.ClusterNode{{Addr: "10.255.0.1:7001"}}}}, nil
		},
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if target, ok := mapped[addr]; ok {
				addr = target
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		MaxRetries: -1,
	})
	t.Cleanup(func() { _ = client.Close() })
	exec := &goRedisClusterExecutor{goRedisExecutor: &goRedisExecutor{client: client}, cluster: client}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := exec.DoOnNode(ctx, liveAnnounced, "PING")
	require.NoError(t, err)
	assert.Equal(t, "PONG", got)

	_, err = exec.DoOnNode(ctx, deadAnnounced, "PING")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused", "the reason is the real dial error")
}
