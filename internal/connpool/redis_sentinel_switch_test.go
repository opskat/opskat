package connpool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// respServer 是只认少量命令的 RESP 假服务：handle 返回原始回复，空串表示不回复（订阅连接保持）。
type respServer struct {
	ln     net.Listener
	mu     sync.Mutex
	conns  []net.Conn
	handle func(args []string) string
}

func startRESPServer(t *testing.T, handle func(args []string) string) *respServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &respServer{ln: ln, handle: handle}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(s.stop)
	return s
}

func (s *respServer) addr() string { return s.ln.Addr().String() }

// stop 关闭监听与全部已建立的连接，模拟节点宕机。
func (s *respServer) stop() {
	_ = s.ln.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

func (s *respServer) serve(c net.Conn) {
	r := bufio.NewReader(c)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return
		}
		reply := "+OK\r\n" // 连接握手（AUTH / CLIENT SETINFO 等）
		if cmd := strings.ToUpper(args[0]); cmd != "AUTH" && cmd != "CLIENT" {
			reply = s.handle(args)
		}
		if reply != "" {
			if _, err := io.WriteString(c, reply); err != nil {
				return
			}
		}
	}
}

func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := range args {
		if _, err := r.ReadString('\n'); err != nil { // $<len>
			return nil, err
		}
		v, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		args[i] = strings.TrimRight(v, "\r\n")
	}
	return args, nil
}

func bulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

func masterServer(args []string) string {
	switch strings.ToUpper(args[0]) {
	case "PING":
		return "+PONG\r\n"
	default:
		return "-ERR unknown command\r\n"
	}
}

// 哨兵报告新主节点（主从切换）时记入结构化日志：旧主节点、新主节点、组名与资产，不含密钥。
func TestDialRedisLogsSentinelMasterSwitch(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })

	masterA := startRESPServer(t, masterServer)
	masterB := startRESPServer(t, masterServer)
	var mu sync.Mutex
	current := masterA.addr()
	sentinel := startRESPServer(t, func(args []string) string {
		switch strings.ToUpper(args[0]) {
		case "SENTINEL":
			switch strings.ToLower(args[1]) {
			case "get-master-addr-by-name":
				mu.Lock()
				host, port, _ := net.SplitHostPort(current)
				mu.Unlock()
				return "*2\r\n" + bulk(host) + bulk(port)
			default:
				return "*0\r\n"
			}
		case "SUBSCRIBE":
			var out strings.Builder
			for i, ch := range args[1:] {
				out.WriteString("*3\r\n" + bulk("subscribe") + bulk(ch) + ":" + strconv.Itoa(i+1) + "\r\n")
			}
			return out.String()
		case "PING":
			return "+PONG\r\n"
		default:
			return "-ERR unknown command\r\n"
		}
	})

	cfg := &asset_entity.RedisConfig{
		Mode:             asset_entity.RedisModeSentinel,
		Nodes:            []string{sentinel.addr()},
		MasterName:       "mymaster",
		SentinelPassword: "sentinel-plain-secret",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, closer, err := DialRedis(ctx, &asset_entity.Asset{ID: 42}, cfg, "", nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		if closer != nil {
			_ = closer.Close()
		}
	})
	assert.Empty(t, logs.FilterMessage("redis sentinel master switched").All(), "first master is not a switch")

	mu.Lock()
	current = masterB.addr()
	mu.Unlock()
	masterA.stop()

	require.Eventually(t, func() bool {
		return client.Ping(ctx).Err() == nil
	}, 5*time.Second, 50*time.Millisecond)

	switched := logs.FilterMessage("redis sentinel master switched").All()
	require.Len(t, switched, 1)
	fields := switched[0].ContextMap()
	assert.Equal(t, masterA.addr(), fields["from"])
	assert.Equal(t, masterB.addr(), fields["to"])
	assert.Equal(t, "mymaster", fields["masterName"])
	assert.Equal(t, int64(42), fields["assetID"])
	assert.NotContains(t, fmt.Sprint(fields), "sentinel-plain-secret")
}
