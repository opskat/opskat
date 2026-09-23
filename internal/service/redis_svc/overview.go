package redis_svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// parseInfoFields 把 INFO 文本解析为 field → value（忽略 # 段标题与空行）。
func parseInfoFields(info string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(info, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[key] = value
		}
	}
	return fields
}

func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// clusterNodeStatus 归纳节点状态：集群判定的故障优先，其次是本机连不上。
func clusterNodeStatus(n clusterNodeInfo, reachErr error) (status, reason string) {
	switch {
	case slices.Contains(n.Flags, "fail"):
		status = "fail"
	case slices.Contains(n.Flags, "fail?"):
		status = "pfail"
	case reachErr != nil:
		status = "unreachable"
	default:
		return "ok", ""
	}
	if reachErr != nil {
		return status, reachErr.Error()
	}
	return status, fmt.Sprintf("flags=%s link=%s", strings.Join(n.Flags, ","), n.LinkState)
}

// clusterOverview 汇总集群概览：CLUSTER INFO 的状态与 slot 统计、节点拓扑（从节点挂在主节点下）、
// 各节点 INFO 指标，以及 infoNode（为空时取第一个主节点）的 INFO 原文。
func clusterOverview(ctx context.Context, c clusterExecutor, infoNode string) (RedisClusterOverview, error) {
	nodes, err := clusterTopology(ctx, c)
	if err != nil {
		return RedisClusterOverview{}, err
	}
	if infoNode == "" {
		for _, n := range nodes {
			if n.isMaster() && len(n.Slots) > 0 {
				infoNode = n.Addr
				break
			}
		}
	} else if !slices.ContainsFunc(nodes, func(n clusterNodeInfo) bool { return n.Addr == infoNode }) {
		return RedisClusterOverview{}, fmt.Errorf("%s is not a node of this Redis cluster; nodes: %s",
			infoNode, strings.Join(nodeAddrs(nodes), ", "))
	}

	infos, infoErrs := onEachNode(ctx, c, nodeAddrs(nodes), "INFO")
	out := RedisClusterOverview{InfoNode: infoNode}
	views := make([]RedisClusterNode, len(nodes))
	clusterInfoFrom := ""
	for i, n := range nodes {
		view := RedisClusterNode{
			ID:         n.ID,
			Addr:       n.Addr,
			Role:       "replica",
			MasterID:   n.MasterID,
			Slots:      formatSlotRanges(n.Slots),
			SlotCount:  n.slotCount(),
			Flags:      n.Flags,
			LinkState:  n.LinkState,
			Keys:       -1,
			UsedMemory: -1,
			OpsPerSec:  -1,
		}
		if n.isMaster() {
			view.Role = "master"
		}
		view.Status, view.Error = clusterNodeStatus(n, infoErrs[i])
		if infoErrs[i] != nil {
			logUnreachableNode(ctx, n.Addr, infoErrs[i])
		} else {
			text := fmt.Sprint(infos[i])
			fields := parseInfoFields(text)
			view.Reachable = true
			view.Keys = 0
			for _, db := range ParseKeyspaceInfo(text) {
				if db.DB == 0 {
					view.Keys = db.Keys
				}
			}
			view.UsedMemory = parseInt64(fields["used_memory"])
			view.UsedMemoryHuman = fields["used_memory_human"]
			view.OpsPerSec = parseInt64(fields["instantaneous_ops_per_sec"])
			if clusterInfoFrom == "" {
				clusterInfoFrom = n.Addr
			}
		}
		if n.Addr == infoNode {
			if infoErrs[i] != nil {
				out.InfoError = infoErrs[i].Error()
			} else {
				out.Info = fmt.Sprint(infos[i])
			}
		}
		if n.isMaster() && len(n.Slots) > 0 {
			if view.Reachable {
				out.TotalKeys += view.Keys
			} else {
				out.KeysPartial = true
			}
		}
		views[i] = view
	}

	if clusterInfoFrom == "" {
		return RedisClusterOverview{}, fmt.Errorf("load Redis cluster info: no reachable node")
	}
	result, err := c.DoOnNode(ctx, clusterInfoFrom, "CLUSTER", "INFO")
	if err != nil {
		return RedisClusterOverview{}, fmt.Errorf("load Redis cluster info: %w", err)
	}
	info := parseInfoFields(fmt.Sprint(result))
	out.State = info["cluster_state"]
	out.SlotsAssigned = int(parseInt64(info["cluster_slots_assigned"]))
	out.SlotsOK = int(parseInt64(info["cluster_slots_ok"]))
	out.SlotsPFail = int(parseInt64(info["cluster_slots_pfail"]))
	out.SlotsFail = int(parseInt64(info["cluster_slots_fail"]))

	out.Masters = groupClusterNodes(views)
	return out, nil
}

// groupClusterNodes 把从节点挂到所属主节点下；找不到主节点的从节点留在顶层。输入已按地址排序。
func groupClusterNodes(views []RedisClusterNode) []RedisClusterNode {
	masterIdx := map[string]int{}
	var top []RedisClusterNode
	for _, v := range views {
		if v.Role == "master" {
			masterIdx[v.ID] = len(top)
			top = append(top, v)
		}
	}
	for _, v := range views {
		if v.Role == "master" {
			continue
		}
		if i, ok := masterIdx[v.MasterID]; ok {
			top[i].Replicas = append(top[i].Replicas, v)
		} else {
			top = append(top, v)
		}
	}
	return top
}

// sentinelSource 是一个已连上的哨兵：exec 发送 SENTINEL 命令，addr 为资产配置中的该哨兵地址。
type sentinelSource struct {
	exec redisExecutor
	addr string
}

// sentinelStatus 由哨兵 flags 归纳状态。
func sentinelStatus(flags string) string {
	parts := strings.Split(flags, ",")
	switch {
	case slices.Contains(parts, "o_down"):
		return "odown"
	case slices.Contains(parts, "s_down"):
		return "sdown"
	case slices.Contains(parts, "disconnected"):
		return "disconnected"
	}
	return "ok"
}

// toStringMap 把 SENTINEL 的回复（RESP3 map 或 RESP2 扁平数组）转为 map。
func toStringMap(v any) map[string]string {
	out := map[string]string{}
	switch m := v.(type) {
	case map[any]any:
		for k, val := range m {
			out[fmt.Sprint(k)] = fmt.Sprint(val)
		}
	case map[string]any:
		for k, val := range m {
			out[k] = fmt.Sprint(val)
		}
	case map[string]string:
		for k, val := range m {
			out[k] = val
		}
	case []any:
		for i := 0; i+1 < len(m); i += 2 {
			out[fmt.Sprint(m[i])] = fmt.Sprint(m[i+1])
		}
	}
	return out
}

func toStringMaps(v any) []map[string]string {
	items, _ := v.([]any)
	out := make([]map[string]string, 0, len(items))
	for _, item := range items {
		out = append(out, toStringMap(item))
	}
	return out
}

func sentinelAddr(m map[string]string) string { return net.JoinHostPort(m["ip"], m["port"]) }

// replicaReplication 是主节点 INFO replication 中一个从节点的复制状态。
type replicaReplication struct{ offset, lag int64 }

// parseMasterReplication 解析主节点 INFO replication：master_repl_offset 与各从节点（按 ip:port）。
func parseMasterReplication(info string) (int64, map[string]replicaReplication) {
	fields := parseInfoFields(info)
	replicas := map[string]replicaReplication{}
	for key, value := range fields {
		if !strings.HasPrefix(key, "slave") {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(key, "slave")); err != nil {
			continue
		}
		kv := map[string]string{}
		for _, part := range strings.Split(value, ",") {
			if k, v, ok := strings.Cut(part, "="); ok {
				kv[k] = v
			}
		}
		replicas[net.JoinHostPort(kv["ip"], kv["port"])] = replicaReplication{offset: parseInt64(kv["offset"]), lag: parseInt64(kv["lag"])}
	}
	return parseInt64(fields["master_repl_offset"]), replicas
}

// sentinelOverview 汇总哨兵概览：组、当前主节点与 quorum、从节点复制状态、哨兵列表。
// data 为连向当前主节点的执行器（dataErr 非空时为 nil），只用于读取复制延迟。
func sentinelOverview(ctx context.Context, s sentinelSource, data redisExecutor, dataErr error, masterName string) (RedisSentinelOverview, error) {
	masterRaw, err := s.exec.Do(ctx, "SENTINEL", "MASTER", masterName)
	if err != nil {
		return RedisSentinelOverview{}, fmt.Errorf("load Redis sentinel master %q: %w", masterName, err)
	}
	replicasRaw, err := s.exec.Do(ctx, "SENTINEL", "REPLICAS", masterName)
	if err != nil {
		return RedisSentinelOverview{}, fmt.Errorf("load Redis sentinel replicas: %w", err)
	}
	sentinelsRaw, err := s.exec.Do(ctx, "SENTINEL", "SENTINELS", masterName)
	if err != nil {
		return RedisSentinelOverview{}, fmt.Errorf("load Redis sentinels: %w", err)
	}

	master := toStringMap(masterRaw)
	out := RedisSentinelOverview{
		MasterName: masterName,
		Master:     RedisSentinelNode{Addr: sentinelAddr(master), Flags: master["flags"], Status: sentinelStatus(master["flags"])},
		Quorum:     int(parseInt64(master["quorum"])),
		Sentinels:  []RedisSentinelNode{{Addr: s.addr, Flags: "sentinel", Status: "ok", Queried: true}},
	}

	masterOffset := int64(-1)
	var replication map[string]replicaReplication
	if data != nil {
		info, err := data.Do(ctx, "INFO", "replication")
		if err != nil {
			dataErr = err
		} else {
			masterOffset, replication = parseMasterReplication(fmt.Sprint(info))
		}
	}
	if dataErr != nil {
		out.MasterError = dataErr.Error()
	}

	for _, r := range toStringMaps(replicasRaw) {
		replica := RedisSentinelReplica{
			Addr:       sentinelAddr(r),
			Flags:      r["flags"],
			Status:     sentinelStatus(r["flags"]),
			LinkStatus: r["master-link-status"],
			Offset:     parseInt64(r["slave-repl-offset"]),
			LagSeconds: -1,
			LagBytes:   -1,
		}
		if repl, ok := replication[replica.Addr]; ok {
			replica.Offset = repl.offset
			replica.LagSeconds = repl.lag
			replica.LagBytes = masterOffset - repl.offset
		}
		out.Replicas = append(out.Replicas, replica)
	}
	for _, sn := range toStringMaps(sentinelsRaw) {
		out.Sentinels = append(out.Sentinels, RedisSentinelNode{Addr: sentinelAddr(sn), Flags: sn["flags"], Status: sentinelStatus(sn["flags"])})
	}
	return out, nil
}

// ClusterOverview 返回集群概览；infoNode 为要返回 INFO 的节点地址，空表示第一个主节点。
func (s *Service) ClusterOverview(ctx context.Context, assetID int64, infoNode string) (RedisClusterOverview, error) {
	var out RedisClusterOverview
	err := s.withClient(ctx, assetID, -1, func(ctx context.Context, exec redisExecutor) error {
		cluster, ok := exec.(clusterExecutor)
		if !ok {
			return fmt.Errorf("该 Redis 资产不是集群模式")
		}
		var err error
		out, err = clusterOverview(ctx, cluster, infoNode)
		return err
	})
	return out, err
}

// SentinelOverview 返回哨兵模式概览：依次尝试资产配置的哨兵直到连上一个，
// 另连当前主节点读取复制延迟（连不上时概览仍返回，MasterError 说明原因）。
func (s *Service) SentinelOverview(ctx context.Context, assetID int64) (RedisSentinelOverview, error) {
	target, err := loadRedisTarget(ctx, assetID)
	if err != nil {
		return RedisSentinelOverview{}, err
	}
	if target.cfg.EffectiveMode() != asset_entity.RedisModeSentinel {
		return RedisSentinelOverview{}, fmt.Errorf("该 Redis 资产不是哨兵模式")
	}
	opCtx, cancel := target.opContext(ctx)
	defer cancel()

	var source sentinelSource
	var errs []error
	for _, addr := range target.cfg.Nodes {
		client, closer, err := s.dialSentinel(opCtx, target, addr)
		if err != nil {
			logUnreachableNode(opCtx, addr, err)
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
			continue
		}
		defer closeRedisClient(client, closer)
		source = sentinelSource{exec: &goRedisExecutor{client: client}, addr: addr}
		break
	}
	if source.exec == nil {
		return RedisSentinelOverview{}, fmt.Errorf("连接 Redis 哨兵失败: %w", errors.Join(errs...))
	}

	var data redisExecutor
	client, closer, dataErr := connpool.DialRedis(opCtx, target.asset, target.cfg, target.password, s.sshPool)
	if dataErr == nil {
		defer closeRedisClient(client, closer)
		data = &goRedisExecutor{client: client}
	}
	return sentinelOverview(opCtx, source, data, dataErr, target.cfg.MasterName)
}

// dialSentinel 直连一个哨兵节点：以单机方式拨号（沿用资产的隧道 / 代理 / TLS 设置），
// 使用哨兵认证。
func (s *Service) dialSentinel(ctx context.Context, target redisTarget, addr string) (redis.UniversalClient, io.Closer, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sentinel address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sentinel address %q: %w", addr, err)
	}
	cfg := *target.cfg
	cfg.Mode = asset_entity.RedisModeStandalone
	cfg.Host, cfg.Port = host, port
	cfg.Database = 0
	cfg.Username = target.cfg.SentinelUsername
	return connpool.DialRedis(ctx, target.asset, &cfg, target.cfg.SentinelPassword, s.sshPool)
}
