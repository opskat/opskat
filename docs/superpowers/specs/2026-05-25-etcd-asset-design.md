# etcd 资产接入设计

- **状态**:Draft
- **日期**:2026-05-25
- **作者**:王一之 + Claude
- **范围**:首期接入 etcd 作为一种新的资产类型,提供 AI 工具 + KV 浏览树 + 查询面板。**不**含 watch / lease UI / member 管理 UI / 快照导出(留到后续)。
- **UI/UX 视觉稿**:`/Users/codfrm/Desktop/opskat.pen`(3 屏:资产表单 / KV 浏览页 / 查询面板)

## 1. 背景与目标

OpsKat 已支持 SSH / 数据库 / Redis / MongoDB / Kafka / K8s 资产。etcd 是云原生基础设施(k8s 控制面、特征开关、分布式锁、服务发现)的核心组件,运维人员在排障/变更时常需直连读写。当前用户只能通过外置 `etcdctl` + 手抄端点/证书,跨多 endpoint、跨 SSH 隧道访问非常繁琐。

**目标**:把 etcd 接入到现有 OpsKat 资产/AI/审计/凭证体系,首期覆盖:

- 资产 CRUD,凭证用现有 credential 体系加密
- 多 endpoint 集群、TLS、mTLS、SSH 隧道
- AI 工具 `exec_etcd`(单工具 + 结构化 op)
- KV 浏览树(懒加载、按 `/` 虚构层级、只读 + 预览)
- 查询面板(etcdctl 风格命令行 + 模板下拉)
- 命令策略复用 `RedisPolicy` 结构 + 新的内置权限组

**非目标(首期)**:

- 实时 watch / KV 树自动刷新(后续 B 方案)
- lease 续约 / member 管理 / 快照 / defrag 的专用 UI(AI 工具里以管理命令出现,默认 deny)
- 树里直接编辑 / 重命名 key(走查询面板)
- 跨 endpoint 多隧道(首期只对第一个可达 endpoint 起隧道)

## 2. 范围决策(用户已确认)

| Q | 决策 |
|---|---|
| 首期广度 | C 方案 = AI + KV 浏览树 + 查询面板;**不**含 watch / lease UI |
| Endpoint 拓扑 | `endpoints []string`(Kafka 模式) |
| 认证范围 | 无认证 + 用户名密码 + 服务端 TLS + mTLS + SSH 隧道,**全部支持** |
| 策略结构 | 复用 `RedisPolicy` 结构 + 新增 etcd 内置权限组 |
| AI 工具 | 单 `exec_etcd` + 结构化 `op` 字段(不解析 etcdctl 字符串) |
| KV 树 | 分隔符固定 `/`,懒加载 limit 1000,只读预览,大 value 阈值 64KB |
| 查询面板 | 命令行风格 + 模板下拉;销毁性操作走 confirm 预览 |
| 实施路线 | A = 精简 KV,一个 PR 内 ship 完;无 watch |

## 3. 架构

```
┌─ Frontend (React 19 + Zustand 5) ────────────────────────┐
│  src/components/asset/forms/EtcdForm.tsx                 │
│  src/components/etcd/                                    │
│    ├─ EtcdTreePane.tsx       (KV 浏览树, 懒加载)         │
│    ├─ EtcdQueryPane.tsx      (命令行 + 模板下拉)         │
│    ├─ EtcdResultTable.tsx    (key/value/rev 表格)        │
│    └─ useEtcdStore.ts        (per-asset 状态)            │
│  src/i18n/locales/{zh-CN,en}/common.json 新增 etcd.* 键    │
└──────────┬───────────────────────────────────────────────┘
           │ Wails IPC bindings(生成在 frontend/wailsjs/go/app/App)
           ▼
┌─ internal/app/etcd.go (薄绑定层) ────────────────────────┐
│  EtcdTestConnection / EtcdExec / EtcdListPrefix /        │
│  EtcdKeyHistory                                          │
└──────────┬───────────────────────────────────────────────┘
           ▼
┌─ internal/service/etcd_svc/ ─────────────────────────────┐
│  service.go      业务编排:策略检查 → 拿连接 → 执行 → 审计 │
│  ops.go          get / put / del / txn / lease / admin   │
│  command.go      查询面板命令字符串解析(前导切词)        │
└──────────┬───────────────────────────────────────────────┘
           ▼
┌─ internal/connpool/etcd.go ──────────────────────────────┐
│  GetOrDial(ctx, asset, cfg, password, sshPool)           │
│  - 复用 BuildTLSConfig + SSHTunnel                        │
│  - 持久 client 缓存 (per-assetID, idle 5min 关闭)         │
└──────────┬───────────────────────────────────────────────┘
           ▼
        go.etcd.io/etcd/client/v3 (官方 gRPC 客户端)
```

横向接入点(不改其他资产类型代码):

- `internal/assettype/etcd.go` — 实现 `AssetTypeHandler`,注册到 registry
- `internal/model/entity/asset_entity/asset.go` —
  - 新增 `AssetTypeEtcd = "etcd"`
  - 新增 `EtcdConfig` 结构 + `IsEtcd()` / `GetEtcdConfig` / `SetEtcdConfig`
  - 加入 `validate` 与 `CanConnect` 的 switch 分支
  - `EtcdPolicy` 类型别名指向 `policy.RedisPolicy`,`GetEtcdPolicy()`/`SetEtcdPolicy()` 方法
- `internal/model/entity/policy/policy.go` —
  - 新增 `BuiltinEtcdReadOnly` / `BuiltinEtcdDangerousDeny` 常量
  - 新增 `DefaultEtcdPolicy()`(引用上述两个内置组)
  - **扩展 `Holder` 接口**新增 `GetEtcdPolicy()` 方法,Asset 与 Group 都实现
- `internal/model/entity/policy_group_entity/policy_group.go` —
  - 新增 `PolicyTypeEtcd = "etcd"`
  - 在 `seedBuiltinPolicyGroups` 中新增两条 etcd 内置组(读规则 + 危险拒绝规则)
  - `Group` 实现 `GetEtcdPolicy()`
- `internal/ai/policy/policy_group_resolve.go` — 新增 `ResolveEtcdGroups`(复用 redis 逻辑,只换 PolicyType 常量)
- `internal/ai/policy/policy_tester.go` — `case "etcd":` 路由,**复用** `testRedisPolicy` 实现(参数化或新加一个等价函数,内部 matcher 仍用 `MatchRedisRule`)
- `internal/ai/tool/tools_data.go` — 新增 `exec_etcd` 工具定义
- `internal/ai/tool_handler_etcd.go`(新文件)— exec_etcd 调用 dispatcher
- 前端 Wails binding 在 `make dev` 时自动生成

**依赖**:`go.etcd.io/etcd/client/v3`(当前 `go.mod` 未引入),需 `go mod tidy`。是 etcd 官方维护的 gRPC 客户端,生产广泛使用。

**migration**:零新文件 —— 资产数据走 `assets.config` 的 JSON 字段,策略走 `assets.command_policy`,凭证走现有 `credentials` 表。内置权限组通过 `seedBuiltinPolicyGroups` 在启动时插入(已存在的种子机制)。

## 4. 数据模型

### 4.1 `EtcdConfig`(序列化进 `assets.config`)

```go
type EtcdConfig struct {
    Endpoints []string `json:"endpoints"`           // 至少 1 个,形如 "host:port"
    Username  string   `json:"username,omitempty"`  // 留空 = 不启用 RBAC

    // 凭证(沿用 credential_resolver 通用模式)
    Password     string `json:"password,omitempty"`     // AES-256-GCM 密文
    CredentialID int64  `json:"credentialId,omitempty"`
    // Password 与 CredentialID 二选一

    // 传输 TLS
    TLS           bool   `json:"tls,omitempty"`
    TLSServerName string `json:"tlsServerName,omitempty"`
    TLSInsecure   bool   `json:"tlsInsecure,omitempty"`
    TLSCAFile     string `json:"tlsCaFile,omitempty"`

    // mTLS 客户端证书
    TLSCertFile string `json:"tlsCertFile,omitempty"`
    TLSKeyFile  string `json:"tlsKeyFile,omitempty"`

    // 超时
    DialTimeoutSeconds    int `json:"dialTimeoutSeconds,omitempty"`    // 默认 5
    CommandTimeoutSeconds int `json:"commandTimeoutSeconds,omitempty"` // 默认 10
}
```

SSH 隧道**不**进 `EtcdConfig`,走顶层 `Asset.SSHTunnelID`(与 MongoDB 当前做法对齐)。

### 4.2 `EtcdPolicy`

```go
// internal/model/entity/policy/policy.go
type EtcdPolicy = RedisPolicy   // 结构完全相同: AllowList/DenyList/Groups

func DefaultEtcdPolicy() *EtcdPolicy {
    return &EtcdPolicy{
        Groups: []string{BuiltinEtcdReadOnly, BuiltinEtcdDangerousDeny},
    }
}

const (
    BuiltinEtcdReadOnly      = "builtin:etcd-readonly"
    BuiltinEtcdDangerousDeny = "builtin:etcd-dangerous-deny"
)
```

### 4.3 内置权限组规则

匹配字符串形态为 `"<op> [<key>] [<flags>]"`,glob 风格(复用 `MatchRedisRule`):

```go
// 在 policy_group_entity.seedBuiltinPolicyGroups 追加两条:
{
    BuiltinID:   policy.BuiltinEtcdReadOnly,
    Name:        "etcd Read-Only",
    Description: "Allow etcd read-only operations",
    PolicyType:  PolicyTypeEtcd,
    Policy: mustMarshal(&policy.EtcdPolicy{
        AllowList: []string{
            "get *", "range *",
            "endpoint *",
            "member list",
            "lease list", "lease ttl *",
            "auth status",
            "user list", "user get *",
            "role list", "role get *",
        },
    }),
},
{
    BuiltinID:   policy.BuiltinEtcdDangerousDeny,
    Name:        "etcd Dangerous Deny",
    Description: "Deny dangerous etcd operations",
    PolicyType:  PolicyTypeEtcd,
    Policy: mustMarshal(&policy.EtcdPolicy{
        DenyList: []string{
            "auth enable", "auth disable",
            "user add *", "user delete *", "user passwd *",
            "role add *", "role delete *", "role grant-permission *", "role revoke-permission *",
            "member add *", "member remove *", "member update *",
            "move-leader *",
            "defrag",
            "compact *",
            "alarm disarm *",
            "snapshot save *",
        },
    }),
},
```

其余 op(`put`、`del`、`txn`、`lease grant/revoke/keep-alive`、`lock`、`elect`)既不在 AllowList 也不在 DenyList,**由调用方按默认 NeedConfirm 处理**(`RedisPolicy` 没有 `DefaultAction` 字段,这是查询面板/AI runner 的约定行为,与 Redis 现状一致)。

> 注:命令字符串使用**小写**(etcdctl 约定),与 Redis 大写形成区分;`MatchRedisRule` 是大小写相关的 glob,etcd 的所有 matcher 输入与规则都统一用小写,前端命令解析也归一化为小写。

### 4.4 数据库迁移

**零文件**。资产实体走 `assets.config` 的 JSON 字段,策略走 `assets.command_policy`,内置组通过 `seedBuiltinPolicyGroups` 启动时 upsert。新增的 `BuiltinEtcdReadOnly` / `BuiltinEtcdDangerousDeny` 两条由 seed 函数自动写入。

## 5. IPC 边界 (`internal/app/etcd.go`)

```go
// 测试连接(资产保存前 / 详情页"测试"按钮)
func (a *App) EtcdTestConnection(ctx context.Context, assetID int64) (TestResult, error)

// AI 工具 + 查询面板 + KV 树操作的统一入口
func (a *App) EtcdExec(ctx context.Context, req EtcdExecRequest) (EtcdExecResult, error)

// 树懒加载专用
func (a *App) EtcdListPrefix(ctx context.Context, req EtcdListPrefixRequest) (EtcdListPrefixResult, error)

// 单 key 的历史 revisions
func (a *App) EtcdKeyHistory(ctx context.Context, assetID int64, key string, limit int) ([]EtcdKeyVersion, error)
```

```go
type EtcdExecRequest struct {
    AssetID    int64
    Op         string         // "get" | "put" | "del" | "txn" | "lease_grant" | "lease_revoke" | "endpoint_status" | ...
    Key        string
    Value      string         // for put
    Prefix     bool           // for get/del
    Limit      int64          // for get
    Revision   int64          // for get historical
    LeaseID    int64          // attach to put
    Args       map[string]any // 兜底参数(txn 的 compare/then/else)
    ApprovalID string         // confirm 完成后的 token
    Source     string         // "ai" | "query" | "tree" | "approval"  审计字段
}

type EtcdExecResult struct {
    Op       string
    KVs      []EtcdKV
    Count    int64
    Revision int64
    Header   *EtcdHeader
}

type EtcdListPrefixRequest struct {
    AssetID int64
    Prefix  string
    Delim   string  // 固定 "/"
    Limit   int64   // 默认 1000
}

type EtcdListPrefixResult struct {
    Dirs      []string // 直接子"目录"名(末尾不含 delim)
    Leaves    []EtcdKVHeader  // 直接子叶子(key + metadata,不含 value)
    Truncated bool
    Total     int64
}
```

## 6. 服务层流程

`EtcdExec` 主流程:

```
1. asset_svc.GetByID(assetID) — 查资产
2. assettype.Get("etcd").ResolvePassword(ctx, asset) — 解出明文密码
3. policy.Match(ctx, "etcd", asset.CommandPolicy, op+" "+key+flagsStr) — 策略判定
   - Allow: 继续
   - NeedConfirm: 若 req.ApprovalID 空,返回 PolicyDecisionPending,前端弹 confirm 并带 approvalID 重发
                  若 req.ApprovalID 已校验通过,继续
   - Deny: audit.Record(denied) + 返回 ErrPolicyDeny
4. connpool.GetOrDial(ctx, asset, cfg, password, sshPool) — 拿 client
5. ops.Dispatch(ctx, client, req) — 一个 op 对应一个 RPC
6. audit.Record(start/end/fail) — 三态审计
7. 返回 EtcdExecResult
```

### 6.1 连接池

```go
// internal/connpool/etcd.go
type etcdEntry struct {
    client   *clientv3.Client
    tunnel   io.Closer            // SSH 隧道(若有)
    lastUsed atomic.Int64
}

// 模块级 sync.Map[assetID]*etcdEntry
// idle 5min 后台 ticker 清理
// 资产被删除/更新时 Invalidate(assetID) 主动失效

func GetOrDial(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig,
               password string, sshPool *sshpool.Pool) (*clientv3.Client, error)

func Invalidate(assetID int64)
```

**SSH 隧道策略**:多 endpoint + 隧道场景,**只对第一个 endpoint 起隧道**,etcd client 通过自定义 dialer 透传。剩余 endpoint 视为不可达。首期局限,后续可扩。

### 6.2 KV 树懒加载

`EtcdListPrefix({prefix:"/config/", delim:"/", limit:1000})` 流程:

```
1. clientv3.Get(ctx, prefix, WithPrefix, WithKeysOnly, WithLimit(limit))
   - WithKeysOnly: 不传 value,带宽小一个数量级
   - 响应的 `More` 字段表示是否截断(etcd 原生支持,无需 limit+1 探测)
2. 服务层按 "/" 切一层:
   - "/config/db"        → leaf  "db"
   - "/config/cache"     → leaf  "cache"
   - "/config/svc/api"   → dir   "svc"
   - "/config/svc/web"   → dir   "svc"(去重)
3. 返回 {Dirs, Leaves, Truncated, Total}
```

前端 `useEtcdStore` 用 `Map<prefix, TreeNode[]>` 缓存,点开同一 prefix 不二次 RPC。手动刷新时调 `Invalidate(prefix)` 后再请求。

### 6.3 命令字符串解析(`command.go`)

仅查询面板用,把 `get /config --prefix --limit=100` 切成 `{op:"get", key:"/config", prefix:true, limit:100}`,再走 `EtcdExec`。**不**做 etcdctl 完全兼容,只识别我们支持的 op 与 flag。txn 通过模板填一个 JSON 块(走 `Args.txn`),不解析复杂 DSL。

## 7. AI 工具

```go
// internal/ai/tool/tools_data.go
{
    NameStr: "exec_etcd",
    DescStr: "Execute an etcd KV/lease/admin operation on an etcd asset. " +
             "Operations: get, put, del, txn, lease_grant, lease_revoke, lease_ttl, " +
             "endpoint_status, endpoint_health, member_list, user_list, role_list. " +
             "All keys must start with '/'. Read ops return latest revision by default; " +
             "pass 'revision' to read historical (subject to compaction).",
    InputSchemaJSON: map[string]any{
        "asset_id": {Type:"number", Description:"etcd asset ID. Use list_assets with asset_type='etcd'."},
        "op":       {Type:"string", Enum:["get","put","del","txn","lease_grant","lease_revoke","lease_ttl","endpoint_status","endpoint_health","member_list","user_list","role_list"]},
        "key":      {Type:"string", Description:"Key (or prefix when --prefix). Required for get/put/del."},
        "value":    {Type:"string", Description:"Value for put. UTF-8 string; for binary use base64 with type=binary."},
        "prefix":   {Type:"boolean", Description:"Treat key as prefix for get/del."},
        "limit":    {Type:"number"},
        "revision": {Type:"number", Description:"Read at historical revision."},
        "lease_id": {Type:"number"},
        "txn":      {Type:"object", Description:"For op=txn: {compare:[...], success:[...], failure:[...]}."},
    }
}
```

`tool_handler_etcd.go` 把工具调用映射为 `EtcdExecRequest{Source:"ai"}`,**策略检查、审计、连接管理与查询面板共用一条路径**。

## 8. 错误处理与边界

### 8.1 错误分层

| 来源 | 类型 | 前端反馈 |
|---|---|---|
| 配置错误(endpoints 空 / cert 文件不存在) | `*ConfigError` | 资产表单内联红字 |
| 网络/隧道错误 | `*ConnError`(带 endpoint) | toast + 连接徽章变红 + 重试 |
| 认证错误(401/403, mTLS cert invalid) | `*AuthError` | toast + 引导"检查凭证" |
| 策略拒绝 | `*PolicyDenyError`(带 matched rule) | confirm 弹窗或拒绝 toast |
| etcd 业务错误(lease 不存在等) | gRPC code 透传 + 中文消息 | 表格内 inline 错误行 |
| timeout | dial/command 区分 | toast + 提示调整 CommandTimeout |
| panic | recover() + zap.Stack | toast "内部错误,详见日志" |

### 8.2 关键边界

- `get` 不存在的 key → 空数组,**不是错误**;前端显示 "key not found" 占位
- `--prefix + del` → 即便策略 allow,**前端必须**先 `get --prefix --keys-only --limit=100` 预览影响 key 数量,二次 confirm 后才真正 `del`(对齐 [[feedback_destructive_confirm]])
- value > 1MB → 服务层 `*ValueTooLargeError` 拒绝;value > 64KB → 前端在树/表格里截断显示
- 非 UTF-8 value → 元数据标 `binary`,默认不展开,提供 hex 视图
- 全部 endpoint dial 失败 → 报最后一个 endpoint 的 gRPC error
- SSH 隧道断开 → `connpool.Invalidate` + 下次 EtcdExec 重新 dial,**不**做隐式 retry
- rev 已 compact → gRPC `OutOfRange` 翻译为"该版本已被压缩,最早可用版本 N"
- 并发 confirm → `approvalID` 关联前后两次调用,5min TTL

### 8.3 日志(对齐 CLAUDE.md "关键流程要打日志")

每次 `EtcdExec` 服务层打 **开始/结束/失败 三态**:

```go
logger.Ctx(ctx).Info("etcd exec start",
    zap.Int64("assetID", req.AssetID),
    zap.String("op", req.Op),
    zap.String("key", req.Key),
    zap.Bool("prefix", req.Prefix),
    zap.String("source", req.Source),
)
// 完成: zap.Duration("elapsed", ...), zap.Int64("count", ...)
// 失败: zap.Error(err)
```

`zap.Stack("stack")` 用于 `connpool.GetOrDial` dialer 内部 recover(),防止 etcd SDK goroutine panic 炸主进程。

**禁止打**:value 内容(可能含密钥)、密码、TLS 私钥、watch event 流(首期无 watch)。统一 `logger.Ctx(ctx)`,不使用 `logger.Default()`(对齐 [[feedback_framework_intent_over_grep]])。

## 9. 前端

### 9.1 资产表单 `EtcdForm.tsx`

字段(顺序)(详见 UI 视觉稿 Screen 1):

1. **基本信息**:Name(必填) / Group / Tags / Icon
2. **Endpoints**(必填,至少 1 条):多行输入或 chip 列表,逐项校验 `host:port`
3. **认证**:Username(可空) / Password 源(凭证下拉 + 明文输入二选一,走 `PasswordSourceField`)
4. **TLS / mTLS**(toggle):Server Name / CA File / Cert File / Key File / "跳过证书校验"
5. **高级**:SSH 隧道下拉(从 SSH 资产列表选)/ DialTimeout / CommandTimeout
6. 底部:"测试连接" / "取消" / "保存"

### 9.2 KV 浏览页 `EtcdTreePane.tsx + 详情`

左右分栏(详见 UI 视觉稿 Screen 2):

- **左 (420px)**:工具栏(连接状态徽章 + endpoint 下拉)+ 搜索过滤 + 树
  - 树虚构层级:点开 `/config/` 触发 `EtcdListPrefix`,按 `/` 切分
  - 节点显示 leaf 数(目录侧)+ folder/file 图标
  - 选中态用左竖条 + 蓝底高亮
- **右**:面包屑 + key 元信息行(MOD REV / CREATE REV / VERSION / LEASE / SIZE / VALUE TYPE)+ 值视图(JSON / Text / Hex 切换)+ 操作栏(在查询面板编辑 / 查看历史 revisions / 导出 / 删除)
- 编辑/新增统一跳转到查询面板,**树本身只读**

### 9.3 查询面板 `EtcdQueryPane.tsx`

布局(详见 UI 视觉稿 Screen 3):

- 命令行输入(单行,⌘ Enter 执行)+ "模板"下拉(get/put/del/txn/lease/member 常见命令)+ "执行"按钮
- 常用命令 chip 列(快速填充)
- 结果区:状态行(成功/失败 + 行数 + 耗时 + endpoint)+ 表格(KEY / VALUE / MOD REV / VERSION / LEASE)
- 销毁性操作(put / del / txn 含写)**统一走 ConfirmDialog**:对 del 显示预览 key 数量
- 底部状态栏:策略检查通过提示 + 历史快捷键

### 9.4 store

`useEtcdStore.ts`(per-asset):

- `treeCache: Map<prefix, TreeNode[]>`
- `selectedKey: string | null`
- `selectedKeyDetail: EtcdKV | null`
- `queryHistory: string[]`(localStorage 持久化)
- `lastResult: EtcdExecResult | null`

### 9.5 i18n

`src/i18n/locales/{zh-CN,en}/common.json` 在 `common` 顶层新增 `etcd.*` 子树(form/tree/query/error/policy),所有键统一通过 `t("etcd.xxx")` 访问。

## 10. 测试策略

### 10.1 Go 单元测试

| 文件 | 重点 | 工具 |
|---|---|---|
| `internal/model/entity/asset_entity/asset_test.go` | `validateEtcd`/`GetEtcdConfig`/`SetEtcdConfig`/`IsEtcd`/`CanConnect` 分支 | testify 表驱动 |
| `internal/assettype/etcd_test.go` | `Type/DefaultPort/SafeView/ResolvePassword/ValidateCreateArgs/ApplyCreate/ApplyUpdate` | testify;SafeView 必须不含密码/私钥 |
| `internal/connpool/etcd_test.go` | `buildEtcdClientConfig` 纯函数:cfg + password → `clientv3.Config` | testify;**不**起真 etcd |
| `internal/service/etcd_svc/command_test.go` | 命令字符串解析 | goconvey + testify 表驱动 |
| `internal/service/etcd_svc/ops_test.go` | op dispatch + 错误分类映射 | mockgen 生成 `clientv3.KV/Lease/Cluster` mock |
| `internal/ai/policy/policy_tester_test.go` | `case "etcd"` 路由 + 一组规则交叉测试 | testify 表驱动 |
| `internal/ai/tool/tools_data_test.go` | `exec_etcd` schema 校验 + 必填缺失报错 | testify |

### 10.2 集成测试

`internal/connpool/etcd_integration_test.go`,`go.etcd.io/etcd/server/v3/embed` 起单节点;build tag `integration`(`make test` 默认不跑,CI 单独 job)。覆盖端到端 dial → put → get → del → close。不覆盖 mTLS / SSH 隧道。

### 10.3 前端测试(vitest + RTL + happy-dom)

- `EtcdForm.test.tsx`:endpoints 多行解析 / TLS toggle 联动 / 必填校验 / mTLS 字段
- `useEtcdStore.test.ts`:树缓存命中 / `--prefix` del 弹 confirm / 选中态
- `EtcdQueryPane.test.tsx`:⌘ Enter 触发 / destructive 走 ConfirmDialog / 模板填充
- `EtcdTreePane.test.tsx`:懒加载触发一次 `EtcdListPrefix` / truncated +N
- `EtcdResultTable.test.tsx`:value 长截断 + 展开 / binary 不解析

Wails runtime 走现成 mock(`src/__tests__/setup.ts`)。

### 10.4 E2E

新增 `tests/fixtures/etcd_demo/` fixture:embed etcd → Wails 启动 → 添加 etcd 资产(127.0.0.1:2379, 无认证)→ 测试连接 → 浏览树 → 查询面板 put/get/del → 退出。本地 + Nightly 跑,默认不进 CI。

### 10.5 验收清单

- [ ] 添加 etcd 资产 → 测试连接成功(无认证)
- [ ] mTLS 资产(localhost + 自签 cert)→ 测试连接成功
- [ ] SSH 隧道(`bastion → etcd`)→ 浏览树 + put/get/del 全通
- [ ] AI `exec_etcd`:`get` 直通,`put` 触发 confirm,`member_add` 被 deny
- [ ] 树懒加载 limit 1000 命中时显示 truncated 提示
- [ ] `del --prefix /` 在前端被预览拦截(显示影响 key 数 + 二次 confirm)
- [ ] 凭证、TLS 私钥**不**出现在任何日志 / SafeView / 审计
- [ ] 所有新代码走 `logger.Ctx(ctx)`,不出现 `logger.Default()`

## 11. 文件清单

新增:

- `internal/connpool/etcd.go`
- `internal/connpool/etcd_test.go`
- `internal/connpool/etcd_integration_test.go`(build tag `integration`)
- `internal/service/etcd_svc/service.go`
- `internal/service/etcd_svc/ops.go`
- `internal/service/etcd_svc/command.go`
- `internal/service/etcd_svc/*_test.go`
- `internal/assettype/etcd.go`
- `internal/assettype/etcd_test.go`
- `internal/app/etcd.go`
- `internal/ai/tool_handler_etcd.go`
- `frontend/src/components/asset/forms/EtcdForm.tsx`
- `frontend/src/components/etcd/EtcdTreePane.tsx`
- `frontend/src/components/etcd/EtcdQueryPane.tsx`
- `frontend/src/components/etcd/EtcdResultTable.tsx`
- `frontend/src/components/etcd/useEtcdStore.ts`
- `tests/fixtures/etcd_demo/`(e2e fixture)

修改(横向接入):

- `internal/model/entity/asset_entity/asset.go` — 加 `AssetTypeEtcd` / `EtcdConfig` / validate / CanConnect / Holder.GetEtcdPolicy
- `internal/model/entity/policy/policy.go` — `EtcdPolicy` alias + `DefaultEtcdPolicy` + 两个 Builtin 常量 + Holder 接口扩展
- `internal/model/entity/policy_group_entity/policy_group.go` — `PolicyTypeEtcd` + seed 两个内置组 + Group.GetEtcdPolicy
- `internal/ai/policy/policy_tester.go` — `case "etcd"`
- `internal/ai/policy/policy_group_resolve.go` — `ResolveEtcdGroups`
- `internal/ai/tool/tools_data.go` — 新增 `exec_etcd`
- `frontend/src/i18n/locales/{zh-CN,en}/common.json` — `etcd.*` 子树
- `frontend/src/components/asset/AssetForm.tsx`(若有 type 分发)— 接入 EtcdForm
- `go.mod`(`go mod tidy` 后引入 `go.etcd.io/etcd/client/v3`)

## 12. 风险与回滚

| 风险 | 缓解 |
|---|---|
| `go.etcd.io/etcd/client/v3` 依赖体积大(grpc + cobra 等) | 接受;它是 etcd 唯一稳定 Go SDK,无对等替代 |
| etcd SDK 在 Wails 包内体积膨胀 | `make build-embed` 后测产物增量,若 >20MB 再考虑动态加载 |
| 多 endpoint + SSH 隧道首期只支持一个 endpoint | 文档明示;后续若用户提需求再补"多 endpoint 多隧道" |
| watch 缺失导致 KV 树需手动刷新 | 验收清单标注;后续 B 方案加 watch |
| 命令字符串解析与 etcdctl 不完全兼容 | 文档说明;命令面板只支持我们实现的 op 子集,带"模板"下拉减少手写 |

回滚:本设计纯加法,不修改其他资产路径;若 etcd 整体被否,删除上述新增文件 + 撤销修改文件的 diff 即可,无 schema 变更需要倒回。
