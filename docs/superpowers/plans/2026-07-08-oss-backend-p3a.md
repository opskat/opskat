# OSS 对象浏览器 P3a 后端绑定 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 P3b 前端对象浏览器提供后端绑定 —— 列表分页、复制/移动(重命名)、批量删除、新建文件夹、原生流式上传下载(带进度+取消)。纯 Go 绑定面,可用本地 MinIO 独立回归,不含任何前端。复用 P1 已落地的 `credential_resolver` + `connpool.GetOrDialOSS` + SFTP 已验证的 `internal/pkg/transfer` 进度栈 + Wails `EventsEmit`。

**Architecture:** 三层不变 —— bindings(`internal/app/oss/*.go`)→ service(`internal/service/oss_svc/*.go`)→ connpool(`internal/connpool/oss.go`)。service 通过 `Client` 窄接口(`types.go`)消费对象存储,`minioAdapter`(`client_minio.go`)把 `*minio.Client` 适配成 `Client`,`mock_ossclient` 用于单测。所有 Client-消费逻辑抽成自由函数 `xxxWith(ctx, c Client, ...)`(仿既有 `listObjectsWith`),经 `export_test.go` shim 暴露给外部测试包做 gomock 单测。流式上传/下载逻辑放 **app 层**(新文件 `oss_transfer.go`,镜像 `internal/app/ssh/sftp.go`),service 只暴露 `PutObject`/`GetObject` 原语;进度基元(`ProgressReader`/`Copy`)沉到 `internal/pkg/transfer`(该包既有职责就是「所有文件传输共用一套」)。

**Tech Stack:** Wails v2 (`github.com/wailsapp/wails/v2@v2.12.0`), Go 1.26, `github.com/minio/minio-go/v7@v7.2.1`, mockgen `go.uber.org/mock@v0.6.0` (reflect mode), testify, module `github.com/opskat/opskat`。

## Global Constraints

- **DTO 命名:** Wails 面上的 DTO 一律 camelCase json tag(与 P1 一致);存储层配置(`OSSConfig`)保持 snake_case,本期不触碰。
- **测试包与 mock 环:** 新增 gomock 测试一律写在 `package oss_svc_test`,通过 `export_test.go` shim(P1 约定,`var XxxWith = xxxWith`)引用自由函数,规避 mock_ossclient(import `oss_svc`)造成的 import cycle。纯函数(`normalizeFolderPrefix`/`aggregateRemoveErrors`)用同包白盒测试(`package oss_svc`,如 `client_minio_test.go`)直接调用,不走 shim。
- **改 Client 接口后必须重生成 mock:** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`(directive 在 `doc.go`,mockgen `go.uber.org/mock` v0.6.0,reflect 模式)。
- **传输事件严格复用 SFTP 契约:** 事件名 `"transfer:progress:"+transferID`,载荷 `transfer.Progress`,`Status ∈ {progress,done,error,cancelled}`,`progress` 经 `transfer.Reporter` 100ms 节流,终态立即 emit。transferID 经 `transfer.GenerateID("oss")` 生成。
- **原生文件对话框在 Go 内弹:** `wailsRuntime.OpenMultipleFilesDialog` / `wailsRuntime.SaveFileDialog`(签名见 `wails/v2@v2.12.0/pkg/runtime/dialog.go`),绝不由前端弹。
- **边界校验只在 app 层:** IPC 入口校验 `assetID>0`、`bucket!=""`、必需 key/prefix 非空、`len(keys)>0`;app↔service↔connpool 之间 Go-to-Go 互信,不重复判空。
- **变更操作审计 = 结构化日志(镜像既有做法):** 事实核查结论 —— 既有 OSS/etcd/redis app 绑定**均不写 `audit_logs` 表**,只有 `external_edit_svc` + system settings 写;etcd 变更仅经 `logger.Ctx(ctx)` 结构化日志(`internal/app/etcd/etcd_ops.go:40`)。本计划据「mirror existing」镜像该做法:每个变更绑定成功后 `logger.Ctx(ctx).Info(...)` 记一条关键流日志(遵循 `docs/DEVELOP.md → Logging for key flows`:`logger.Ctx`、强类型 zap 字段、不 `zap.Any`)。**不新引入 `audit_logs` 表写入**(那会与所有兄弟资产类型不一致,属范围扩张)。若确需落 `audit_logs`,参照 `external_edit_svc.writeAudit → audit_repo.Audit().Create(&audit_entity.AuditLog{Source:"desktop", ...})` —— 但需 controller 单独确认(见交付说明)。
- **机密:** SK 恒经 `credential_resolver.Default().ResolvePasswordGeneric`(已在 `oss_svc.Service.lookup` 内);绝不出现在 DTO / 日志。
- **无 policy:** OSS `PolicyKind()==""` 不变,变更操作不做审批门控(沿用 P1 决策)。
- **Gate 命令(每个任务收尾跑):** `go build ./...`;`go test ./internal/service/oss_svc/... ./internal/app/oss/... ./internal/pkg/transfer/...`;`golangci-lint run`(**不是 `go vet`**,本仓用 golangci-lint);`gofmt -l internal/`(输出须为空)。
- **提交:** gitmoji + 中文 subject,一个任务一个 commit,不带 issue / 评审编号。
- **范围护栏(spec §1,禁止实现):** 文件夹级重命名/移动、递归目录上传/前缀下载、对象 ACL、Bucket 创建/删除、OSS 审批门控。

---

## Task 1 — ListObjects 分页(maxKeys + 续传游标)

**Files:**
- Modify `internal/service/oss_svc/types.go`(改 `Client.ListObjects` 签名;`ListObjectsRequest`/`ListObjectsResult` 增字段)
- Modify `internal/service/oss_svc/client_minio.go`(`minioAdapter.ListObjects` 有界读通道)
- Modify `internal/service/oss_svc/ops.go`(`listObjectsWith` 扩签名 + 截断/游标/拆分;`defaultListMaxKeys` 常量)
- Modify `internal/service/oss_svc/service.go`(`Service.ListObjects` 传 `MaxKeys`/`ContinuationToken`)
- Regenerate `internal/service/oss_svc/mock_ossclient/mock.go`
- Modify `internal/service/oss_svc/ops_test.go`(既有两测更新到新签名 + 新增分页测)
- `internal/app/oss/oss_ops.go` 的 `OSSListObjects` 绑定体**不改**(DTO 新字段透传)

**Interfaces:**
- Consumes: `minio.ListObjectsOptions{Prefix, Recursive, MaxKeys, StartAfter}`、`(*minio.Client).ListObjects(ctx, bucket, opts) <-chan minio.ObjectInfo`(`minio-go/v7@v7.2.1/api-list.go:764`)。
- Produces:
  - `Client.ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error)`
  - `listObjectsWith(ctx context.Context, c Client, bucket, prefix string, maxKeys int, startAfter string) (*ListObjectsResult, error)`
  - `ListObjectsRequest{AssetID, Bucket, Prefix, MaxKeys, ContinuationToken}`、`ListObjectsResult{Prefixes, Objects, NextContinuationToken, IsTruncated}`

### Steps

- [ ] **RED — 更新既有测 + 新增分页测.** 把 `internal/service/oss_svc/ops_test.go` 整体替换为下列内容(既有两测改到新 5 参签名,新增两个分页测):

```go
package oss_svc_test

import (
	"context"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestListObjectsWithSplitsPrefixesAndObjects(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "assets-prod", "images/", 200, "").Return([]oss_svc.ObjectItem{
		{Key: "images/thumbnails/", IsPrefix: true},
		{Key: "images/hero.jpg", Size: 2516480},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "assets-prod", "images/", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"images/thumbnails/"}, res.Prefixes)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "images/hero.jpg", res.Objects[0].Key)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
}

// 空 bucket/前缀应序列化为 JSON "[]" 而非 "null"。
func TestListObjectsWithEmptyBucketReturnsEmptySlicesNotNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "empty-bucket", "", 200, "").Return([]oss_svc.ObjectItem{}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "empty-bucket", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, res.Prefixes)
	assert.Equal(t, []oss_svc.ObjectItem{}, res.Objects)
	assert.NotNil(t, res.Prefixes)
	assert.NotNil(t, res.Objects)
}

// maxKeys+1 项 → 截断:丢掉最后一项,next = 第 maxKeys 项的 Key。
func TestListObjectsWithTruncatesAndSetsNextCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// maxKeys=2,adapter 会多读 1 项;这里 mock 直接返回 3 项模拟"还有下一页"。
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "").Return([]oss_svc.ObjectItem{
		{Key: "docs/a.md", Size: 10},
		{Key: "docs/b.md", Size: 20},
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "")
	require.NoError(t, err)
	assert.True(t, res.IsTruncated)
	assert.Equal(t, "docs/b.md", res.NextContinuationToken)
	require.Len(t, res.Objects, 2)
	assert.Equal(t, "docs/a.md", res.Objects[0].Key)
	assert.Equal(t, "docs/b.md", res.Objects[1].Key)
}

// 续读:startAfter 透传给 Client;不足一页则不截断。
func TestListObjectsWithResumeCursorNotTruncated(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "docs/", 2, "docs/b.md").Return([]oss_svc.ObjectItem{
		{Key: "docs/c.md", Size: 30},
	}, nil)

	res, err := oss_svc.ListObjectsWith(context.Background(), c, "b", "docs/", 2, "docs/b.md")
	require.NoError(t, err)
	assert.False(t, res.IsTruncated)
	assert.Empty(t, res.NextContinuationToken)
	require.Len(t, res.Objects, 1)
	assert.Equal(t, "docs/c.md", res.Objects[0].Key)
}
```

- [ ] **run-it-fails.** `go test ./internal/service/oss_svc/...`
  预期编译失败:`too many arguments in call to c.EXPECT().ListObjects`(mock 仍是 3 参)、`too many arguments in call to oss_svc.ListObjectsWith`、`res.IsTruncated undefined`、`res.NextContinuationToken undefined`。

- [ ] **GREEN(接口 + DTO).** 在 `internal/service/oss_svc/types.go`:改 `Client.ListObjects` 一行签名,并替换两个 DTO 结构:

```go
	ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error)
```

```go
type ListObjectsRequest struct {
	AssetID           int64  `json:"assetId"`
	Bucket            string `json:"bucket"`
	Prefix            string `json:"prefix"`
	MaxKeys           int    `json:"maxKeys"`
	ContinuationToken string `json:"continuationToken"`
}
type ListObjectsResult struct {
	Prefixes              []string     `json:"prefixes"`
	Objects               []ObjectItem `json:"objects"`
	NextContinuationToken string       `json:"nextContinuationToken"`
	IsTruncated           bool         `json:"isTruncated"`
}
```

- [ ] **GREEN(adapter 有界读).** 在 `internal/service/oss_svc/client_minio.go` 替换 `minioAdapter.ListObjects`(多读 1 项供 service 判断截断;`defer cancel()` 停止 minio 后续翻页 goroutine):

```go
func (a *minioAdapter) ListObjects(ctx context.Context, bucket, prefix string, maxKeys int, startAfter string) ([]ObjectItem, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make([]ObjectItem, 0, maxKeys+1)
	for obj := range a.mc.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:     prefix,
		Recursive:  false,
		StartAfter: startAfter,
		MaxKeys:    maxKeys + 1,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, toObjectItem(obj))
		if len(out) > maxKeys {
			break // 已读到 maxKeys+1,足以判断"还有下一页";cancel 停止后续
		}
	}
	return out, nil
}
```

- [ ] **GREEN(service 分页逻辑).** 在 `internal/service/oss_svc/ops.go` 顶部加常量并替换 `listObjectsWith`:

```go
package oss_svc

import "context"

const defaultListMaxKeys = 200

// listObjectsWith 读一层有界页:拆"文件夹"前缀与对象;超出 maxKeys 则回续传游标。
func listObjectsWith(ctx context.Context, c Client, bucket, prefix string, maxKeys int, startAfter string) (*ListObjectsResult, error) {
	limit := maxKeys
	if limit <= 0 {
		limit = defaultListMaxKeys
	}
	items, err := c.ListObjects(ctx, bucket, prefix, limit, startAfter)
	if err != nil {
		return nil, err
	}
	res := &ListObjectsResult{Prefixes: []string{}, Objects: []ObjectItem{}}
	if len(items) > limit {
		res.IsTruncated = true
		res.NextContinuationToken = items[limit-1].Key
		items = items[:limit]
	}
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

- [ ] **GREEN(service 入口透传).** 在 `internal/service/oss_svc/service.go` 替换 `Service.ListObjects` 末行:

```go
	return listObjectsWith(ctx, c, req.Bucket, req.Prefix, req.MaxKeys, req.ContinuationToken)
```

- [ ] **GREEN(重生成 mock).** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`
  预期 `mock.go` 的 `ListObjects` 变成 5 参(`ctx, bucket, prefix, maxKeys, startAfter`)。

- [ ] **run-it-passes.** `go test ./internal/service/oss_svc/...` → 4 个 List 测全过。

- [ ] **commit.** `♻️ OSS ListObjects 支持有界分页与续传游标`

---

## Task 2 — CopyObject + MoveObject(单对象复制/重命名/移动)

**Files:**
- Modify `internal/service/oss_svc/types.go`(`Client` 加 `CopyObject`;加 `CopyRequest` DTO)
- Modify `internal/service/oss_svc/client_minio.go`(`minioAdapter.CopyObject`)
- Modify `internal/service/oss_svc/ops.go`(`copyObjectWith` / `moveObjectWith`)
- Modify `internal/service/oss_svc/service.go`(`Service.CopyObject` / `Service.MoveObject`)
- Modify `internal/service/oss_svc/export_test.go`(导出 `CopyObjectWith` / `MoveObjectWith`)
- Create `internal/service/oss_svc/copymove_test.go`(`package oss_svc_test`,gomock 顺序断言)
- Regenerate `mock_ossclient/mock.go`
- Modify `internal/app/oss/oss_ops.go`(`OSSCopyObject` / `OSSMoveObject`)

**Interfaces:**
- Consumes: `(*minio.Client).CopyObject(ctx, dst minio.CopyDestOptions, src minio.CopySrcOptions) (minio.UploadInfo, error)`(`api-copy-object.go:26`);`minio.CopyDestOptions{Bucket, Object}`、`minio.CopySrcOptions{Bucket, Object}`(`api-compose-object.go:37,199`);既有 `Client.RemoveObject(ctx, bucket, key) error`。
- Produces:
  - `Client.CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error`
  - `copyObjectWith(ctx, c Client, srcBucket, srcKey, dstBucket, dstKey string) error`
  - `moveObjectWith(ctx, c Client, srcBucket, srcKey, dstBucket, dstKey string) error`
  - `CopyRequest{AssetID, SrcBucket, SrcKey, DstBucket, DstKey}`
  - `Service.CopyObject(ctx, *CopyRequest) error`、`Service.MoveObject(ctx, *CopyRequest) error`
  - `OSS.OSSCopyObject(CopyRequest) error`、`OSS.OSSMoveObject(CopyRequest) error`

### Steps

- [ ] **RED — 顺序 + copy 失败不删 测.** 新建 `internal/service/oss_svc/copymove_test.go`:

```go
package oss_svc_test

import (
	"context"
	"errors"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCopyObjectWithForwardsArgs(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().CopyObject(gomock.Any(), "src", "a/x.txt", "dst", "b/y.txt").Return(nil)

	err := oss_svc.CopyObjectWith(context.Background(), c, "src", "a/x.txt", "dst", "b/y.txt")
	require.NoError(t, err)
}

// Move = copy 成功后再删源;顺序必须 copy→remove。
func TestMoveObjectWithCopiesThenRemovesInOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	copyCall := c.EXPECT().CopyObject(gomock.Any(), "b", "old.txt", "b", "new.txt").Return(nil)
	c.EXPECT().RemoveObject(gomock.Any(), "b", "old.txt").Return(nil).After(copyCall)

	err := oss_svc.MoveObjectWith(context.Background(), c, "b", "old.txt", "b", "new.txt")
	require.NoError(t, err)
}

// copy 失败绝不接着 delete —— 未对 RemoveObject 设期望,若被调用 gomock 会 fail。
func TestMoveObjectWithCopyFailsDoesNotRemove(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().CopyObject(gomock.Any(), "b", "old.txt", "b", "new.txt").Return(errors.New("access denied"))

	err := oss_svc.MoveObjectWith(context.Background(), c, "b", "old.txt", "b", "new.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}
```

- [ ] **run-it-fails.** `go test ./internal/service/oss_svc/...`
  预期编译失败:`undefined: oss_svc.CopyObjectWith` / `oss_svc.MoveObjectWith`,以及 mock 无 `CopyObject` 方法。

- [ ] **GREEN(接口 + DTO).** `types.go`:`Client` 接口内 `RemoveObject` 后加一行,并新增 `CopyRequest`:

```go
	CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error
```

```go
type CopyRequest struct { // OSSCopyObject / OSSMoveObject 共用
	AssetID   int64  `json:"assetId"`
	SrcBucket string `json:"srcBucket"`
	SrcKey    string `json:"srcKey"`
	DstBucket string `json:"dstBucket"`
	DstKey    string `json:"dstKey"`
}
```

- [ ] **GREEN(adapter).** `client_minio.go`,`RemoveObject` 之后加:

```go
func (a *minioAdapter) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	_, err := a.mc.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
		minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey},
	)
	return err
}
```

- [ ] **GREEN(自由函数).** `ops.go` 末尾加:

```go
func copyObjectWith(ctx context.Context, c Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	return c.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey)
}

// moveObjectWith 先 copy 成功再删源;copy 失败原样返回,绝不删除源对象。
func moveObjectWith(ctx context.Context, c Client, srcBucket, srcKey, dstBucket, dstKey string) error {
	if err := c.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return err
	}
	return c.RemoveObject(ctx, srcBucket, srcKey)
}
```

- [ ] **GREEN(export shim).** `export_test.go` 加:

```go
// CopyObjectWith / MoveObjectWith 导出供外部测试包(oss_svc_test)做 gomock 单测。
var (
	CopyObjectWith = copyObjectWith
	MoveObjectWith = moveObjectWith
)
```

- [ ] **GREEN(service 方法).** `service.go` 加(`Service.RemoveObject` 之后):

```go
func (s *Service) CopyObject(ctx context.Context, req *CopyRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return copyObjectWith(ctx, c, req.SrcBucket, req.SrcKey, req.DstBucket, req.DstKey)
}

func (s *Service) MoveObject(ctx context.Context, req *CopyRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return moveObjectWith(ctx, c, req.SrcBucket, req.SrcKey, req.DstBucket, req.DstKey)
}
```

- [ ] **GREEN(重生成 mock).** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`

- [ ] **run-it-passes.** `go test ./internal/service/oss_svc/...` → copymove 3 测过。

- [ ] **GREEN(app 绑定 + 日志).** `internal/app/oss/oss_ops.go` 的 import 补 logger/zap,并加两个绑定。import 块改为:

```go
import (
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/service/oss_svc"
)
```

绑定(文件末尾追加):

```go
// OSSCopyObject 服务端复制单个对象(同/跨 Bucket 与前缀)。
func (o *OSS) OSSCopyObject(req oss_svc.CopyRequest) error {
	if req.AssetID <= 0 || req.SrcBucket == "" || req.SrcKey == "" || req.DstBucket == "" || req.DstKey == "" {
		return fmt.Errorf("invalid request: assetID, src/dst bucket and key are required")
	}
	ctx := o.i18nCtx()
	if err := o.service.CopyObject(ctx, &req); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("oss copy object",
		zap.Int64("assetId", req.AssetID),
		zap.String("srcBucket", req.SrcBucket), zap.String("srcKey", req.SrcKey),
		zap.String("dstBucket", req.DstBucket), zap.String("dstKey", req.DstKey))
	return nil
}

// OSSMoveObject 复制成功后删除源,覆盖重命名与移动。
func (o *OSS) OSSMoveObject(req oss_svc.CopyRequest) error {
	if req.AssetID <= 0 || req.SrcBucket == "" || req.SrcKey == "" || req.DstBucket == "" || req.DstKey == "" {
		return fmt.Errorf("invalid request: assetID, src/dst bucket and key are required")
	}
	ctx := o.i18nCtx()
	if err := o.service.MoveObject(ctx, &req); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("oss move object",
		zap.Int64("assetId", req.AssetID),
		zap.String("srcBucket", req.SrcBucket), zap.String("srcKey", req.SrcKey),
		zap.String("dstBucket", req.DstBucket), zap.String("dstKey", req.DstKey))
	return nil
}
```

- [ ] **build + gate.** `go build ./...` && `gofmt -l internal/` (空) && `golangci-lint run`。

- [ ] **commit.** `✨ OSS 单对象复制与移动(重命名)绑定`

---

## Task 3 — RemoveObjects(批量删除 + 部分失败聚合)

**Files:**
- Modify `internal/service/oss_svc/types.go`(`Client` 加 `RemoveObjects`;加 `RemoveObjectsRequest`)
- Modify `internal/service/oss_svc/client_minio.go`(`minioAdapter.RemoveObjects` + 纯函数 `aggregateRemoveErrors`)
- Modify `internal/service/oss_svc/ops.go`(`removeObjectsWith`)
- Modify `internal/service/oss_svc/service.go`(`Service.RemoveObjects`)
- Modify `internal/service/oss_svc/export_test.go`(导出 `RemoveObjectsWith`)
- Modify `internal/service/oss_svc/client_minio_test.go`(白盒测 `aggregateRemoveErrors`)
- Create `internal/service/oss_svc/removeobjects_test.go`(`package oss_svc_test`,透传断言)
- Regenerate `mock_ossclient/mock.go`
- Modify `internal/app/oss/oss_ops.go`(`OSSRemoveObjects`)

**Interfaces:**
- Consumes: `(*minio.Client).RemoveObjects(ctx, bucketName string, objectsCh <-chan minio.ObjectInfo, opts minio.RemoveObjectsOptions) <-chan minio.RemoveObjectError`(`api-remove.go:305`);`minio.RemoveObjectError{ObjectName string, VersionID string, Err error}`(`api-remove.go:219`);`minio.RemoveObjectsOptions{GovernanceBypass bool}`。
- Produces:
  - `Client.RemoveObjects(ctx context.Context, bucket string, keys []string) error`
  - `aggregateRemoveErrors(errs []minio.RemoveObjectError) error`(纯函数)
  - `removeObjectsWith(ctx, c Client, bucket string, keys []string) error`
  - `RemoveObjectsRequest{AssetID, Bucket, Keys}`
  - `Service.RemoveObjects(ctx, *RemoveObjectsRequest) error`
  - `OSS.OSSRemoveObjects(RemoveObjectsRequest) error`

> **分层说明(reconcile spec §10):** 部分失败**聚合**发生在 boundary(adapter,唯一持有 minio `RemoveObjectError` 通道处),抽成纯函数 `aggregateRemoveErrors` 做白盒单测 —— 这比"用 mock 断言聚合"更贴近真实逻辑所在层。`removeObjectsWith` + `Service.RemoveObjects` 是薄透传,用 mock 断言 keys 正确传入。

### Steps

- [ ] **RED(纯函数聚合,白盒).** 在 `internal/service/oss_svc/client_minio_test.go` 顶部 import 补 `"errors"`,并追加:

```go
func TestAggregateRemoveErrorsNilWhenEmpty(t *testing.T) {
	assert.NoError(t, aggregateRemoveErrors(nil))
	assert.NoError(t, aggregateRemoveErrors([]minio.RemoveObjectError{}))
}

func TestAggregateRemoveErrorsReportsEachFailure(t *testing.T) {
	err := aggregateRemoveErrors([]minio.RemoveObjectError{
		{ObjectName: "logs/a.txt", Err: errors.New("AccessDenied")},
		{ObjectName: "logs/b.txt", Err: errors.New("NoSuchKey")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logs/a.txt")
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.Contains(t, err.Error(), "logs/b.txt")
}
```

> `client_minio_test.go` 现无 `require`;RED 步骤同时把 import 改为 `"github.com/stretchr/testify/assert"` + `"github.com/stretchr/testify/require"` + `"errors"`。

- [ ] **RED(透传,外部包).** 新建 `internal/service/oss_svc/removeobjects_test.go`:

```go
package oss_svc_test

import (
	"context"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestRemoveObjectsWithForwardsKeys(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	keys := []string{"logs/1.txt", "logs/2.txt", "logs/3.txt"}
	c.EXPECT().RemoveObjects(gomock.Any(), "b", keys).Return(nil)

	err := oss_svc.RemoveObjectsWith(context.Background(), c, "b", keys)
	require.NoError(t, err)
}
```

- [ ] **run-it-fails.** `go test ./internal/service/oss_svc/...`
  预期编译失败:`undefined: aggregateRemoveErrors`、`undefined: oss_svc.RemoveObjectsWith`,mock 无 `RemoveObjects`。

- [ ] **GREEN(接口 + DTO).** `types.go`:`Client` 内 `CopyObject` 后加一行;并加 `RemoveObjectsRequest`:

```go
	RemoveObjects(ctx context.Context, bucket string, keys []string) error
```

```go
type RemoveObjectsRequest struct {
	AssetID int64    `json:"assetId"`
	Bucket  string   `json:"bucket"`
	Keys    []string `json:"keys"`
}
```

- [ ] **GREEN(adapter + 纯函数).** `client_minio.go`:import 补 `"fmt"`、`"strings"`;追加:

```go
func (a *minioAdapter) RemoveObjects(ctx context.Context, bucket string, keys []string) error {
	objCh := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		objCh <- minio.ObjectInfo{Key: k}
	}
	close(objCh)
	var errs []minio.RemoveObjectError
	for rerr := range a.mc.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		errs = append(errs, rerr)
	}
	return aggregateRemoveErrors(errs)
}

// aggregateRemoveErrors 把 minio 批量删除通道里的逐项错误聚合成一个显式报告;
// 空则 nil。绝不静默丢弃部分失败。
func aggregateRemoveErrors(errs []minio.RemoveObjectError) error {
	if len(errs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, fmt.Sprintf("%s: %v", e.ObjectName, e.Err))
	}
	return fmt.Errorf("批量删除部分失败(%d): %s", len(errs), strings.Join(parts, "; "))
}
```

- [ ] **GREEN(自由函数 + shim + service).** `ops.go` 末尾:

```go
func removeObjectsWith(ctx context.Context, c Client, bucket string, keys []string) error {
	return c.RemoveObjects(ctx, bucket, keys)
}
```

`export_test.go` 的 `var (...)` 块内加 `RemoveObjectsWith = removeObjectsWith`。`service.go` 加:

```go
func (s *Service) RemoveObjects(ctx context.Context, req *RemoveObjectsRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return removeObjectsWith(ctx, c, req.Bucket, req.Keys)
}
```

- [ ] **GREEN(重生成 mock).** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`

- [ ] **run-it-passes.** `go test ./internal/service/oss_svc/...` → 聚合 2 测 + 透传 1 测过。

- [ ] **GREEN(app 绑定 + 日志).** `oss_ops.go` 末尾追加:

```go
// OSSRemoveObjects 批量删除对象(一次调用一条关键流日志)。
func (o *OSS) OSSRemoveObjects(req oss_svc.RemoveObjectsRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || len(req.Keys) == 0 {
		return fmt.Errorf("invalid request: assetID, bucket and non-empty keys are required")
	}
	ctx := o.i18nCtx()
	if err := o.service.RemoveObjects(ctx, &req); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("oss remove objects",
		zap.Int64("assetId", req.AssetID),
		zap.String("bucket", req.Bucket),
		zap.Int("count", len(req.Keys)))
	return nil
}
```

- [ ] **build + gate.** `go build ./...` && `gofmt -l internal/`(空) && `golangci-lint run`。

- [ ] **commit.** `✨ OSS 批量删除对象绑定(聚合部分失败)`

---

## Task 4 — CreateFolder(前缀规范化 + 零字节 PUT)

**Files:**
- Modify `internal/service/oss_svc/types.go`(`Client` 加 `PutObject`;加 `CreateFolderRequest`)
- Modify `internal/service/oss_svc/client_minio.go`(`minioAdapter.PutObject`;import 补 `"io"`)
- Modify `internal/service/oss_svc/ops.go`(`normalizeFolderPrefix` + `createFolderWith`;import 补 `"fmt"`、`"strings"`)
- Modify `internal/service/oss_svc/service.go`(`Service.CreateFolder`)
- Modify `internal/service/oss_svc/export_test.go`(导出 `CreateFolderWith`)
- Modify `internal/service/oss_svc/client_minio_test.go`(白盒测 `normalizeFolderPrefix`)
- Create `internal/service/oss_svc/createfolder_test.go`(`package oss_svc_test`,PutObject 参数断言)
- Regenerate `mock_ossclient/mock.go`
- Modify `internal/app/oss/oss_ops.go`(`OSSCreateFolder`)

**Interfaces:**
- Consumes: `(*minio.Client).PutObject(ctx, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)`(`api-put-object.go:329`);`minio.PutObjectOptions{ContentType string, ...}`(field `api-put-object.go:79`)。
- Produces:
  - `Client.PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error`
  - `normalizeFolderPrefix(prefix string) (string, error)`(纯函数)
  - `createFolderWith(ctx, c Client, bucket, prefix string) error`
  - `CreateFolderRequest{AssetID, Bucket, Prefix}`
  - `Service.CreateFolder(ctx, *CreateFolderRequest) error`
  - `OSS.OSSCreateFolder(CreateFolderRequest) error`

### Steps

- [ ] **RED(纯函数,白盒).** 在 `client_minio_test.go` 追加:

```go
func TestNormalizeFolderPrefixAppendsSlash(t *testing.T) {
	got, err := normalizeFolderPrefix("docs/2026")
	require.NoError(t, err)
	assert.Equal(t, "docs/2026/", got)
}

func TestNormalizeFolderPrefixKeepsTrailingSlash(t *testing.T) {
	got, err := normalizeFolderPrefix("docs/2026/")
	require.NoError(t, err)
	assert.Equal(t, "docs/2026/", got)
}

func TestNormalizeFolderPrefixTrimsAndRejectsEmpty(t *testing.T) {
	got, err := normalizeFolderPrefix("  reports  ")
	require.NoError(t, err)
	assert.Equal(t, "reports/", got)

	_, err = normalizeFolderPrefix("   ")
	require.Error(t, err)
}
```

- [ ] **RED(PutObject 参数,外部包).** 新建 `internal/service/oss_svc/createfolder_test.go`:

```go
package oss_svc_test

import (
	"context"
	"io"
	"testing"

	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// CreateFolder 规范化前缀为以 "/" 结尾,并做零字节 PUT。
func TestCreateFolderWithNormalizesAndZeroBytePut(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().PutObject(
		gomock.Any(), "b", "docs/",
		gomock.AssignableToTypeOf((io.Reader)(nil)), int64(0), gomock.Any(),
	).Return(nil)

	err := oss_svc.CreateFolderWith(context.Background(), c, "b", "docs")
	require.NoError(t, err)
}

func TestCreateFolderWithRejectsEmptyPrefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	// 空前缀应在 PutObject 之前失败,故不设 PutObject 期望。
	err := oss_svc.CreateFolderWith(context.Background(), c, "b", "  ")
	require.Error(t, err)
}
```

- [ ] **run-it-fails.** `go test ./internal/service/oss_svc/...`
  预期编译失败:`undefined: normalizeFolderPrefix`、`undefined: oss_svc.CreateFolderWith`,mock 无 `PutObject`。

- [ ] **GREEN(接口 + DTO).** `types.go`:import 补 `"io"`;`Client` 内 `RemoveObjects` 后加一行;加 `CreateFolderRequest`:

```go
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error
```

```go
type CreateFolderRequest struct {
	AssetID int64  `json:"assetId"`
	Bucket  string `json:"bucket"`
	Prefix  string `json:"prefix"` // 服务端规范化为以 "/" 结尾
}
```

- [ ] **GREEN(adapter).** `client_minio.go`:import 补 `"io"`;追加:

```go
func (a *minioAdapter) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType string) error {
	_, err := a.mc.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}
```

- [ ] **GREEN(纯函数 + 自由函数).** `ops.go`:import 由 `import "context"` 改为:

```go
import (
	"context"
	"fmt"
	"strings"
)
```

追加:

```go
// normalizeFolderPrefix 校验并规范化文件夹前缀为以 "/" 结尾;空则报错。
func normalizeFolderPrefix(prefix string) (string, error) {
	p := strings.TrimSpace(prefix)
	if p == "" {
		return "", fmt.Errorf("文件夹前缀不能为空")
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, nil
}

// createFolderWith 对 <prefix>/ 做零字节 PUT(S3 文件夹占位符约定)。
func createFolderWith(ctx context.Context, c Client, bucket, prefix string) error {
	normalized, err := normalizeFolderPrefix(prefix)
	if err != nil {
		return err
	}
	return c.PutObject(ctx, bucket, normalized, strings.NewReader(""), 0, "application/x-directory")
}
```

- [ ] **GREEN(shim + service).** `export_test.go` 的 `var (...)` 加 `CreateFolderWith = createFolderWith`。`service.go` 加:

```go
func (s *Service) CreateFolder(ctx context.Context, req *CreateFolderRequest) error {
	c, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return err
	}
	return createFolderWith(ctx, c, req.Bucket, req.Prefix)
}
```

- [ ] **GREEN(重生成 mock).** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`

- [ ] **run-it-passes.** `go test ./internal/service/oss_svc/...` → normalize 3 测 + createFolder 2 测过。

- [ ] **GREEN(app 绑定 + 日志).** `oss_ops.go` 末尾追加:

```go
// OSSCreateFolder 在指定前缀下新建"文件夹"(零字节占位对象)。
func (o *OSS) OSSCreateFolder(req oss_svc.CreateFolderRequest) error {
	if req.AssetID <= 0 || req.Bucket == "" || req.Prefix == "" {
		return fmt.Errorf("invalid request: assetID, bucket and prefix are required")
	}
	ctx := o.i18nCtx()
	if err := o.service.CreateFolder(ctx, &req); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("oss create folder",
		zap.Int64("assetId", req.AssetID),
		zap.String("bucket", req.Bucket),
		zap.String("prefix", req.Prefix))
	return nil
}
```

- [ ] **build + gate.** `go build ./...` && `gofmt -l internal/`(空) && `golangci-lint run`。

- [ ] **commit.** `✨ OSS 新建文件夹绑定(前缀规范化零字节 PUT)`

---

## Task 5 — 流式上传(原生多选对话框 + 拖拽路径 + 进度上报)

**Files:**
- Modify `internal/pkg/transfer/transfer.go`(新增 `ProgressReader` + `NewProgressReader`;import 补 `"context"`、`"io"`)
- Modify `internal/pkg/transfer/transfer_test.go`(`ProgressReader` 单测)
- Modify `internal/service/oss_svc/service.go`(`Service.PutObject` app 面原语;import 补 `"io"`)
- Modify `internal/app/oss/oss.go`(`OSS` 结构加 `cancels sync.Map`;import 补 `"sync"`)
- Create `internal/app/oss/oss_transfer.go`(`OSSUploadObject` / `OSSUploadObjectPath` + 内部 `uploadObject` + `deriveUploadKey` + `emitProgress`/`emitTerminal`)
- Create `internal/app/oss/oss_transfer_test.go`(`package oss`,白盒测 `deriveUploadKey`)

**Interfaces:**
- Consumes: `Client.PutObject`(T4);`transfer.GenerateID`、`transfer.Reporter`、`transfer.Progress`、`transfer.Status*`(`internal/pkg/transfer/transfer.go`);`wailsRuntime.OpenMultipleFilesDialog(ctx, wailsRuntime.OpenDialogOptions) ([]string, error)`(`wails/v2@v2.12.0/pkg/runtime/dialog.go:55`);`wailsRuntime.EventsEmit(ctx, name, data...)`。
- Produces:
  - `transfer.ProgressReader`(io.Reader,读时按 Reporter 节流上报,ctx 取消即中断)
  - `transfer.NewProgressReader(ctx context.Context, transferID, currentFile string, r io.Reader, total int64, onProgress func(Progress)) *ProgressReader`
  - `Service.PutObject(ctx context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string) error`
  - `deriveUploadKey(keyPrefix, localPath string) string`(纯函数)
  - `OSS.OSSUploadObject(assetID int64, bucket, keyPrefix string) ([]string, error)`
  - `OSS.OSSUploadObjectPath(assetID int64, bucket, key, localPath string) (string, error)`

> **诚实测试边界:** 单测覆盖可抽取的纯/确定性件 —— `transfer.ProgressReader`(累计字节 + ctx 取消)与 `deriveUploadKey`(key 派生)。`OSSUploadObject`/`OSSUploadObjectPath` 的「原生对话框 + goroutine + `EventsEmit` + `connect` 真拨号」路径**不做假单测**,列为 MinIO 集成/手动验证(见步骤末的观察项)。

### Steps

- [ ] **RED(ProgressReader 单测).** 在 `internal/pkg/transfer/transfer_test.go`(`package transfer`)追加:

```go
func TestProgressReaderReportsCumulativeBytes(t *testing.T) {
	var got []Progress
	src := bytes.NewReader(bytes.Repeat([]byte("x"), 100))
	pr := NewProgressReader(context.Background(), "oss-1", "hero.jpg", src, 100, func(p Progress) {
		got = append(got, p)
	})

	buf := make([]byte, 40)
	n, err := pr.Read(buf) // 首条 progress 立即放行(lastEmit 零值)
	require.NoError(t, err)
	require.Equal(t, 40, n)
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, "oss-1", last.TransferID)
	assert.Equal(t, StatusProgress, last.Status)
	assert.Equal(t, "hero.jpg", last.CurrentFile)
	assert.Equal(t, int64(40), last.BytesDone)
	assert.Equal(t, int64(100), last.BytesTotal)
}

func TestProgressReaderAbortsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr := NewProgressReader(ctx, "oss-1", "hero.jpg", bytes.NewReader([]byte("data")), 4, func(Progress) {})

	_, err := pr.Read(make([]byte, 4))
	assert.ErrorIs(t, err, context.Canceled)
}
```

> 若 `transfer_test.go` 尚未 import `bytes`/`context`/`require`,RED 步骤一并补齐 import。

- [ ] **run-it-fails.** `go test ./internal/pkg/transfer/...`
  预期编译失败:`undefined: NewProgressReader` / `undefined: ProgressReader`。

- [ ] **GREEN(transfer 原语).** `internal/pkg/transfer/transfer.go`:import 由现三项加 `"context"`、`"io"`;文件末尾追加:

```go
// ProgressReader 包裹一个 io.Reader:在 sink 拥有读循环(如 minio PutObject)的流式上传里,
// 于源 reader 侧观测进度并经 Reporter 节流上报;ctx 取消即中断读取。同一传输的 Read 串行,
// 内部无需加锁(与 Reporter 约定一致)。
type ProgressReader struct {
	ctx         context.Context
	r           io.Reader
	reporter    *Reporter
	transferID  string
	currentFile string
	total       int64
	done        int64
}

// NewProgressReader 构造进度 reader,内部持有独立 Reporter(100ms 节流)。
func NewProgressReader(ctx context.Context, transferID, currentFile string, r io.Reader, total int64, onProgress func(Progress)) *ProgressReader {
	return &ProgressReader{
		ctx:         ctx,
		r:           r,
		reporter:    NewReporter(onProgress),
		transferID:  transferID,
		currentFile: currentFile,
		total:       total,
	}
}

func (p *ProgressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.reporter.Report(Progress{
			TransferID:  p.transferID,
			Status:      StatusProgress,
			CurrentFile: p.currentFile,
			FilesTotal:  1,
			BytesDone:   p.done,
			BytesTotal:  p.total,
		})
	}
	return n, err
}
```

- [ ] **run-it-passes(transfer).** `go test ./internal/pkg/transfer/...` → ProgressReader 2 测过。

- [ ] **GREEN(service 上传原语).** `service.go`:import 补 `"io"`;追加:

```go
// PutObject 是给 app 层流式上传用的原语:connect 后把(通常已包进度)reader 写入对象。
func (s *Service) PutObject(ctx context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string) error {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return err
	}
	return c.PutObject(ctx, bucket, key, r, size, contentType)
}
```

- [ ] **GREEN(OSS 结构加 cancel 注册表).** `internal/app/oss/oss.go`:import 补 `"sync"`;结构体加字段:

```go
// OSS binder。
type OSS struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	service *oss_svc.Service
	cancels sync.Map // transferID -> context.CancelFunc(在途传输取消注册表,仿 sftp_svc)
}
```

> `sync.Map` 零值可用,`New` 无需改。

- [ ] **RED(deriveUploadKey 单测).** 新建 `internal/app/oss/oss_transfer_test.go`:

```go
package oss

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeriveUploadKeyJoinsPrefixAndBase(t *testing.T) {
	assert.Equal(t, "images/hero.jpg", deriveUploadKey("images/", "/Users/me/pics/hero.jpg"))
	assert.Equal(t, "hero.jpg", deriveUploadKey("", "/Users/me/pics/hero.jpg"))
}
```

- [ ] **run-it-fails(app).** `go test ./internal/app/oss/...` → `undefined: deriveUploadKey`。

- [ ] **GREEN(oss_transfer.go 上传).** 新建 `internal/app/oss/oss_transfer.go`:

```go
package oss

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/opskat/opskat/internal/pkg/transfer"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// deriveUploadKey 目标 key = keyPrefix + 本地文件名。
func deriveUploadKey(keyPrefix, localPath string) string {
	return keyPrefix + filepath.Base(localPath)
}

func contentTypeFor(localPath string) string {
	if ct := mime.TypeByExtension(filepath.Ext(localPath)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (o *OSS) emitProgress(transferID string, p transfer.Progress) {
	wailsRuntime.EventsEmit(o.ctx, "transfer:progress:"+transferID, p)
}

// emitTerminal 依据 err 发出 done/error/cancelled 终态(立即,不节流)。
func (o *OSS) emitTerminal(transferID string, err error) {
	switch {
	case err == nil:
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusDone})
	case errors.Is(err, context.Canceled):
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusCancelled})
	default:
		o.emitProgress(transferID, transfer.Progress{TransferID: transferID, Status: transfer.StatusError, Error: err.Error()})
	}
}

// OSSUploadObject 弹原生多选对话框,对每个选中文件起一路流式上传,返回各 transferID。
// 用户取消对话框 → 返回空切片。
func (o *OSS) OSSUploadObject(assetID int64, bucket, keyPrefix string) ([]string, error) {
	if assetID <= 0 || bucket == "" {
		return nil, fmt.Errorf("invalid request: assetID and bucket are required")
	}
	localPaths, err := wailsRuntime.OpenMultipleFilesDialog(o.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件",
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if len(localPaths) == 0 {
		return []string{}, nil // 用户取消
	}

	ids := make([]string, 0, len(localPaths))
	for _, localPath := range localPaths {
		transferID := transfer.GenerateID("oss")
		key := deriveUploadKey(keyPrefix, localPath)
		go o.uploadObject(transferID, assetID, bucket, key, localPath)
		ids = append(ids, transferID)
	}
	return ids, nil
}

// OSSUploadObjectPath 无对话框(拖拽遮罩用),流式上传单个本地文件到指定 key。
func (o *OSS) OSSUploadObjectPath(assetID int64, bucket, key, localPath string) (string, error) {
	if assetID <= 0 || bucket == "" || key == "" || localPath == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket, key and localPath are required")
	}
	transferID := transfer.GenerateID("oss")
	go o.uploadObject(transferID, assetID, bucket, key, localPath)
	return transferID, nil
}

// uploadObject 单文件流式上传:注册取消 → 打开文件 → 包进度 reader → service.PutObject → emit 终态。
func (o *OSS) uploadObject(transferID string, assetID int64, bucket, key, localPath string) {
	ctx, cancel := context.WithCancel(o.ctx)
	o.cancels.Store(transferID, cancel)
	defer func() {
		o.cancels.Delete(transferID)
		cancel()
	}()

	f, err := os.Open(localPath) //nolint:gosec // path 来自用户文件对话框/拖拽
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("打开本地文件失败: %w", err))
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}

	pr := transfer.NewProgressReader(ctx, transferID, filepath.Base(localPath), f, info.Size(), func(p transfer.Progress) {
		o.emitProgress(transferID, p)
	})
	err = o.service.PutObject(ctx, assetID, bucket, key, pr, info.Size(), contentTypeFor(localPath))
	o.emitTerminal(transferID, err)
}
```

- [ ] **run-it-passes(app).** `go test ./internal/app/oss/...` → `deriveUploadKey` 测过。

- [ ] **build + gate.** `go build ./...` && `gofmt -l internal/`(空) && `golangci-lint run`。

- [ ] **MinIO 集成/手动验证(不写假单测).** 对话框 + goroutine + `EventsEmit` + 真拨号路径按下法验证其一:
  - **(a) 适配器层集成测(推荐,覆盖字节完整性):** 见 Task 6 步骤末登记的 `client_minio_integration_test.go`(`//go:build integration`),其中对同一 `minioAdapter` 跑 `PutObject`→`GetObject` 往返并断言字节一致(上传原语正确性)。用本地 MinIO 跑:`MINIO_ENDPOINT=127.0.0.1:9000 go test -tags=integration ./internal/service/oss_svc/...`。
  - **(b) app 层观察式验证:** 起应用后,前端尚无(P3b),故用一次性调用 `OSSUploadObjectPath(assetID, bucket, "probe/x.bin", "/tmp/x.bin")`,再 `tail -f logs/opskat.log` 确认无 error;并用 `mc ls <alias>/<bucket>/probe/` 或 `OSSStatObject` 确认对象落地、`Size` 与本地一致。

- [ ] **commit.** `✨ OSS 流式上传绑定(多选对话框 + 拖拽 + 进度)`

---

## Task 6 — 流式下载 + 取消(原生保存对话框 + 32KB 循环 + 取消注册表)

**Files:**
- Modify `internal/pkg/transfer/transfer.go`(新增 `Copy` 共享拷贝循环)
- Modify `internal/pkg/transfer/transfer_test.go`(`Copy` 单测)
- Modify `internal/service/oss_svc/types.go`(`Client` 加 `GetObject`)
- Modify `internal/service/oss_svc/client_minio.go`(`minioAdapter.GetObject`)
- Modify `internal/service/oss_svc/service.go`(`Service.GetObject`)
- Regenerate `mock_ossclient/mock.go`
- Modify `internal/app/oss/oss_transfer.go`(`OSSDownloadObject` + `downloadObject` + `OSSCancelTransfer`)
- Create `internal/service/oss_svc/client_minio_integration_test.go`(`//go:build integration`,本地 MinIO 往返)

**Interfaces:**
- Consumes: `(*minio.Client).GetObject(ctx, bucket, object string, opts minio.GetObjectOptions) (*minio.Object, error)`(`api-get-object.go:32`);`*minio.Object` 实现 `io.ReadCloser`(`Read` `api-get-object.go:377`、`Close` `:633`)且有 `Stat() (minio.ObjectInfo, error)`(`:431`);`wailsRuntime.SaveFileDialog(ctx, wailsRuntime.SaveDialogOptions{DefaultFilename, Title}) (string, error)`(`dialog.go:66`)。
- Produces:
  - `transfer.Copy(ctx context.Context, transferID string, dst io.Writer, src io.Reader, totalBytes int64, currentFile string, onProgress func(Progress)) error`
  - `Client.GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error)`
  - `Service.GetObject(ctx context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error)`
  - `OSS.OSSDownloadObject(assetID int64, bucket, key string) (string, error)`
  - `OSS.OSSCancelTransfer(transferID string) error`

> **诚实测试边界:** 单测覆盖 `transfer.Copy`(确定性:字节一致 + 进度上报 + ctx 取消中断)。`OSSDownloadObject` 的对话框 + goroutine + `GetObject` 真拨号 + `EventsEmit` 路径列为 MinIO 集成/手动验证(见步骤末)。`OSSCancelTransfer` 的注册表命中在 `transfer.Copy` 的 ctx-取消测里间接验证逻辑成立。

### Steps

- [ ] **RED(Copy 单测).** 在 `internal/pkg/transfer/transfer_test.go` 追加:

```go
func TestCopyStreamsAllBytesAndReports(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat([]byte("y"), 70*1024)) // >2 个 32KB 分片
	var dst bytes.Buffer
	var got []Progress
	err := Copy(context.Background(), "oss-2", &dst, src, int64(70*1024), "big.bin", func(p Progress) {
		got = append(got, p)
	})
	require.NoError(t, err)
	assert.Equal(t, 70*1024, dst.Len())
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, StatusProgress, last.Status)
	assert.Equal(t, int64(70*1024), last.BytesTotal)
}

func TestCopyAbortsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Copy(ctx, "oss-2", &bytes.Buffer{}, bytes.NewReader([]byte("data")), 4, "x", func(Progress) {})
	assert.ErrorIs(t, err, context.Canceled)
}
```

- [ ] **run-it-fails.** `go test ./internal/pkg/transfer/...` → `undefined: Copy`。

- [ ] **GREEN(transfer.Copy).** `internal/pkg/transfer/transfer.go` 末尾追加(镜像 `sftp_svc.copyWithProgress:733`,内部起独立 Reporter):

```go
// Copy 以 32KiB 分片把 src 流式写入 dst,经独立 Reporter(100ms 节流)上报进度,
// ctx 取消即中断。镜像 sftp_svc.copyWithProgress,让每种传输源共用一套节流拷贝循环。
func Copy(ctx context.Context, transferID string, dst io.Writer, src io.Reader, totalBytes int64, currentFile string, onProgress func(Progress)) error {
	buf := make([]byte, 32*1024)
	var bytesDone int64
	reporter := NewReporter(onProgress)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			bytesDone += int64(n)
			reporter.Report(Progress{
				TransferID:  transferID,
				Status:      StatusProgress,
				CurrentFile: currentFile,
				FilesTotal:  1,
				BytesDone:   bytesDone,
				BytesTotal:  totalBytes,
			})
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
```

> 注:`sftp_svc.copyWithProgress` 仍保留原样(SFTP 是 hot subsystem,收敛其到 `transfer.Copy` 属跨子系统重构,不在 P3a 范围);此处新增共享原语供 OSS 复用,后续可单独立项收敛 SFTP。

- [ ] **run-it-passes(transfer).** `go test ./internal/pkg/transfer/...` → Copy 2 测过。

- [ ] **GREEN(Client.GetObject + adapter).** `types.go`:`Client` 内 `PutObject` 后加一行:

```go
	GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error)
```

`client_minio.go` 追加(`*minio.Object` 即 io.ReadCloser;`Stat()` 拿总大小做进度分母,失败则关流):

```go
func (a *minioAdapter) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error) {
	obj, err := a.mc.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, err
	}
	return obj, info.Size, nil
}
```

- [ ] **GREEN(service.GetObject).** `service.go` 追加:

```go
// GetObject 是给 app 层流式下载用的原语:connect 后返回对象流 + 总大小。
func (s *Service) GetObject(ctx context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error) {
	c, err := s.connect(ctx, assetID)
	if err != nil {
		return nil, 0, err
	}
	return c.GetObject(ctx, bucket, key)
}
```

- [ ] **GREEN(重生成 mock).** `PATH="$PATH:$(go env GOPATH)/bin" go generate ./internal/service/oss_svc/...`
  预期 mock 新增 `GetObject`(3 参 → `io.ReadCloser, int64, error`)。

- [ ] **GREEN(oss_transfer.go 下载 + 取消).** `internal/app/oss/oss_transfer.go`:import 补 `"path"`;追加:

```go
// OSSDownloadObject 弹原生保存对话框(默认名 = key 末段),流式下载并报进度,返回 transferID。
// 用户取消对话框 → 返回空串。
func (o *OSS) OSSDownloadObject(assetID int64, bucket, key string) (string, error) {
	if assetID <= 0 || bucket == "" || key == "" {
		return "", fmt.Errorf("invalid request: assetID, bucket and key are required")
	}
	localPath, err := wailsRuntime.SaveFileDialog(o.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: path.Base(key),
		Title:           "保存到本地",
	})
	if err != nil {
		return "", fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if localPath == "" {
		return "", nil // 用户取消
	}
	transferID := transfer.GenerateID("oss")
	go o.downloadObject(transferID, assetID, bucket, key, localPath)
	return transferID, nil
}

// downloadObject 单对象流式下载:注册取消 → GetObject 拿流 → 建本地文件 → transfer.Copy → emit 终态。
func (o *OSS) downloadObject(transferID string, assetID int64, bucket, key, localPath string) {
	ctx, cancel := context.WithCancel(o.ctx)
	o.cancels.Store(transferID, cancel)
	defer func() {
		o.cancels.Delete(transferID)
		cancel()
	}()

	rc, size, err := o.service.GetObject(ctx, assetID, bucket, key)
	if err != nil {
		o.emitTerminal(transferID, err)
		return
	}
	defer func() { _ = rc.Close() }()

	f, err := os.Create(localPath) //nolint:gosec // path 来自保存对话框
	if err != nil {
		o.emitTerminal(transferID, fmt.Errorf("创建本地文件失败: %w", err))
		return
	}
	defer func() { _ = f.Close() }()

	err = transfer.Copy(ctx, transferID, f, rc, size, path.Base(key), func(p transfer.Progress) {
		o.emitProgress(transferID, p)
	})
	o.emitTerminal(transferID, err)
}

// OSSCancelTransfer 经注册表取消在途上传/下载(命中即调用其 CancelFunc,ctx 取消触发 cancelled 终态)。
func (o *OSS) OSSCancelTransfer(transferID string) error {
	if transferID == "" {
		return fmt.Errorf("invalid transferID")
	}
	if v, ok := o.cancels.Load(transferID); ok {
		v.(context.CancelFunc)()
	}
	return nil
}
```

- [ ] **run-it-passes(all).** `go test ./internal/service/oss_svc/... ./internal/app/oss/... ./internal/pkg/transfer/...` → 全过。

- [ ] **GREEN(集成测,build tag).** 新建 `internal/service/oss_svc/client_minio_integration_test.go`(仅 `-tags=integration` 编译,回归上传/下载字节完整性 + copy/move/create-folder/分页):

```go
//go:build integration

package oss_svc

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/require"
)

// 需本地 MinIO(S3 兼容):MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY / MINIO_BUCKET。
func newIntegrationAdapter(t *testing.T) (Client, string) {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT 未设置,跳过集成测")
	}
	cfg := &asset_entity.OSSConfig{
		Endpoint:     endpoint,
		AccessKeyID:  os.Getenv("MINIO_ACCESS_KEY"),
		UsePathStyle: true,
	}
	mc, err := connpool.DialOSS(cfg, os.Getenv("MINIO_SECRET_KEY"))
	require.NoError(t, err)
	return newMinioAdapter(mc), os.Getenv("MINIO_BUCKET")
}

func TestIntegrationPutGetRoundTripKeepsBytes(t *testing.T) {
	c, bucket := newIntegrationAdapter(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("opskat-oss-"), 5000)

	require.NoError(t, c.PutObject(ctx, bucket, "p3a/roundtrip.bin", bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"))

	rc, size, err := c.GetObject(ctx, bucket, "p3a/roundtrip.bin")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	require.Equal(t, int64(len(payload)), size)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.True(t, bytes.Equal(payload, got))

	require.NoError(t, c.RemoveObjects(ctx, bucket, []string{"p3a/roundtrip.bin"}))
}
```

- [ ] **MinIO 集成/手动验证.** `MINIO_ENDPOINT=127.0.0.1:9000 MINIO_ACCESS_KEY=... MINIO_SECRET_KEY=... MINIO_BUCKET=... go test -tags=integration ./internal/service/oss_svc/...` → 往返字节一致。取消终态观察:起应用触发一次下载后立刻 `OSSCancelTransfer(id)`,`tail logs/opskat.log` 应无残留 error,前端(P3b)将收到 `transfer:progress:<id>` 的 `cancelled` 终态;本地半文件由调用方后续清理(P3b 决策,不在本期)。

- [ ] **build + gate.** `go build ./...` && `gofmt -l internal/`(空) && `golangci-lint run`。

- [ ] **commit.** `✨ OSS 流式下载与取消绑定(保存对话框 + 32KB 循环)`

---

## Self-review 对照 spec §3–§10

- **§3 绑定面:** `OSSListObjects`(T1 扩分页)/ `OSSCopyObject`·`OSSMoveObject`(T2)/ `OSSRemoveObjects`(T3)/ `OSSCreateFolder`(T4)/ `OSSUploadObject`·`OSSUploadObjectPath`(T5)/ `OSSDownloadObject`·`OSSCancelTransfer`(T6)。P1 既有绑定不改。✅
- **§4 Client/Service 扩展:** `CopyObject`(T2)/`RemoveObjects`(T3)/`PutObject`(T4)/`GetObject`(T6)/`ListObjects` 扩签名(T1)。**分歧记录:** spec §4 内联把 `Client.ListObjects` 写成直接返回 `(objs, prefixes, next, truncated, err)`;为满足 §10「分页游标经 mock 单测」,本计划让 `Client.ListObjects` 返回扁平 `[]ObjectItem`,拆分/截断/游标逻辑留在可 mock 的 service 自由函数 `listObjectsWith`(即 §7「service 侧设上界」)。同理 §4 的 `RemoveObjects(...) error` 保留,聚合逻辑落在 boundary 的纯函数 `aggregateRemoveErrors`(白盒测),而非"service 层 mock 聚合"。两处均在对应任务与本节标注。✅
- **§5 流式架构:** `transfer.GenerateID("oss")`、Go 内对话框、goroutine + Reporter + `EventsEmit`、上传计数 reader(`ProgressReader`)、下载 32KB 循环(`transfer.Copy`)、cancel 注册表(`OSS.cancels`)—— T5/T6。✅
- **§6 DTO:** `CopyRequest`(T2)/`RemoveObjectsRequest`(T3)/`CreateFolderRequest`(T4)/`ListObjectsRequest`·`ListObjectsResult` 增字段(T1),全 camelCase。✅
- **§7 分页语义:** `defaultListMaxKeys=200`、有界读(adapter 读 maxKeys+1)、`IsTruncated`+`NextContinuationToken`(=最后一项 Key 作 StartAfter)、Prefixes/Objects 均计入页大小 —— T1。✅
- **§8 安全/审计/错误:** 边界校验只在 app 层(各绑定)、SK 恒经 `credential_resolver`(既有 `lookup`)、变更操作结构化日志(见 Global Constraints 审计说明 + 事实核查分歧)、copy 失败不删(T2 `moveObjectWith` + 专测)、批量删除聚合不静默(T3)、`PolicyKind()==""` 不变(不触碰)。✅
- **§9 复用地图:** 传输栈镜像 `ssh/sftp.go`、循环仿 `sftp_svc.copyWithProgress`、进度基元 `transfer.*`、连接/凭证 `connpool.GetOrDialOSS`、OCP 直扩 `Client` 接口不改分发器 —— 贯穿 T5/T6 及各任务。✅
- **§10 测试策略:** MoveObject 顺序 + copy 失败不删(T2)、CreateFolder 前缀规范化(T4)、RemoveObjects 部分失败聚合(T3)、ListObjects 分页游标(T1),全 `package oss_svc_test` + `export_test.go` shim;集成(本地 MinIO)字节完整性(T6 build-tag 测);取消终态(T6 观察项)。✅

**未干净映射的 spec 项:**
- **§8 审计** —— spec 要求"经既有审计 canonical 入口各记一条",但事实核查:既有 OSS/etcd/redis 绑定**不写 `audit_logs`**,唯一 desktop 审计写入者是 `external_edit_svc`。本计划按任务书"mirror existing"镜像为 `logger.Ctx` 结构化日志,**未落 `audit_logs` 表**。若需真正入 `audit_logs`,须 controller 确认(见 Global Constraints)。
- **§8 P1 遗留(`assettype/oss.go SafeView()` 漏 `connect_timeout`)** —— spec 明言"若顺手补则加断言,否则不动";本计划范围内不动(与 OSS 对象浏览无耦合),不作为任务。
