package redis_svc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// probeNodeTimeout 限定逐个直连集群宣告节点的等待时间。
const probeNodeTimeout = 3 * time.Second

// Probe 用一份未保存的配置连接 Redis 并探测部署模式与拓扑（资产表单「测试连接」与自动识别）。
// password 为明文数据节点密码；cfg.Proxy.Password 与 cfg.SentinelPassword 须为明文。
// 数据节点连不上（含认证失败）返回错误；哨兵模式下哨兵需要认证、或未填 master_name 时，
// 只返回哨兵探测结果而不连接数据节点。
func (s *Service) Probe(ctx context.Context, cfg *asset_entity.RedisConfig, password string) (RedisProbeResult, error) {
	mode := cfg.EffectiveMode()
	logger.Ctx(ctx).Info("redis probe start", zap.String("mode", mode), zap.String("host", cfg.Host), zap.Strings("nodes", cfg.Nodes))
	out, err := s.probe(ctx, cfg, password)
	if err != nil {
		logger.Ctx(ctx).Error("redis probe failed", zap.String("mode", mode), zap.Error(err))
		return RedisProbeResult{}, err
	}
	logger.Ctx(ctx).Info("redis probe done", zap.String("mode", mode), zap.String("detectedMode", out.DetectedMode))
	return out, nil
}

func (s *Service) probe(ctx context.Context, cfg *asset_entity.RedisConfig, password string) (RedisProbeResult, error) {
	asset := &asset_entity.Asset{}
	mode := cfg.EffectiveMode()
	var out RedisProbeResult
	if mode == asset_entity.RedisModeSentinel {
		target := redisTarget{asset: asset, cfg: cfg, password: password}
		dial := func(ctx context.Context, addr string) (redisExecutor, func(), error) {
			client, closer, err := s.dialSentinel(ctx, target, addr)
			if err != nil {
				return nil, nil, err
			}
			return &goRedisExecutor{client: client}, func() { closeRedisClient(client, closer) }, nil
		}
		sentinel, detected, err := probeSentinels(ctx, cfg.Nodes, cfg.MasterName, cfg.NodeAddressMap, dial)
		if err != nil {
			return RedisProbeResult{}, fmt.Errorf("连接 Redis 哨兵失败: %w", err)
		}
		out.Sentinel, out.DetectedMode = sentinel, detected
		if sentinel.AuthRequired || cfg.MasterName == "" {
			out.ModeMismatch = modeMismatch(out.DetectedMode, mode)
			return out, nil
		}
	}

	client, closer, err := connpool.DialRedis(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		return RedisProbeResult{}, err
	}
	defer closeRedisClient(client, closer)
	exec := &goRedisExecutor{client: client}
	switch mode {
	case asset_entity.RedisModeStandalone:
		out.DetectedMode = detectRedisMode(ctx, exec)
	case asset_entity.RedisModeCluster:
		cluster := &goRedisClusterExecutor{goRedisExecutor: exec, cluster: client.(*redis.ClusterClient)}
		out.Cluster, out.DetectedMode = probeCluster(ctx, cluster)
	}
	out.ModeMismatch = modeMismatch(out.DetectedMode, mode)
	return out, nil
}

func modeMismatch(detected, configured string) bool {
	return detected != "" && detected != configured
}

// detectRedisMode 返回 INFO server 的 redis_mode；读不到时为空。
func detectRedisMode(ctx context.Context, exec redisExecutor) string {
	info, err := exec.Do(ctx, "INFO", "server")
	if err != nil {
		logger.Ctx(ctx).Warn("redis probe: read INFO server failed", zap.Error(err))
		return ""
	}
	return parseInfoFields(fmt.Sprint(info))["redis_mode"]
}

// probeCluster 读取集群主从数，并经集群传输（配置的隧道 / 代理与地址映射）并发 PING
// CLUSTER NODES 中的每个节点（含不在 CLUSTER SLOTS 里的故障主、宕机从），找出连不上的；
// 回复了错误（如 NOAUTH）的节点视为可达。集群状态与 redis_mode 从第一个应答 PING 的节点读取
// （不经 slot 路由，免得落到不可达的主节点上）。拓扑读不到时返回 nil。
func probeCluster(ctx context.Context, c clusterExecutor) (*RedisProbeCluster, string) {
	nodes, err := clusterTopology(ctx, c)
	if err != nil {
		logger.Ctx(ctx).Warn("redis probe: load cluster topology failed", zap.Error(err))
		return nil, ""
	}
	out := &RedisProbeCluster{UnreachableNodes: []string{}}
	for _, n := range nodes {
		switch {
		case n.isMaster():
			out.Masters++
		case slices.Contains(n.Flags, "slave"):
			out.Replicas++
		}
	}

	pingCtx, cancel := context.WithTimeout(ctx, probeNodeTimeout)
	defer cancel()
	_, errs := onEachNode(pingCtx, c, nodeAddrs(nodes), "PING")
	answered := ""
	for i, err := range errs {
		switch {
		case err == nil:
			if answered == "" {
				answered = nodes[i].Addr
			}
		case isRedisReply(err):
		default:
			logUnreachableNode(ctx, nodes[i].Addr, err)
			out.UnreachableNodes = append(out.UnreachableNodes, nodes[i].Addr)
		}
	}
	if answered == "" {
		logger.Ctx(ctx).Warn("redis probe: no cluster node answered PING")
		return out, ""
	}
	node := nodeExecutor{c: c, addr: answered}
	if raw, err := node.Do(ctx, "CLUSTER", "INFO"); err != nil {
		logger.Ctx(ctx).Warn("redis probe: read CLUSTER INFO failed", zap.String("addr", answered), zap.Error(err))
	} else {
		out.State = parseInfoFields(fmt.Sprint(raw))["cluster_state"]
	}
	return out, detectRedisMode(ctx, node)
}

// nodeExecutor 把集群中的单个节点适配为 redisExecutor。
type nodeExecutor struct {
	c    clusterExecutor
	addr string
}

func (n nodeExecutor) Do(ctx context.Context, args ...any) (any, error) {
	return n.c.DoOnNode(ctx, n.addr, args...)
}

// sentinelDialFunc 连接一个已配置的哨兵；成功时返回执行器与关闭函数。
type sentinelDialFunc func(ctx context.Context, addr string) (redisExecutor, func(), error)

// probeSentinels 依次连接已配置的哨兵，从第一个应答的哨兵读取 redis_mode、监控组与其他哨兵。
// 没有哨兵应答时：有哨兵回复认证错误则返回 AuthRequired，否则返回连接错误。
func probeSentinels(ctx context.Context, nodes []string, masterName string, addrMap map[string]string, dial sentinelDialFunc) (*RedisProbeSentinel, string, error) {
	var errs []error
	authRequired := false
	for _, addr := range nodes {
		exec, closeFn, err := dial(ctx, addr)
		if err != nil {
			authRequired = authRequired || isAuthError(err)
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
			continue
		}
		out := readSentinel(ctx, exec, nodes, masterName, addrMap)
		mode := detectRedisMode(ctx, exec)
		closeFn()
		return out, mode, nil
	}
	if authRequired {
		return &RedisProbeSentinel{AuthRequired: true, Groups: []RedisProbeSentinelGroup{}, OtherSentinels: []string{}}, "", nil
	}
	return nil, "", errors.Join(errs...)
}

// readSentinel 读取监控组；选中组（masterName，未填且只有一个组时取该组）存在时
// 补充其当前主节点与尚未配置的其他哨兵。读取失败的部分保持缺省。
func readSentinel(ctx context.Context, exec redisExecutor, nodes []string, masterName string, addrMap map[string]string) *RedisProbeSentinel {
	out := &RedisProbeSentinel{Groups: []RedisProbeSentinelGroup{}, OtherSentinels: []string{}}
	raw, err := exec.Do(ctx, "SENTINEL", "MASTERS")
	if err != nil {
		logger.Ctx(ctx).Warn("redis probe: read SENTINEL MASTERS failed", zap.Error(err))
		return out
	}
	for _, m := range toStringMaps(raw) {
		out.Groups = append(out.Groups, RedisProbeSentinelGroup{
			Name:       m["name"],
			MasterAddr: sentinelAddr(m),
			Replicas:   int(parseInt64(m["num-slaves"])),
		})
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Name < out.Groups[j].Name })

	group := masterName
	if group == "" && len(out.Groups) == 1 {
		group = out.Groups[0].Name
	}
	idx := slices.IndexFunc(out.Groups, func(g RedisProbeSentinelGroup) bool { return g.Name == group })
	if idx < 0 {
		return out
	}
	out.MasterAddr = out.Groups[idx].MasterAddr

	raw, err = exec.Do(ctx, "SENTINEL", "SENTINELS", group)
	if err != nil {
		logger.Ctx(ctx).Warn("redis probe: read SENTINEL SENTINELS failed", zap.String("masterName", group), zap.Error(err))
		return out
	}
	for _, sn := range toStringMaps(raw) {
		addr := sentinelAddr(sn)
		if slices.Contains(nodes, addr) {
			continue
		}
		if mapped, ok := addrMap[addr]; ok && slices.Contains(nodes, mapped) {
			continue
		}
		out.OtherSentinels = append(out.OtherSentinels, addr)
	}
	return out
}

// isAuthError 判断 err 是否为服务端的认证失败回复（未认证或密码错误）。
func isAuthError(err error) bool {
	var redisErr redis.Error
	if !errors.As(err, &redisErr) {
		return false
	}
	msg := redisErr.Error()
	return strings.HasPrefix(msg, "NOAUTH") || strings.HasPrefix(msg, "WRONGPASS") || strings.HasPrefix(msg, "ERR invalid password")
}
