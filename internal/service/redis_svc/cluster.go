package redis_svc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// errUnknownClusterNode 表示地址不属于当前集群客户端已知的节点。
var errUnknownClusterNode = errors.New("node is not part of the cluster")

// clusterExecutor 是集群模式下的执行能力。Do（继承自 redisExecutor）按命令中 key 的 slot 路由；
// 其余方法用于按节点操作。生产实现为 goRedisClusterExecutor。
type clusterExecutor interface {
	redisExecutor
	// DoOnNode 在地址为 addr 的节点（主或从）上执行；addr 不是已知节点时返回 errUnknownClusterNode。
	DoOnNode(ctx context.Context, addr string, args ...any) (any, error)
	// ShardAddrs 返回客户端已知的全部节点地址（不做网络探测），用于找一个可达节点读取拓扑。
	ShardAddrs(ctx context.Context) ([]string, error)
	// MasterForKey 返回负责 key 所在 slot 的主节点地址。
	MasterForKey(ctx context.Context, key string) (string, error)
}

// validateDBForMode 拒绝对集群选择 db0 以外的库（db<0 表示使用资产配置的默认库）。
func validateDBForMode(mode string, db int) error {
	if mode == asset_entity.RedisModeCluster && db > 0 {
		return fmt.Errorf("集群只有 db0，不能选择 db%d", db)
	}
	return nil
}

// slotRange 是闭区间 [Start, End] 的 slot 范围。
type slotRange struct{ Start, End int }

// clusterNodeInfo 是 CLUSTER NODES 的一行。
type clusterNodeInfo struct {
	ID        string
	Addr      string
	Flags     []string
	MasterID  string
	LinkState string
	Slots     []slotRange
}

func (n clusterNodeInfo) isMaster() bool { return slices.Contains(n.Flags, "master") }

func (n clusterNodeInfo) slotCount() int {
	total := 0
	for _, r := range n.Slots {
		total += r.End - r.Start + 1
	}
	return total
}

// parseClusterNodes 解析 CLUSTER NODES 回复：
// <id> <ip:port@cport[,hostname]> <flags> <master|-> <ping> <pong> <epoch> <link> <slot>...
// 迁移中的 slot 标记（[slot->-id] / [slot-<-id]）不计入 slot 范围。
func parseClusterNodes(text string) []clusterNodeInfo {
	var nodes []clusterNodeInfo
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		addr, _, _ := strings.Cut(fields[1], "@")
		addr, _, _ = strings.Cut(addr, ",")
		node := clusterNodeInfo{
			ID:        fields[0],
			Addr:      addr,
			Flags:     strings.Split(fields[2], ","),
			LinkState: fields[7],
		}
		if fields[3] != "-" {
			node.MasterID = fields[3]
		}
		for _, item := range fields[8:] {
			if strings.HasPrefix(item, "[") {
				continue
			}
			startText, endText, isRange := strings.Cut(item, "-")
			start, err := strconv.Atoi(startText)
			if err != nil {
				continue
			}
			end := start
			if isRange {
				if end, err = strconv.Atoi(endText); err != nil {
					continue
				}
			}
			node.Slots = append(node.Slots, slotRange{Start: start, End: end})
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// formatSlotRanges 把 slot 范围格式化为 "0-5460,10923" 形式。
func formatSlotRanges(ranges []slotRange) string {
	parts := make([]string, 0, len(ranges))
	for _, r := range ranges {
		if r.Start == r.End {
			parts = append(parts, strconv.Itoa(r.Start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", r.Start, r.End))
		}
	}
	return strings.Join(parts, ",")
}

// keySlot 按 Redis 集群规则计算 key 的 slot：CRC16(XMODEM) mod 16384，
// key 含非空 hash tag（第一个 { 与其后第一个 } 之间）时只哈希 tag。
func keySlot(key string) int {
	if start := strings.IndexByte(key, '{'); start >= 0 {
		if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
			key = key[start+1 : start+1+end]
		}
	}
	var crc uint16
	for i := 0; i < len(key); i++ {
		crc ^= uint16(key[i]) << 8
		for b := 0; b < 8; b++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return int(crc) % 16384
}

// clusterTopology 从第一个可达节点读取 CLUSTER NODES，按地址排序返回。
func clusterTopology(ctx context.Context, c clusterExecutor) ([]clusterNodeInfo, error) {
	addrs, err := c.ShardAddrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Redis cluster nodes: %w", err)
	}
	var errs []error
	for _, addr := range addrs {
		result, err := c.DoOnNode(ctx, addr, "CLUSTER", "NODES")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
			continue
		}
		nodes := parseClusterNodes(fmt.Sprint(result))
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Addr < nodes[j].Addr })
		return nodes, nil
	}
	return nil, fmt.Errorf("load Redis cluster nodes: no reachable node: %w", errors.Join(errs...))
}

// clusterMasters 返回负责 slot 的主节点（按地址排序）。
func clusterMasters(ctx context.Context, c clusterExecutor) ([]clusterNodeInfo, error) {
	nodes, err := clusterTopology(ctx, c)
	if err != nil {
		return nil, err
	}
	masters := make([]clusterNodeInfo, 0, len(nodes))
	for _, n := range nodes {
		if n.isMaster() && len(n.Slots) > 0 {
			masters = append(masters, n)
		}
	}
	return masters, nil
}

// onEachNode 并发地对每个地址执行同一条命令，按下标返回结果与错误。
func onEachNode(ctx context.Context, c clusterExecutor, addrs []string, args ...any) ([]any, []error) {
	results := make([]any, len(addrs))
	errs := make([]error, len(addrs))
	var wg sync.WaitGroup
	for i, addr := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = c.DoOnNode(ctx, addr, args...)
		}()
	}
	wg.Wait()
	return results, errs
}

func nodeAddrs(nodes []clusterNodeInfo) []string {
	addrs := make([]string, len(nodes))
	for i, n := range nodes {
		addrs[i] = n.Addr
	}
	return addrs
}

func logUnreachableNode(ctx context.Context, addr string, err error) {
	logger.Ctx(ctx).Warn("redis cluster node unreachable", zap.String("addr", addr), zap.Error(err))
}

// isRedisReply 判断 err 是服务端回复的错误（如 NOPERM），而不是连接层失败。
func isRedisReply(err error) bool {
	var redisErr redis.Error
	return errors.As(err, &redisErr)
}

// 集群扫描游标形如 "<节点内游标>@<主节点地址>"；"0" 表示从头开始 / 已扫完。
func encodeClusterCursor(nodeCursor, addr string) string { return nodeCursor + "@" + addr }

func decodeClusterCursor(cursor string) (nodeCursor, addr string, err error) {
	nodeCursor, addr, ok := strings.Cut(cursor, "@")
	if !ok || nodeCursor == "" || addr == "" {
		return "", "", fmt.Errorf("invalid Redis cluster scan cursor %q", cursor)
	}
	return nodeCursor, addr, nil
}

// scanClusterKeys 在扫描范围内的主节点上按地址顺序依次 SCAN 并合并结果：凑满 req.Count
// 即返回，游标记录停在哪个主节点及其节点内游标，下一页从那里续扫。
// 每页先并发 PING 范围内的主节点，不可达的跳过并在 Unreachable 中报告。
func scanClusterKeys(ctx context.Context, c clusterExecutor, req RedisScanRequest) (RedisScanResponse, error) {
	masters, err := clusterMasters(ctx, c)
	if err != nil {
		return RedisScanResponse{}, err
	}
	if req.Node != "" {
		idx := slices.IndexFunc(masters, func(n clusterNodeInfo) bool { return n.Addr == req.Node })
		if idx < 0 {
			return RedisScanResponse{}, fmt.Errorf("%s is not a master of this Redis cluster; masters: %s",
				req.Node, strings.Join(nodeAddrs(masters), ", "))
		}
		masters = masters[idx : idx+1]
	}

	start, startCursor := 0, "0"
	if req.Cursor != "0" {
		nodeCursor, addr, err := decodeClusterCursor(req.Cursor)
		if err != nil {
			return RedisScanResponse{}, err
		}
		start = sort.Search(len(masters), func(i int) bool { return masters[i].Addr >= addr })
		if start < len(masters) && masters[start].Addr == addr {
			startCursor = nodeCursor
		}
	}

	reachable := make([]bool, len(masters))
	_, pingErrs := onEachNode(ctx, c, nodeAddrs(masters), "PING")
	scanned := 0
	for i, err := range pingErrs {
		reachable[i] = err == nil
		if err != nil {
			logUnreachableNode(ctx, masters[i].Addr, err)
		} else if i < start {
			scanned++
		}
	}

	keys := make([]string, 0)
	cursor := "0"
scan:
	for i := start; i < len(masters); i++ {
		if !reachable[i] {
			continue
		}
		nodeCursor := "0"
		if i == start {
			nodeCursor = startCursor
		}
		for {
			args := []any{"SCAN", nodeCursor, "MATCH", req.Match, "COUNT", req.Count}
			if req.Type != "" {
				args = append(args, "TYPE", req.Type)
			}
			result, err := c.DoOnNode(ctx, masters[i].Addr, args...)
			if err != nil {
				if isRedisReply(err) {
					return RedisScanResponse{}, fmt.Errorf("scan Redis keys on %s: %w", masters[i].Addr, err)
				}
				logUnreachableNode(ctx, masters[i].Addr, err)
				reachable[i] = false
				break
			}
			next, nodeKeys, err := parseScanResult(result)
			if err != nil {
				return RedisScanResponse{}, err
			}
			keys = append(keys, nodeKeys...)
			nodeCursor = next
			if nodeCursor == "0" {
				scanned++
			}
			if int64(len(keys)) >= req.Count {
				if nodeCursor != "0" {
					cursor = encodeClusterCursor(nodeCursor, masters[i].Addr)
				} else if j := slices.Index(reachable[i+1:], true); j >= 0 {
					cursor = encodeClusterCursor("0", masters[i+1+j].Addr)
				}
				break scan
			}
			if nodeCursor == "0" {
				break
			}
		}
	}

	resp := RedisScanResponse{
		Cursor:         cursor,
		Keys:           keys,
		HasMore:        cursor != "0",
		ScannedMasters: scanned,
		TotalMasters:   len(masters),
	}
	for i, ok := range reachable {
		if !ok {
			resp.Unreachable = append(resp.Unreachable, RedisClusterNodeRef{Addr: masters[i].Addr, Slots: formatSlotRanges(masters[i].Slots)})
		}
	}
	return resp, nil
}

// listClusterDatabases 返回集群唯一的 db0：Keys 为可达主节点 DBSIZE 之和，Masters 为各主节点计数。
func listClusterDatabases(ctx context.Context, c clusterExecutor) ([]RedisDatabase, error) {
	masters, err := clusterMasters(ctx, c)
	if err != nil {
		return nil, err
	}
	results, errs := onEachNode(ctx, c, nodeAddrs(masters), "DBSIZE")
	db := RedisDatabase{DB: 0, Masters: make([]RedisClusterMasterKeys, len(masters))}
	for i, m := range masters {
		entry := RedisClusterMasterKeys{Addr: m.Addr, Slots: formatSlotRanges(m.Slots), Keys: -1}
		if errs[i] != nil {
			logUnreachableNode(ctx, m.Addr, errs[i])
		} else {
			entry.Keys = toInt64(results[i])
			entry.Reachable = true
			db.Keys += entry.Keys
		}
		db.Masters[i] = entry
	}
	return []RedisDatabase{db}, nil
}

// deleteKeysEach 逐个 key 执行 DEL（集群中多个 key 可能跨 slot），单个失败不影响其余 key。
func deleteKeysEach(ctx context.Context, exec redisExecutor, keys []string) RedisDeleteResult {
	var out RedisDeleteResult
	for _, key := range keys {
		result, err := exec.Do(ctx, "DEL", key)
		if err != nil {
			out.Failed = append(out.Failed, RedisKeyFailure{Key: key, Error: err.Error()})
			continue
		}
		out.Deleted += toInt64(result)
	}
	return out
}

// goRedisClusterExecutor 基于 go-redis ClusterClient 实现 clusterExecutor；
// Do 由 ClusterClient 按 slot 路由，按节点执行复用集群客户端持有的节点连接。
type goRedisClusterExecutor struct {
	*goRedisExecutor
	cluster *redis.ClusterClient
}

func (e *goRedisClusterExecutor) ShardAddrs(ctx context.Context) ([]string, error) {
	return connpool.RedisClusterNodeAddrs(ctx, e.cluster, false)
}

func (e *goRedisClusterExecutor) DoOnNode(ctx context.Context, addr string, args ...any) (any, error) {
	node, err := connpool.RedisClusterNode(ctx, e.cluster, addr)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("%s: %w", addr, errUnknownClusterNode)
	}
	return e.run(ctx, node, args)
}

func (e *goRedisClusterExecutor) MasterForKey(ctx context.Context, key string) (string, error) {
	node, err := e.cluster.MasterForKey(ctx, key)
	if err != nil {
		return "", err
	}
	return node.Options().Addr, nil
}
