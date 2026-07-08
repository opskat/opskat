# OSS 资产类型 — P1 后端基础 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 OpsKat 后端支持一个可创建、可连接、可浏览的「对象存储 (OSS)」资产类型(S3 兼容,涵盖 S3 / 阿里云 OSS / 腾讯云 COS / MinIO),暴露列 Bucket / 列对象 / 取对象信息 / 删除 / 预签名 URL 的 Wails 绑定。

**Architecture:** 遵循既有资产类型的分层与注册约定(参 `docs/adding-an-asset-type.md`):`asset_entity` 存 `OSSConfig`(JSON,在 `Asset.Config`);`assettype.AssetTypeHandler` 注册处理器(不改分发 switch);`connpool` 用 minio-go 建/缓存客户端;`oss_svc` 是浏览服务,依赖一个自定义窄接口 `Client`(可 mock);`internal/app/oss` 是 Wails 绑定(在此做边界校验);凭证经 `credential_resolver` 解析。以 **etcd** 为一比一参照。

**Tech Stack:** Go 1.26.0 · Wails v2 · `github.com/minio/minio-go/v7`(S3 兼容 SDK)· GORM(cago `db`)· 测试 `testify` + `go.uber.org/mock`(mockgen)。

## Global Constraints

- 模块路径 `github.com/opskat/opskat`;Go `1.26.0`。
- **注册,不 switch**:新类型实现 `assettype.AssetTypeHandler` + `init()` 内 `Register()`,严禁在共享代码里 `if type == "oss"`(AGENTS.md)。
- **边界校验只在 `internal/app/oss/*.go`**;service/repo/connpool 之间信任输入,不加 `if x == nil`。
- **`SafeView` 绝不泄密**:不得返回 `SecretAccessKey` / `CredentialID`。
- **凭证**:AK 明文;SK 走 `credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)`;`OSSConfig` 必须实现 `GetCredentialID() int64` / `GetPassword() string`。
- **不设默认 Bucket 字段**(账号级模型)。
- mock 放 `mock_*/`,用 `go.uber.org/mock`(mockgen)。测试用 `testify`。
- 测试命令:`make test`(全量)/ `go test ./internal/service/oss_svc/... -v`(包级)。后端静态检查用 `golangci-lint run`(非 `go vet`)。
- 提交用 gitmoji + 简短中文主题(与仓库现有 git log 一致);仅在刻意关联 issue 时主题才带 `#编号`。
- 依赖引入后同步 `go mod tidy`。

---

### Task 1: OSSConfig 资产实体配置

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`(资产类型常量块,约 :17-27,加一行)
- Create: `internal/model/entity/asset_entity/oss_config.go`
- Test: `internal/model/entity/asset_entity/oss_config_test.go`

**Interfaces:**
- Produces: 常量 `AssetTypeOSS = "oss"`;类型 `OSSConfig`;方法 `(*Asset).IsOSS() bool`、`(*Asset).GetOSSConfig() (*OSSConfig, error)`、`(*Asset).SetOSSConfig(*OSSConfig) error`;`(*OSSConfig).GetCredentialID() int64`、`(*OSSConfig).GetPassword() string`。

- [ ] **Step 1: 写失败测试**

`internal/model/entity/asset_entity/oss_config_test.go`:
```go
package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSSConfigRoundTrip(t *testing.T) {
	a := &Asset{Type: AssetTypeOSS}
	cfg := &OSSConfig{
		Provider: "s3", Endpoint: "s3.us-east-1.amazonaws.com", Region: "us-east-1",
		AccessKeyID: "AKIA", SecretAccessKey: "cipher", UseSSL: true,
	}
	require.NoError(t, a.SetOSSConfig(cfg))

	got, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", got.Endpoint)
	assert.Equal(t, "us-east-1", got.Region)
	assert.True(t, got.UseSSL)
}

func TestOSSConfigPasswordSource(t *testing.T) {
	cfg := &OSSConfig{CredentialID: 7, SecretAccessKey: "cipher"}
	assert.Equal(t, int64(7), cfg.GetCredentialID())
	assert.Equal(t, "cipher", cfg.GetPassword())
}

func TestGetOSSConfigWrongType(t *testing.T) {
	a := &Asset{Type: AssetTypeSSH}
	_, err := a.GetOSSConfig()
	require.Error(t, err)
}
```

- [ ] **Step 2: 运行,确认失败**

Run: `go test ./internal/model/entity/asset_entity/ -run TestOSSConfig -v`
Expected: 编译失败(`undefined: AssetTypeOSS` / `OSSConfig`)。

- [ ] **Step 3: 加常量**

在 `asset.go` 的资产类型常量块(与 `AssetTypeEtcd = "etcd"` 同处)追加:
```go
	AssetTypeOSS = "oss"
```

- [ ] **Step 4: 建 `oss_config.go`**

`internal/model/entity/asset_entity/oss_config.go`:
```go
package asset_entity

import (
	"errors"

	"github.com/opskat/opskat/internal/pkg/jsonfield"
)

// OSSConfig 是对象存储(OSS)资产的每资产配置,序列化到 Asset.Config。
type OSSConfig struct {
	Provider        string `json:"provider"`        // s3 | aliyun-oss | tencent-cos | minio | s3-compat
	Endpoint        string `json:"endpoint"`        // host 或 scheme://host[:port]
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"` // 内联时为 AES-256-GCM 密文;托管时为空
	CredentialID    int64  `json:"credentialId"`    // >0 表示引用托管密码凭证
	UsePathStyle    bool   `json:"usePathStyle"`
	UseSSL          bool   `json:"useSSL"`
	ConnectTimeout  int    `json:"connectTimeout"` // 秒;0 表示默认
}

// GetCredentialID 实现 credential_resolver.PasswordSource。
func (c *OSSConfig) GetCredentialID() int64 { return c.CredentialID }

// GetPassword 实现 credential_resolver.PasswordSource。
func (c *OSSConfig) GetPassword() string { return c.SecretAccessKey }

// IsOSS 判断资产是否为对象存储类型。
func (a *Asset) IsOSS() bool { return a.Type == AssetTypeOSS }

// GetOSSConfig 解析资产配置 JSON 为 OSSConfig。
func (a *Asset) GetOSSConfig() (*OSSConfig, error) {
	if !a.IsOSS() {
		return nil, errors.New("资产不是 OSS 类型")
	}
	return jsonfield.Unmarshal[OSSConfig](a.Config, "OSS配置")
}

// SetOSSConfig 将 cfg 序列化进资产配置 JSON。
func (a *Asset) SetOSSConfig(cfg *OSSConfig) error {
	s, err := jsonfield.Marshal(cfg, "OSS配置")
	if err != nil {
		return err
	}
	a.Config = s
	return nil
}
```
> 注:`jsonfield` 的确切 import 路径与 `GetEtcdConfig`(asset.go 内)一致——若本文件报路径错,复制 asset.go 顶部的 jsonfield import 行。

- [ ] **Step 5: 运行,确认通过**

Run: `go test ./internal/model/entity/asset_entity/ -run TestOSSConfig -v`
Expected: PASS(3 个用例)。

- [ ] **Step 6: 提交**

```bash
go mod tidy
git add internal/model/entity/asset_entity/asset.go internal/model/entity/asset_entity/oss_config.go internal/model/entity/asset_entity/oss_config_test.go
git commit -m "✨ OSS 资产实体配置 (OSSConfig)"
```

---

### Task 2: OSS 资产类型 handler

**Files:**
- Create: `internal/assettype/oss.go`
- Test: `internal/assettype/oss_test.go`

**Interfaces:**
- Consumes: `AssetTypeOSS`、`OSSConfig`、`(*Asset).Get/SetOSSConfig`(Task 1);`Register`、`Arg*`(`internal/assettype/registry.go`);`credential_resolver.Default().ResolvePasswordGeneric`;`credential_svc.Default().Encrypt`;`connpool.InvalidateOSS`(Task 3)。
- Produces: `ossHandler` 实现 `assettype.AssetTypeHandler`,`init()` 内 `Register(&ossHandler{})`。P1 不设策略:`PolicyKind() == ""`、`DefaultPolicy() == nil`。

- [ ] **Step 1: 写失败测试**(仿 `etcd_test.go`)

`internal/assettype/oss_test.go`:
```go
package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSSHandlerRegistered(t *testing.T) {
	h, ok := Get(asset_entity.AssetTypeOSS)
	require.True(t, ok)
	assert.Equal(t, "oss", h.Type())
}

func TestOSSValidateCreateArgs(t *testing.T) {
	h := &ossHandler{}
	require.Error(t, h.ValidateCreateArgs(map[string]any{}))
	require.Error(t, h.ValidateCreateArgs(map[string]any{"endpoint": "s3.amazonaws.com"}))
	require.NoError(t, h.ValidateCreateArgs(map[string]any{"endpoint": "s3.amazonaws.com", "access_key_id": "AKIA"}))
}

func TestOSSApplyCreateArgsAndSafeView(t *testing.T) {
	h := &ossHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeOSS}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"provider": "s3", "endpoint": "s3.us-east-1.amazonaws.com",
		"region": "us-east-1", "access_key_id": "AKIA", "use_ssl": true,
	}))
	cfg, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", cfg.Endpoint)
	assert.True(t, cfg.UseSSL)

	sv := h.SafeView(a)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", sv["endpoint"])
	_, hasSecret := sv["secretAccessKey"]
	assert.False(t, hasSecret, "SafeView 不得泄露密钥")
	_, hasCred := sv["credentialId"]
	assert.False(t, hasCred)
}
```
> 测试刻意不传 `secret_access_key`,避免单测触发 `credential_svc` 加密初始化。

- [ ] **Step 2: 运行,确认失败**

Run: `go test ./internal/assettype/ -run TestOSS -v`
Expected: 编译失败(`undefined: ossHandler`)。

- [ ] **Step 3: 建 handler**

`internal/assettype/oss.go`:
```go
package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type ossHandler struct{}

func init() { Register(&ossHandler{}) }

func (h *ossHandler) Type() string     { return asset_entity.AssetTypeOSS }
func (h *ossHandler) DefaultPort() int { return 0 }

func (h *ossHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{
		"provider":     cfg.Provider,
		"endpoint":     cfg.Endpoint,
		"region":       cfg.Region,
		"accessKeyId":  cfg.AccessKeyID,
		"usePathStyle": cfg.UsePathStyle,
		"useSSL":       cfg.UseSSL,
		// SecretAccessKey / CredentialID 故意不返回
	}
}

func (h *ossHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return "", fmt.Errorf("get oss config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *ossHandler) DefaultPolicy() any  { return nil }
func (h *ossHandler) PolicyKind() string { return "" }

func (h *ossHandler) ValidateCreateArgs(args map[string]any) error {
	if ArgString(args, "endpoint") == "" {
		return fmt.Errorf("missing required parameter: endpoint")
	}
	if ArgString(args, "access_key_id") == "" {
		return fmt.Errorf("missing required parameter: access_key_id")
	}
	return nil
}

func (h *ossHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.OSSConfig{
		Provider:       ArgString(args, "provider"),
		Endpoint:       ArgString(args, "endpoint"),
		Region:         ArgString(args, "region"),
		AccessKeyID:    ArgString(args, "access_key_id"),
		CredentialID:   ArgInt64(args, "credential_id"),
		UsePathStyle:   ArgBool(args, "use_path_style"),
		UseSSL:         ArgBool(args, "use_ssl"),
		ConnectTimeout: ArgInt(args, "connect_timeout"),
	}
	if secret := ArgString(args, "secret_access_key"); secret != "" {
		encrypted, err := credential_svc.Default().Encrypt(secret)
		if err != nil {
			return fmt.Errorf("encrypt oss secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}
	return a.SetOSSConfig(cfg)
}

func (h *ossHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetOSSConfig()
	if err != nil {
		return err
	}
	if _, ok := args["provider"]; ok {
		cfg.Provider = ArgString(args, "provider")
	}
	if _, ok := args["endpoint"]; ok {
		cfg.Endpoint = ArgString(args, "endpoint")
	}
	if _, ok := args["region"]; ok {
		cfg.Region = ArgString(args, "region")
	}
	if _, ok := args["access_key_id"]; ok {
		cfg.AccessKeyID = ArgString(args, "access_key_id")
	}
	if _, ok := args["use_path_style"]; ok {
		cfg.UsePathStyle = ArgBool(args, "use_path_style")
	}
	if _, ok := args["use_ssl"]; ok {
		cfg.UseSSL = ArgBool(args, "use_ssl")
	}
	if _, ok := args["connect_timeout"]; ok {
		cfg.ConnectTimeout = ArgInt(args, "connect_timeout")
	}
	if _, ok := args["credential_id"]; ok {
		cfg.CredentialID = ArgInt64(args, "credential_id")
	}
	if secret := ArgString(args, "secret_access_key"); secret != "" {
		encrypted, err := credential_svc.Default().Encrypt(secret)
		if err != nil {
			return fmt.Errorf("encrypt oss secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}
	if err := a.SetOSSConfig(cfg); err != nil {
		return err
	}
	connpool.InvalidateOSS(a.ID)
	return nil
}
```
> 依赖 `connpool.InvalidateOSS`(Task 3)。若先做本任务,Task 3 未完成会编译失败——建议顺序执行(或先临时删掉这行 + 该 import,Task 3 完成后补回)。

- [ ] **Step 4: 运行,确认通过**

Run: `go test ./internal/assettype/ -run TestOSS -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/assettype/oss.go internal/assettype/oss_test.go
git commit -m "✨ OSS 资产类型 handler"
```

---

### Task 3: minio 客户端连接池 (connpool)

**Files:**
- Modify: `go.mod` / `go.sum`(加 `github.com/minio/minio-go/v7`)
- Create: `internal/connpool/oss.go`
- Test: `internal/connpool/oss_test.go`

**Interfaces:**
- Consumes: `asset_entity.OSSConfig`(Task 1);minio-go。
- Produces: `buildMinioOptions(cfg *asset_entity.OSSConfig, secret string) (string, *minio.Options, error)`;`DialOSS(cfg, secret) (*minio.Client, error)`;`GetOrDialOSS(ctx, assetID int64, cfg, secret) (*minio.Client, error)`;`InvalidateOSS(assetID int64)`。

- [ ] **Step 1: 加依赖**

Run: `go get github.com/minio/minio-go/v7@latest && go mod tidy`
Expected: `go.mod` 出现 `github.com/minio/minio-go/v7`。

- [ ] **Step 2: 写失败测试**

`internal/connpool/oss_test.go`:
```go
package connpool

import (
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMinioOptionsPlainHostSSL(t *testing.T) {
	ep, opts, err := buildMinioOptions(&asset_entity.OSSConfig{
		Endpoint: "s3.us-east-1.amazonaws.com", Region: "us-east-1", UseSSL: true, AccessKeyID: "AKIA",
	}, "sk")
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", ep)
	assert.True(t, opts.Secure)
	assert.Equal(t, "us-east-1", opts.Region)
	assert.Equal(t, minio.BucketLookupAuto, opts.BucketLookup)
}

func TestBuildMinioOptionsSchemeStrippedPathStyle(t *testing.T) {
	ep, opts, err := buildMinioOptions(&asset_entity.OSSConfig{
		Endpoint: "http://127.0.0.1:9000", UsePathStyle: true,
	}, "sk")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9000", ep)
	assert.False(t, opts.Secure)
	assert.Equal(t, minio.BucketLookupPath, opts.BucketLookup)
}

func TestBuildMinioOptionsEmptyEndpoint(t *testing.T) {
	_, _, err := buildMinioOptions(&asset_entity.OSSConfig{}, "sk")
	require.Error(t, err)
}
```

- [ ] **Step 3: 运行,确认失败**

Run: `go test ./internal/connpool/ -run TestBuildMinioOptions -v`
Expected: 编译失败(`undefined: buildMinioOptions`)。

- [ ] **Step 4: 建 `oss.go`**

`internal/connpool/oss.go`:
```go
package connpool

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// buildMinioOptions 从 OSS 配置 + 解密后的密钥推导 minio 端点与选项(纯函数,单测)。
func buildMinioOptions(cfg *asset_entity.OSSConfig, secret string) (string, *minio.Options, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return "", nil, fmt.Errorf("oss endpoint is empty")
	}
	secure := cfg.UseSSL
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return "", nil, fmt.Errorf("parse oss endpoint: %w", err)
		}
		secure = u.Scheme == "https"
		endpoint = u.Host
	}
	lookup := minio.BucketLookupAuto
	if cfg.UsePathStyle {
		lookup = minio.BucketLookupPath
	}
	return endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, secret, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	}, nil
}

// DialOSS 新建一个 minio 客户端(HTTP 客户端,无需显式 Close)。
func DialOSS(cfg *asset_entity.OSSConfig, secret string) (*minio.Client, error) {
	endpoint, opts, err := buildMinioOptions(cfg, secret)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("dial oss: %w", err)
	}
	return client, nil
}

type ossPool struct {
	mu      sync.Mutex
	clients map[int64]*minio.Client
}

var globalOSSPool = &ossPool{clients: map[int64]*minio.Client{}}

// GetOrDialOSS 返回缓存的 minio 客户端,没有则新建并缓存。
func GetOrDialOSS(_ context.Context, assetID int64, cfg *asset_entity.OSSConfig, secret string) (*minio.Client, error) {
	globalOSSPool.mu.Lock()
	defer globalOSSPool.mu.Unlock()
	if c, ok := globalOSSPool.clients[assetID]; ok {
		return c, nil
	}
	c, err := DialOSS(cfg, secret)
	if err != nil {
		return nil, err
	}
	if assetID > 0 {
		globalOSSPool.clients[assetID] = c
	}
	return c, nil
}

// InvalidateOSS 丢弃某资产的缓存客户端(配置更新/删除时调用)。
func InvalidateOSS(assetID int64) {
	globalOSSPool.mu.Lock()
	delete(globalOSSPool.clients, assetID)
	globalOSSPool.mu.Unlock()
}
```
> minio 客户端基于 HTTP transport,无长连接需 GC 关闭;P1 用简单缓存 + 失效即可,idle-GC 可后续按 `connpool/etcd.go` 补齐。

- [ ] **Step 5: 运行,确认通过**

Run: `go test ./internal/connpool/ -run TestBuildMinioOptions -v`
Expected: PASS(3 个用例)。

- [ ] **Step 6: 提交**

```bash
git add go.mod go.sum internal/connpool/oss.go internal/connpool/oss_test.go
git commit -m "✨ OSS minio 客户端连接池"
```

---

### Task 4: oss_svc 浏览服务

**Files:**
- Create: `internal/service/oss_svc/types.go`
- Create: `internal/service/oss_svc/client_minio.go`
- Create: `internal/service/oss_svc/service.go`
- Create: `internal/service/oss_svc/ops.go`
- Create: `internal/service/oss_svc/doc.go`(mockgen 指令)
- Create: `internal/service/oss_svc/mock_ossclient/mock.go`(生成物)
- Test: `internal/service/oss_svc/ops_test.go`、`internal/service/oss_svc/client_minio_test.go`

**Interfaces:**
- Consumes: `connpool.GetOrDialOSS`/`DialOSS`(Task 3);`asset_svc.Asset().Get(ctx, id)`;`credential_resolver.Default().ResolvePasswordGeneric`;minio-go。
- Produces:
  - DTO:`BucketItem{Name string; CreationDate int64}`、`ObjectItem{Key string; Size int64; LastModified int64; ETag string; StorageClass string; ContentType string; IsPrefix bool}`。
  - 接口 `Client`(见下 types.go)。
  - `New() *Service`;方法 `ListBuckets(ctx, assetID int64) ([]BucketItem, error)`、`ListObjects(ctx, *ListObjectsRequest) (*ListObjectsResult, error)`、`StatObject(ctx, *ObjectRequest) (*ObjectItem, error)`、`RemoveObject(ctx, *ObjectRequest) error`、`PresignGet(ctx, *PresignRequest) (string, error)`、`PresignPut(ctx, *PresignRequest) (string, error)`、`TestConnection(ctx, assetID int64) error`、`TestConfig(ctx, *asset_entity.OSSConfig, secret string) error`。
  - Request 类型:`ListObjectsRequest{AssetID int64; Bucket, Prefix string}`、`ObjectRequest{AssetID int64; Bucket, Key string}`、`PresignRequest{AssetID int64; Bucket, Key string; ExpirySecs int}`;`ListObjectsResult{Prefixes []string; Objects []ObjectItem}`。

- [ ] **Step 1: 建 types.go**

`internal/service/oss_svc/types.go`:
```go
package oss_svc

import (
	"context"
	"time"
)

// BucketItem 是账号下的一个 Bucket。
type BucketItem struct {
	Name         string `json:"name"`
	CreationDate int64  `json:"creationDate"`
}

// ObjectItem 是 Bucket 下的一个对象或"文件夹"前缀。
type ObjectItem struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified int64  `json:"lastModified"`
	ETag         string `json:"etag"`
	StorageClass string `json:"storageClass"`
	ContentType  string `json:"contentType"`
	IsPrefix     bool   `json:"isPrefix"`
}

// Client 是服务依赖的窄对象存储接口(可 mock)。
type Client interface {
	ListBuckets(ctx context.Context) ([]BucketItem, error)
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectItem, error)
	StatObject(ctx context.Context, bucket, key string) (ObjectItem, error)
	RemoveObject(ctx context.Context, bucket, key string) error
	PresignGet(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
	BucketExists(ctx context.Context, bucket string) (bool, error)
}

type ListObjectsRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Prefix  string `json:"prefix"`
}
type ListObjectsResult struct {
	Prefixes []string     `json:"prefixes"`
	Objects  []ObjectItem `json:"objects"`
}
type ObjectRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Key     string `json:"key"`
}
type PresignRequest struct {
	AssetID    int64  `json:"assetId"`
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	ExpirySecs int    `json:"expirySecs"`
}
```

- [ ] **Step 2: 建 doc.go + 生成 mock**

`internal/service/oss_svc/doc.go`:
```go
package oss_svc

//go:generate mockgen -destination=./mock_ossclient/mock.go -package=mock_ossclient github.com/opskat/opskat/internal/service/oss_svc Client
```
Run: `go generate ./internal/service/oss_svc/...`
Expected: 生成 `internal/service/oss_svc/mock_ossclient/mock.go`(含 `NewMockClient`)。

- [ ] **Step 3: 写失败测试**

`internal/service/oss_svc/ops_test.go`:
```go
package oss_svc

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListObjectsWithSplitsPrefixesAndObjects(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/").Return([]ObjectItem{
		{Key: "images/thumbnails/", IsPrefix: true},
		{Key: "images/hero.jpg", Size: 2516480},
	}, nil)

	res, err := listObjectsWith(context.Background(), c, "assets-prod", "images/")
	require.NoError(t, err)
	assert.Equal(t, []string{"images/thumbnails/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "images/hero.jpg", res.Objects[0].Key)
}
```
`internal/service/oss_svc/client_minio_test.go`:
```go
package oss_svc

import (
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
)

func TestToObjectItemDetectsFolderPrefix(t *testing.T) {
	got := toObjectItem(minio.ObjectInfo{Key: "images/thumbnails/", Size: 0})
	assert.True(t, got.IsPrefix)
}

func TestToObjectItemMapsObject(t *testing.T) {
	got := toObjectItem(minio.ObjectInfo{
		Key: "images/hero.jpg", Size: 2516480, ETag: "9b2c", LastModified: time.Unix(1751811127, 0),
	})
	assert.False(t, got.IsPrefix)
	assert.Equal(t, int64(2516480), got.Size)
	assert.Equal(t, int64(1751811127), got.LastModified)
	assert.Equal(t, "9b2c", got.ETag)
}
```

- [ ] **Step 4: 运行,确认失败**

Run: `go test ./internal/service/oss_svc/ -v`
Expected: 编译失败(`undefined: listObjectsWith` / `toObjectItem`)。

- [ ] **Step 5: 建 client_minio.go**

`internal/service/oss_svc/client_minio.go`:
```go
package oss_svc

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
)

// minioAdapter 把 *minio.Client 适配成窄接口 Client。
type minioAdapter struct{ mc *minio.Client }

func newMinioAdapter(mc *minio.Client) Client { return &minioAdapter{mc: mc} }

func (a *minioAdapter) ListBuckets(ctx context.Context) ([]BucketItem, error) {
	bs, err := a.mc.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BucketItem, 0, len(bs))
	for _, b := range bs {
		out = append(out, BucketItem{Name: b.Name, CreationDate: b.CreationDate.Unix()})
	}
	return out, nil
}

func (a *minioAdapter) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectItem, error) {
	var out []ObjectItem
	for obj := range a.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: false}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, toObjectItem(obj))
	}
	return out, nil
}

func (a *minioAdapter) StatObject(ctx context.Context, bucket, key string) (ObjectItem, error) {
	info, err := a.mc.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectItem{}, err
	}
	return toObjectItem(info), nil
}

func (a *minioAdapter) RemoveObject(ctx context.Context, bucket, key string) error {
	return a.mc.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (a *minioAdapter) PresignGet(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := a.mc.PresignedGetObject(ctx, bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *minioAdapter) PresignPut(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := a.mc.PresignedPutObject(ctx, bucket, key, expiry)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *minioAdapter) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return a.mc.BucketExists(ctx, bucket)
}

// toObjectItem 把 minio.ObjectInfo 映射为 DTO;"文件夹"前缀表现为 Key 以 "/" 结尾且 Size 为 0。
func toObjectItem(o minio.ObjectInfo) ObjectItem {
	item := ObjectItem{
		Key: o.Key, Size: o.Size, ETag: o.ETag,
		StorageClass: o.StorageClass, ContentType: o.ContentType,
	}
	if !o.LastModified.IsZero() {
		item.LastModified = o.LastModified.Unix()
	}
	if o.Size == 0 && len(o.Key) > 0 && o.Key[len(o.Key)-1] == '/' {
		item.IsPrefix = true
	}
	return item
}
```

- [ ] **Step 6: 建 ops.go + service.go**

`internal/service/oss_svc/ops.go`:
```go
package oss_svc

import "context"

// listObjectsWith 把一层平铺列表拆成"文件夹"前缀与对象。
func listObjectsWith(ctx context.Context, c Client, bucket, prefix string) (*ListObjectsResult, error) {
	items, err := c.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	res := &ListObjectsResult{}
	for _, it := range items {
		if it.IsPrefix {
			res.Prefixes = append(res.Prefixes, it.Key)
		} else {
			res.Objects = append(res.Objects, it)
		}
	}
	return res, nil
}
```
`internal/service/oss_svc/service.go`:
```go
package oss_svc

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

const defaultPresignExpiry = time.Hour

type Service struct{}

func New() *Service { return &Service{} }

func (s *Service) lookup(ctx context.Context, assetID int64) (*asset_entity.Asset, *asset_entity.OSSConfig, string, error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsOSS() {
		return nil, nil, "", fmt.Errorf("资产不是 OSS 类型")
	}
	cfg, err := asset.GetOSSConfig()
	if err != nil {
		return nil, nil, "", fmt.Errorf("获取 OSS 配置失败: %w", err)
	}
	secret, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("解析 OSS 凭据失败: %w", err)
	}
	return asset, cfg, secret, nil
}

func (s *Service) connect(ctx context.Context, assetID int64) (Client, error) {
	asset, cfg, secret, err := s.lookup(ctx, assetID)
	if err != nil {
		return nil, err
	}
	mc, err := connpool.GetOrDialOSS(ctx, asset.ID, cfg, secret)
	if err != nil {
		return nil, err
	}
	return newMinioAdapter(mc), nil
}

func (s *Service) ListBuckets(ctx context.Context, assetID int64) ([]BucketItem, error) {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return c.ListBuckets(ctx)
}

func (s *Service) ListObjects(ctx context.Context, req *ListObjectsRequest) (*ListObjectsResult, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return nil, err
	}
	return listObjectsWith(ctx, c, req.Bucket, req.Prefix)
}

func (s *Service) StatObject(ctx context.Context, req *ObjectRequest) (*ObjectItem, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return nil, err
	}
	item, err := c.StatObject(ctx, req.Bucket, req.Key)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) RemoveObject(ctx context.Context, req *ObjectRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return c.RemoveObject(ctx, req.Bucket, req.Key)
}

func (s *Service) PresignGet(ctx context.Context, req *PresignRequest) (string, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return "", err
	}
	return c.PresignGet(ctx, req.Bucket, req.Key, presignExpiry(req.ExpirySecs))
}

func (s *Service) PresignPut(ctx context.Context, req *PresignRequest) (string, error) {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return "", err
	}
	return c.PresignPut(ctx, req.Bucket, req.Key, presignExpiry(req.ExpirySecs))
}

// TestConnection 用已保存的资产拨号并列 Bucket 以验证凭证。
func (s *Service) TestConnection(ctx context.Context, assetID int64) error {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return err
	}
	_, err = c.ListBuckets(ctx)
	return err
}

// TestConfig 验证一份未保存的配置(表单"测试连接")。
func (s *Service) TestConfig(ctx context.Context, cfg *asset_entity.OSSConfig, secret string) error {
	mc, err := connpool.DialOSS(cfg, secret)
	if err != nil {
		return err
	}
	_, err = newMinioAdapter(mc).ListBuckets(ctx)
	return err
}

func presignExpiry(secs int) time.Duration {
	if secs <= 0 {
		return defaultPresignExpiry
	}
	return time.Duration(secs) * time.Second
}
```
> 确认 `asset_svc.Asset().Get(ctx, id)` 签名与 `etcd_svc/service.go` 中一致(若不同,照抄该处调用)。

- [ ] **Step 7: 运行,确认通过**

Run: `go test ./internal/service/oss_svc/... -v`
Expected: PASS(`TestListObjectsWith*`、`TestToObjectItem*`)。

- [ ] **Step 8: 提交**

```bash
git add internal/service/oss_svc/
git commit -m "✨ oss_svc 对象存储浏览服务"
```

---

### Task 5: Wails 绑定 + 主程序接线 + 冒烟

**Files:**
- Create: `internal/app/oss/oss.go`
- Create: `internal/app/oss/oss_ops.go`
- Test: `internal/app/oss/oss_ops_test.go`
- Modify: `main.go`(import + 构造 `ossB` + 加入 `binders` 与 `Bind` 两处)

**Interfaces:**
- Consumes: `oss_svc.New()` 及其方法(Task 4);`conntest.Register(assetType string, fn func(ctx, configJSON, plainPassword string) error)`;`i18n.Ctx`;`jsonfield.Unmarshal`。
- Produces: `oss.New(appCtx context.Context, lang LangProvider) *OSS`(实现 `Startup(ctx)`/`Cleanup()`);Wails 方法 `OSSTestConnection(assetID int64) error`、`OSSListBuckets(assetID int64) ([]oss_svc.BucketItem, error)`、`OSSListObjects(oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error)`、`OSSStatObject(oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error)`、`OSSRemoveObject(oss_svc.ObjectRequest) error`、`OSSPresignGet(oss_svc.PresignRequest) (string, error)`、`OSSPresignPut(oss_svc.PresignRequest) (string, error)`。

- [ ] **Step 1: 写失败测试(边界校验)**

`internal/app/oss/oss_ops_test.go`:
```go
package oss

import (
	"testing"

	"github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/stretchr/testify/require"
)

func TestOSSListObjectsValidatesInput(t *testing.T) {
	o := &OSS{service: oss_svc.New()}
	_, err := o.OSSListObjects(oss_svc.ListObjectsRequest{AssetID: 0, Bucket: "b"})
	require.Error(t, err)
	_, err = o.OSSListObjects(oss_svc.ListObjectsRequest{AssetID: 1, Bucket: ""})
	require.Error(t, err)
}

func TestOSSListBucketsValidatesInput(t *testing.T) {
	o := &OSS{service: oss_svc.New()}
	_, err := o.OSSListBuckets(0)
	require.Error(t, err)
}
```
> 仅覆盖"非法输入提前返回"分支(不触达 service/连接),因此无需 live client。

- [ ] **Step 2: 运行,确认失败**

Run: `go test ./internal/app/oss/ -v`
Expected: 编译失败(`undefined: OSS`)。

- [ ] **Step 3: 建 oss.go**

`internal/app/oss/oss.go`:
```go
package oss

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/i18n"
	"github.com/opskat/opskat/internal/pkg/jsonfield"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

// LangProvider 提供当前 UI 语言,用于 i18n 上下文。
type LangProvider interface{ Lang() string }

type OSS struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	service *oss_svc.Service
}

func New(appCtx context.Context, lang LangProvider) *OSS {
	o := &OSS{appCtx: appCtx, lang: lang, service: oss_svc.New()}
	conntest.Register(asset_entity.AssetTypeOSS, o.testConnection)
	return o
}

func (o *OSS) Startup(ctx context.Context) { o.ctx = ctx }
func (o *OSS) Cleanup()                    {}

func (o *OSS) i18nCtx() context.Context { return i18n.Ctx(o.ctx, o.lang.Lang()) }

// testConnection 是通用表单"测试连接"经 conntest 分派的钩子。
func (o *OSS) testConnection(ctx context.Context, configJSON, plainSecret string) error {
	cfg, err := jsonfield.Unmarshal[asset_entity.OSSConfig](configJSON, "OSS配置")
	if err != nil {
		return err
	}
	return o.service.TestConfig(ctx, cfg, plainSecret)
}
```
> `i18n` 与 `jsonfield` 的确切 import 路径:分别照抄 `internal/app/etcd/etcd_ops.go`(i18n)与 `internal/model/entity/asset_entity/asset.go`(jsonfield)顶部 import。

- [ ] **Step 4: 建 oss_ops.go**

`internal/app/oss/oss_ops.go`:
```go
package oss

import (
	"fmt"

	"github.com/opskat/opskat/internal/service/oss_svc"
)

func (o *OSS) OSSTestConnection(assetID int64) error {
	if assetID <= 0 {
		return fmt.Errorf("invalid assetID")
	}
	return o.service.TestConnection(o.i18nCtx(), assetID)
}

func (o *OSS) OSSListBuckets(assetID int64) ([]oss_svc.BucketItem, error) {
	if assetID <= 0 {
		return nil, fmt.Errorf("invalid assetID")
	}
	return o.service.ListBuckets(o.i18nCtx(), assetID)
}

func (o *OSS) OSSListObjects(req oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error) {
	if req.AssetID <= 0 || req.Bucket == "" {
		return nil, fmt.Errorf("invalid request: assetID and bucket are required")
	}
	return o.service.ListObjects(o.i18nCtx(), &req)
}

func (o *OSS) OSSStatObject(req oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return nil, fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.StatObject(o.i18nCtx(), &req)
}

func (o *OSS) OSSRemoveObject(req oss_svc.ObjectRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.RemoveObject(o.i18nCtx(), &req)
}

func (o *OSS) OSSPresignGet(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.PresignGet(o.i18nCtx(), &req)
}

func (o *OSS) OSSPresignPut(req oss_svc.PresignRequest) (string, error) {
	if req.AssetID <= 0 || req.Bucket == "" || req.Key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	return o.service.PresignPut(o.i18nCtx(), &req)
}
```

- [ ] **Step 5: 运行,确认通过**

Run: `go test ./internal/app/oss/ -v`
Expected: PASS。

- [ ] **Step 6: 接线 main.go**

在 `main.go`:
1. import 段加 `"github.com/opskat/opskat/internal/app/oss"`。
2. 在其它 binder 构造处(`etcdB := etcd.New(appCtx, sys, pool)` 附近)加:
```go
	ossB := oss.New(appCtx, sys)
```
3. 在 `binders := []Lifecycle{...}` 切片末尾加入 `ossB`。
4. 在 `appOptions.Bind: []interface{}{...}` 列表末尾加入 `ossB`。

- [ ] **Step 7: 编译 + 重新生成绑定 + 静态检查**

Run:
```bash
go build ./...
golangci-lint run ./internal/app/oss/... ./internal/service/oss_svc/... ./internal/assettype/... ./internal/connpool/... ./internal/model/entity/asset_entity/...
wails generate module
```
Expected: `go build` 成功;lint 无新增问题;`frontend/wailsjs/go/oss/OSS.*` 生成(供 P2/P3 调用)。

- [ ] **Step 8: 冒烟验证(观察副作用,遵循 AGENTS.md)**

用本地 MinIO 起一个 S3 兼容端点:
```bash
docker run -d --name opskat-minio -p 9000:9000 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio server /data
```
运行应用,在「添加资产 → 对象存储」用 endpoint `http://127.0.0.1:9000`、AK/SK `minioadmin`、Path-style 开,保存;连接后调用列 Bucket。
验证:`logs/opskat.log` 无错误、能列出 Bucket;`opskat.db` 的 `assets` 表出现该 OSS 资产(`type='oss'`,`config` JSON 不含明文 SK)。
> 若 P2 前端尚未就绪,可临时写一个 `go test` 集成用例:创建 OSS 资产 → `oss_svc.New().TestConfig(ctx, cfg, "minioadmin")` 返回 nil(需连本地 MinIO,标 `//go:build integration` 以免污染单测)。

- [ ] **Step 9: 提交**

```bash
git add internal/app/oss/ main.go
git commit -m "🔌 注册 OSS Wails 绑定并接入主程序"
```

---

## 交付物与后续

P1 完成后:后端可创建/连接/浏览 OSS 资产,前端可通过 `wailsjs/go/oss/OSS.*` 调用列 Bucket/对象、取信息、删除、预签名。**P2**(前端表单+注册)与 **P3**(前端浏览器)在此基础上开展。

**P1 明确不含**(留待后续,均为独立可增量的 binding/服务方法):
- **重命名 / 移动 / 复制**:S3 无原生 rename,需 `CopyObject` + `RemoveObject` 组合的 `OSSRenameObject`/`OSSMoveObject` binding —— **P3 的右键"重命名/移动/复制"依赖它**,应在 P3 开工前补一个小任务(照 Task 4/5 追加 Client.CopyObject + service + binding + 测试)。
- **原生上传 / 下载与分片进度**:P1 用预签名 PUT/GET 由前端直传直下;后端代理传输 + 进度事件后续再加。
- **新建文件夹**:S3 无真实目录,前端"新建文件夹"= PUT 一个 `<prefix>/` 空对象,可复用预签名 PUT 或后续加 `OSSCreatePrefix` binding。
- **OSS 策略/命令分组与审计门禁**(参 etcd:服务层不感知策略,门禁在调用方/AI Runner)。
- **SSH 隧道 / 代理**、**专用 AK/SK 凭证类型**。
