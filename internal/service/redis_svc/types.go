package redis_svc

const (
	RedisValueFormatRaw    = "raw"
	RedisValueFormatJSON   = "json"
	RedisValueFormatHex    = "hex"
	RedisValueFormatBase64 = "base64"
)

// RedisDatabase describes one logical Redis database from INFO keyspace.
type RedisDatabase struct {
	DB      int   `json:"db"`
	Keys    int64 `json:"keys"`
	Expires int64 `json:"expires"`
	AvgTTL  int64 `json:"avgTtl"`
	// Masters 仅集群模式填写（集群只有 db0，此时列表只有这一项）：各主节点的 key 数，
	// 供「扫描范围」选择器使用，按地址排序；Keys 为其中可达主节点 DBSIZE 之和。
	// ts_type 让生成的 TS 模型不带 convertValues，保持现有前端以对象字面量构造本类型的兼容。
	Masters []RedisClusterMasterKeys `json:"masters,omitempty" ts_type:"RedisClusterMasterKeys[]"`
}

// RedisClusterMasterKeys 是集群中一个主节点的 key 计数。
type RedisClusterMasterKeys struct {
	Addr string `json:"addr"` // 主节点 host:port（集群宣告地址），即 RedisScanRequest.Node 的取值
	// Slots 为该主节点负责的 slot 范围，形如 "0-5460" 或 "0-100,200"（逗号分隔的闭区间）。
	Slots     string `json:"slots"`
	Keys      int64  `json:"keys"`      // DBSIZE；不可达时为 -1
	Reachable bool   `json:"reachable"` // 本次能否连上该主节点
}

// RedisClusterNodeRef 标识一个集群主节点及其 slot 范围（用于不可达提示）。
type RedisClusterNodeRef struct {
	Addr  string `json:"addr"`  // host:port
	Slots string `json:"slots"` // 同 RedisClusterMasterKeys.Slots
}

// RedisScanRequest controls bounded key scanning.
type RedisScanRequest struct {
	AssetID int64  `json:"assetId"`
	DB      int    `json:"db"`
	Cursor  string `json:"cursor"`
	Match   string `json:"match"`
	Type    string `json:"type"`
	Count   int64  `json:"count"`
	Exact   bool   `json:"exact"`
	// Node 仅集群模式有效：扫描范围。空 = 全部主节点；否则为某个主节点地址
	// （取自 RedisDatabase.Masters[].Addr），只扫该节点。精确查询（Exact）按 slot 直达，忽略 Node。
	Node string `json:"node,omitempty"`
}

// RedisScanResponse 是一页扫描结果。Cursor 对前端是不透明的：原样带回下一次请求，
// "0" 表示已扫完（HasMore=false）。集群模式下 Cursor 编码了「当前主节点 + 节点内游标」，
// 翻页在各主节点之间续扫；扫描范围（Node）或过滤条件变化时应从 "0" 重新开始。
type RedisScanResponse struct {
	Cursor  string   `json:"cursor"`
	Keys    []string `json:"keys"`
	HasMore bool     `json:"hasMore"`
	// 以下仅集群的非精确扫描填写；TotalMasters==0 表示覆盖信息不适用
	// （单机 / 哨兵，或集群的精确查询）。
	// ScannedMasters 为扫描范围内（截至本页、累计）已完整扫完的可达主节点数，即「已扫 k/n」的 k。
	ScannedMasters int `json:"scannedMasters,omitempty"`
	// TotalMasters 为扫描范围内的主节点数（全部主节点，或 Node 指定时为 1），即 n。
	TotalMasters int `json:"totalMasters,omitempty"`
	// Unreachable 为扫描范围内本次不可达、被跳过的主节点（每页都按当前探测结果完整给出），
	// 其 slot 范围内的 key 未列出。ts_type 原因同 RedisDatabase.Masters。
	Unreachable []RedisClusterNodeRef `json:"unreachable,omitempty" ts_type:"RedisClusterNodeRef[]"`
}

type RedisKeyRequest struct {
	AssetID int64  `json:"assetId"`
	DB      int    `json:"db"`
	Key     string `json:"key"`
	Cursor  string `json:"cursor,omitempty"`
	Offset  int64  `json:"offset,omitempty"`
	Count   int64  `json:"count,omitempty"`
}

type RedisKeyDetail struct {
	Key           string `json:"key"`
	Type          string `json:"type"`
	TTL           int64  `json:"ttl"`
	Size          int64  `json:"size"`
	Total         int64  `json:"total"`
	Value         any    `json:"value"`
	ValueCursor   string `json:"valueCursor"`
	ValueOffset   int64  `json:"valueOffset"`
	HasMoreValues bool   `json:"hasMoreValues"`
	// Slot 与 Node 仅集群模式填写：key 所在 slot（0-16383）与负责该 slot 的主节点 host:port。
	Slot *int   `json:"slot,omitempty"`
	Node string `json:"node,omitempty"`
}

// RedisDeleteResult 是批量删除的结果。单机 / 哨兵一次 DEL 删除全部 key，失败时整体报错；
// 集群逐个 key 执行 DEL，单个 key 失败不影响其余 key，失败项记入 Failed（此时调用不报错）。
type RedisDeleteResult struct {
	Deleted int64             `json:"deleted"`          // 实际删除的 key 数（不存在的 key 不计）
	Failed  []RedisKeyFailure `json:"failed,omitempty"` // 删除失败的 key 及 Redis 返回的错误
}

// RedisKeyFailure 是单个 key 操作失败的原因。
type RedisKeyFailure struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

type RedisHashEntry struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type RedisZSetEntry struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

type RedisStreamEntry struct {
	ID     string            `json:"id"`
	Fields map[string]string `json:"fields"`
}

type RedisStringSetRequest struct {
	AssetID int64  `json:"assetId"`
	DB      int    `json:"db"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Format  string `json:"format"`
}

type RedisStreamField struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type RedisSlowLogEntry struct {
	ID             int64    `json:"id"`
	Timestamp      int64    `json:"timestamp"`
	DurationMicros int64    `json:"durationMicros"`
	Command        []string `json:"command"`
	Client         string   `json:"client,omitempty"`
	ClientName     string   `json:"clientName,omitempty"`
}

// RedisFormattedValue is a display-only projection of a Redis value.
type RedisFormattedValue struct {
	Format string `json:"format"`
	Value  string `json:"value"`
	Valid  bool   `json:"valid"`
	Error  string `json:"error,omitempty"`
}

// RedisClusterOverview 是集群概览（Redis.RedisClusterOverview 的返回值）。
type RedisClusterOverview struct {
	// State 为 CLUSTER INFO 的 cluster_state："ok" 或 "fail"。
	State string `json:"state"`
	// Slot 统计取自 CLUSTER INFO；集群共 16384 个 slot，
	// 不可用 slot 数 = 16384 - SlotsOK，slot 覆盖 = SlotsOK / 16384。
	SlotsAssigned int `json:"slotsAssigned"`
	SlotsOK       int `json:"slotsOk"`
	SlotsPFail    int `json:"slotsPfail"`
	SlotsFail     int `json:"slotsFail"`
	// TotalKeys 为可达主节点 key 数之和；KeysPartial=true 表示有主节点不可达，
	// 真实总数 ≥ TotalKeys（界面显示「≥ TotalKeys」）。
	TotalKeys   int64 `json:"totalKeys"`
	KeysPartial bool  `json:"keysPartial"`
	// Masters 为所有带 master 标志的节点（按地址排序），各自的从节点挂在 Replicas 下；
	// 找不到所属主节点的从节点也以 Role="replica" 列在此处。
	Masters []RedisClusterNode `json:"masters"`
	// InfoNode 为本次返回 INFO 的节点（请求为空时取第一个主节点）；Info 为其 INFO 原文，
	// 该节点不可达时 Info 为空、InfoError 为原因。
	InfoNode  string `json:"infoNode"`
	Info      string `json:"info"`
	InfoError string `json:"infoError,omitempty"`
}

// RedisClusterNode 是集群中的一个节点（CLUSTER NODES 的一行 + 该节点 INFO 的指标）。
type RedisClusterNode struct {
	ID        string   `json:"id"`       // 完整节点 ID（界面取前缀显示）
	Addr      string   `json:"addr"`     // host:port（集群宣告地址），可作为 INFO 节点
	Role      string   `json:"role"`     // "master" | "replica"
	MasterID  string   `json:"masterId"` // 从节点所属主节点 ID；主节点为空
	Slots     string   `json:"slots"`    // slot 范围，同 RedisClusterMasterKeys.Slots；无 slot 为空
	SlotCount int      `json:"slotCount"`
	Flags     []string `json:"flags"`     // CLUSTER NODES 原始标志，如 master / slave / fail? / fail
	LinkState string   `json:"linkState"` // "connected" | "disconnected"
	// Status："ok" | "pfail"（集群怀疑故障，fail?）| "fail"（集群判定故障）| "unreachable"（本机连不上）。
	Status string `json:"status"`
	// Error 为非 ok 时的原因：连接错误，或集群标志 / 链路状态说明。
	Error     string `json:"error,omitempty"`
	Reachable bool   `json:"reachable"` // 本次能否连上该节点取 INFO
	// 以下取自该节点 INFO；不可达时为 -1 / 空（界面显示「—」）。
	Keys            int64  `json:"keys"` // db0 的 key 数
	UsedMemory      int64  `json:"usedMemory"`
	UsedMemoryHuman string `json:"usedMemoryHuman"`
	OpsPerSec       int64  `json:"opsPerSec"`
	// Replicas 仅主节点：其从节点（按地址排序）。
	Replicas []RedisClusterNode `json:"replicas,omitempty"`
}

// RedisSentinelOverview 是哨兵模式概览（Redis.RedisSentinelOverview 的返回值）。
// 服务器 / 内存 / 运行状态面板和完整 INFO 仍通过数据连接（始终指向当前主节点）获取。
type RedisSentinelOverview struct {
	MasterName string            `json:"masterName"` // 哨兵监控的组名
	Master     RedisSentinelNode `json:"master"`     // 哨兵报告的当前主节点
	// MasterError 非空表示连不上当前主节点，复制延迟因此未知（LagSeconds/LagBytes 为 -1）。
	MasterError string                 `json:"masterError,omitempty"`
	Quorum      int                    `json:"quorum"`
	Replicas    []RedisSentinelReplica `json:"replicas"`
	// Sentinels 为哨兵节点：第一项是本次应答的哨兵（Queried=true，地址取资产配置），
	// 其余为它所知道的其他哨兵。
	Sentinels []RedisSentinelNode `json:"sentinels"`
}

// RedisSentinelNode 是哨兵视角下的一个节点（主节点或哨兵）。
type RedisSentinelNode struct {
	Addr  string `json:"addr"`  // host:port
	Flags string `json:"flags"` // 哨兵报告的原始 flags，如 "master,s_down"
	// Status 由 flags 归纳："ok" | "sdown"（主观下线）| "odown"（客观下线）| "disconnected"。
	Status  string `json:"status"`
	Queried bool   `json:"queried,omitempty"` // 是否本次应答的哨兵
}

// RedisSentinelReplica 是当前主节点的一个从节点。
type RedisSentinelReplica struct {
	Addr       string `json:"addr"`
	Flags      string `json:"flags"`
	Status     string `json:"status"`     // 同 RedisSentinelNode.Status
	LinkStatus string `json:"linkStatus"` // 与主节点的复制链路：master-link-status，"ok" | "err"
	// Offset 为复制偏移：优先取主节点 INFO replication，其次取哨兵报告的 slave-repl-offset。
	Offset int64 `json:"offset"`
	// LagSeconds 为主节点 INFO 报告的 lag（秒）；LagBytes = master_repl_offset - Offset。
	// 主节点不可达或主节点未列出该从节点时均为 -1。
	LagSeconds int64 `json:"lagSeconds"`
	LagBytes   int64 `json:"lagBytes"`
}
