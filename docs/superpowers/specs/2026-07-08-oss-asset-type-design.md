# OSS（对象存储）资产类型规格

> 状态：已实现；本文按当前分支 `feature/oss-asset-type` 的已提交代码整理，是 OSS 功能的单一规格。
> 日期：2026-07-10。
> 历史上的 P1 / P2 / P3a / P3b-1 / P3b-2 / P3b-3 拆分规格与实施计划已合并到本文，不再分别维护。

## 1. 目标与产品边界

OpsKat 将 OSS 建模为一种账号级资产：一项资产保存一套访问凭据和一个 Endpoint，连接后列出该账号可见的 Bucket，再按 Bucket / 前缀浏览对象。资产不是单个 Bucket，浏览器内也不提供跨资产账号切换器。

支持的厂商预设为：

- Amazon S3
- 阿里云 OSS
- 腾讯云 COS
- MinIO
- 其他 S3 兼容服务

当前版本提供资产配置、连接测试、Bucket / 对象浏览、对象上传下载、删除、详情查看、图片缩略图和预签名分享。不提供 OSS 专属 AI 控制台；AI 能力继续走全局助手和既有扩展工具。

## 2. 当前能力

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| OSS 资产注册、创建、编辑、详情、连接测试 | 已实现 | 后端与前端均通过资产类型注册表接入 |
| 厂商预设 | 已实现 | 切换已知厂商会预填 Endpoint、Region 和 Path-style；S3 兼容保留用户输入 |
| 内联 Secret Access Key / 托管密码凭据 | 已实现 | Access Key ID 为普通字段；Secret 复用通用密码来源模型 |
| Bucket 列表 | 已实现 | 一个资产展示账号下全部可见 Bucket |
| 前缀树与面包屑导航 | 已实现 | 前缀树按层懒加载，不在客户端一次性构建完整 Bucket 树 |
| 对象列表分页 | 已实现 | 默认每页 200 项，使用 continuation token 继续加载 |
| 列表 / 网格视图 | 已实现 | 网格视图包含图片缩略图和文件类型图标回退 |
| 对象详情 | 已实现 | 展示名称、大小、类型、存储类别、修改时间、ETag 等已返回元数据 |
| 单个 / 批量删除对象 | 已实现 | 破坏性操作经确认框；批量删除错误不会静默吞掉 |
| 上传 | 已实现 | 原生多选文件对话框与桌面文件拖放；单文件对应一个传输任务 |
| 下载 | 已实现 | 单对象、原生保存对话框 |
| 传输进度与取消 | 已实现 | 上传 / 下载共用 `transfer:progress:<id>` 事件协议；完成项 5 秒后自动移除 |
| 预签名 GET / PUT URL | 已实现 | 分享弹窗支持方法与有效期；仅在用户显式生成后展示只读 URL |
| 新建文件夹 | 已实现 | `OSSCreateFolder` 以零字节、`/` 结尾对象实现，浏览器工具栏提供入口 |
| 单对象复制 / 移动 / 重命名 | 已实现 | 详情面板调用 `OSSCopyObject` / `OSSMoveObject`；重命名是同 Bucket 移动 |
| 文件夹递归删除 | 已实现 | 前端递归分页枚举子前缀与对象后批量删除；执行前二次确认 |
| 文件夹递归上传、下载、复制、移动 | 未实现 | 前缀递归及部分失败语义需独立设计 |
| Bucket 创建 / 删除 | 未实现 | 当前产品定位是浏览账号已有 Bucket |
| ACL、Bucket 策略、版本、生命周期 | 未实现 | 厂商差异较大，不在当前范围 |
| OSS 审批策略 | 未实现且当前不启用 | `PolicyKind()==""`、`DefaultPolicy()==nil` |

## 3. 资产配置与凭据

### 3.1 持久化配置

配置序列化在 `Asset.Config`，当前字段与 `asset_entity.OSSConfig` 一致：

```json
{
  "provider": "s3 | aliyun-oss | tencent-cos | minio | s3-compat",
  "endpoint": "host 或 scheme://host[:port]",
  "region": "区域",
  "access_key_id": "非机密访问标识",
  "secret_access_key": "内联模式下的 AES-256-GCM 密文",
  "credential_id": 0,
  "use_path_style": false,
  "use_ssl": true,
  "skip_tls_verify": false,
  "connect_timeout": 0,
  "part_size_mb": 0
}
```

`secret_access_key` 与 `credential_id` 二选一：

- 手动输入时，Secret 经 `credential_svc` 加密后保存，`credential_id` 清零。
- 托管模式引用既有 `password` 凭据，Secret 由 `credential_resolver.ResolvePasswordGeneric` 在使用时解析。
- `SafeView` 只返回非机密连接字段，不返回 Secret 或凭据 ID。

`connect_timeout` 单位为秒，`0` 表示使用默认值；`part_size_mb` 为 `0` 时由 MinIO SDK 自动选择。`skip_tls_verify` 仅用于用户明确配置的私有 S3 兼容 Endpoint。

### 3.2 厂商预填

前端当前预设为：

| 厂商 | Endpoint | Region | Path-style |
| --- | --- | --- | --- |
| Amazon S3 | `s3.us-east-1.amazonaws.com` | `us-east-1` | 关闭 |
| 阿里云 OSS | `oss-cn-hangzhou.aliyuncs.com` | `cn-hangzhou` | 关闭 |
| 腾讯云 COS | `cos.ap-guangzhou.myqcloud.com` | `ap-guangzhou` | 关闭 |
| MinIO | `http://localhost:9000` | `us-east-1` | 开启 |
| S3 兼容 | 不覆盖 | 不覆盖 | 不覆盖 |

已知厂商切换会覆盖这三个预填字段；S3 兼容或未知值只切换 `provider`，保留用户已经输入的连接参数。

## 4. 用户界面与交互

### 4.1 资产配置

OSS 使用注册式 `OSSConfigSection`，包含“连接 / 高级”两个 Tab：

- 连接：厂商、Endpoint、Region、Access Key ID、Secret 的密码来源。
- 高级：Path-style、HTTPS、跳过 TLS 证书校验、连接超时和分片大小；最终持久化字段以 §3.1 为准。
- 详情卡复用通用 `DetailSection` / `DetailGrid` 体系展示非机密配置。

OSS 注册为 `connectAction: "query"`、`canConnect: true`。`canConnectInNewTab` 当前保持 `false`，因为现有“新标签连接”入口只走终端连接路径，尚未按 query 类型分派。

### 4.2 对象浏览器

对象浏览器使用 query-model tab，结构为：

```text
OSSBrowserPanel
├── 左栏：Bucket 列表 + 当前 Bucket 的懒加载前缀树
├── 中栏
│   ├── 面包屑、刷新、上传、列表/网格切换
│   ├── 对象列表或网格
│   ├── 传输进度 dock（存在任务时显示）
│   └── 当前页统计
└── 右栏：选中对象的详情面板
```

左栏和详情栏均可拖动调整宽度。Bucket、当前前缀、分页、选择、焦点对象、视图模式和缩略图 URL 按 tab 隔离存储；关闭 tab 时清理浏览状态、传输订阅和临时数据。

### 4.3 浏览与分页语义

- 选中 Bucket 后，以空前缀列出根层级。
- 展开树节点或进入文件夹时，只请求该前缀的直接子前缀和对象。
- `OSSListObjects` 的 `maxKeys<=0` 使用服务端默认值；前端当前传 200。
- 服务端客户端实现最多取 `maxKeys+1` 项，用额外一项判断是否截断；响应只返回当前页，并用本页最后一项的 key 作为下一页 token。
- 继续加载会追加到当前列表，不覆盖已加载项。
- 刷新会重新加载当前前缀；上传完成且目标前缀仍是当前前缀时自动刷新。

### 4.4 详情、缩略图与分享

- 列表行单击聚焦对象并打开详情栏；关闭详情会清空焦点。
- 图片缩略图使用预签名 GET URL，进入可视区域时懒加载。同一对象的进行中请求去重；失败后不会在同一状态下重复请求。
- 非图片或图片加载失败时展示按内容类型 / 扩展名选择的文件图标。
- 分享弹窗支持 GET / PUT 与固定有效期选项，生成失败明确提示。
- 预签名 URL 只在分享弹窗的只读区域出现；详情面板不直接生成或复制 bearer URL。

### 4.5 删除

- 列表视图支持多选；单选调用 `OSSRemoveObject`，多选调用 `OSSRemoveObjects`。
- 详情栏支持删除当前聚焦对象。
- 所有删除都先展示 `ConfirmDialog`，成功后刷新当前前缀并使用 `notifySuccess`；失败通过 `toast.error` 暴露原始错误。
- 文件夹删除会递归分页枚举前缀下的对象和子前缀，确认后通过批量删除绑定执行。

### 4.6 上传、下载与拖放

- 工具栏上传调用 Go 侧原生多选文件对话框；用户取消返回空任务列表，不视为错误。
- 桌面拖放通过全局文件拖放协调器按坐标命中 OSS 面板，再对每个本地路径调用 `OSSUploadObjectPath`。OSS 不直接占用 Wails 的全局 `OnFileDrop` 单例。
- 下载从对象行或详情栏发起，Go 侧打开原生保存对话框；用户取消返回空任务 ID。
- 当前不限制前端并发；多选上传由后端为每个文件启动独立 goroutine。

## 5. 后端架构与绑定契约

### 5.1 分层与连接

```text
Wails IPC：internal/app/oss
        ↓
Service：internal/service/oss_svc
        ↓
Client 接口 / MinIO 实现
        ↓
连接池：internal/connpool/oss.go
```

- `internal/assettype/oss.go` 在 `init()` 中注册 handler，不修改共享分发器。
- app 层负责 IPC 边界参数校验、原生文件对话框、传输 goroutine、进度事件和关键流日志。
- service 层负责资产 / 凭据解析、对象操作语义和可 mock 的窄 `Client` 接口。
- `connpool.GetOrDialOSS` 按资产复用客户端；资产配置更新后调用 `InvalidateOSS`。
- MinIO SDK 是 S3 兼容传输实现，厂商差异通过 Endpoint、Region、SSL 和 Path-style 参数化。

### 5.2 当前 Wails 方法

读取与连接：

- `OSSTestConnection(assetID)`
- `OSSListBuckets(assetID)`
- `OSSListObjects(ListObjectsRequest)`
- `OSSStatObject(ObjectRequest)`
- `OSSPresignGet(PresignRequest)`
- `OSSPresignPut(PresignRequest)`

对象变更：

- `OSSRemoveObject(ObjectRequest)`
- `OSSRemoveObjects(RemoveObjectsRequest)`
- `OSSCopyObject(CopyRequest)`
- `OSSMoveObject(CopyRequest)`
- `OSSCreateFolder(CreateFolderRequest)`

传输：

- `OSSUploadObject(assetID, bucket, keyPrefix) -> []transferID`
- `OSSUploadObjectPath(assetID, bucket, key, localPath) -> transferID`
- `OSSDownloadObject(assetID, bucket, key) -> transferID`
- `OSSStartTransfer(transferID)`
- `OSSCancelTransfer(transferID)`

`OSSMoveObject` 的语义是先复制、复制成功后再删除源对象；复制失败绝不删除源。`OSSCreateFolder` 将前缀规范化为以 `/` 结尾，并写入零字节 `application/x-directory` 对象。

### 5.3 DTO

主要 DTO 的 JSON 契约为：

```text
BucketItem       { name, creationDate }
ObjectItem       { key, size, lastModified, etag, storageClass, contentType, isPrefix }
ListObjectsReq   { assetId, bucket, prefix, maxKeys, continuationToken }
ListObjectsResult{ prefixes, objects, nextContinuationToken, isTruncated }
ObjectRequest    { assetId, bucket, key }
PresignRequest   { assetId, bucket, key, expirySecs }
CopyRequest      { assetId, srcBucket, srcKey, dstBucket, dstKey }
RemoveObjectsReq { assetId, bucket, keys }
CreateFolderReq  { assetId, bucket, prefix }
```

## 6. 传输协议

每个上传 / 下载任务使用 `transfer.GenerateID("oss")` 生成 ID，并在以下事件上报告进度：

```text
transfer:progress:<transferID>
```

载荷沿用共享 `transfer.Progress`：

```text
{
  transferId,
  status: "progress" | "done" | "error" | "cancelled",
  currentFile,
  filesCompleted,
  filesTotal,
  bytesDone,
  bytesTotal,
  speed,
  error
}
```

普通进度按 100ms 节流，终态立即发送。创建上传/下载任务时后端先返回 pending `transferID`；前端创建进度行并完成 `EventsOn` 后调用 `OSSStartTransfer`，避免快速失败或完成的终态在订阅前丢失。OSS 明确发送 `cancelled`，前端不通过错误字符串推断取消。前端将 wire 状态 `progress` 映射为本地 `active`；完成任务 5 秒后移除，失败和取消任务保留到用户清理。

上传用计数 reader 将读取量报告为进度；下载以 32KB 循环写本地文件并报告进度。取消通过服务端 `transferID -> context.CancelFunc` 注册表传播。当前没有重试、前端并发限流或取消后的半文件清理承诺。

## 7. 安全、错误与可观测性

- Secret Access Key 不进入 SafeView、DTO 或日志。
- IPC 参数只在 app 边界校验；service / client 内部调用不重复做无意义判空。
- 错误必须返回并由 UI 展示，不将失败降级为空列表或默认状态。
- OSS 当前没有审批策略门控。
- OSS 直连绑定沿用同类直连功能的现状：关键操作写结构化日志，但不单独写入 `audit_logs` 表。若要让所有直连绑定统一进入审计表，应作为跨子系统改造处理。
- 复制后删除、批量删除等部分失败必须显式返回；不得吞掉单项错误。

## 8. 代码所有权与主要文件

| 关注点 | 当前所有者 |
| --- | --- |
| 资产配置实体 | `internal/model/entity/asset_entity/oss_config.go` |
| 资产类型 handler | `internal/assettype/oss.go` |
| 客户端连接池 | `internal/connpool/oss.go` |
| OSS service / MinIO client | `internal/service/oss_svc/` |
| Wails 绑定与传输 | `internal/app/oss/` |
| 前端资产注册 | `frontend/src/lib/assetTypes/oss.ts` |
| 配置序列化与厂商预填 | `frontend/src/components/asset/OSSConfigSection.config.ts` |
| 资产表单 / 详情卡 | `frontend/src/components/asset/OSSConfigSection.tsx`、`detail/OSSDetailInfoCard.tsx` |
| 浏览状态 | `frontend/src/stores/ossBrowserStore.ts` |
| 传输状态 | `frontend/src/stores/ossTransferStore.ts` |
| 浏览器容器 | `frontend/src/components/query/OSSBrowserPanel.tsx` |
| OSS 展示组件 | `frontend/src/components/oss/` |
| 懒前缀树 | `frontend/src/lib/ossPrefixTree.ts` |

## 9. 测试与验收

当前自动化覆盖包括：

- Go：配置序列化、asset type handler、连接参数、MinIO client、对象列举分页、复制 / 移动顺序、批量删除错误、新建文件夹、传输取消和 app 边界校验。
- MinIO 集成测试：真实 S3 兼容服务上的客户端操作；环境不可用时按测试约定跳过。
- 前端：配置 parse / build 与厂商预填、资产表单、注册、懒前缀树、浏览 store、传输 store、Bucket 树、面包屑、列表、网格、详情、缩略图、分享弹窗、拖放和浏览器容器。
- i18n：`en` / `zh-CN` 键集合锁步。

提交前至少运行与变更范围相符的测试。完整 OSS 回归建议：

```bash
go test ./internal/model/entity/asset_entity ./internal/assettype ./internal/connpool ./internal/service/oss_svc ./internal/app/oss
cd frontend && pnpm test && npx tsc -b && pnpm lint
```

真实验收使用本地 MinIO 或其他测试账号：创建资产并测试连接，浏览大前缀并翻页，上传 / 拖放 / 下载并取消大文件，删除单个和多个对象，切换列表 / 网格、查看缩略图和详情、生成 GET / PUT 分享 URL。验证时读取 `logs/opskat.log` 观察关键流和错误；不要把 `audit_logs` 中没有 OSS 直连记录误判为失败。

## 10. 后续扩展约束

后续功能应沿现有 seam 扩展：对象操作加入 `oss_svc.Client` 与 service，再暴露窄 IPC；前端状态进入 OSS store，展示组件保持 props-in / callbacks-out。不要在共享分发器中新增 `assetType == "oss"` 分支，也不要复制凭据、文件拖放、传输事件、通知或确认框基础设施。

优先候选范围：

1. 为已存在的 `OSSCreateFolder`、`OSSCopyObject`、`OSSMoveObject` 增加前端入口和完整交互。
2. 设计文件夹递归操作的分页、取消、幂等和部分失败报告。
3. 改造通用 query 资产的“在新标签打开”分派后，再启用 OSS 的 `canConnectInNewTab`。
4. 如确需统一审计，整体设计直连 Wails 操作的审计入口，不为 OSS 单独引入第二套路径。
5. ACL、版本、生命周期、Bucket 管理等厂商差异能力须单独规格化，不应塞入通用对象浏览流程。
