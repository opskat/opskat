package connpooltest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// RedisServer 是一个 Redis 假节点，统计客户端建立过的 TCP 连接数，用来断言「拨号了几次」。
// 应答只覆盖浏览器常用的几条：握手（HELLO 回错误让客户端退回 RESP2，AUTH / CLIENT / SELECT 回 OK）、
// PING、SCAN（空库）、TYPE（none）、INFO（空）；集群节点另答 CLUSTER SLOTS / NODES / INFO
// （单节点持有全部 slot）。其余命令回错误。
type RedisServer struct {
	ln       net.Listener
	cluster  bool
	accepted atomic.Int64
	mu       sync.Mutex
	conns    []net.Conn
}

// StartRedisServer 在 127.0.0.1 的随机端口上起一个单机 RedisServer，测试结束时关闭。
func StartRedisServer(t *testing.T) *RedisServer {
	return startRedisServer(t, false)
}

// StartRedisClusterNode 起一个独自持有全部 slot 的单节点集群。
func StartRedisClusterNode(t *testing.T) *RedisServer {
	return startRedisServer(t, true)
}

func startRedisServer(t *testing.T, cluster bool) *RedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &RedisServer{ln: ln, cluster: cluster}
	go s.accept()
	t.Cleanup(s.stop)
	return s
}

// Host / Port 是监听地址。
func (s *RedisServer) Host() string { return s.ln.Addr().(*net.TCPAddr).IP.String() }
func (s *RedisServer) Port() int    { return s.ln.Addr().(*net.TCPAddr).Port }

// Addr 是 host:port。
func (s *RedisServer) Addr() string { return s.ln.Addr().String() }

// Accepted 返回至今接受过的 TCP 连接数。
func (s *RedisServer) Accepted() int { return int(s.accepted.Load()) }

func (s *RedisServer) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.accepted.Add(1)
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.serve(c)
	}
}

func (s *RedisServer) stop() {
	_ = s.ln.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

func (s *RedisServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if _, err := io.WriteString(c, s.reply(args)); err != nil {
			return
		}
	}
}

func (s *RedisServer) reply(args []string) string {
	if len(args) == 0 {
		return "-ERR empty command\r\n"
	}
	if s.cluster && strings.EqualFold(args[0], "CLUSTER") && len(args) > 1 {
		return s.clusterReply(strings.ToUpper(args[1]))
	}
	switch strings.ToUpper(args[0]) {
	case "AUTH", "CLIENT", "SELECT":
		return "+OK\r\n"
	case "PING":
		return "+PONG\r\n"
	case "SCAN":
		return "*2\r\n$1\r\n0\r\n*0\r\n"
	case "TYPE":
		return "+none\r\n"
	case "INFO":
		return "$0\r\n\r\n"
	default:
		return fmt.Sprintf("-ERR unknown command '%s'\r\n", args[0])
	}
}

func (s *RedisServer) clusterReply(sub string) string {
	switch sub {
	case "SLOTS":
		return fmt.Sprintf("*1\r\n*3\r\n:0\r\n:16383\r\n*2\r\n%s:%d\r\n", bulk(s.Host()), s.Port())
	case "NODES":
		return bulk(fmt.Sprintf("0000000000000000000000000000000000000001 %s@1%d myself,master - 0 0 1 connected 0-16383\n", s.Addr(), s.Port()))
	case "INFO":
		return bulk("cluster_state:ok\r\ncluster_slots_assigned:16384\r\ncluster_known_nodes:1\r\n")
	default:
		return fmt.Sprintf("-ERR unknown CLUSTER subcommand '%s'\r\n", sub)
	}
}

func bulk(v string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(v), v) }

func readCommand(r *bufio.Reader) ([]string, error) {
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
