# OSS 对象浏览器 · P3a 后端绑定 — 设计规格

> 状态:设计定稿(brainstorming 2026-07-08),待写实施计划。
> 范围:为 P3b 前端对象浏览器提供后端绑定 —— 复制 / 移动(重命名)/ 批量删除 / 新建文件夹 / 原生流式上传下载(带进度)/ 取消 / 列表分页。
> 分支:`feature/oss-asset-type`(P1 后端 + P2 前端已在此分支,均未合并)。
> 上游规格:`docs/superpowers/specs/2026-07-08-oss-asset-type-design.md`(整体 OSS 特性)。

## 1. 目标与边界

P3 对象浏览器被拆为 **P3a(后端绑定)→ P3b(前端工作区)**,各自独立 spec→plan→实现。本 spec 只覆盖 **P3a 后端**:纯 Go 绑定面,可用本地 MinIO 独立回归,**不含任何前端**。复用 P1 已落地的凭证解析(`credential_resolver`)、连接池(`connpool.GetOrDialOSS`)、以及 SFTP 已验证的传输基础设施(`internal/pkg/transfer` + Wails `EventsEmit`)。

**明确不在 P3a(已与用户确认延后):**
- 文件夹级重命名 / 移动(前缀递归 copy+delete 的部分失败语义单独立项)。
- 递归目录上传 / 前缀(文件夹)下载。
- 对象 ACL(厂商差异大、常被 bucket-owner-enforced 关闭)—— 详情抽屉本期只给元数据 + 预览。
- Bucket 创建 / 删除(产品模型是账号级浏览既有 Bucket)。
- OSS 审批策略门控(OSS 维持 `PolicyKind()==""`;变更操作**审计**但不走审批,沿用 P1 决策)。

## 2. 决策记录(本次 brainstorming 锁定)

1. **传输模型 = 原生 Go 流式 + 进度**,逐字复用 SFTP 传输栈(非预签名浏览器直传)。
2. **复制 / 重命名 / 移动 = 仅单对象**;文件夹级延后。重命名与移动在绑定层是同一操作(`OSSMoveObject` = copy+delete,重命名即同 Bucket 换 key)。
3. **列表分页 = 现在就加**(`maxKeys` + 续传游标),避免把海量 key 一次性塞进单个 IPC 响应。
4. **批量删除 = 加 `OSSRemoveObjects`**(minio `RemoveObjects` 通道,一次调用一条审计)。
5. **ACL 延后**;详情/预览用既有 `OSSStatObject` + `OSSPresignGet`。
6. **上传范围 = 单个文件(多选 + 拖拽)**;下载 = 单对象。

## 3. Wails 绑定面

**保留不变(P1 已有):** `OSSTestConnection` / `OSSListBuckets` / `OSSStatObject` / `OSSPresignGet` / `OSSPresignPut` / `OSSRemoveObject`(单个)。

**扩展:**
- `OSSListObjects(req ListObjectsRequest) (*ListObjectsResult, error)` —— 入参增 `maxKeys int` + `continuationToken string`,出参增 `nextContinuationToken string` + `isTruncated bool`。某层级子项很多时返回**有界页**而非全量(见 §7 分页语义)。

**新增(非流式,写入 `internal/app/oss/oss_ops.go`):**
- `OSSCopyObject(req CopyRequest) error` —— 服务端 `CopyObject`(src→dst,同/跨 Bucket 与前缀)。
- `OSSMoveObject(req CopyRequest) error` —— copy 成功后再 remove src;**覆盖重命名与移动**。
- `OSSRemoveObjects(req RemoveObjectsRequest) error` —— 批量删除。
- `OSSCreateFolder(req CreateFolderRequest) error` —— 对 `<prefix>/`(以 `/` 结尾)做零字节 PUT。

**新增(流式,写入新文件 `internal/app/oss/oss_transfer.go`,镜像 `internal/app/ssh/sftp.go`):**
- `OSSUploadObject(assetID int64, bucket, keyPrefix string) ([]string, error)` —— 在 Go 内弹原生 `OpenMultipleFilesDialog`,对每个选中文件生成 `transferID`、起 goroutine 用 `PutObject` 流式上传并报进度,返回各 `transferID`(用户取消对话框 → 返回空)。目标 key = `keyPrefix + filepath.Base(localPath)`。
- `OSSUploadObjectPath(assetID int64, bucket, key, localPath string) (string, error)` —— 无对话框(拖拽遮罩用),流式上传单个本地文件到指定 key,返回 `transferID`。
- `OSSDownloadObject(assetID int64, bucket, key string) (string, error)` —— 在 Go 内弹原生 `SaveFileDialog`(`DefaultFilename = path.Base(key)`),流式下载并报进度,返回 `transferID`(用户取消对话框 → 返回空串)。
- `OSSCancelTransfer(transferID string) error` —— 取消在途传输。

所有绑定在 app 层做边界校验(`assetID>0`、`bucket!=""`、必需 key 非空),与既有 `oss_ops.go` 一致。

## 4. Client 接口与 Service 扩展

**`oss_svc.Client`(`internal/service/oss_svc/types.go`)新增方法**(全部可 mock,须扩 `mock_ossclient`):
```go
CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey string) error
RemoveObjects(ctx, bucket string, keys []string) error
PutObject(ctx, bucket, key string, r io.Reader, size int64, contentType string) error
GetObject(ctx, bucket, key string) (io.ReadCloser, int64, error) // 返回流 + 总大小(供进度分母)
// ListObjects 直接扩展签名支持分页(见 §7),不保留旧签名(P1 内唯一调用方一并改),避免并存两个列举入口:
// ListObjects(ctx, bucket, prefix string, maxKeys int, startAfter string) (objs []ObjectItem, prefixes []string, next string, truncated bool, err error)
```
`client_minio.go` 用 minio-go v7 实现:`CopyObject`(`minio.CopyObject` + `CopySrcOptions`/`CopyDestOptions`)、`RemoveObjects`(`minio.RemoveObjects` 通道,聚合返回的 `RemoveObjectError`)、`PutObject`/`GetObject`(minio 对应方法)。

**Service(`internal/service/oss_svc/service.go` / `ops.go`)新增:**
- `CopyObject` / `MoveObject`(= `CopyObject` 成功后 `RemoveObject(src)`;copy 失败则不删、原样返回错误)。
- `RemoveObjects`(批量;聚合部分失败为一个错误报告)。
- `CreateFolder`(校验并规范化 `prefix` 以 `/` 结尾 → `PutObject(bucket, prefix, empty, 0)`)。
- `ListObjects` 分页(读通道至多 `maxKeys` 项,超出则回 `nextContinuationToken`)。
- 上传 / 下载的**流式**逻辑放 **app 层**(`oss_transfer.go`),service 只暴露 `PutObject`/`GetObject` 原语 —— 与 SFTP 的「app 层 emit、service 层拷贝」分工一致(见 §5)。

所有 service 方法先经 `lookup(assetID)` 解析资产 + `connect`(`connpool.GetOrDialOSS`)拿 `*minio.Client`,SK 经 `credential_resolver.Default().ResolvePasswordGeneric` —— 与 P1 既有路径同构。

## 5. 传输流式架构(逐字复用 SFTP 栈)

`internal/app/oss/oss_transfer.go` 镜像 `internal/app/ssh/sftp.go`:
1. `transferID := transfer.GenerateID("oss")`(`internal/pkg/transfer/transfer.go`)。
2. 原生对话框经 `wailsRuntime.OpenMultipleFilesDialog` / `SaveFileDialog`(在 Go 内,不由前端弹)。
3. 起 goroutine,构造 `reporter := transfer.NewReporter(onProgress)`;`onProgress` 闭包 `wailsRuntime.EventsEmit(ctx, "transfer:progress:"+transferID, p)` —— **事件名与载荷同 SFTP 完全一致**(`transfer.Progress`:`TransferID/Status/CurrentFile/BytesDone/BytesTotal/Speed/Error`,`Status ∈ {progress,done,error,cancelled}`,`progress` 事件 100ms 节流)。
4. 流式:
   - 上传:`os.Open(localPath)` → 包一层计数 reader(每读一段 `reporter.Report`)→ `client.PutObject(bucket, key, reader, size, contentType)`。
   - 下载:`client.GetObject` → `os.Create(localPath)` → `copyWithProgress` 式 32KB 循环(可抽取或仿 `sftp_svc.copyWithProgress`),每段 `reporter.Report`;`ctx.Err()` 支持取消。
5. `OSSCancelTransfer` 经一张 `transferID → context.CancelFunc` 注册表取消(仿 SFTP 的 cancel 机制)。终态(done/error/cancelled)立即 emit。

**收益**:P3b 可原样复用 `sftpStore` 式的 `EventsOn("transfer:progress:"+id)` 订阅 —— 传输进度前端管线是资产无关的。

## 6. 数据类型(DTO,camelCase,Wails 面)

```go
type CopyRequest struct {           // OSSCopyObject / OSSMoveObject 共用
    AssetID   int64  `json:"assetId"`
    SrcBucket string `json:"srcBucket"`
    SrcKey    string `json:"srcKey"`
    DstBucket string `json:"dstBucket"`
    DstKey    string `json:"dstKey"`
}
type RemoveObjectsRequest struct {
    AssetID int64    `json:"assetId"`
    Bucket  string   `json:"bucket"`
    Keys    []string `json:"keys"`
}
type CreateFolderRequest struct {
    AssetID int64  `json:"assetId"`
    Bucket  string `json:"bucket"`
    Prefix  string `json:"prefix"`   // 服务端规范化为以 "/" 结尾
}
// ListObjectsRequest 增:MaxKeys int `json:"maxKeys"`; ContinuationToken string `json:"continuationToken"`
// ListObjectsResult  增:NextContinuationToken string `json:"nextContinuationToken"`; IsTruncated bool `json:"isTruncated"`
```

## 7. 分页语义

minio-go 的 `ListObjects` 通道内部会自动翻页,当前 service 把整层子项**全部**读进切片 —— 前缀下有几十万 key 时会撑爆内存与单次 IPC 载荷。分页 = **在 service 侧对通道读取设上界**:读至多 `maxKeys`(缺省一个合理值,如 200)项即停,若通道仍有下一项则回 `isTruncated=true` + `nextContinuationToken`(用「最后一个 key」作 `StartAfter` 游标续读)。`Prefixes`(子文件夹)与 `Objects`(叶子)分别计入页大小。P3b 据 `isTruncated` 做无限滚动 / 「加载更多」。

## 8. 安全 / 审计 / 错误处理

- **边界校验**只在 app 层(IPC 入口);service↔repository↔connpool 之间 Go-to-Go 互信,不重复判空。
- **机密**:SK 恒经 `credential_resolver` 解析,绝不出现在 DTO / 日志;`SafeView` 不受本期影响。
- **审计 / 日志**:事实核查 —— 既有 OSS/etcd/redis 的直连 Wails 绑定**不写 `audit_logs` 表**(该表仅由 `external_edit_svc` / `ai/audit` / `app/system/settings` 写);故本期**镜像既有直连绑定行为**,对每个变更类操作(copy/move/remove/removeObjects/createFolder/upload)记**结构化日志**(`logger.Ctx` + zap 字段,遵循 `docs/DEVELOP.md` 关键流日志规则),**不**新引入 `audit_logs` 写入(否则 OSS 会成为唯一写审计表的直连绑定,前后不一致且超范围)。若日后要统一直连绑定入审计表,单独立项。
- **错误不吞**:copy 失败绝不接着 delete(move 保证「先成后删」);批量删除聚合部分失败为显式错误,不静默丢弃。
- **无 policy**:OSS `PolicyKind()==""` 不变;变更操作不做审批门控(本期决策)。
- **P1 遗留(非本期缺陷,可顺带修):** `assettype/oss.go` `SafeView()` 漏 `connect_timeout` —— 若计划阶段顺手补齐则加一条断言,否则不动。

## 9. 复用地图(具体锚点)

- 传输栈镜像:`internal/app/ssh/sftp.go`(`SFTPUpload:33` / `SFTPDownload:109` / `SFTPUploadFile:179` / `SFTPCancelTransfer:221`;`wailsRuntime.OpenFileDialog`/`SaveFileDialog`/`OpenMultipleFilesDialog`;`EventsEmit("transfer:progress:"+id)`)。
- 流式循环:`internal/service/sftp_svc/sftp.go`(`copyWithProgress:734`,32KB 循环 + `ctx.Err()` 取消)。
- 进度基元:`internal/pkg/transfer/transfer.go`(`Progress` 结构、`Reporter`(100ms 节流 + Speed)、`GenerateID`、状态常量)。
- OSS 既有:`internal/app/oss/{oss.go,oss_ops.go}`、`internal/service/oss_svc/{types.go,service.go,client_minio.go,ops.go,export_test.go,mock_ossclient/mock.go}`。
- 连接 / 凭证:`internal/connpool/oss.go`(`GetOrDialOSS`/`DialOSS`/`InvalidateOSS`)、`credential_resolver.Default().ResolvePasswordGeneric`。
- 扩展点(OCP):新方法加到 `oss_svc.Client` 接口 + `RegisterXxx` 无关(直接扩接口),**不改任何分发器 switch**;`go generate` 重生成 `mock_ossclient`。

## 10. 测试策略

- **Service 逻辑(mock `mock_ossclient`)**:`MoveObject` 的 copy-then-delete **顺序**与「copy 失败不 delete」;`CreateFolder` 的前缀规范化;`RemoveObjects` 部分失败聚合;`ListObjects` 分页游标(`maxKeys` 截断 + `nextContinuationToken` 续读)。全部 `package oss_svc_test`(P1 已确立的外部测试包 + `export_test.go` 约定,避免 mock 导入环)。
- **集成(本地 MinIO,S3 兼容)**:真实 copy/move/create-folder/分页,以及流式上传下载的字节完整性。
- **取消**:`ctx` 取消打断在途流式传输,断言 emit `cancelled` 终态且本地/远端不落半文件(或明确清理)。
- **观察式验证**(遵循 AGENTS.md「观察而非断言」):`opsctl` 或跑应用后读 `logs/opskat.log` 与 `audit_logs` 确认各变更操作副作用。

## 11. 交付物与后续

**P3a 交付**:上表全部 Wails 绑定 + `oss_svc.Client`/service 扩展 + `oss_transfer.go` 流式栈 + 扩展的 mock + 单测/集成测。产物 `frontend/wailsjs/go/oss/OSS.*` 由 `wails generate` 生成(gitignore)。

**P3b(后续,独立 spec)**:前端对象浏览器工作区 —— query-model tab 接线(`tabStore`/`queryStore`/`MainPanel` 加 `oss` 分支、翻 `canConnect:true`)、仿 `EtcdPanel` 壳、Bucket + 前缀树(复用 `redisKeyTree` `separator="/"` 或服务端 `Prefixes` 懒展开)、新增面包屑原语、对象列表(列表/网格)、详情抽屉、传输 dock(复用本 P3a 的 `transfer:progress:` 事件)、预签名分享弹窗、右键菜单、新建文件夹 / 拖拽上传 / 空态。
