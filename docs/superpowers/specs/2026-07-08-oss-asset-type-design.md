# OSS (对象存储) 资产类型 — 设计规格

> 状态:设计定稿(UI/UX 已在 `~/Desktop/opskat.pen` 完成 15 帧),待实现。
> 日期:2026-07-08 · 范围:新增「对象存储 OSS」资产类型,支持 Amazon S3 / 阿里云 OSS / 腾讯云 COS / MinIO / S3 兼容。

## 1. 目标与定位

为 OpsKat 增加一个 **OSS(对象存储)资产类型**,让用户像管理 SSH/数据库一样管理对象存储:配置账号连接 → 浏览 Bucket 与对象 → 上传/下载/删除/重命名 → 生成预签名分享链接。第一版聚焦**可视化浏览器**,不做 OSS 专属 AI 界面(AI 走现有全局助手 / ext_exec)。

## 2. 产品决策(已与用户确认)

- **资产模型**:一个 OSS 资产 = 一套凭证 + 一个 Endpoint(账号级),**不是**单个 Bucket。连接后先列出该账号下所有 Bucket,再钻取到对象。浏览器 UI **锁定在这一个资产**上——工具栏不设账号/Endpoint 切换器(资产名只出现在 Tab 上)。
- **厂商呈现**:连接 Tab 内一个「厂商」下拉(S3 / 阿里云 OSS / 腾讯云 COS / MinIO / S3 兼容),选中后**智能预填** Endpoint 模板、Region、Path-style 默认值。
- **Path-style**:S3 / OSS / COS 默认关闭(virtual-hosted),**MinIO 需开启**。
- **AI**:本版仅可视化浏览器,无 OSS 专属 AI 控制台。
- **凭证**:复用现有凭证体系,**不新增 AK/SK 专用类型**。`Access Key ID` 是非机密标识 → 表单明文字段;`Secret Access Key` 是机密 → 走 `PasswordSourceField`(密码来源:手动输入 / 托管密码),托管时选择一个 `password` 类型的密码凭证。
- **移除**:不设「默认 Bucket」配置(与账号级模型冲突)。

## 3. 信息架构 / 导航

```
OSS 资产 (Tab)
└─ 对象浏览器
   ├─ 左:Bucket 列表 + 选中 Bucket 的前缀树(懒加载,key 按 "/" 切成"文件夹")
   └─ 右:面包屑(bucket / prefix / …) + 工具栏 + 对象列表(列表/网格) + 详情抽屉
```

凭证在独立的 **「密钥管理」整页**(左侧栏 `KeyRound` 进入,Cmd+Shift+K)中集中管理——沿用现状,**非**新建"凭证库"。

## 4. UX / 屏幕清单(opskat.pen,15 帧)

统一暗色 app-shell(appBar h38 / tabs h36 / toolbar h44 / split / status;调色板 `#0F1115` `#161A22` `#1F232C`,强调 `#2563EB`/`#60A5FA`),对齐既有 etcd 帧。

**表单 / 配置**
1. `oss-asset-form` — 连接弹窗(≈600px)。资产类型下拉(对象存储)→ [图标 | 名称 | 分组] 行 → 下划线 Tab(连接 / 高级)→「添加备注」→ 页脚 测试连接 / 取消 / 保存。连接 Tab:厂商、Endpoint、Region、Access Key ID、密码来源 + Secret Access Key。
2. `oss-form-advanced-tab` — 高级 Tab:Path-style、使用 HTTPS、跳过 SSL 校验、连接超时、分片大小。
3. `oss-form-managed-cred` — Secret Key「托管密码」态 + 选择密码凭据下拉展开。

**对象浏览器**
4. `oss-object-browser` — 核心。左 Bucket+前缀树 / 右 面包屑+工具栏+对象列表(名称·大小·存储类型·修改时间;标准/低频/归档 彩色标签)+ 状态栏 + 列表/网格切换。
5. `oss-object-detail` — 浏览器 + 右侧详情抽屉(图片预览、元数据、ACL、下载/复制URL/生成预签名/删除)。
6. `oss-transfers` — 浏览器 + 底部传输队列 dock(分片上传/下载进度、完成/失败/取消)。
7. `oss-presigned-dialog` — 预签名 URL 分享弹窗(方法 / 有效期 / URL / 复制)。
8. `oss-object-context-menu` — 对象右键菜单(预览/下载/复制URL/生成预签名/重命名/移动/复制/属性/删除)。

**密钥管理 / 凭证**
9. `credential-manager` — 「密钥管理」整页:导入 / 生成 / 新建密码 + 凭证卡片列表(密码凭证 + SSH 密钥)。
10. `credential-create-password` — 新建密码凭证弹窗(名称 / 用户名=AK / 密码 / 备注)。

**边界态**
11. `oss-empty-states` — 无 Bucket / 空目录 / 无托管凭证。
12. `oss-delete-confirms` — 删除对象 + 删除凭证(含"正在被 X 资产使用"告警)。
13. `oss-new-folder-dialog` · 14. `oss-drop-overlay`(拖拽上传遮罩)· 15. `oss-grid-view`(缩略图网格)。

## 5. 配置字段与数据

**连接配置(资产 Config)**:`provider`(s3/aliyun-oss/tencent-cos/minio/s3-compat)、`endpoint`、`region`、`accessKeyId`、`secretAccessKey`(inline 加密 或 `credential_id` 托管)、`usePathStyle`(bool)、`useSSL`(bool)、`connectTimeoutSec`、`partSizeMB`。厂商预填模板:S3 `s3.<region>.amazonaws.com`、OSS `oss-<region>.aliyuncs.com`、COS `cos.<region>.myqcloud.com`、MinIO `http://<host>:9000`(path-style 开)。

**对象列表项**:key/name、size、storageClass、lastModified、etag、contentType;前缀(folder)按 `/` 聚合。

## 6. 实现复用地图

**后端**(Go,`internal/`):
- 新增 `assettype` handler + `*_policy.go`,`init()` 内 `Register()`(照 `redis.go`/`k8s.go`),**不改分发器 switch**。
- Service/Repository 分层;凭证解析走现有 `credential_resolver`(`credential_id` → 密码凭证解密取 SK)。
- 新增 OSS 客户端封装(S3 兼容 SDK,path-style/region 参数化);Wails 绑定:列 Bucket / 列对象(前缀分页)/ 上传 / 下载 / 删除 / 重命名(copy+delete)/ 生成预签名 URL / 分片上传。
- 审计、连接池、加密走既有 canonical 入口。

**前端**(`frontend/src`):
- 注册 `lib/assetTypes/oss.ts` + `import "./oss"`(`index.ts`),加入 `__tests__/registry.test.ts` 有序断言。
- 表单:`OSSConfigSection.tsx`(`forwardRef<AssetFormHandle>`,`useConfigSection` + `ConfigGroupSchema`(连接/高级)+ `ConfigTabs`)+ `OSSConfigSection.config.ts`(parse/build/DEFAULTS + golden test)。Secret Key 复用 `PasswordSourceField`;厂商切换预填在 config 层实现。
- 详情卡:`detail/OSSDetailInfoCard.tsx`(复用 `DetailSection`/`DetailGrid`/`InfoItem`)。
- 工作区:仿 `EtcdPanel`(可缩放 tree+content,`useResizeHandle`),对象列表/操作/传输 lift 自 SFTP `file-manager/`(`FileList` 行模型、`FloatingMenu` 右键、`TransferSection` 进度、`NameDialog`/`PropertiesDialog`);前缀树复用 `redisKeyTree` 分隔符逻辑。
- **面包屑为新增原语**(仓库暂无 Breadcrumb)。
- Tab 接线:扩展 `stores/tabStore.ts` `QueryTabMeta.assetType` 联合 + `queryStore.ts`,`MainPanel.tsx` 加分支,`App.tsx` `handleConnectAsset` 分派;或按 k8s 走 `page` tab。
- i18n:en + zh-CN 同步加键。
- 凭证:沿用「密钥管理」(`CredentialManager.tsx`);托管选择走 `ListCredentialsByType("password")`。

## 7. 不在本版范围 / 待定

- **专用 AK/SK 凭证类型**:已决定沿用密码凭证;若后续要一条凭证同存 AK+SK,需改 `credential_entity.Credential` 模型 + 密钥管理 UI + 托管选择过滤,单独立项。
- OSS 专属 AI 控制台。
- 跨 Bucket / 跨资产的对象复制、版本管理、生命周期规则、Bucket 策略编辑等高级能力(后续迭代)。

## 8. 验证策略

- 后端:`go test` 覆盖 config parse/build(golden)、policy、预签名 URL 生成、前缀分页;用 MinIO(本地 S3 兼容)做集成回归。
- 前端:`vitest` 覆盖 `oss.config.ts` golden、registry 断言、面包屑/前缀树纯函数。
- 端到端:`opsctl` 或运行应用后读结构化日志(`logs/opskat.log`)与 `audit_logs` 观察副作用(遵循 AGENTS.md「观察而非断言」)。
