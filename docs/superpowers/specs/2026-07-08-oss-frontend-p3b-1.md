# OSS 对象浏览器 · P3b-1 核心浏览 + 删除 — 设计规格

> 状态:设计定稿(brainstorming 2026-07-08),待写实施计划。
> 范围:对象浏览器工作区**第一子阶段** —— tab 接线 + 面板壳 + Bucket 列表/懒前缀树 + 面包屑 + 分页对象列表(列表视图)+ 刷新 + 单个/多选删除 + 空/加载/错误态。
> 分支:`feature/oss-asset-type`(P1 后端 + P2 表单 + P3a 浏览绑定已在此分支,均未合并)。
> 上游:整体 spec `docs/superpowers/specs/2026-07-08-oss-asset-type-design.md`;后端绑定 spec `docs/superpowers/specs/2026-07-08-oss-backend-p3a.md`。

## 1. 目标与边界

P3(对象浏览器)= **P3a 后端绑定(已实现)→ P3b 前端工作区**。P3b 再拆 **P3b-1 核心浏览+删除 → P3b-2 传输 → P3b-3 详情+分享+网格**,各自 spec→plan→实现。本 spec 只覆盖 **P3b-1**:让用户从资产双击「连接」进入一个作用于该 OSS 资产的对象浏览器,浏览 Bucket 与对象、翻页、删除。

**明确不在 P3b-1(后续子阶段):**
- P3b-2:上传(多选对话框 + 拖拽遮罩)、下载、传输队列 dock、新建文件夹。
- P3b-3:详情抽屉(预览/元数据/操作)、预签名分享弹窗、网格/缩略图视图、重命名/移动/复制、**文件夹(前缀)递归删除**。
- 全程:OSS 专属 AI 控制台(走全局助手)。

## 2. 决策记录(本次 brainstorming 锁定)

1. **Tab 模型 = query-model**(非 k8s page-tab)。`oss.ts` 已 `connectAction:"query"`,`App.tsx` 通用 query 路径已路由。
2. **前缀树 = 懒加载·服务端分级**:展开一个文件夹才 `OSSListObjects(prefix, 非递归)` 取该层直接子前缀 + 对象,分页(用 P3a 的 `isTruncated`/`nextContinuationToken` 游标)。**不**用 `redisKeyTree.buildKeyTree` 的一次性全量客户端建树(会撑爆大 Bucket、抵消 P3a 分页)。
3. **删除 = 仅对象 + 多选批量**(`OSSRemoveObject` / `OSSRemoveObjects`)。文件夹(前缀)递归删除(需前端编排 list-all→batch)延后至 P3b-3。
4. **壳 = 仿 `EtcdPanel`**(共享 `@opskat/ui useResizeHandle`,左树 + 1px 手柄 + 右内容)。
5. **无全屏 loading**:各面板局部 skeleton/spinner(遵循项目约定)。

## 3. Tab 接线(小而确定的改动集)

- `frontend/src/lib/assetTypes/oss.ts`:`canConnect` 翻 `true`、`canConnectInNewTab` 翻 `true`(其余注册不变;`connectAction:"query"` 已就位)。更新 `registry.test.ts` 里 oss 的 `canConnect`/`canConnectInNewTab` 断言(P2 曾断言为 false)。
- `frontend/src/stores/tabStore.ts`:`QueryTabMeta.assetType` 联合追加 `"oss"`(约 `:29`)。
- `frontend/src/stores/queryStore.ts`:`assetType` 联合追加 `"oss"`;`openQueryTab` 对 `oss` 生成 tab(无需解析 redis/db 专属 config —— OSS 浏览态在独立 store,tab meta 只带 `assetId`)。
- `frontend/src/components/layout/MainPanel.tsx`:query 分支 `switch(meta.assetType)` 追加 `case "oss": return <OSSBrowserPanel tabId={...} />`(懒 import,与 etcd/redis 同款)。
- `frontend/src/App.tsx`:**不改** —— `handleConnectAsset` 的通用 `connectAction==="query"` 分支(约 `:287-289`)已路由 oss。
- i18n:`frontend/src/i18n/locales/{en,zh-CN}/common.json` 新增 `oss.browser.*`(en/zh 锁步);`nav.oss` / `asset.typeOSS` 已存在(P2)。

## 4. 面板壳与组件

**新建:**

| 文件 | 单一职责 |
| --- | --- |
| `frontend/src/components/query/OSSBrowserPanel.tsx` | 壳:仿 `EtcdPanel` 的可缩放左右分栏(`useResizeHandle`),订阅 `ossBrowserStore`(按 `tabId`),挂载时 `loadBuckets` |
| `frontend/src/components/oss/OSSBucketTree.tsx` | 左栏:Bucket 列表 + 选中 Bucket 的懒前缀树(展开触发 `expandNode`) |
| `frontend/src/components/oss/OSSObjectList.tsx` | 右栏对象列表(列表视图):行 = 名称/大小/存储类型/修改时间;文件夹双击导航;多选 checkbox;滚动到底 + `isTruncated` → `loadNextPage` |
| `frontend/src/components/oss/OSSBreadcrumb.tsx` | 面包屑 + 操作条(net-new 原语):`bucket / prefix / …` 可点导航;操作条本期只含「刷新」(上传/新建文件夹/网格切换按钮**隐藏**,待 P3b-2/3 落地时再加,不放禁用占位) |
| `frontend/src/components/oss/ossPrefixTree.ts` | 懒前缀树纯模型:节点 populate/expand、`/` 分隔;借 `redisKeyTree` 的 flatten+`expandedSet` **渲染**范式,但子节点按层懒填(非一次性建树) |
| `frontend/src/stores/ossBrowserStore.ts` | 每 tab 浏览态 + 动作(见 §5) |

**修改**(除 §3 接线外):无(注册化 + 独立 store,`AssetForm`/`AssetDetail` 不涉及)。

**复用**:`ConfirmDialog`(删除确认)、`notify.ts` 的 `notifySuccess`(删除成功 top-center)、`toast.error`(失败,右下)、空态/skeleton 既有范式、`@opskat/ui` 原语。

## 5. 状态模型 —— `ossBrowserStore.ts`

Zustand,按 `tabId` 分片(多个 OSS tab 互不干扰)。每 tab:
```
{
  assetId: number,
  buckets: BucketItem[] | null,          // null=未加载
  currentBucket: string,
  currentPrefix: string,                  // 以 "/" 结尾或空(bucket 根)
  tree: Record<string, {                  // key=prefix;懒填
    childPrefixes: string[],
    loaded: boolean,
    cursor: string,                       // nextContinuationToken(该层未读完时)
    truncated: boolean,
  }>,
  expanded: Set<string>,                  // 树展开态(prefix 集合)
  listing: { objects: ObjectItem[], prefixes: string[], cursor: string, truncated: boolean } | null, // 当前 prefix 的右栏列表
  selection: Set<string>,                 // 选中对象 key
  loading: { buckets, listing, page } booleans,
  error: string | null,
}
```
动作:`loadBuckets(assetId)` · `selectBucket(bucket)` · `navigateToPrefix(prefix)` · `expandNode(prefix)`(懒展开树节点)· `loadNextPage()`(右栏续页)· `toggleSelect(key)` / `clearSelection` · `deleteSelected()` / `deleteOne(key)` · `refresh()`。所有列举经 `OSSListObjects({assetId, bucket, prefix, maxKeys, continuationToken})`。

## 6. 数据流

- **挂载** → `loadBuckets` → `OSSListBuckets(assetId)` → 左栏 Bucket 列表(空 → 无 Bucket 空态)。
- **选 Bucket** → `selectBucket` → `navigateToPrefix("")` → `OSSListObjects(bucket,"",maxKeys,"")` → `{Prefixes,Objects}` 填树根 + 右栏首页 + 面包屑=bucket。
- **导航**(树节点点选 / 右栏文件夹双击 / 面包屑点击)→ `navigateToPrefix(prefix)` → `OSSListObjects(bucket,prefix)` → 更新右栏 + 面包屑;树对应节点懒填。
- **翻页**:右栏滚动到底且 `listing.truncated` → `loadNextPage()` → `OSSListObjects(...,continuationToken=listing.cursor)` → **追加** objects(不覆盖)。
- **刷新** → `refresh()` → 重载当前 prefix(清该 prefix 的 tree/listing 缓存后重取)。
- **删除** → 选中 → `deleteSelected`(多选走 `OSSRemoveObjects({assetId,bucket,keys})`;单个走 `OSSRemoveObject`)→ `ConfirmDialog` 确认 → 成功后重载当前 prefix + `clearSelection` + `notifySuccess`;失败 `toast.error`(批量部分失败:后端聚合错误原样提示)。

## 7. 错误 / 空 / 加载

- **错误不吞**:任何绑定错误 → `toast.error(msg)`;store `error` 字段仅供局部展示,不静默吞。
- **空态**(仿 `oss-empty-states`):无 Bucket、空目录 —— 局部占位,不报错。
- **加载**:Bucket 加载中 → 左栏 skeleton;prefix 列举中 → 右栏 skeleton;翻页中 → 列表底部 spinner。**无全屏遮罩**。
- **删除确认**:破坏性,走 `ConfirmDialog`(单个显示 key,多选显示计数),不直接执行。

## 8. 复用地图(具体锚点)

- Tab 接线锚点:`App.tsx handleConnectAsset`(通用 query 路径 `~:287-289`,不改)、`tabStore.ts` `QueryTabMeta.assetType`(`~:29`)、`queryStore.ts` `openQueryTab`、`MainPanel.tsx` query `switch(meta.assetType)`(etcd/redis 分支旁)。
- 壳:`frontend/src/components/query/EtcdPanel.tsx`(结构范式)+ `@opskat/ui useResizeHandle`(`{defaultSize,minSize,maxSize,targetRef} → {size,handleMouseDown}`)。
- 树渲染范式:`frontend/src/lib/redisKeyTree.ts`(`flattenTree`/`expandedSet` 的**渲染**思路;**不**用其 `buildKeyTree` 全量建树)。
- 面包屑分段参考:`frontend/src/components/etcd/EtcdKeyDetail.tsx` 的 `breadcrumbSegments`(无共享组件,net-new)。
- P3a 绑定(消费):`OSSListBuckets(assetId)` / `OSSListObjects(req)`(分页)/ `OSSRemoveObject(req)` / `OSSRemoveObjects(req)`;DTO 见 `internal/service/oss_svc/types.go`(camelCase)。生成物 `frontend/wailsjs/go/oss/OSS.*`(gitignore)。
- Toast:`frontend/src/lib/notify.ts` `notifySuccess`(成功 top-center);错误 `toast.error`(右下)。

## 9. 测试策略

- **纯逻辑(vitest)**:`ossPrefixTree` 节点 populate/expand;面包屑 `bucket/prefix/` → 分段;翻页追加(不覆盖);selection 增删。
- **Store(vitest,mock oss binder via `src/__tests__/setup.ts`)**:`loadBuckets`/`selectBucket`/`navigateToPrefix`/`loadNextPage`(游标续读 + 追加)/`deleteSelected`(单/多、成功/失败)/`refresh` 的状态迁移。
- **组件(@testing-library/react)**:`OSSBreadcrumb` 渲染 + 点击导航;`OSSObjectList` 行渲染 + 文件夹双击导航 + 多选;空态;`registry.test.ts` 断言 `canConnect:true`/`canConnectInNewTab:true`;`MainPanel` 渲染出 oss 分支。
- **i18n 锁步**:en/zh 同键脚本校验(沿用 P2 校验方式)。
- **观察式验证**(AGENTS.md「观察而非断言」):跑应用 → 双击 OSS 资产连接 → 用本地 MinIO 浏览 Bucket/前缀、翻页、删除,读 `logs/opskat.log` 与 `audit_logs`(结构化日志)确认副作用。

## 10. 交付物与后续

**P3b-1 交付**:可用的「浏览 + 删除」对象浏览器(tab 接线、`OSSBrowserPanel` 壳、Bucket 列表 + 懒前缀树、面包屑、分页对象列表、刷新、单/多选删除 + 确认、空/加载/错误态)、配套纯逻辑/store/组件测试与 en/zh i18n。

**后续(独立 spec)**:P3b-2 传输(上传多选对话框 + 拖拽遮罩、下载、传输 dock、新建文件夹 —— 消费 P3a 的 `OSSUpload*`/`OSSDownloadObject`/`OSSCancelTransfer` + `transfer:progress:<id>` 事件;**注意** OSS 发显式 `cancelled` 状态,前端按显式处理,非 SFTP 的从 error 子串推断);P3b-3 详情抽屉 + 预签名分享 + 网格/缩略图(预签名 GET URL)+ 重命名/移动/复制 + 文件夹递归删除。
