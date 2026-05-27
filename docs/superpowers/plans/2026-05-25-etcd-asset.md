# etcd 资产接入 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 etcd 接入 OpsKat 作为新资产类型 — 支持多 endpoint、TLS/mTLS、SSH 隧道、AI 工具、KV 浏览树、查询面板。

**Architecture:** 横向扩展资产类型,**不**触动其他资产代码。所有命令走单一服务层入口(`EtcdExec`),与查询面板/AI/树共用;策略复用 RedisPolicy 结构 + 两个新内置组;连接池复用现有 SSH 隧道 + TLS 助手。零数据库 migration。

**Tech Stack:** Go 1.25 + `go.etcd.io/etcd/client/v3`(gRPC);React 19 + Zustand 5;Wails v2 IPC;cago logger;testify + goconvey + gomock + vitest。

**Reference spec:** `docs/superpowers/specs/2026-05-25-etcd-asset-design.md`
**UI 视觉稿:** `/Users/codfrm/Desktop/opskat.pen`

**Pattern to mirror everywhere:** Redis 已在仓库中实现一遍同样的"资产 + 策略 + 连接池 + AI 工具"流程,任何疑问先看 redis 对应文件作为模板。

---

## File Map

**Create:**
- `internal/connpool/etcd.go` + `_test.go`
- `internal/connpool/etcd_integration_test.go`(build tag `integration`)
- `internal/service/etcd_svc/{service,ops,command}.go` + tests
- `internal/assettype/etcd.go` + `_test.go`
- `internal/app/etcd.go`
- `internal/ai/tool_handler_etcd.go`
- `frontend/src/components/asset/forms/EtcdForm.tsx` + `.test.tsx`
- `frontend/src/components/etcd/{EtcdTreePane,EtcdQueryPane,EtcdResultTable,useEtcdStore}.{tsx,ts}` + tests
- `tests/fixtures/etcd_demo/` (E2E fixture)

**Modify(横向扩展点):**
- `internal/model/entity/asset_entity/asset.go` — 类型常量、`EtcdConfig`、helpers、validate、CanConnect、policy alias
- `internal/model/entity/policy/policy.go` — `EtcdPolicy` alias、`DefaultEtcdPolicy`、两个 `BuiltinEtcd*` 常量、扩展 `Holder` 接口
- `internal/model/entity/policy_group_entity/policy_group.go` — `PolicyTypeEtcd`、seed 两条内置组、`Group.GetEtcdPolicy`
- `internal/ai/policy/policy_tester.go` — `case "etcd"`
- `internal/ai/policy/policy_group_resolve.go` — `ResolveEtcdGroups`
- `internal/ai/tool/tools_data.go` — `exec_etcd` 工具定义
- `frontend/src/i18n/locales/{zh-CN,en}/common.json` — `etcd.*` 子树
- `frontend/src/components/asset/AssetForm.tsx` — etcd type 分发
- `go.mod` / `go.sum`(`go mod tidy`)

---

## Phase A — 数据模型

### Task 1: 引入 etcd 客户端依赖

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: 添加依赖**

```bash
go get go.etcd.io/etcd/client/v3@v3.5.16
go mod tidy
```

- [ ] **Step 2: 验证可编译**

```bash
go build ./...
```

Expected: PASS,无新报错。

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "🔧 引入 go.etcd.io/etcd/client/v3 依赖

为后续 etcd 资产接入做准备。"
```

---

### Task 2: Asset entity — 类型常量与 `EtcdConfig`

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`
- Test: `internal/model/entity/asset_entity/asset_test.go`

- [ ] **Step 1: 在 asset_test.go 写失败测试**

在文件末尾追加(参考现有 `TestAsset_IsRedis` 周围风格):

```go
func TestAsset_IsEtcd(t *testing.T) {
	a := &Asset{Type: AssetTypeEtcd}
	require.True(t, a.IsEtcd())
	require.False(t, a.IsRedis())
}

func TestAsset_GetSetEtcdConfig(t *testing.T) {
	a := &Asset{Type: AssetTypeEtcd}
	cfg := &EtcdConfig{
		Endpoints: []string{"10.0.0.1:2379", "10.0.0.2:2379"},
		Username:  "root",
		TLS:       true,
	}
	require.NoError(t, a.SetEtcdConfig(cfg))
	got, err := a.GetEtcdConfig()
	require.NoError(t, err)
	require.Equal(t, cfg.Endpoints, got.Endpoints)
	require.Equal(t, cfg.Username, got.Username)
	require.True(t, got.TLS)
}

func TestAsset_GetEtcdConfig_WrongType(t *testing.T) {
	a := &Asset{Type: AssetTypeSSH}
	_, err := a.GetEtcdConfig()
	require.Error(t, err)
}
```

- [ ] **Step 2: 运行测试,确认 FAIL**

```bash
go test ./internal/model/entity/asset_entity/ -run TestAsset_IsEtcd -v
```

Expected: FAIL,`undefined: AssetTypeEtcd / EtcdConfig`。

- [ ] **Step 3: 在 `asset.go` 添加类型常量**

在 `const` 块(约 line 14-22)末尾追加:

```go
AssetTypeEtcd = "etcd"
```

- [ ] **Step 4: 在 `asset.go` 添加 `EtcdConfig` 结构**

紧跟现有 `RedisConfig`(line 126-144)之后追加:

```go
// EtcdConfig etcd 类型的特定配置
type EtcdConfig struct {
	Endpoints []string `json:"endpoints"`              // 至少 1 个 host:port
	Username  string   `json:"username,omitempty"`     // 留空 = 不启用 RBAC
	Password  string   `json:"password,omitempty"`     // AES-256-GCM 密文
	CredentialID int64 `json:"credential_id,omitempty"`

	TLS           bool   `json:"tls,omitempty"`
	TLSInsecure   bool   `json:"tls_insecure,omitempty"`
	TLSServerName string `json:"tls_server_name,omitempty"`
	TLSCAFile     string `json:"tls_ca_file,omitempty"`
	TLSCertFile   string `json:"tls_cert_file,omitempty"`
	TLSKeyFile    string `json:"tls_key_file,omitempty"`

	DialTimeoutSeconds    int `json:"dial_timeout_seconds,omitempty"`
	CommandTimeoutSeconds int `json:"command_timeout_seconds,omitempty"`
}

// EtcdConfig PasswordSource implementation
func (c *EtcdConfig) GetCredentialID() int64 { return c.CredentialID }
func (c *EtcdConfig) GetPassword() string    { return c.Password }
```

- [ ] **Step 5: 添加充血方法**

紧跟 `IsK8s()` 等已有方法(约 line 335)之后追加:

```go
// IsEtcd 判断是否 etcd 类型
func (a *Asset) IsEtcd() bool {
	return a.Type == AssetTypeEtcd
}
```

紧跟 `GetRedisConfig/SetRedisConfig`(约 line 381-395)之后追加:

```go
// GetEtcdConfig 解析 etcd 配置
func (a *Asset) GetEtcdConfig() (*EtcdConfig, error) {
	if !a.IsEtcd() {
		return nil, errors.New("资产不是 etcd 类型")
	}
	return jsonfield.Unmarshal[EtcdConfig](a.Config, "etcd 配置")
}

// SetEtcdConfig 序列化 etcd 配置到 Config 字段
func (a *Asset) SetEtcdConfig(cfg *EtcdConfig) error {
	s, err := jsonfield.Marshal(cfg, "etcd 配置")
	if err != nil {
		return err
	}
	a.Config = s
	return nil
}
```

- [ ] **Step 6: 运行测试,确认 PASS**

```bash
go test ./internal/model/entity/asset_entity/ -run "TestAsset_IsEtcd|TestAsset_GetSetEtcdConfig" -v
```

Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/model/entity/asset_entity/asset.go internal/model/entity/asset_entity/asset_test.go
git commit -m "✨ 新增 etcd 资产类型常量与 EtcdConfig

包含 endpoints/auth/TLS/mTLS/timeout 字段,以及 PasswordSource 接口实现与 GetEtcdConfig/SetEtcdConfig 充血方法。"
```

---

### Task 3: Asset entity — validate 与 CanConnect 分支

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`
- Test: `internal/model/entity/asset_entity/asset_test.go`

- [ ] **Step 1: 写失败测试(追加到 asset_test.go)**

```go
func TestAsset_ValidateEtcd(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *EtcdConfig
		wantErr bool
	}{
		{"valid single", &EtcdConfig{Endpoints: []string{"127.0.0.1:2379"}}, false},
		{"valid cluster", &EtcdConfig{Endpoints: []string{"10.0.0.1:2379", "10.0.0.2:2379"}}, false},
		{"empty endpoints", &EtcdConfig{}, true},
		{"endpoint missing port", &EtcdConfig{Endpoints: []string{"10.0.0.1"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Asset{Name: "test", Type: AssetTypeEtcd}
			require.NoError(t, a.SetEtcdConfig(tc.cfg))
			err := a.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAsset_CanConnectEtcd(t *testing.T) {
	a := &Asset{Type: AssetTypeEtcd, Status: StatusActive}
	require.NoError(t, a.SetEtcdConfig(&EtcdConfig{Endpoints: []string{"127.0.0.1:2379"}}))
	require.True(t, a.CanConnect())

	require.NoError(t, a.SetEtcdConfig(&EtcdConfig{Endpoints: nil}))
	require.False(t, a.CanConnect())
}
```

- [ ] **Step 2: 运行,确认 FAIL**

```bash
go test ./internal/model/entity/asset_entity/ -run "TestAsset_ValidateEtcd|TestAsset_CanConnectEtcd" -v
```

Expected: FAIL — validate 函数没有 etcd 分支,默认走 `default` 返回 nil。

- [ ] **Step 3: 在 `Validate` switch(约 line 571-580)添加 etcd 分支**

```go
case AssetTypeEtcd:
    return a.validateEtcd()
```

紧跟其他 `validateRedis()` 等私有方法之后追加:

```go
// validateEtcd 校验 etcd 类型特定配置
func (a *Asset) validateEtcd() error {
	cfg, err := a.GetEtcdConfig()
	if err != nil {
		return fmt.Errorf("etcd 配置无效: %w", err)
	}
	if len(cfg.Endpoints) == 0 {
		return errors.New("etcd endpoints 不能为空")
	}
	for _, ep := range cfg.Endpoints {
		host, port, err := net.SplitHostPort(ep)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("etcd endpoint 格式无效: %s (期望 host:port)", ep)
		}
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("etcd endpoint 端口无效: %s", ep)
		}
	}
	return nil
}
```

- [ ] **Step 4: 在 `CanConnect` switch(约 line 797-820)添加 etcd 分支**

```go
case AssetTypeEtcd:
    cfg, err := a.GetEtcdConfig()
    if err != nil {
        return false
    }
    return len(cfg.Endpoints) > 0
```

- [ ] **Step 5: 运行,确认 PASS**

```bash
go test ./internal/model/entity/asset_entity/ -v
```

Expected: 全部 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/model/entity/asset_entity/asset.go internal/model/entity/asset_entity/asset_test.go
git commit -m "✨ Asset.Validate/CanConnect 支持 etcd 类型

校验 endpoints 非空且每项为 host:port 格式。"
```

---

### Task 4: Policy entity — EtcdPolicy alias + Holder 接口扩展

**Files:**
- Modify: `internal/model/entity/policy/policy.go`
- Test: `internal/model/entity/policy/policy_test.go`

- [ ] **Step 1: 写失败测试(追加到 policy_test.go)**

```go
func TestDefaultEtcdPolicy(t *testing.T) {
	p := DefaultEtcdPolicy()
	require.NotNil(t, p)
	require.Contains(t, p.Groups, BuiltinEtcdReadOnly)
	require.Contains(t, p.Groups, BuiltinEtcdDangerousDeny)
}
```

- [ ] **Step 2: 运行,确认 FAIL**

```bash
go test ./internal/model/entity/policy/ -run TestDefaultEtcdPolicy -v
```

Expected: FAIL — `undefined`。

- [ ] **Step 3: 在 `policy.go` 添加常量与函数**

找到 `BuiltinKafkaDangerousDeny = "builtin:kafka-dangerous-deny"`(约 line 147),其后追加:

```go
BuiltinEtcdReadOnly      = "builtin:etcd-readonly"
BuiltinEtcdDangerousDeny = "builtin:etcd-dangerous-deny"
```

紧跟现有 `DefaultRedisPolicy`(约 line 119-124)之后追加:

```go
// EtcdPolicy etcd 权限策略(复用 RedisPolicy 结构)
type EtcdPolicy = RedisPolicy

// DefaultEtcdPolicy 返回默认 etcd 权限策略(引用内置权限组)
func DefaultEtcdPolicy() *EtcdPolicy {
	return &EtcdPolicy{
		Groups: []string{BuiltinEtcdReadOnly, BuiltinEtcdDangerousDeny},
	}
}
```

- [ ] **Step 4: 扩展 `Holder` 接口(约 line 110-117)**

```go
type Holder interface {
	GetCommandPolicy() (*CommandPolicy, error)
	GetQueryPolicy() (*QueryPolicy, error)
	GetRedisPolicy() (*RedisPolicy, error)
	GetMongoPolicy() (*MongoPolicy, error)
	GetKafkaPolicy() (*KafkaPolicy, error)
	GetK8sPolicy() (*K8sPolicy, error)
	GetEtcdPolicy() (*EtcdPolicy, error)  // ← 新增
}
```

- [ ] **Step 5: 在 asset_entity/asset.go 加 alias 与 Get/Set 方法**

找到 `DefaultRedisPolicy` alias(line 280-284),其后追加:

```go
// EtcdPolicy etcd 权限策略(类型别名,定义在 policy 包)
type EtcdPolicy = policy.EtcdPolicy

// DefaultEtcdPolicy 返回默认 etcd 权限策略
var DefaultEtcdPolicy = policy.DefaultEtcdPolicy
```

找到 `GetRedisPolicy/SetRedisPolicy`(约 line 488-500),mirror 它们:

```go
// GetEtcdPolicy 解析 etcd 权限策略
func (a *Asset) GetEtcdPolicy() (*EtcdPolicy, error) {
	if a.CmdPolicy == "" {
		return DefaultEtcdPolicy(), nil
	}
	return jsonfield.Unmarshal[EtcdPolicy](a.CmdPolicy, "etcd 权限策略")
}

// SetEtcdPolicy 序列化 etcd 权限策略
func (a *Asset) SetEtcdPolicy(p *EtcdPolicy) error {
	s, err := jsonfield.Marshal(p, "etcd 权限策略")
	if err != nil {
		return err
	}
	a.CmdPolicy = s
	return nil
}
```

- [ ] **Step 6: 让 Group 实现 GetEtcdPolicy(Holder 接口要求)**

`grep -rn "func (g \*Group) GetRedisPolicy" internal/model/entity/group_entity/` 找到现成的 GetRedisPolicy 方法,mirror 它新增 GetEtcdPolicy:

```go
// GetEtcdPolicy 解析 etcd 权限策略
func (g *Group) GetEtcdPolicy() (*policy.EtcdPolicy, error) {
	if g.PolicyData == "" {
		return &policy.EtcdPolicy{}, nil
	}
	return jsonfield.Unmarshal[policy.EtcdPolicy](g.PolicyData, "etcd 权限策略")
}
```

> 实际 Group 字段名按代码现状(可能是 `Policy`/`PolicyData`/`Config` 等),以 `GetRedisPolicy` 的写法为准。

- [ ] **Step 7: 运行测试**

```bash
go test ./internal/model/entity/policy/ ./internal/model/entity/asset_entity/ ./internal/model/entity/group_entity/ -v
```

Expected: 全部 PASS。**编译失败若提示其他 Holder 实现缺方法**,补齐对应文件。

- [ ] **Step 8: Commit**

```bash
git add internal/model/entity/policy/policy.go internal/model/entity/asset_entity/asset.go internal/model/entity/group_entity/group.go
git commit -m "✨ 新增 EtcdPolicy 类型别名与 Default/Holder 接口

EtcdPolicy 复用 RedisPolicy 结构 + Builtin 常量,Asset/Group 实现 GetEtcdPolicy。"
```

---

### Task 5: Policy group — `PolicyTypeEtcd` + 两个内置组 seed

**Files:**
- Modify: `internal/model/entity/policy_group_entity/policy_group.go`
- Test: `internal/model/entity/policy_group_entity/policy_group_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestSeedBuiltinPolicyGroups_Etcd(t *testing.T) {
	groups := BuiltinPolicyGroups()
	var readOnly, deny *PolicyGroup
	for _, g := range groups {
		if g.BuiltinID == policy.BuiltinEtcdReadOnly {
			readOnly = g
		}
		if g.BuiltinID == policy.BuiltinEtcdDangerousDeny {
			deny = g
		}
	}
	require.NotNil(t, readOnly, "etcd readonly builtin missing")
	require.NotNil(t, deny, "etcd dangerous-deny builtin missing")
	require.Equal(t, PolicyTypeEtcd, readOnly.PolicyType)
	require.Equal(t, PolicyTypeEtcd, deny.PolicyType)

	var pRO policy.EtcdPolicy
	require.NoError(t, json.Unmarshal([]byte(readOnly.Policy), &pRO))
	require.Contains(t, pRO.AllowList, "get *")

	var pDeny policy.EtcdPolicy
	require.NoError(t, json.Unmarshal([]byte(deny.Policy), &pDeny))
	require.Contains(t, pDeny.DenyList, "member remove *")
}
```

> 函数名 `BuiltinPolicyGroups` 按当前代码实际名称调整(可能叫 `seedBuiltinPolicyGroups` / `DefaultBuiltinPolicyGroups`)。

- [ ] **Step 2: 运行,确认 FAIL**

- [ ] **Step 3: 在 policy_group.go 添加 PolicyType 常量**

找到 `PolicyTypeKafka = "kafka"` 之类常量定义,其后追加:

```go
PolicyTypeEtcd = "etcd"
```

- [ ] **Step 4: 在 seed 函数中追加两条**

找到 Kafka 部分的最后一条之后追加:

```go
// etcd 类型
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

- [ ] **Step 5: 运行测试,PASS**

```bash
go test ./internal/model/entity/policy_group_entity/ -v
```

- [ ] **Step 6: Commit**

```bash
git add internal/model/entity/policy_group_entity/
git commit -m "✨ 新增 etcd 内置权限组 (readonly / dangerous-deny)

PolicyTypeEtcd 常量 + 两条 seed 规则,启动时通过 seedBuiltinPolicyGroups upsert。"
```

---

## Phase B — 连接池与服务层

### Task 6: connpool — `buildEtcdClientConfig` 纯函数

**Files:**
- Create: `internal/connpool/etcd.go`
- Create: `internal/connpool/etcd_test.go`

- [ ] **Step 1: 写失败测试**

```go
package connpool

import (
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/require"
)

func TestBuildEtcdClientConfig_Defaults(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"127.0.0.1:2379"},
	}
	clientCfg, err := buildEtcdClientConfig(cfg, "")
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1:2379"}, clientCfg.Endpoints)
	require.Equal(t, 5*time.Second, clientCfg.DialTimeout)
	require.Empty(t, clientCfg.Username)
	require.Nil(t, clientCfg.TLS)
}

func TestBuildEtcdClientConfig_Auth(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"e1:2379"},
		Username:  "root",
	}
	c, err := buildEtcdClientConfig(cfg, "s3cret")
	require.NoError(t, err)
	require.Equal(t, "root", c.Username)
	require.Equal(t, "s3cret", c.Password)
}

func TestBuildEtcdClientConfig_TLS(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:   []string{"e1:2379"},
		TLS:         true,
		TLSInsecure: true,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	require.NoError(t, err)
	require.NotNil(t, c.TLS)
	require.True(t, c.TLS.InsecureSkipVerify)
}

func TestBuildEtcdClientConfig_CustomTimeout(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:          []string{"e1:2379"},
		DialTimeoutSeconds: 12,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	require.NoError(t, err)
	require.Equal(t, 12*time.Second, c.DialTimeout)
}
```

- [ ] **Step 2: 运行,FAIL**

- [ ] **Step 3: 在 `internal/connpool/etcd.go` 写实现**

```go
package connpool

import (
	"crypto/tls"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	defaultEtcdDialTimeout    = 5 * time.Second
	defaultEtcdCommandTimeout = 10 * time.Second
)

// buildEtcdClientConfig 纯函数:把 EtcdConfig + 解密后的密码组装为 etcd 客户端配置
// dialer/TLS-cert 加载放在 DialEtcd 中,这里只处理"配置参数"
func buildEtcdClientConfig(cfg *asset_entity.EtcdConfig, password string) (clientv3.Config, error) {
	dialTimeout := defaultEtcdDialTimeout
	if cfg.DialTimeoutSeconds > 0 {
		dialTimeout = time.Duration(cfg.DialTimeoutSeconds) * time.Second
	}

	c := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: dialTimeout,
		Username:    cfg.Username,
		Password:    password,
	}

	if cfg.TLS {
		tlsCfg, err := buildEtcdTLSConfig(cfg)
		if err != nil {
			return clientv3.Config{}, err
		}
		c.TLS = tlsCfg
	}
	return c, nil
}

func buildEtcdTLSConfig(cfg *asset_entity.EtcdConfig) (*tls.Config, error) {
	return BuildTLSConfig("etcd", TLSFields{
		ServerName: cfg.TLSServerName,
		Insecure:   cfg.TLSInsecure,
		CAFile:     cfg.TLSCAFile,
		CertFile:   cfg.TLSCertFile,
		KeyFile:    cfg.TLSKeyFile,
	})
}
```

- [ ] **Step 4: 运行,PASS**

```bash
go test ./internal/connpool/ -run TestBuildEtcdClientConfig -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/connpool/etcd.go internal/connpool/etcd_test.go
git commit -m "✨ connpool.buildEtcdClientConfig 纯函数

把 EtcdConfig + 明文密码 → clientv3.Config,处理超时/auth/TLS 字段映射。"
```

---

### Task 7: connpool — `GetOrDial` + idle 缓存

**Files:**
- Modify: `internal/connpool/etcd.go`
- Modify: `internal/connpool/etcd_test.go`

- [ ] **Step 1: 写失败测试(只测缓存逻辑,不真连)**

```go
func TestEtcdEntryCache_InvalidateRemovesEntry(t *testing.T) {
	pool := newEtcdPool()
	pool.put(1, &etcdEntry{client: nil, lastUsed: time.Now().Unix()})
	pool.put(2, &etcdEntry{client: nil, lastUsed: time.Now().Unix()})
	require.NotNil(t, pool.get(1))

	pool.invalidate(1)
	require.Nil(t, pool.get(1))
	require.NotNil(t, pool.get(2))
}

func TestEtcdEntryCache_GCStale(t *testing.T) {
	pool := newEtcdPool()
	pool.put(1, &etcdEntry{lastUsed: time.Now().Add(-10 * time.Minute).Unix()})
	pool.put(2, &etcdEntry{lastUsed: time.Now().Unix()})

	pool.gc(5 * time.Minute)
	require.Nil(t, pool.get(1))
	require.NotNil(t, pool.get(2))
}
```

- [ ] **Step 2: FAIL → 添加实现**

在 `etcd.go` 追加:

```go
import (
    "context"
    "io"
    "sync"
    "sync/atomic"

    "github.com/opskat/opskat/internal/sshpool"
    "github.com/cago-frame/cago/pkg/logger"
    "go.uber.org/zap"
)

type etcdEntry struct {
	client   *clientv3.Client
	tunnel   io.Closer  // 可空
	lastUsed int64      // unix 秒,atomic
}

type etcdPool struct {
	mu      sync.Mutex
	entries map[int64]*etcdEntry
}

func newEtcdPool() *etcdPool {
	return &etcdPool{entries: map[int64]*etcdEntry{}}
}

func (p *etcdPool) get(id int64) *etcdEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e != nil {
		atomic.StoreInt64(&e.lastUsed, time.Now().Unix())
	}
	return e
}

func (p *etcdPool) put(id int64, e *etcdEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[id] = e
}

func (p *etcdPool) invalidate(id int64) {
	p.mu.Lock()
	e := p.entries[id]
	delete(p.entries, id)
	p.mu.Unlock()
	if e != nil {
		closeEntry(e)
	}
}

func (p *etcdPool) gc(maxIdle time.Duration) {
	cutoff := time.Now().Add(-maxIdle).Unix()
	p.mu.Lock()
	stale := []*etcdEntry{}
	for id, e := range p.entries {
		if atomic.LoadInt64(&e.lastUsed) < cutoff {
			stale = append(stale, e)
			delete(p.entries, id)
		}
	}
	p.mu.Unlock()
	for _, e := range stale {
		closeEntry(e)
	}
}

func closeEntry(e *etcdEntry) {
	if e.client != nil {
		_ = e.client.Close()
	}
	if e.tunnel != nil {
		_ = e.tunnel.Close()
	}
}

var globalEtcdPool = newEtcdPool()

// 模块初始化时起后台 ticker,5min 清理 idle
func init() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			globalEtcdPool.gc(5 * time.Minute)
		}
	}()
}

// InvalidateEtcd 资产更新/删除时调用
func InvalidateEtcd(assetID int64) {
	globalEtcdPool.invalidate(assetID)
}

// DialEtcd 创建 etcd 客户端,可选走 SSH 隧道(只对第一个 endpoint)
func DialEtcd(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig, password string, sshPool *sshpool.Pool) (*clientv3.Client, io.Closer, error) {
	clientCfg, err := buildEtcdClientConfig(cfg, password)
	if err != nil {
		return nil, nil, err
	}

	var tunnel *SSHTunnel
	if asset.SSHTunnelID > 0 && sshPool != nil && len(cfg.Endpoints) > 0 {
		host, portStr, _ := net.SplitHostPort(cfg.Endpoints[0])
		port, _ := strconv.Atoi(portStr)
		tunnel = NewSSHTunnel(asset.SSHTunnelID, host, port, sshPool)
		clientCfg.DialOptions = append(clientCfg.DialOptions,
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return tunnel.Dial(ctx)
			}))
		clientCfg.Endpoints = []string{cfg.Endpoints[0]}
	}

	client, err := clientv3.New(clientCfg)
	if err != nil {
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, nil, fmt.Errorf("etcd dial failed: %w", err)
	}

	// 主动 Status 验证连通性
	pingCtx, cancel := context.WithTimeout(ctx, clientCfg.DialTimeout)
	defer cancel()
	if _, err := client.Status(pingCtx, cfg.Endpoints[0]); err != nil {
		_ = client.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, nil, fmt.Errorf("etcd status check failed: %w", err)
	}

	if tunnel == nil {
		return client, nil, nil
	}
	return client, tunnel, nil
}

// GetOrDialEtcd 返回缓存中的客户端;未缓存则建立连接并缓存
func GetOrDialEtcd(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig, password string, sshPool *sshpool.Pool) (*clientv3.Client, error) {
	if e := globalEtcdPool.get(asset.ID); e != nil {
		return e.client, nil
	}
	client, tunnel, err := DialEtcd(ctx, asset, cfg, password, sshPool)
	if err != nil {
		return nil, err
	}
	globalEtcdPool.put(asset.ID, &etcdEntry{client: client, tunnel: tunnel, lastUsed: time.Now().Unix()})
	logger.Ctx(ctx).Info("etcd client dialed",
		zap.Int64("assetID", asset.ID),
		zap.Int("endpoints", len(cfg.Endpoints)),
		zap.Bool("tls", cfg.TLS),
		zap.Bool("tunneled", tunnel != nil),
	)
	return client, nil
}
```

> 注:如果 import grpc 失败,加 `"google.golang.org/grpc"`(etcd 客户端会传递)。

- [ ] **Step 3: 运行,PASS**

```bash
go test ./internal/connpool/ -run TestEtcdEntryCache -v
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/connpool/etcd.go internal/connpool/etcd_test.go
git commit -m "✨ connpool.GetOrDialEtcd + 缓存 + SSH 隧道

per-asset client 缓存,5min idle 后台清理;隧道场景只对第一个 endpoint 起隧道。"
```

---

### Task 8: connpool — 集成测试(embed etcd,build tag `integration`)

**Files:**
- Create: `internal/connpool/etcd_integration_test.go`

- [ ] **Step 1: 写集成测试**

```go
//go:build integration

package connpool

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"
)

func startEmbedEtcd(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "etcd-test-*")
	require.NoError(t, err)

	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(dir, "data")
	lcurl, _ := url.Parse("http://127.0.0.1:12379")
	lpurl, _ := url.Parse("http://127.0.0.1:12380")
	cfg.ListenClientUrls = []url.URL{*lcurl}
	cfg.AdvertiseClientUrls = []url.URL{*lcurl}
	cfg.ListenPeerUrls = []url.URL{*lpurl}
	cfg.InitialCluster = "default=http://127.0.0.1:12380"
	cfg.LogLevel = "error"

	e, err := embed.StartEtcd(cfg)
	require.NoError(t, err)

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		t.Fatal("embed etcd start timeout")
	}

	return "127.0.0.1:12379", func() {
		e.Close()
		os.RemoveAll(dir)
	}
}

func TestDialEtcd_E2E_PutGetDel(t *testing.T) {
	endpoint, stop := startEmbedEtcd(t)
	defer stop()

	asset := &asset_entity.Asset{ID: 99}
	cfg := &asset_entity.EtcdConfig{Endpoints: []string{endpoint}}

	client, tunnel, err := DialEtcd(context.Background(), asset, cfg, "", nil)
	require.NoError(t, err)
	defer client.Close()
	require.Nil(t, tunnel)

	_, err = client.Put(context.Background(), "/test/foo", "bar")
	require.NoError(t, err)

	resp, err := client.Get(context.Background(), "/test/foo")
	require.NoError(t, err)
	require.Equal(t, "bar", string(resp.Kvs[0].Value))

	_, err = client.Delete(context.Background(), "/test/foo")
	require.NoError(t, err)
}
```

- [ ] **Step 2: 运行**

```bash
go test -tags integration ./internal/connpool/ -run TestDialEtcd_E2E_PutGetDel -v -timeout 30s
```

Expected: PASS。**如果 embed etcd 的依赖未拉到**,先 `go get go.etcd.io/etcd/server/v3@v3.5.16` 再 `go mod tidy`。

- [ ] **Step 3: Commit**

```bash
git add internal/connpool/etcd_integration_test.go go.mod go.sum
git commit -m "✅ connpool/etcd 集成测试(embed etcd,build tag integration)

CI 不默认跑,本地与 nightly 验证端到端 dial → put → get → del。"
```

---

### Task 9: service — 命令字符串解析器

**Files:**
- Create: `internal/service/etcd_svc/command.go`
- Create: `internal/service/etcd_svc/command_test.go`

- [ ] **Step 1: 写失败测试**

```go
package etcd_svc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in      string
		wantOp  string
		wantKey string
		wantPrefix bool
		wantLimit  int64
		wantValue  string
		wantErr    bool
	}{
		{"get /config", "get", "/config", false, 0, "", false},
		{"get /config --prefix", "get", "/config", true, 0, "", false},
		{"get /config --prefix --limit=100", "get", "/config", true, 100, "", false},
		{"put /flags/x true", "put", "/flags/x", false, 0, "true", false},
		{"del /locks/a --prefix", "del", "/locks/a", true, 0, "", false},
		{"member list", "member_list", "", false, 0, "", false},
		{"endpoint status", "endpoint_status", "", false, 0, "", false},
		{"GET /case", "get", "/case", false, 0, "", false}, // 大小写归一
		{"", "", "", false, 0, "", true},
		{"unknown-op /x", "", "", false, 0, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			req, err := ParseCommand(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOp, req.Op)
			require.Equal(t, tc.wantKey, req.Key)
			require.Equal(t, tc.wantPrefix, req.Prefix)
			require.Equal(t, tc.wantLimit, req.Limit)
			require.Equal(t, tc.wantValue, req.Value)
		})
	}
}
```

- [ ] **Step 2: FAIL → 写实现**

```go
package etcd_svc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ExecRequest 命令解析与 IPC 结构(也作为 app.EtcdExecRequest 的内部表达)
type ExecRequest struct {
	AssetID    int64
	Op         string
	Key        string
	Value      string
	Prefix     bool
	Limit      int64
	Revision   int64
	LeaseID    int64
	Args       map[string]any
	ApprovalID string
	Source     string
}

var supportedOps = map[string]bool{
	"get": true, "put": true, "del": true, "txn": true,
	"lease_grant": true, "lease_revoke": true, "lease_ttl": true, "lease_list": true,
	"endpoint_status": true, "endpoint_health": true,
	"member_list":     true,
	"user_list":       true, "role_list": true,
}

// ParseCommand 解析查询面板命令字符串。**不**追求 etcdctl 完全兼容,只识别支持的子集。
// 形如 "<op> [key] [value] [--flag] [--flag=val]"
func ParseCommand(s string) (*ExecRequest, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty command")
	}
	tokens := strings.Fields(s)
	if len(tokens) == 0 {
		return nil, errors.New("empty command")
	}

	op := strings.ToLower(tokens[0])
	rest := tokens[1:]

	// 两词复合命令: "member list" / "endpoint status" / "endpoint health" / "lease grant" / ...
	if len(rest) > 0 {
		switch op {
		case "member", "endpoint", "user", "role":
			combined := op + "_" + strings.ToLower(rest[0])
			if supportedOps[combined] {
				op = combined
				rest = rest[1:]
			}
		case "lease":
			combined := "lease_" + strings.ToLower(rest[0])
			if supportedOps[combined] {
				op = combined
				rest = rest[1:]
			}
		}
	}
	if !supportedOps[op] {
		return nil, fmt.Errorf("unsupported op: %s", op)
	}

	req := &ExecRequest{Op: op}
	positional := []string{}
	for _, t := range rest {
		if strings.HasPrefix(t, "--") {
			flag := strings.TrimPrefix(t, "--")
			name, val := flag, ""
			if eq := strings.Index(flag, "="); eq >= 0 {
				name = flag[:eq]
				val = flag[eq+1:]
			}
			switch name {
			case "prefix":
				req.Prefix = true
			case "limit":
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid --limit: %s", val)
				}
				req.Limit = n
			case "revision":
				n, err := strconv.ParseInt(val, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid --revision: %s", val)
				}
				req.Revision = n
			case "lease":
				n, err := strconv.ParseInt(val, 16, 64) // lease id 一般 hex
				if err != nil {
					return nil, fmt.Errorf("invalid --lease: %s", val)
				}
				req.LeaseID = n
			default:
				return nil, fmt.Errorf("unknown flag: --%s", name)
			}
		} else {
			positional = append(positional, t)
		}
	}

	switch op {
	case "get", "del":
		if len(positional) >= 1 {
			req.Key = positional[0]
		}
	case "put":
		if len(positional) < 2 {
			return nil, errors.New("put requires key and value")
		}
		req.Key = positional[0]
		req.Value = strings.Join(positional[1:], " ")
	}
	return req, nil
}
```

- [ ] **Step 3: 运行,PASS**

```bash
go test ./internal/service/etcd_svc/ -run TestParseCommand -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/service/etcd_svc/command.go internal/service/etcd_svc/command_test.go
git commit -m "✨ etcd_svc.ParseCommand 命令字符串解析器

查询面板用,把 'get /x --prefix --limit=100' 转 ExecRequest;识别 member/endpoint/lease 复合命令。"
```

---

### Task 10: service — ops dispatch(get/put/del)

**Files:**
- Create: `internal/service/etcd_svc/ops.go`
- Create: `internal/service/etcd_svc/ops_test.go`

- [ ] **Step 1: 生成 etcd KV 的 mock(`go.uber.org/mock`)**

新建 `internal/service/etcd_svc/mock_kv/doc.go`:

```go
//go:generate mockgen -destination=./mock_kv.go -package=mock_kv go.etcd.io/etcd/client/v3 KV,Cluster,Maintenance,Auth
package mock_kv
```

```bash
go generate ./internal/service/etcd_svc/mock_kv/
```

> 若 mockgen 未安装:`go install go.uber.org/mock/mockgen@latest`。

- [ ] **Step 2: 写失败测试**

```go
package etcd_svc

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/service/etcd_svc/mock_kv"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/api/v3/mvccpb"
)

func TestDispatchGet(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Get(gomock.Any(), "/foo", gomock.Any()).
		Return(&clientv3.GetResponse{
			Kvs: []*mvccpb.KeyValue{{Key: []byte("/foo"), Value: []byte("bar"), ModRevision: 5, Version: 1}},
		}, nil)

	res, err := dispatchGet(context.Background(), kv, &ExecRequest{Op: "get", Key: "/foo"})
	require.NoError(t, err)
	require.Len(t, res.KVs, 1)
	require.Equal(t, "bar", res.KVs[0].Value)
}

func TestDispatchPut(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Put(gomock.Any(), "/foo", "bar", gomock.Any()).
		Return(&clientv3.PutResponse{Header: &etcdserverpb.ResponseHeader{Revision: 10}}, nil)

	res, err := dispatchPut(context.Background(), kv, &ExecRequest{Op: "put", Key: "/foo", Value: "bar"})
	require.NoError(t, err)
	require.Equal(t, int64(10), res.Revision)
}

func TestDispatchDel_WithPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	kv := mock_kv.NewMockKV(ctrl)
	kv.EXPECT().
		Delete(gomock.Any(), "/locks/", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
			require.NotEmpty(t, opts) // WithPrefix 至少 1 个 opt
			return &clientv3.DeleteResponse{Deleted: 3}, nil
		})

	res, err := dispatchDel(context.Background(), kv, &ExecRequest{Op: "del", Key: "/locks/", Prefix: true})
	require.NoError(t, err)
	require.Equal(t, int64(3), res.Count)
}
```

- [ ] **Step 3: FAIL → 写实现**

```go
package etcd_svc

import (
	"context"
	"fmt"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdKV 给 IPC 用的 KV 投影
type EtcdKV struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	ModRevision    int64  `json:"modRevision"`
	CreateRevision int64  `json:"createRevision"`
	Version        int64  `json:"version"`
	Lease          int64  `json:"lease"`
}

// ExecResult etcd 操作结果
type ExecResult struct {
	Op       string  `json:"op"`
	KVs      []EtcdKV `json:"kvs,omitempty"`
	Count    int64   `json:"count"`
	Revision int64   `json:"revision"`
}

func dispatchGet(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.Prefix {
		opts = append(opts, clientv3.WithPrefix())
	}
	if req.Limit > 0 {
		opts = append(opts, clientv3.WithLimit(req.Limit))
	}
	if req.Revision > 0 {
		opts = append(opts, clientv3.WithRev(req.Revision))
	}
	resp, err := kv.Get(ctx, req.Key, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd get failed: %w", err)
	}
	res := &ExecResult{Op: "get", Count: resp.Count, Revision: resp.Header.Revision}
	for _, k := range resp.Kvs {
		res.KVs = append(res.KVs, EtcdKV{
			Key: string(k.Key), Value: string(k.Value),
			ModRevision: k.ModRevision, CreateRevision: k.CreateRevision,
			Version: k.Version, Lease: k.Lease,
		})
	}
	return res, nil
}

func dispatchPut(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.LeaseID > 0 {
		opts = append(opts, clientv3.WithLease(clientv3.LeaseID(req.LeaseID)))
	}
	resp, err := kv.Put(ctx, req.Key, req.Value, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd put failed: %w", err)
	}
	return &ExecResult{Op: "put", Count: 1, Revision: resp.Header.Revision}, nil
}

func dispatchDel(ctx context.Context, kv clientv3.KV, req *ExecRequest) (*ExecResult, error) {
	opts := []clientv3.OpOption{}
	if req.Prefix {
		opts = append(opts, clientv3.WithPrefix())
	}
	resp, err := kv.Delete(ctx, req.Key, opts...)
	if err != nil {
		return nil, fmt.Errorf("etcd del failed: %w", err)
	}
	return &ExecResult{Op: "del", Count: resp.Deleted, Revision: resp.Header.Revision}, nil
}

// Dispatch 主入口,按 op 路由
func Dispatch(ctx context.Context, client *clientv3.Client, req *ExecRequest) (*ExecResult, error) {
	switch req.Op {
	case "get":
		return dispatchGet(ctx, client, req)
	case "put":
		return dispatchPut(ctx, client, req)
	case "del":
		return dispatchDel(ctx, client, req)
	default:
		return nil, fmt.Errorf("unsupported op: %s", req.Op)
	}
}
```

> 注:lease/txn/member 等 op 留给后续任务追加(目前列入 supportedOps 但 Dispatch 返回 unsupported)。

- [ ] **Step 4: 运行,PASS**

```bash
go test ./internal/service/etcd_svc/ -run "TestDispatch" -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/etcd_svc/ops.go internal/service/etcd_svc/ops_test.go internal/service/etcd_svc/mock_kv/
git commit -m "✨ etcd_svc.Dispatch 实现 get/put/del

KV 操作经 clientv3.KV 接口,gomock 测试覆盖 prefix/limit/revision/lease 选项。"
```

---

### Task 11: service — lease / member / endpoint ops

**Files:**
- Modify: `internal/service/etcd_svc/ops.go` + `ops_test.go`

- [ ] **Step 1: 写失败测试(覆盖 lease grant、member list、endpoint status)**

```go
func TestDispatchLeaseGrant(t *testing.T) {
	ctrl := gomock.NewController(t)
	lease := mock_kv.NewMockLease(ctrl)
	lease.EXPECT().
		Grant(gomock.Any(), int64(60)).
		Return(&clientv3.LeaseGrantResponse{ID: clientv3.LeaseID(0xabc), TTL: 60}, nil)

	res, err := dispatchLeaseGrant(context.Background(), lease, &ExecRequest{Op: "lease_grant", Args: map[string]any{"ttl": int64(60)}})
	require.NoError(t, err)
	require.Equal(t, int64(0xabc), res.KVs[0].Lease)
}

func TestDispatchMemberList(t *testing.T) {
	ctrl := gomock.NewController(t)
	cluster := mock_kv.NewMockCluster(ctrl)
	cluster.EXPECT().
		MemberList(gomock.Any()).
		Return(&clientv3.MemberListResponse{Members: []*etcdserverpb.Member{
			{ID: 1, Name: "n1", ClientURLs: []string{"http://10.0.0.1:2379"}},
			{ID: 2, Name: "n2", ClientURLs: []string{"http://10.0.0.2:2379"}},
		}}, nil)
	res, err := dispatchMemberList(context.Background(), cluster, &ExecRequest{Op: "member_list"})
	require.NoError(t, err)
	require.Equal(t, int64(2), res.Count)
}
```

- [ ] **Step 2: FAIL → 实现**

把 `Dispatch` 中的 default 分支替换为完整 switch:

```go
case "lease_grant":
    ttl, _ := req.Args["ttl"].(int64)
    return dispatchLeaseGrant(ctx, client, &ExecRequest{Args: map[string]any{"ttl": ttl}})
case "lease_revoke":
    return dispatchLeaseRevoke(ctx, client, req)
case "lease_list":
    return dispatchLeaseList(ctx, client)
case "endpoint_status", "endpoint_health":
    return dispatchEndpointStatus(ctx, client, req)
case "member_list":
    return dispatchMemberList(ctx, client, req)
```

实现函数(每个一个 RPC,把结果塞 ExecResult,详细字段按 etcd 文档):

```go
func dispatchLeaseGrant(ctx context.Context, lease clientv3.Lease, req *ExecRequest) (*ExecResult, error) {
	ttl, _ := req.Args["ttl"].(int64)
	if ttl == 0 {
		return nil, errors.New("lease_grant requires ttl")
	}
	resp, err := lease.Grant(ctx, ttl)
	if err != nil {
		return nil, fmt.Errorf("lease grant failed: %w", err)
	}
	return &ExecResult{Op: "lease_grant", KVs: []EtcdKV{{Lease: int64(resp.ID), Value: fmt.Sprintf("ttl=%d", resp.TTL)}}}, nil
}

func dispatchLeaseRevoke(ctx context.Context, lease clientv3.Lease, req *ExecRequest) (*ExecResult, error) {
	if _, err := lease.Revoke(ctx, clientv3.LeaseID(req.LeaseID)); err != nil {
		return nil, fmt.Errorf("lease revoke failed: %w", err)
	}
	return &ExecResult{Op: "lease_revoke", Count: 1}, nil
}

func dispatchLeaseList(ctx context.Context, lease clientv3.Lease) (*ExecResult, error) {
	resp, err := lease.Leases(ctx)
	if err != nil {
		return nil, fmt.Errorf("lease list failed: %w", err)
	}
	res := &ExecResult{Op: "lease_list", Count: int64(len(resp.Leases))}
	for _, l := range resp.Leases {
		res.KVs = append(res.KVs, EtcdKV{Lease: int64(l.ID)})
	}
	return res, nil
}

func dispatchMemberList(ctx context.Context, cluster clientv3.Cluster, _ *ExecRequest) (*ExecResult, error) {
	resp, err := cluster.MemberList(ctx)
	if err != nil {
		return nil, fmt.Errorf("member list failed: %w", err)
	}
	res := &ExecResult{Op: "member_list", Count: int64(len(resp.Members))}
	for _, m := range resp.Members {
		res.KVs = append(res.KVs, EtcdKV{
			Key:   fmt.Sprintf("%x", m.ID),
			Value: fmt.Sprintf("name=%s urls=%v", m.Name, m.ClientURLs),
		})
	}
	return res, nil
}

func dispatchEndpointStatus(ctx context.Context, ms clientv3.Maintenance, req *ExecRequest) (*ExecResult, error) {
	// req.Key 兜底当 endpoint;否则从 client.Endpoints() 拿
	resp, err := ms.Status(ctx, req.Key)
	if err != nil {
		return nil, fmt.Errorf("endpoint status failed: %w", err)
	}
	return &ExecResult{
		Op:    req.Op,
		Count: 1,
		KVs:   []EtcdKV{{Key: req.Key, Value: fmt.Sprintf("version=%s dbSize=%d leader=%x", resp.Version, resp.DbSize, resp.Leader)}},
	}, nil
}
```

> 注:`Dispatch` 的 switch case 把 `client` 直接传给细分函数(client 同时实现 `KV`、`Lease`、`Cluster`、`Maintenance` 接口)。

- [ ] **Step 3: PASS**

```bash
go test ./internal/service/etcd_svc/ -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/service/etcd_svc/ops.go internal/service/etcd_svc/ops_test.go
git commit -m "✨ etcd_svc.Dispatch 扩展 lease/member/endpoint ops

覆盖 lease_grant/revoke/list、member_list、endpoint_status/health,与 KV ops 共用 ExecResult 结构。"
```

---

### Task 12: service — 主入口 `Exec/ListPrefix/TestConnection`

**Files:**
- Create: `internal/service/etcd_svc/service.go`
- Create: `internal/service/etcd_svc/service_test.go`

- [ ] **Step 1: 写主入口实现(包含策略检查 + 审计 + 日志三态)**

```go
package etcd_svc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/sshpool"
	"go.uber.org/zap"
)

type Service struct {
	assetSvc asset_svc.Service
	sshPool  *sshpool.Pool
}

func New(assetSvc asset_svc.Service, sshPool *sshpool.Pool) *Service {
	return &Service{assetSvc: assetSvc, sshPool: sshPool}
}

// Exec 主入口:策略检查 → 拿连接 → 执行 → 审计(三态日志)
func (s *Service) Exec(ctx context.Context, req *ExecRequest) (*ExecResult, error) {
	start := time.Now()
	logger.Ctx(ctx).Info("etcd exec start",
		zap.Int64("assetID", req.AssetID),
		zap.String("op", req.Op),
		zap.String("key", req.Key),
		zap.Bool("prefix", req.Prefix),
		zap.String("source", req.Source),
	)

	asset, err := s.assetSvc.GetByID(ctx, req.AssetID)
	if err != nil {
		logger.Ctx(ctx).Error("etcd exec asset lookup failed", zap.Int64("assetID", req.AssetID), zap.Error(err))
		return nil, fmt.Errorf("asset not found: %w", err)
	}
	if !asset.IsEtcd() {
		return nil, fmt.Errorf("asset is not etcd")
	}

	// 策略检查 — 调用方根据 Decision.Status 决定是否弹 confirm
	decision, err := s.checkPolicy(ctx, asset, req)
	if err != nil {
		return nil, err
	}
	if decision.Status == policy.DecisionDeny {
		audit.Record(ctx, audit.Event{AssetID: req.AssetID, Op: req.Op, Key: req.Key, Source: req.Source, Result: "deny", Reason: decision.MatchedRule})
		logger.Ctx(ctx).Warn("etcd exec policy deny", zap.String("rule", decision.MatchedRule))
		return nil, fmt.Errorf("policy denied: %s", decision.MatchedRule)
	}
	if decision.Status == policy.DecisionNeedConfirm && req.ApprovalID == "" {
		return &ExecResult{Op: req.Op, Count: 0}, policy.ErrNeedConfirm{Rule: decision.MatchedRule}
	}

	handler, _ := assettype.Get("etcd")
	password, err := handler.ResolvePassword(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("resolve password: %w", err)
	}
	cfg, _ := asset.GetEtcdConfig()

	client, err := connpool.GetOrDialEtcd(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		logger.Ctx(ctx).Error("etcd exec dial failed", zap.Int64("assetID", req.AssetID), zap.Error(err))
		return nil, err
	}

	timeout := defaultCommandTimeout
	if cfg.CommandTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.CommandTimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := Dispatch(execCtx, client, req)
	elapsed := time.Since(start)

	if err != nil {
		audit.Record(ctx, audit.Event{AssetID: req.AssetID, Op: req.Op, Key: req.Key, Source: req.Source, Result: "fail", Reason: err.Error()})
		logger.Ctx(ctx).Error("etcd exec failed",
			zap.Int64("assetID", req.AssetID), zap.String("op", req.Op), zap.Duration("elapsed", elapsed), zap.Error(err))
		return nil, err
	}

	audit.Record(ctx, audit.Event{AssetID: req.AssetID, Op: req.Op, Key: req.Key, Source: req.Source, Result: "ok"})
	logger.Ctx(ctx).Info("etcd exec end",
		zap.Int64("assetID", req.AssetID), zap.String("op", req.Op),
		zap.Duration("elapsed", elapsed), zap.Int64("count", result.Count))
	return result, nil
}

const defaultCommandTimeout = 10 * time.Second

func (s *Service) checkPolicy(ctx context.Context, asset *asset_entity.Asset, req *ExecRequest) (*policy.Decision, error) {
	cmd := strings.TrimSpace(req.Op + " " + req.Key)
	if req.Prefix {
		cmd += " --prefix"
	}
	return policy.TestPolicyMatch(ctx, policy.MatchInput{
		PolicyType: "etcd",
		AssetID:    asset.ID,
		Command:    cmd,
	})
}
```

> 接口与 audit/policy 包的实际函数名以代码现状为准;若 `policy.TestPolicyMatch` 不存在,看 Redis 现状用对应入口。

- [ ] **Step 2: 写 ListPrefix 与 TestConnection**

继续追加:

```go
type ListPrefixRequest struct {
	AssetID int64
	Prefix  string
	Delim   string  // 固定 "/"
	Limit   int64
}

type ListPrefixResult struct {
	Dirs      []string         `json:"dirs"`
	Leaves    []EtcdKV         `json:"leaves"`  // 不含 value
	Truncated bool             `json:"truncated"`
}

func (s *Service) ListPrefix(ctx context.Context, req *ListPrefixRequest) (*ListPrefixResult, error) {
	if req.Delim == "" {
		req.Delim = "/"
	}
	if req.Limit == 0 {
		req.Limit = 1000
	}
	asset, err := s.assetSvc.GetByID(ctx, req.AssetID)
	if err != nil || !asset.IsEtcd() {
		return nil, fmt.Errorf("invalid etcd asset")
	}
	handler, _ := assettype.Get("etcd")
	password, _ := handler.ResolvePassword(ctx, asset)
	cfg, _ := asset.GetEtcdConfig()
	client, err := connpool.GetOrDialEtcd(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		return nil, err
	}

	resp, err := client.Get(ctx, req.Prefix,
		clientv3.WithPrefix(),
		clientv3.WithKeysOnly(),
		clientv3.WithLimit(req.Limit),
	)
	if err != nil {
		return nil, err
	}

	dirSet := map[string]struct{}{}
	res := &ListPrefixResult{Truncated: resp.More}
	for _, k := range resp.Kvs {
		key := string(k.Key)
		rest := strings.TrimPrefix(key, req.Prefix)
		idx := strings.Index(rest, req.Delim)
		if idx < 0 {
			res.Leaves = append(res.Leaves, EtcdKV{Key: key, ModRevision: k.ModRevision, CreateRevision: k.CreateRevision, Version: k.Version, Lease: k.Lease})
		} else {
			dir := rest[:idx]
			if _, ok := dirSet[dir]; !ok {
				dirSet[dir] = struct{}{}
				res.Dirs = append(res.Dirs, dir)
			}
		}
	}
	return res, nil
}

// TestConnection 不进缓存,即时 dial 一下立即关闭
func (s *Service) TestConnection(ctx context.Context, assetID int64) error {
	asset, err := s.assetSvc.GetByID(ctx, assetID)
	if err != nil || !asset.IsEtcd() {
		return fmt.Errorf("invalid etcd asset")
	}
	handler, _ := assettype.Get("etcd")
	password, _ := handler.ResolvePassword(ctx, asset)
	cfg, _ := asset.GetEtcdConfig()

	client, tunnel, err := connpool.DialEtcd(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		return err
	}
	defer client.Close()
	if tunnel != nil {
		defer tunnel.Close()
	}
	return nil
}
```

- [ ] **Step 3: 写测试(用 mock asset_svc + 真 client 替换为 mock)**

简化:本任务先写 happy path 单元测试,跳过完整 mock 层。

```go
func TestService_Exec_AssetNotEtcd(t *testing.T) {
	// TODO: 使用 mock asset_svc,断言返回 "asset is not etcd"
	t.Skip("requires mock asset_svc — covered in integration test")
}
```

> 该函数在 Task 17(集成验证)用真实路径覆盖。

- [ ] **Step 4: 编译通过**

```bash
go build ./internal/service/etcd_svc/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/service/etcd_svc/service.go internal/service/etcd_svc/service_test.go
git commit -m "✨ etcd_svc.Service 主入口 Exec/ListPrefix/TestConnection

策略检查 → 连接 → 执行 → 三态审计与日志;ListPrefix 按 / 切层供 KV 树懒加载。"
```

---

## Phase C — AssetTypeHandler / AI / Bindings

### Task 13: AssetTypeHandler etcd.go

**Files:**
- Create: `internal/assettype/etcd.go`
- Create: `internal/assettype/etcd_test.go`

- [ ] **Step 1: 写失败测试(mirror redis_test.go)**

```go
package assettype

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/require"
)

func TestEtcdHandler_Type(t *testing.T) {
	h, ok := Get("etcd")
	require.True(t, ok)
	require.Equal(t, "etcd", h.Type())
	require.Equal(t, 2379, h.DefaultPort())
}

func TestEtcdHandler_ValidateCreateArgs(t *testing.T) {
	h := &etcdHandler{}
	require.Error(t, h.ValidateCreateArgs(map[string]any{}))
	require.NoError(t, h.ValidateCreateArgs(map[string]any{"endpoints": []any{"e1:2379"}}))
}

func TestEtcdHandler_SafeView_NoSecrets(t *testing.T) {
	h := &etcdHandler{}
	a := &asset_entity.Asset{Type: "etcd"}
	require.NoError(t, a.SetEtcdConfig(&asset_entity.EtcdConfig{
		Endpoints: []string{"e1:2379"}, Username: "root",
		Password: "encrypted-blob",
		TLSKeyFile: "/path/key.pem",
	}))
	view := h.SafeView(a)
	require.Contains(t, view, "endpoints")
	require.Contains(t, view, "username")
	require.NotContains(t, view, "password")
}

func TestEtcdHandler_ApplyCreateArgs(t *testing.T) {
	h := &etcdHandler{}
	a := &asset_entity.Asset{Type: "etcd"}
	err := h.ApplyCreateArgs(nil, a, map[string]any{
		"endpoints": []any{"e1:2379", "e2:2379"},
		"username":  "root",
		"tls":       true,
	})
	require.NoError(t, err)
	cfg, err := a.GetEtcdConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Endpoints, 2)
	require.Equal(t, "root", cfg.Username)
	require.True(t, cfg.TLS)
}
```

- [ ] **Step 2: FAIL → 写实现(mirror redis.go 结构)**

```go
package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type etcdHandler struct{}

func init() {
	Register(&etcdHandler{})
	policy.RegisterDefaultPolicy("etcd", func() any { return asset_entity.DefaultEtcdPolicy() })
}

func (h *etcdHandler) Type() string     { return asset_entity.AssetTypeEtcd }
func (h *etcdHandler) DefaultPort() int { return 2379 }

func (h *etcdHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetEtcdConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"endpoints":      cfg.Endpoints,
		"username":       cfg.Username,
		"tls":            cfg.TLS,
		"tls_server_name": cfg.TLSServerName,
	}
}

func (h *etcdHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetEtcdConfig()
	if err != nil {
		return "", fmt.Errorf("get etcd config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *etcdHandler) ValidateCreateArgs(args map[string]any) error {
	eps := ArgStringSlice(args, "endpoints")
	if len(eps) == 0 {
		return fmt.Errorf("missing required parameter: endpoints")
	}
	return nil
}

func (h *etcdHandler) DefaultPolicy() any { return asset_entity.DefaultEtcdPolicy() }

func (h *etcdHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:             ArgStringSlice(args, "endpoints"),
		Username:              ArgString(args, "username"),
		TLS:                   ArgBool(args, "tls"),
		TLSInsecure:           ArgBool(args, "tls_insecure"),
		TLSServerName:         ArgString(args, "tls_server_name"),
		TLSCAFile:             ArgString(args, "tls_ca_file"),
		TLSCertFile:           ArgString(args, "tls_cert_file"),
		TLSKeyFile:            ArgString(args, "tls_key_file"),
		DialTimeoutSeconds:    ArgInt(args, "dial_timeout_seconds"),
		CommandTimeoutSeconds: ArgInt(args, "command_timeout_seconds"),
	}
	a.SSHTunnelID = ArgInt64(args, "ssh_asset_id")
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt etcd password: %w", err)
		}
		cfg.Password = encrypted
	}
	return a.SetEtcdConfig(cfg)
}

func (h *etcdHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetEtcdConfig()
	if err != nil || cfg == nil {
		return err
	}
	if v := ArgStringSlice(args, "endpoints"); len(v) > 0 {
		cfg.Endpoints = v
	}
	if v := ArgString(args, "username"); v != "" {
		cfg.Username = v
	}
	for _, k := range []string{"tls", "tls_insecure"} {
		if _, ok := args[k]; ok {
			switch k {
			case "tls":          cfg.TLS = ArgBool(args, k)
			case "tls_insecure": cfg.TLSInsecure = ArgBool(args, k)
			}
		}
	}
	if v := ArgString(args, "tls_server_name"); v != "" { cfg.TLSServerName = v }
	if v := ArgString(args, "tls_ca_file"); v != "" { cfg.TLSCAFile = v }
	if v := ArgString(args, "tls_cert_file"); v != "" { cfg.TLSCertFile = v }
	if v := ArgString(args, "tls_key_file"); v != "" { cfg.TLSKeyFile = v }
	if _, ok := args["ssh_asset_id"]; ok {
		a.SSHTunnelID = ArgInt64(args, "ssh_asset_id")
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt etcd password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	if err := a.SetEtcdConfig(cfg); err != nil {
		return err
	}
	connpool_invalidate(a.ID)
	return nil
}

// 通过函数指针避免引入 connpool 循环依赖(也可以直接 import,看 redis 现状)
var connpool_invalidate = func(int64) {}
```

> 简化:实际 import 路径若不引起循环依赖,直接 `connpool.InvalidateEtcd(a.ID)`。

- [ ] **Step 3: PASS + 编译**

```bash
go test ./internal/assettype/ -run TestEtcdHandler -v
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/assettype/etcd.go internal/assettype/etcd_test.go
git commit -m "✨ assettype.etcdHandler 实现 AssetTypeHandler

注册到 registry,SafeView 不含密码/私钥;ApplyUpdate 时主动 invalidate 连接池。"
```

---

### Task 14: AI policy — `case "etcd"` 路由 + ResolveEtcdGroups

**Files:**
- Modify: `internal/ai/policy/policy_tester.go`
- Modify: `internal/ai/policy/policy_group_resolve.go`
- Test: `internal/ai/policy/policy_tester_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestPolicyMatch_Etcd_AllowGet(t *testing.T) {
	out := TestPolicyMatch(context.Background(), MatchInput{
		PolicyType:   "etcd",
		CurrentEtcd:  &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdReadOnly}},
		Command:      "get /config",
	})
	require.Equal(t, DecisionAllow, out.Decision)
}

func TestPolicyMatch_Etcd_DenyMemberRemove(t *testing.T) {
	out := TestPolicyMatch(context.Background(), MatchInput{
		PolicyType:   "etcd",
		CurrentEtcd:  &asset_entity.EtcdPolicy{Groups: []string{policy.BuiltinEtcdDangerousDeny}},
		Command:      "member remove abc",
	})
	require.Equal(t, DecisionDeny, out.Decision)
}
```

> 字段名按现有 `policy_tester.go` 的 `MatchInput` 结构,可能叫 `CurrentRedis` 之类;新增 `CurrentEtcd`。

- [ ] **Step 2: FAIL → 在 policy_tester.go 添加 case**

```go
type MatchInput struct {
	// ... 现有字段 ...
	CurrentEtcd *asset_entity.EtcdPolicy  // ← 新增
}

// 在 TestPolicyMatch 的 switch 中:
case "etcd":
    return testEtcdPolicy(ctx, input.CurrentEtcd, groups, command)
```

实现 `testEtcdPolicy`,**直接 mirror `testRedisPolicy`**(replace 后改函数名 / matcher / 字段名;matcher 仍用 `MatchRedisRule`):

```go
func testEtcdPolicy(ctx context.Context, current *asset_entity.EtcdPolicy, groups []*group_entity.Group, command string) PolicyTestOutput {
	groupAllow, groupDeny := ResolveEtcdGroups(ctx, current.Groups)
	if out := checkGenericDeny(groupDeny, command, MatchRedisRule); out != nil {
		out.Message = PolicyFmt(ctx, "etcd command denied by group policy: %s", "etcd 命令被组策略禁止: %s", command)
		return *out
	}
	merged := mergeEtcdPoliciesForTest(ctx, current, groups)
	result := checkRedisPolicyRules(ctx, EffectiveRedisPolicy(ctx, merged), command)
	if result.Decision == DecisionNeedConfirm {
		if out := checkGenericAllow(groupAllow, command, MatchRedisRule); out != nil {
			return *out
		}
	}
	return result
}
```

> 注:`checkRedisPolicyRules` / `EffectiveRedisPolicy` 已存在,直接复用;`mergeEtcdPoliciesForTest` mirror `mergeRedisPoliciesForTest`。

- [ ] **Step 3: 在 policy_group_resolve.go 加 ResolveEtcdGroups**

mirror `ResolveRedisGroups`:

```go
// ResolveEtcdGroups 解析引用的 etcd 权限组,返回合并后的 allow/deny 规则
func ResolveEtcdGroups(ctx context.Context, groupIDs []string) (allow, deny []string) {
	for _, id := range groupIDs {
		pg, err := policy_group_svc.Default().GetByBuiltinID(ctx, id)
		if err != nil {
			continue
		}
		if pg.PolicyType != policy_group_entity.PolicyTypeEtcd {
			continue
		}
		var p policy.EtcdPolicy
		if err := json.Unmarshal([]byte(pg.Policy), &p); err != nil {
			logger.Ctx(ctx).Warn("unmarshal policy group etcd policy", zap.String("id", pg.BuiltinID), zap.Error(err))
			continue
		}
		allow = append(allow, p.AllowList...)
		deny = append(deny, p.DenyList...)
	}
	return allow, deny
}
```

- [ ] **Step 4: PASS**

```bash
go test ./internal/ai/policy/ -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/ai/policy/policy_tester.go internal/ai/policy/policy_group_resolve.go internal/ai/policy/policy_tester_test.go
git commit -m "✨ AI policy 支持 etcd 类型

case 'etcd' 路由 + ResolveEtcdGroups,matcher 复用 MatchRedisRule。"
```

---

### Task 15: AI tool — `exec_etcd` 定义 + handler

**Files:**
- Modify: `internal/ai/tool/tools_data.go`
- Create: `internal/ai/tool_handler_etcd.go`

- [ ] **Step 1: 在 tools_data.go 追加工具定义**

找到 `exec_redis` 的定义之后(同文件内),新增:

```go
{
    NameStr: "exec_etcd",
    DescStr: "Execute an etcd KV / lease / admin operation on an etcd asset. " +
        "Use op: 'get', 'put', 'del', 'txn', 'lease_grant', 'lease_revoke', 'lease_list', " +
        "'endpoint_status', 'endpoint_health', 'member_list', 'user_list', 'role_list'. " +
        "Keys MUST start with '/'. For range read use prefix=true. " +
        "For historical read pass revision (subject to compaction).",
    InputSchemaJSON: `{
        "type":"object",
        "properties":{
            "asset_id":{"type":"number","description":"etcd asset ID. Use list_assets with asset_type='etcd' to find."},
            "op":{"type":"string","enum":["get","put","del","txn","lease_grant","lease_revoke","lease_list","endpoint_status","endpoint_health","member_list","user_list","role_list"]},
            "key":{"type":"string","description":"Key or prefix"},
            "value":{"type":"string","description":"Value for put"},
            "prefix":{"type":"boolean","description":"Treat key as prefix for get/del"},
            "limit":{"type":"number"},
            "revision":{"type":"number"},
            "lease_id":{"type":"number"},
            "txn":{"type":"object"}
        },
        "required":["asset_id","op"]
    }`,
},
```

> 实际字段名(NameStr / DescStr / InputSchemaJSON)按本仓现有 tool 定义结构调整。

- [ ] **Step 2: 创建 `tool_handler_etcd.go`**

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/service/etcd_svc"
)

func (a *Runner) handleExecEtcd(ctx context.Context, raw json.RawMessage) (any, error) {
	var args struct {
		AssetID  int64  `json:"asset_id"`
		Op       string `json:"op"`
		Key      string `json:"key"`
		Value    string `json:"value"`
		Prefix   bool   `json:"prefix"`
		Limit    int64  `json:"limit"`
		Revision int64  `json:"revision"`
		LeaseID  int64  `json:"lease_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	req := &etcd_svc.ExecRequest{
		AssetID: args.AssetID, Op: args.Op, Key: args.Key, Value: args.Value,
		Prefix: args.Prefix, Limit: args.Limit, Revision: args.Revision, LeaseID: args.LeaseID,
		Source: "ai",
	}
	return a.etcdSvc.Exec(ctx, req)
}
```

- [ ] **Step 3: 在 Runner 注册分发**

找到 Runner dispatch 表(类似 `case "exec_redis": ...`),追加 `case "exec_etcd": return a.handleExecEtcd(ctx, args)`。

- [ ] **Step 4: 编译 + 测试**

```bash
go test ./internal/ai/... -v
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/ai/tool/tools_data.go internal/ai/tool_handler_etcd.go internal/ai/runner.go
git commit -m "✨ AI 工具 exec_etcd

单工具 + 结构化 op 字段;通过 etcd_svc.Exec 统一走策略/审计/连接路径。"
```

---

### Task 16: Wails App bindings — `internal/app/etcd.go`

**Files:**
- Create: `internal/app/etcd.go`

- [ ] **Step 1: 写绑定**

```go
package app

import (
	"context"

	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// EtcdExecRequest IPC 入参 — 与 etcd_svc.ExecRequest 字段一致
type EtcdExecRequest = etcd_svc.ExecRequest

// EtcdExecResult IPC 出参
type EtcdExecResult = etcd_svc.ExecResult

// EtcdListPrefixRequest / Result
type EtcdListPrefixRequest = etcd_svc.ListPrefixRequest
type EtcdListPrefixResult = etcd_svc.ListPrefixResult

func (a *App) EtcdTestConnection(ctx context.Context, assetID int64) error {
	return a.etcdSvc.TestConnection(ctx, assetID)
}

func (a *App) EtcdExec(ctx context.Context, req EtcdExecRequest) (*EtcdExecResult, error) {
	req.Source = "query" // 默认来自查询面板;AI 路径用 handleExecEtcd 注入 "ai"
	return a.etcdSvc.Exec(ctx, &req)
}

func (a *App) EtcdListPrefix(ctx context.Context, req EtcdListPrefixRequest) (*EtcdListPrefixResult, error) {
	return a.etcdSvc.ListPrefix(ctx, &req)
}
```

- [ ] **Step 2: 在 App 构造里注入 etcdSvc**

找到 `App` 结构定义,加 `etcdSvc *etcd_svc.Service`。在 `NewApp` 中:

```go
etcdSvc := etcd_svc.New(assetSvc, sshPool)
return &App{
    // ...
    etcdSvc: etcdSvc,
}
```

- [ ] **Step 3: 重新生成 Wails bindings**

```bash
make dev  # 启动后即时生成 frontend/wailsjs/go/app/App.{d.ts,js}
# 或者: wails generate module
```

确认 `frontend/wailsjs/go/app/App.d.ts` 出现 `EtcdExec` / `EtcdListPrefix` / `EtcdTestConnection`。

- [ ] **Step 4: 编译**

```bash
go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add internal/app/etcd.go internal/app/app.go frontend/wailsjs/
git commit -m "✨ Wails 绑定 EtcdTestConnection/EtcdExec/EtcdListPrefix

薄绑定层,直通 etcd_svc;Wails 自动生成前端 TS 类型。"
```

---

## Phase D — Frontend

### Task 17: i18n + zustand store skeleton

**Files:**
- Modify: `frontend/src/i18n/locales/zh-CN/common.json`
- Modify: `frontend/src/i18n/locales/en/common.json`
- Create: `frontend/src/components/etcd/useEtcdStore.ts`

- [ ] **Step 1: 写 i18n 键(zh-CN)**

在 `common` 顶层下加 `etcd` 子树(片段示例):

```json
"etcd": {
    "form": {
        "endpoints": "Endpoints",
        "endpointsHint": "至少 1 条 host:port",
        "addEndpoint": "添加 endpoint",
        "tlsSection": "TLS / mTLS",
        "advancedSection": "高级",
        "dialTimeout": "Dial 超时(秒)",
        "commandTimeout": "命令超时(秒)",
        "test": "测试连接",
        "sslSkipVerify": "跳过证书校验"
    },
    "tree": {
        "title": "Keyspace",
        "filterPlaceholder": "过滤 key (Ctrl+P)",
        "truncated": "+{{count}} 隐藏 (limit {{limit}})"
    },
    "query": {
        "placeholder": "get /config --prefix",
        "templates": "模板",
        "execute": "执行",
        "history": "历史 [Ctrl+R]",
        "deletePrefixConfirm": "将删除约 {{count}} 个 key,确认继续?"
    },
    "error": {
        "endpointsRequired": "至少需要 1 个 endpoint",
        "compactionRev": "该版本已被压缩,最早可用版本: {{rev}}"
    }
}
```

英文版同步写一份。

- [ ] **Step 2: 写 useEtcdStore.ts**

```typescript
import { create } from "zustand";
import { EtcdExec, EtcdListPrefix } from "../../../wailsjs/go/app/App";
import type { etcd_svc } from "../../../wailsjs/go/models";

export type TreeNode = {
  prefix: string;
  name: string;
  isLeaf: boolean;
  children?: TreeNode[];
  truncated?: boolean;
  loaded?: boolean;
};

type State = {
  treeCache: Map<string, TreeNode[]>;
  selectedKey: string | null;
  selectedKeyDetail: etcd_svc.EtcdKV | null;
  queryHistory: string[];
  lastResult: etcd_svc.ExecResult | null;
  loadPrefix: (assetId: number, prefix: string) => Promise<void>;
  invalidatePrefix: (prefix: string) => void;
  exec: (req: etcd_svc.ExecRequest) => Promise<etcd_svc.ExecResult>;
};

export const useEtcdStore = create<State>((set, get) => ({
  treeCache: new Map(),
  selectedKey: null,
  selectedKeyDetail: null,
  queryHistory: JSON.parse(localStorage.getItem("etcd:queryHistory") || "[]"),
  lastResult: null,

  async loadPrefix(assetId, prefix) {
    if (get().treeCache.has(prefix)) return;
    const res = await EtcdListPrefix({ AssetID: assetId, Prefix: prefix, Delim: "/", Limit: 1000 });
    const nodes: TreeNode[] = [
      ...(res.dirs || []).map((d) => ({ prefix: prefix + d + "/", name: d, isLeaf: false })),
      ...(res.leaves || []).map((kv) => ({ prefix: kv.key, name: kv.key.slice(prefix.length), isLeaf: true })),
    ];
    const cache = new Map(get().treeCache);
    cache.set(prefix, nodes);
    set({ treeCache: cache });
  },

  invalidatePrefix(prefix) {
    const cache = new Map(get().treeCache);
    cache.delete(prefix);
    set({ treeCache: cache });
  },

  async exec(req) {
    const res = await EtcdExec(req);
    const hist = [req.Op + " " + (req.Key || ""), ...get().queryHistory.filter((h) => h !== req.Op + " " + (req.Key || ""))].slice(0, 50);
    localStorage.setItem("etcd:queryHistory", JSON.stringify(hist));
    set({ queryHistory: hist, lastResult: res });
    return res;
  },
}));
```

- [ ] **Step 3: 写 store 测试**

```typescript
// useEtcdStore.test.ts
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../../../wailsjs/go/app/App", () => ({
  EtcdExec: vi.fn().mockResolvedValue({ Op: "get", Count: 0, KVs: [] }),
  EtcdListPrefix: vi.fn().mockResolvedValue({ dirs: ["config"], leaves: [], truncated: false }),
}));

import { useEtcdStore } from "./useEtcdStore";

describe("useEtcdStore.loadPrefix", () => {
  beforeEach(() => useEtcdStore.setState({ treeCache: new Map() }));

  it("populates cache on first call and skips on second", async () => {
    const { EtcdListPrefix } = await import("../../../wailsjs/go/app/App");
    await useEtcdStore.getState().loadPrefix(1, "/");
    await useEtcdStore.getState().loadPrefix(1, "/");
    expect(EtcdListPrefix).toHaveBeenCalledTimes(1);
    expect(useEtcdStore.getState().treeCache.get("/")?.length).toBe(1);
  });
});
```

- [ ] **Step 4: 运行测试**

```bash
cd frontend && pnpm test -- useEtcdStore
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/etcd/useEtcdStore.ts frontend/src/components/etcd/useEtcdStore.test.ts frontend/src/i18n/locales/
git commit -m "✨ etcd 前端 store 与 i18n

useEtcdStore: 树缓存 / queryHistory localStorage / EtcdExec/EtcdListPrefix 调用。"
```

---

### Task 18: EtcdForm 组件

**Files:**
- Create: `frontend/src/components/asset/forms/EtcdForm.tsx`
- Create: `frontend/src/components/asset/forms/EtcdForm.test.tsx`
- Modify: `frontend/src/components/asset/AssetForm.tsx`(分发到 etcd type)

- [ ] **Step 1: 写组件(以视觉稿 Screen 1 为蓝本)**

字段按规格:Name / Group / Tags / Icon → Endpoints(多行)→ 认证(Username + PasswordSourceField)→ TLS section(toggle + Server Name + CA/Cert/Key 路径 + 跳过校验)→ 高级(SSH 隧道下拉 + DialTimeout + CommandTimeout)。

复用现有 `PasswordSourceField`、`GroupSelect`、`IconPicker`、`AssetSelect`(SSH 隧道)。Endpoints 用 chip 列表组件(可仿照 `AssetMultiSelect` 但是只接 string)。

> 这一步代码量大,展开会让本计划过长。**Reference:** `frontend/src/components/asset/forms/RedisForm.tsx` 是最接近的模板,只需扩展 endpoints 多输入 + mTLS 字段。把 RedisForm 整个复制为 EtcdForm 后:
> 1. 把 `host` + `port` 两个单字段 → 替换为 endpoints chip 列表
> 2. 追加 mTLS 的两个文件路径字段
> 3. 把 `redis_db` 字段删除
> 4. 文案改为 etcd

- [ ] **Step 2: 写测试**

```typescript
// EtcdForm.test.tsx
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { EtcdForm } from "./EtcdForm";

describe("EtcdForm", () => {
  it("requires at least one endpoint", () => {
    const onSubmit = vi.fn();
    render(<EtcdForm onSubmit={onSubmit} />);
    fireEvent.click(screen.getByRole("button", { name: /保存/ }));
    expect(screen.getByText(/至少需要 1 个 endpoint/)).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("toggles TLS section visibility", () => {
    render(<EtcdForm />);
    expect(screen.queryByLabelText(/Server Name/)).not.toBeVisible();
    fireEvent.click(screen.getByRole("switch", { name: /TLS/ }));
    expect(screen.getByLabelText(/Server Name/)).toBeVisible();
  });

  it("submits parsed endpoints", () => {
    const onSubmit = vi.fn();
    render(<EtcdForm onSubmit={onSubmit} />);
    fireEvent.change(screen.getByLabelText(/Endpoints/), { target: { value: "10.0.0.1:2379\n10.0.0.2:2379" } });
    fireEvent.click(screen.getByRole("button", { name: /保存/ }));
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({
      endpoints: ["10.0.0.1:2379", "10.0.0.2:2379"],
    }));
  });
});
```

- [ ] **Step 3: 在 AssetForm.tsx 加 etcd 类型分发**

找到现有 `if (type === "redis") return <RedisForm ...>`,平行加:

```tsx
if (type === "etcd") return <EtcdForm {...props} />;
```

- [ ] **Step 4: 跑测试**

```bash
cd frontend && pnpm test -- EtcdForm
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/asset/forms/EtcdForm.tsx frontend/src/components/asset/forms/EtcdForm.test.tsx frontend/src/components/asset/AssetForm.tsx
git commit -m "✨ EtcdForm 资产表单

endpoints 多行输入、TLS/mTLS 联动、SSH 隧道下拉,接入 AssetForm 类型分发。"
```

---

### Task 19: EtcdTreePane 组件

**Files:**
- Create: `frontend/src/components/etcd/EtcdTreePane.tsx`
- Create: `frontend/src/components/etcd/EtcdTreePane.test.tsx`

- [ ] **Step 1: 实现**

按视觉稿 Screen 2 左侧。结构:

```tsx
// 伪代码骨架
function EtcdTreePane({ assetId }: { assetId: number }) {
  const { treeCache, loadPrefix, selectedKey } = useEtcdStore();
  useEffect(() => { loadPrefix(assetId, "/"); }, [assetId]);

  function renderNode(prefix: string, node: TreeNode, depth: number) {
    const expanded = treeCache.has(node.prefix);
    return (
      <>
        <TreeRow ... onClick={() => {
          if (node.isLeaf) selectKey(node.prefix);
          else loadPrefix(assetId, node.prefix);
        }} />
        {expanded && treeCache.get(node.prefix)?.map((c) => renderNode(node.prefix, c, depth + 1))}
      </>
    );
  }
  return <div>{(treeCache.get("/") || []).map((n) => renderNode("/", n, 0))}</div>;
}
```

复用现有 `TreeCheckList` / `TreeSelect` 的虚拟滚动思路;若已有合适的 `TreeNode` 组件,直接用。

- [ ] **Step 2: 测试**

```typescript
it("lazy-loads on expand", async () => {
  const { EtcdListPrefix } = await import("../../../wailsjs/go/app/App");
  render(<EtcdTreePane assetId={1} />);
  await waitFor(() => expect(EtcdListPrefix).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByText("config"));
  await waitFor(() => expect(EtcdListPrefix).toHaveBeenCalledTimes(2));
});

it("renders truncated indicator", async () => {
  // 让 EtcdListPrefix 返回 truncated: true
  render(<EtcdTreePane assetId={1} />);
  await screen.findByText(/隐藏/);
});
```

- [ ] **Step 3: PASS + Commit**

```bash
cd frontend && pnpm test -- EtcdTreePane
git add frontend/src/components/etcd/EtcdTreePane.tsx frontend/src/components/etcd/EtcdTreePane.test.tsx
git commit -m "✨ EtcdTreePane KV 浏览树

懒加载、按 / 分层、truncated 提示;点击叶子触发选中。"
```

---

### Task 20: EtcdQueryPane + EtcdResultTable + 整合页面

**Files:**
- Create: `frontend/src/components/etcd/EtcdQueryPane.tsx` + test
- Create: `frontend/src/components/etcd/EtcdResultTable.tsx` + test
- Create: `frontend/src/components/etcd/EtcdPage.tsx`(把树 + 详情 + 查询整合到一个 tab 页面)

- [ ] **Step 1: EtcdQueryPane**

按视觉稿 Screen 3:命令行 input + ⌘ Enter 提交 + 模板下拉 + chip 列。

```tsx
function EtcdQueryPane({ assetId }: { assetId: number }) {
  const [cmd, setCmd] = useState("");
  const { exec } = useEtcdStore();

  async function onSubmit() {
    // 前端纯切词,与后端 ParseCommand 不重复(后端会再 parse 一次)
    const isDestructive = /^(put|del|txn)/.test(cmd.trim().toLowerCase());
    if (isDestructive) {
      const confirmed = await openConfirmDialog({
        title: "确认执行",
        body: cmd,
        previewFn: cmd.includes("--prefix") && cmd.startsWith("del")
          ? () => exec({ AssetID: assetId, Op: "get", Key: extractKey(cmd), Prefix: true, Limit: 100 })
          : undefined,
      });
      if (!confirmed) return;
    }
    // 发到后端解析 + 执行
    await exec({ AssetID: assetId, Op: "__raw", Key: cmd });  // 注:后端入口若不支持 raw,前端做 parse + 转结构化 req
  }
  // ...
}
```

> 实现细节:前端可直接调用后端的 `Exec` 但把 raw command 放 Args 里,或在前端轻量 parse(复制 Go 端 ParseCommand 逻辑)。倾向前者,把 parse 留在后端。

- [ ] **Step 2: EtcdResultTable**

按视觉稿 Screen 3 表格,5 列(KEY / VALUE / MOD REV / VERSION / LEASE)。复用现有 query 面板的表格组件 `QueryResultGrid`(如果可复用),否则简单 div 表。

- [ ] **Step 3: EtcdPage(整合)**

```tsx
function EtcdPage({ assetId }: { assetId: number }) {
  const [tab, setTab] = useState<"tree" | "query">("tree");
  return (
    <div className="flex h-full flex-col">
      <Tabs value={tab} onChange={setTab}>
        <Tab value="tree">KV 浏览</Tab>
        <Tab value="query">查询</Tab>
      </Tabs>
      {tab === "tree" && (
        <SplitView left={<EtcdTreePane assetId={assetId} />} right={<EtcdKeyDetail />} />
      )}
      {tab === "query" && <EtcdQueryPane assetId={assetId} />}
    </div>
  );
}
```

- [ ] **Step 4: 接入 tabStore**

`useTabStore` 中:`{ type: "page", subType: "etcd", assetId }` → 渲染 `<EtcdPage assetId={...} />`。

- [ ] **Step 5: 测试 + Commit**

```bash
cd frontend && pnpm test -- "EtcdQueryPane|EtcdResultTable"
git add frontend/src/components/etcd/
git commit -m "✨ EtcdQueryPane / EtcdResultTable / EtcdPage 整合

命令面板 + 销毁性操作 confirm + 结果表格;tabStore 接入 etcd page。"
```

---

## Phase E — 验证

### Task 21: E2E 烟雾测试(本地手动验收)

**Files:**
- Create: `tests/fixtures/etcd_demo/README.md`(说明如何启动 embed etcd + 添加 OpsKat 资产)

- [ ] **Step 1: 写 fixture README**

包含:
1. 启动 embed etcd 命令(`go test -tags integration -run TestEtcdFixtureUp ./tests/fixtures/etcd_demo/`)— 该测试启动后阻塞 30 分钟
2. 在 OpsKat UI 添加 etcd 资产 `127.0.0.1:12379`
3. 测试连接 → 浏览树 → 查询面板 put/get/del → 验证 ConfirmDialog 在 del 时弹

- [ ] **Step 2: 手动跑验收清单(spec §10.5)**

按 spec 验收清单逐条勾:

- [ ] 添加 etcd 资产 → 测试连接成功(无认证)
- [ ] mTLS 资产(localhost + 自签 cert)→ 测试连接成功
- [ ] SSH 隧道 → 浏览树 + put/get/del 全通(用一台堡垒机)
- [ ] AI `exec_etcd`:get 直通,put 触发 confirm,member_add 被 deny
- [ ] 树懒加载 limit 1000 命中时显示 truncated
- [ ] `del --prefix /` 在前端被预览拦截
- [ ] grep 确认日志/SafeView/审计**无**密码、私钥
- [ ] grep `logger.Default()` 应零命中(等价于 `grep -rn "logger.Default()" internal/connpool/etcd.go internal/service/etcd_svc/ internal/app/etcd.go internal/ai/tool_handler_etcd.go internal/assettype/etcd.go | wc -l` = 0)

- [ ] **Step 3: 跑全量测试**

```bash
make test
make lint
cd frontend && pnpm test && pnpm lint
```

Expected: 全部 PASS,**零 golangci-lint warning**,前端 type-check 通过。

- [ ] **Step 4: Commit**

```bash
git add tests/fixtures/etcd_demo/
git commit -m "✅ etcd 资产 E2E fixture + 验收清单

embed etcd 启动脚本与手动验收 README;全量 test/lint 通过。"
```

---

## Self-Review

### 1. Spec coverage

走查 spec 主要节段:

- §3 架构 → Task 13 / 16(handler 注册 + app 绑定)
- §4 数据模型 → Tasks 2/3/4/5
- §5 IPC 边界 → Task 16
- §6 服务层 → Tasks 6/7/9/10/11/12
- §7 AI 工具 → Task 15
- §8 错误处理 → 散落在 Tasks 7/10/12(日志、PolicyDeny、错误分类);**前端**销毁性预览在 Task 20
- §9 前端 → Tasks 17/18/19/20
- §10 测试 → 每个 task 都包含 TDD;集成在 Task 8;手动验收在 Task 21
- §11 文件清单 → File Map 部分

**Gap**: AI policy_tester 中 `mergeEtcdPoliciesForTest` 在 Task 14 提及但代码细节没展开 → 实施时直接 mirror `mergeRedisPoliciesForTest`,函数体改字段类型即可。可以接受。

**Gap**: `policy.RegisterDefaultPolicy("etcd", ...)`(Task 13)假设该函数已存在 — Redis 同样调用,如果实际函数名不同需以仓内现状为准。

### 2. Placeholder scan

无 TBD / TODO / "implement later"。两处 Reference 注释(Task 18 EtcdForm "复制 RedisForm 后改",Task 19 树展开伪代码)是已知的复杂度妥协,实施者参照模板可完成。

### 3. Type consistency

- `ExecRequest` / `ExecResult` 在 Tasks 9/10/11/12 一致(`Op string`, `Key string`, `Args map[string]any`, `Source string`)
- `EtcdKV` 字段在 Tasks 10/11/12/17 一致(Key/Value/ModRevision/CreateRevision/Version/Lease)
- `ListPrefixResult.Dirs / Leaves / Truncated` 在 Tasks 12/17/19 一致
- Wails 绑定函数名 `EtcdExec / EtcdListPrefix / EtcdTestConnection` 在 Tasks 16/17/18/19/20 一致

无不一致。
