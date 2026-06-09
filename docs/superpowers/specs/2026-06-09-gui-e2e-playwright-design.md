# GUI E2E 测试流程 (Playwright × Wails) 设计

- 日期: 2026-06-09
- 范围: **v1 只做 凑烟 + 资产 CRUD**;Playwright 驱动真实运行的 Wails 应用;本地 `make` 一键跑,不上 PR CI。

## 1. 背景与目标

OpsKat 现有自动化测试只覆盖到 service/repository 层(Go `go test`)、前端组件层(vitest + 全 mock 的 Wails 绑定),以及一个窄的 WASM 边界 e2e(`TestE2E_TCP_Roundtrip`)。**没有任何测试真正把整个应用跑起来、像用户一样从 GUI 走一遍。**

本次新增一条 **真实运行的 GUI 端到端链路**:用 Playwright 打开 `wails dev` 暴露的浏览器桥,操作真实前端 → 经真实 Wails IPC → 真实 Go service/repository → 真实 SQLite,验证两类场景:

1. **凑烟**:应用能启动、主布局渲染、能在 Sidebar 页签间导航。
2. **资产 CRUD**:经 UI 表单创建一个 SSH 资产 → 出现在资产树 → 落库 `opskat.db`(用 opsctl 复核)。

**非目标(v1 不做):**
- 真实 SSH/DB/Redis 协议连接、终端输出断言(需起 fixture 服务,留到 v2)。
- CI 集成。`wails dev` 需原生 webview + 完整工具链,Linux CI 上要 xvfb/webkit 依赖,较重也易 flaky;v1 只提供本地 `make` 目标,**不**改 `ci.yml`。
- Windows/Linux 原生窗口自动化。v1 走 `wails dev` 的浏览器桥,与原生窗口无关。
- `audit_logs` 硬断言。GUI 建资产路径不一定写审计,列为 stretch,实现时确认后再决定是否纳入。

## 2. 总体思路

复用 Wails v2.12.0 自带的 **dev 浏览器桥**,不引入任何重型 GUI 自动化方案:

- `wails dev` 默认在 `http://localhost:34115` 起一个带 IPC websocket 桥的 devserver,**外部浏览器可直接访问**,`window.go`/`window.runtime` 通过 ws 桥连到真实 Go 后端。这就是 Playwright 的入口 —— 不碰原生 webview。
- 首屏**无登录/主密码门**(`frontend/src/App.tsx` 直接渲染主布局),e2e 友好,无需 auth 旁路。
- 落库验证**直查 SQLite**(只读打开 `<临时>/opskat.db`,查 `assets` 表)。这是个**独立 oracle**:不经过刚刚写入它的那条 service 路径,真正断言数据落到了磁盘,比走 opsctl(同一套 service 层)更强。
- 隔离靠**临时数据目录 + 固定测试 master key**:桌面端目前不透传这两个 env,需在 `main.go` 补一个小注入口(`bootstrap.Options` 结构体已有 `DataDir`/`MasterKey` 两字段,opsctl 已在用,不是新模式)。

> 设计原则对齐 AGENTS.md:使能改动走既有的 `bootstrap.Options` 与 opsctl env 约定,不另起一套配置入口。落库验证刻意**绕开** service 层、直查表,是为了得到与写入路径解耦的独立验证(资产创建并发症少、表结构稳定,直查只读不构成耦合风险)。

## 3. 链路总览

```
playwright webServer 启动 `wails dev -devserver localhost:34216`(注入临时 env)
        │  Vite(前端 HMR)  +  Wails devserver:34216(专用端口,IPC websocket 桥)
        ▼
Playwright Chromium → http://localhost:34216 → window.go/runtime 经 ws 桥 → 真实 Go 后端
        ▼
真实 service/repository → 临时 <tmp>/opskat.db
        ▲
node:sqlite 只读直查 assets 表  ← 独立复核资产落库
```

## 4. 架构与改动清单

### 4.1 后端使能(`main.go` 小改,in-scope)

| 改动 | 说明 |
|------|------|
| 透传数据目录 / master key | `main.go` 读 `OPSKAT_DATA_DIR` / `OPSKAT_MASTER_KEY`,非空则填进 `bootstrap.Options{DataDir, MasterKey}`(两字段已存在,见 `internal/bootstrap/bootstrap.go:38-42`)。`bootstrap.Init` 已据 `Options.DataDir` 推导 db dsn / logs / salt / master.key,故只需在 `main.go` 顶部把解析出的 `dataDir` 变量统一用起来。 |
| 收敛 `AppDataDir()` 二次调用 | `main.go:152` 的 `bootstrap.AppDataDir()`(extension asset handler)改用同一个解析后的 `dataDir` 变量,避免 env 覆盖后两处不一致。`main.go:161` 已用 `dataDir` 变量,无需改。 |
| 关单实例锁 | `OPSKAT_E2E=1` 时跳过 `SingleInstanceLock`(`main.go:184`)。该锁 UniqueId 固定 `com.opskat.desktop`,是 OS 级锁、与数据目录无关 —— 不关的话,本机真实 OpsKat 在跑时 e2e 第二实例会被锁死、UI 起不来。 |

> master key 注入语义:e2e 传固定测试 key,`ResolveMasterKey(explicit, ...)` 在 explicit 非空时直接返回(`keychain.go`),既不读也不写 OS Keychain → 不污染钥匙串,完全 hermetic。

### 4.2 前端测试选择器 seam(小改,只加断言用到的)

沿用现有 `data-testid` 约定(已存在于 SnippetPopover / FileManagerPanel 等),给流程触达的元素补稳定选择器。**最小集**:

| 元素 | 用途 |
|------|------|
| 主布局根容器 | 凑烟:确认 app 渲染完成 |
| Sidebar 各页签项 | 凑烟:页签导航 |
| 资产树容器 + 资产节点 | CRUD:断言新资产出现 |
| "新建资产" 入口按钮 | CRUD:打开表单 |
| 资产表单(类型选择 / 名称 / host / port / 提交) | CRUD:填表提交 |

> 实现阶段先跑凑烟 spec(当前代码会因缺 test-id 失败),据失败点逐个补 —— 即 TDD 的"先失败"。具体补哪些组件以实际 DOM 为准,不预先硬编码。

### 4.3 E2E 包(顶层 `e2e/` 独立 pnpm 包)

独立于 frontend 的 vitest,避免 spec 互相串、与前端构建解耦。

| 文件 | 内容 |
|------|------|
| `e2e/package.json` | 独立包,devDep:`@playwright/test`、`@types/node`;script `test` → `playwright test`。落库直查用 Node 内置 `node:sqlite`(Node 26 已稳定,免原生依赖)。 |
| `e2e/playwright.config.ts` | `baseURL: http://localhost:34115`;chromium 单 project;`webServer` 拉起 `wails dev` 并等 34115 就绪(`reuseExistingServer` 本地开发友好);`globalSetup`/`globalTeardown`。 |
| `e2e/global-setup.ts` | `mkdtemp` 临时数据目录;设置 `OPSKAT_DATA_DIR` / `OPSKAT_MASTER_KEY`(固定测试 key)/ `OPSKAT_EXTENSIONS=0` / `OPSKAT_E2E=1`;把 tmpdir 路径写到 env 供 teardown / opsctl helper 读。 |
| `e2e/global-teardown.ts` | 删除临时数据目录。 |
| `e2e/tests/smoke.spec.ts` | 应用加载 → 主布局 test-id 可见 → 逐个 Sidebar 页签导航,断言对应视图渲染。 |
| `e2e/tests/asset-crud.spec.ts` | 打开新建资产表单 → 填唯一名 SSH 资产 → 提交 → 断言资产树出现该节点 → 直查 `assets` 表复核落库。 |
| `e2e/fixtures/db.ts` | 用 Node 内置 `node:sqlite`(`DatabaseSync`,`readOnly: true`)打开 `<tmp>/opskat.db`,`PRAGMA busy_timeout`;暴露 `findAssetByName(name)` 查 `assets` 表(`id`/`name`/`type`/`status`)。 |

> 备选(已否决):放 `frontend/e2e/` —— 复用 workspace 依赖方便,但 vitest 易误收 `.spec.ts`、与前端构建耦合,故独立顶层包。
> 备选(已否决):用 opsctl 复核落库 —— 走的是写入资产时同一套 service 层,不是独立 oracle;改为直查 SQLite(用户明确要求)。

### 4.4 落库验证策略

- **主断言**:UI 资产树出现新资产(Playwright locator,自动等待)。
- **持久化(独立 oracle)**:直查 `<tmp>/opskat.db` 的 `assets` 表 —— `SELECT id, name, type, status FROM assets WHERE name = ?`,断言存在且 `type='ssh'`、`status=1`(活跃)。`node:sqlite` 只读打开,设 `PRAGMA busy_timeout` 兜并发写。
- **`audit_logs`**:stretch。实现时确认 GUI 建资产是否写审计(表 `audit_logs` 已存在);写则加一条直查校验,不写则 v1 不强求。

### 4.5 make 目标 + 文档

| 项 | 内容 |
|----|------|
| `make test-e2e-gui` | 装 `e2e` 依赖 + `playwright install chromium`,跑 `pnpm --dir e2e test`。**不**加进 `ci.yml`。 |
| 文档 | `docs/testing-debugging-guide.md` 加一小节:前置(wails CLI / pnpm / chromium)、如何跑、临时数据目录隔离说明。按 `docs/DOC-MAINTENANCE.md` 规则维护。 |

## 5. 防 flaky

- `webServer` 就绪等待 34115 带超时;Playwright 默认对 locator 自动等待,**一律用 `expect(...).toBeVisible()`,不用 sleep**。
- `OPSKAT_EXTENSIONS=0` 跳过慢的 WASM 初始化;`OPSKAT_E2E=1` 关单实例锁 —— 同时提速与确定性。
- **专用 devserver 端口(34216)**,不复用默认 34115:开发机/兄弟项目(如 agentre)可能已在 34115 跑 dev,`reuseExistingServer` 复用它会静默测错 App。boot 冒烟同时断言 `<title>` 含 `OpsKat`,外部 App 占端口时直接失败而非假绿。
- CRUD 用唯一资产名(带时间戳/随机后缀),避免重复跑残留数据串扰;每次 run 用全新临时数据目录,teardown 清理。

## 6. 验证(本设计自身的验收)

按 AGENTS.md「靠观测验证」:

1. `make test-e2e-gui` 在干净 checkout 上从零跑通(临时目录、Playwright 报告)。
2. 凑烟 spec:故意改坏一个 test-id 应让该 spec 失败(证明它真在断言 GUI,不是假绿)。
3. CRUD spec:直查 `assets` 表的结果与 UI 断言一致;跑完临时目录被清理,真实 `~/Library/Application Support/opskat` 不受影响。

## 7. v1 完成后的演进(非本次)

- v2:起本地 sshd fixture(容器/嵌入)→ UI 开终端 → 执行命令 → 断言输出(真协议端到端)。
- v3:评估 nightly CI(Linux + xvfb)是否值得。
