# CI 去重与 Windows 便携版规格

> 状态：设计已确认，未实现。
> 日期：2026-07-15。
> 范围：`.github/workflows/` 三份重复的 `build-desktop` job 收敛为可复用工作流；新增 windows-amd64 便携版产物，并为便携模式补齐数据目录 / master key / opsctl 三处语义。

## 1. 目标

两件事，后者依赖前者：

1. **CI 去重。** `release.yml` / `nightly.yml` / `manual-build.yml` 各自持有一份约 200 行、几乎逐字相同的 `build-desktop` job（编译 opsctl、`wails build`、Apple 签名公证、dmg、deb、NSIS、上传）。差异仅三处：checkout ref、VERSION/COMMIT_ID 来源、artifact 名前缀。任何打包改动都要写三遍，便携版正是第一个撞上它的需求。
2. **windows-amd64 便携版。** 产出 `opskat-<version>-windows-amd64-portable.zip`：解压即用，数据随目录搬迁，不写宿主机注册表 / 凭据管理器。

## 2. 便携语义的定义

「便携」在本规格中指：**把整个 OpsKat 文件夹拷到 U 盘或另一台机器，数据和凭据继续可用，且宿主机不留痕迹。**

这个定义排除了「仅免安装」——那只是把 exe 压成 zip，数据仍写 `%LOCALAPPDATA%\opskat`，换机器数据不跟着走。

达成该定义需要三处配合，缺一则便携不成立：

| 关注点 | 现状 | 便携模式要求 |
| --- | --- | --- |
| 数据目录 | `bootstrap.AppDataDir()` 在 Windows 硬编码 `%LOCALAPPDATA%\opskat` | exe 同级 `data/` |
| master key | `credential_svc.ResolveMasterKey` **优先** OS 凭据管理器，文件仅回退 | 只落 `<data>/master.key`，不碰凭据管理器 |
| opsctl | `embedded.DefaultInstallDir()` 装到 `%LOCALAPPDATA%\opskat` 并改用户 PATH | 与 opskat.exe 同级，随 zip 分发，不改 PATH |

其中 master key 一项是**必须**而非优化：凭据管理器是机器本地的。若便携目录换机器运行，`keyring.Get` 读不到 → 回落文件 → 文件也不存在 → 自动生成新 key → `opskat.db` 里所有已加密凭据永久解不开。这是静默数据损坏，不是降级。

## 3. 便携模式的触发

**exe 同级存在 `data/` 目录即为便携模式**（VS Code 同款做法）。zip 内预置该目录。

选择理由：单一二进制，复用 `-nsis` 已产出的 `opskat.exe`，**零额外构建耗时**；安装版装在 `Program Files`，那里不会有 `data/`，不存在误触发。

被否决的替代：`-tags portable` 单独构建（windows-amd64 job 耗时翻倍，与「优化 CI」目标相左）；`portable.txt` 标记文件（多一个用户不知能否删除的神秘文件）。

## 4. 代码设计

### 4.1 新增叶子包 `internal/portable`

由导入方向决定，非风格偏好：`bootstrap`、`credential_svc`、`embedded` 三处都需判断便携态，而 `bootstrap` 已导入 `credential_svc`，后者反向导入会成环。叶子包（仅依赖标准库）三方可用。

```go
package portable

// dirFor 从可执行文件路径推导便携数据目录；非便携返回 ""。
// 从 Dir 拆出是为可测——os.Executable() 在测试中指向临时测试二进制，无法构造。
func dirFor(exePath string) string {
	dir := filepath.Join(filepath.Dir(exePath), "data")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// Dir 返回便携数据目录，非便携返回 ""。缓存一次，避免每次调用都 stat。
var Dir = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return dirFor(exe)
})
```

不加 `runtime.GOOS == "windows"` 分支：机制本身跨平台，仅眼下只为 windows-amd64 出 zip。加平台分支属于 AGENTS.md 反对的类型字符串分叉。

### 4.2 三处接入点

**`bootstrap.AppDataDir()`** —— 便携分支置于函数开头：

```go
var portableDir = portable.Dir // 测试可替换

func AppDataDir() string {
	if d := portableDir(); d != "" {
		return d
	}
	switch runtime.GOOS { /* 现有平台默认逻辑不变 */ }
}
```

落点选在 `AppDataDir()` 而非新建解析函数，是因为它就是「平台默认数据目录」的生产者，便携目录是它的一个变体——符合 AGENTS.md「在边界归一化一次」。副作用是 `cmd/opsctl/command/` 下 6 处直接调用者（`ext.go` / `batch.go` / `handler.go` / `approval.go` / `grant.go` / `sshproxy.go`）自动便携化，无需逐个改；`ResolvedDataDir()` 亦自动正确，因其回退即 `AppDataDir()`。

**`credential_svc.ResolveMasterKey`** —— 改为选项对象（AGENTS.md 反对裸 bool 位置参数）：

```go
type MasterKeyOptions struct {
	Explicit string
	DataDir  string
	// NoKeychain 便携模式下为 true：master key 只落 <DataDir>/master.key，
	// 不写 OS 凭据管理器。否则换机器读不到 key 会重新生成，DB 内凭据全部解不开。
	NoKeychain bool
}

func ResolveMasterKey(opts MasterKeyOptions) (string, error)
```

`NoKeychain` 为真时跳过 `keyring.Get` / `keyring.Set`（含现有实现第 45 行读文件后的 best-effort 回写同步），直接走文件。调用方仅 `bootstrap.Init` 一处，传 `portableDir() != ""`（复用 4.2 中同一个可替换变量，使该分支可测）。

**`embedded.DefaultInstallDir()` / `InstallOpsctl()`** —— 便携时 `DefaultInstallDir()` 返回 exe 所在目录；`InstallOpsctl()` 跳过 `addToUserPath`。后者写 HKCU `Environment` 注册表并广播 `WM_SETTINGCHANGE`，是实打实的宿主机污染，且 U 盘盘符会变，写入 PATH 亦无意义。同样用可替换包级变量以便测试。

### 4.3 顺带删除的重复

`migrations.RunMigrations(db)` 改为 `RunMigrations(db, dataDir)`，删除 `migrations/202603260001_ai_providers.go` 中那份复制的 `appDataDir()`（其注释称为避免循环引用而复制）。

这不是顺手重构，而是修一个由便携模式暴露的真实缺陷：该迁移读 `<dataDir>/config.json` 并把旧 OpenAI 配置导入 DB。便携版在他人机器上运行时，会读取**宿主机**的 `%LOCALAPPDATA%\opskat\config.json`，把宿主用户的 API key 导进便携库。改为传参同时修好两件事：便携版不再吸宿主机凭据，且该迁移开始尊重 `--data-dir`。

### 4.4 明确不做

`cmd/opsctl/command/` 那 6 处调用 `AppDataDir()` 而非 `ResolvedDataDir()`，意味着 `opsctl --data-dir /tmp/x ext list` 当前会读错目录。这是**既有缺陷**，与便携版无关（4.2 的落点使便携版不受其影响）。单开 issue，不混入本次改动。

## 5. CI 设计

### 5.1 `.github/platforms.json`

平台表的单一事实源，取代现在散落三处的定义（release / nightly 各一份 YAML matrix，manual 一份 40 行内联 JSON heredoc）：

```json
[
  {"os":"macos-latest","goos":"darwin","goarch":"arm64","platform":"darwin-arm64"},
  {"os":"macos-latest","goos":"darwin","goarch":"amd64","platform":"darwin-amd64"},
  {"os":"ubuntu-22.04","goos":"linux","goarch":"amd64","platform":"linux-amd64",
   "webkit_pkg":"libwebkit2gtk-4.0-dev"},
  {"os":"ubuntu-24.04-arm","goos":"linux","goarch":"arm64","platform":"linux-arm64",
   "webkit_pkg":"libwebkit2gtk-4.1-dev","extra_tags":",webkit2_41"},
  {"os":"windows-latest","goos":"windows","goarch":"amd64","platform":"windows-amd64"},
  {"os":"windows-latest","goos":"windows","goarch":"arm64","platform":"windows-arm64"}
]
```

### 5.2 `.github/workflows/build-desktop.yml`（`workflow_call`）

承接现有约 200 行构建打包逻辑。输入：

| 输入 | 必填 | 用途 |
| --- | --- | --- |
| `version` | 是 | 注入 ldflags；三调用方来源不同 |
| `commit-id` | 是 | 注入 `buildinfo.CommitID` |
| `ref` | 否（默认空） | checkout 目标；空=触发 ref（release），nightly 传 `nightly`，manual 传 sha |
| `artifact-prefix` | 是 | `release` / `nightly` / `manual`，保持现有 artifact 名不变 |
| `platforms` | 否（默认空） | 逗号分隔平台过滤，空=全部 |
| `if-no-files-found` | 否（默认 `warn`） | manual 需 `error` |

Apple 签名密钥经调用方 `secrets: inherit` 传入。

内部先跑 `matrix` job，用 jq 读 `platforms.json` 并按 `platforms` 过滤，输出 `{"include":[...]}` 供 build job 的 `strategy.matrix`（沿用 manual-build 现有惯用法）。空匹配时 `::error::` 退出。这顺带删除 manual-build 中的内联 JSON heredoc 与 6 个 `if [ "$BUILD_X" = "true" ]` 分支；面向用户的 6 个 boolean 勾选框保留，仅转成逗号串传下去。

`NODE_OPTIONS: --max-old-space-size=4096` 从三个调用方上移至 build job 的 job 级 env。

### 5.3 调用方

三者各瘦成十余行，`release` / `checksums` 等下游 job 原样保留。以 release.yml 为例：

```yaml
jobs:
  build-desktop:
    uses: ./.github/workflows/build-desktop.yml
    with:
      version: ${{ github.ref_name }}
      commit-id: ${{ github.sha }}
      artifact-prefix: release
    secrets: inherit
  release:
    needs: [build-desktop]
    # ...原样保留
```

### 5.4 便携版打包步骤

仅此一处，位于 `build-desktop.yml`：

```yaml
- name: Package (Windows portable)
  if: matrix.goos == 'windows' && matrix.goarch == 'amd64'
  shell: pwsh
  run: |
    $stage = "portable/opskat-$env:VERSION-${{ matrix.platform }}"
    New-Item -ItemType Directory -Force -Path "$stage/data" | Out-Null
    Copy-Item build/bin/opskat.exe "$stage/opskat.exe"
    Copy-Item internal/embedded/opsctl_bin "$stage/opsctl.exe"
    Copy-Item build/windows/portable-data-readme.txt "$stage/data/README.txt"
    Compress-Archive -Path $stage -DestinationPath "opskat-$env:VERSION-${{ matrix.platform }}-portable.zip"
```

三点说明：

- `opskat.exe` 是 `-nsis` 已顺带产出但当前被丢弃的产物；`opsctl_bin` 已按 matrix 的 `GOOS`/`GOARCH` 编为 windows-amd64。**便携版零额外构建耗时。**
- 暂存目录置于 `portable/` 下，不会被根目录上传 glob `opskat-${VERSION}-${platform}*` 误捞；生成的 zip 在根目录，被同一 glob 自动捞到——上传步骤与 checksums job 无需改动。
- `Compress-Archive -Path $stage`（不带 `\*`）使 zip 内含顶层文件夹，解压不散落。

### 5.5 zip 结构与 `data/README.txt`

```
opskat-<version>-windows-amd64-portable.zip
└── opskat-<version>-windows-amd64/
    ├── opskat.exe
    ├── opsctl.exe
    └── data/
        └── README.txt
```

`data/README.txt` 同时充当便携标记与说明——用户看得懂它为何存在、删了会怎样，不会误删。源文件置于 `build/windows/portable-data-readme.txt`，内容（英文）：

```
OpsKat - Portable Mode
======================

This folder is what makes OpsKat portable.

While this `data` folder sits next to opskat.exe, OpsKat keeps all of its
state here instead of in %LOCALAPPDATA%\opskat:

  opskat.db     your assets, groups, credentials, snippets and audit log
  master.key    the encryption key for the credentials inside opskat.db
  config.json   application settings
  logs/         application logs

You can move or copy the whole OpsKat folder - to a USB drive, another disk,
or another machine - and it will keep working with the same data. OpsKat in
portable mode does not write to the Windows registry, does not modify your
PATH, and does not store anything in the Windows Credential Manager, so the
machine you run it on is left untouched.

Deleting this folder
--------------------

If you delete this folder, OpsKat stops being portable. On the next start it
falls back to the normal per-user location, %LOCALAPPDATA%\opskat, and starts
from an empty database. Your existing data is NOT migrated there - it is
simply no longer used. Keep this folder together with opskat.exe.

To turn portable mode back on later, create an empty folder named `data` next
to opskat.exe again.

Security
--------

master.key is stored here in plain text, in the same folder as the database it
decrypts. That is precisely what makes this folder portable, and it is a
deliberate trade-off: anyone who can read this folder can read every
credential you have stored in OpsKat.

The installer build behaves differently - it keeps master.key in the Windows
Credential Manager, separate from the database, so copying the database alone
is not enough to decrypt it.

Therefore: do not put this folder on shared or synced storage, and prefer an
encrypted volume (for example BitLocker To Go) if you carry it on a USB drive.

opsctl
------

opsctl.exe next to opskat.exe is the OpsKat command line interface. It is
portable too: run it from this folder and it reads and writes this same data
folder, so the app and the CLI always agree on one database.
```

## 6. 测试与验证

按 AGENTS.md 的 TDD 要求，先写失败测试再动实现。

### 6.1 单元测试（价值降序）

1. **`credential_svc.ResolveMasterKey`** —— 守的是「换机器凭据全部解不开」这一故障，优先级最高。用 `keyring.MockInit()`（go-keyring v0.2.8 自带）：
   - `NoKeychain: true` → 返回 key、`<dataDir>/master.key` 落盘、**凭据管理器仍为空**
   - `NoKeychain: true` 且 master.key 已存在 → 读文件、**不回写凭据管理器**
   - `NoKeychain: false` → 维持现有行为（回归保护）
2. **`portable.dirFor`** —— 同级有 `data/` 目录 → 返回该路径；无 → `""`；`data` 是文件而非目录 → `""`。
3. **`bootstrap.AppDataDir`** —— 经替换 `portableDir` 变量测试便携分支与平台默认分支。
4. **`embedded.DefaultInstallDir`** —— 同上，便携时返回 exe 目录。
5. **`migrations.RunMigrations(db, dataDir)`** —— 断言读取传入 dataDir 下的 config.json，而非平台目录。

### 6.2 本地端到端观察

AGENTS.md 要求以可观察副作用验证。便携机制跨平台，故 macOS 上即可真跑：

```bash
make build-cli && mkdir -p ./build/bin/data
./build/bin/opsctl list
ls ./build/bin/data/      # 期望：opskat.db / master.key / logs/
rm -rf ./build/bin/data && ./build/bin/opsctl list
                          # 期望：回落至 ~/Library/Application Support/opskat
```

这验证的是 4.2 的落点主张——「改 `AppDataDir()` 后 opsctl 全链路自动便携」。

### 6.3 CI 侧验证

本地无法运行 Windows 构建。步骤：

1. `actionlint` 静态检查工作流语法。
2. 推分支后 `workflow_dispatch` 触发 manual-build，**仅勾选 windows-amd64**。
3. 下载 artifact，核对 zip 结构符合 5.5，且 `data/README.txt` 存在。

在第 3 步通过前，不得声称 CI 改动可用。

### 6.4 回归门槛

`go test ./...` 与 `golangci-lint`（本仓约定用 golangci-lint 而非 go vet）。`main_test.go` 现有 `OPSKAT_DATA_DIR` 用例须保持绿。

## 7. 文档同步

`docs/ARCHITECTURE.md` 第 74 行描述 `main.go` 的数据目录解析链，加入便携模式后该句过时，须按 DOC-MAINTENANCE 的反漂移要求同步更新。

面向用户的下载 / 安装说明位于独立的 docs 仓（`/Users/codfrm/Code/opskat/docs`），不在本规格范围内。

## 8. 实施顺序

1. `internal/portable` + 测试
2. `credential_svc` `MasterKeyOptions` + 测试
3. `bootstrap` 接入 + 测试
4. `embedded` `DefaultInstallDir` / `InstallOpsctl` + 测试
5. `migrations.RunMigrations(db, dataDir)` + 测试，删除重复的 `appDataDir()`
6. 本地端到端观察（6.2）
7. `.github/platforms.json` + `build-desktop.yml` + 三个调用方改造 + `build/windows/portable-data-readme.txt`
8. `actionlint`，推分支跑 manual-build 验证（6.3）
9. `docs/ARCHITECTURE.md` 同步

## 9. 已知风险

- **工作区存在未提交改动**（`cmd/opsctl/command/{exec,root,session,ssh}.go`、4 个 `docs/*.md`），非本规格产生。其中 `cmd/opsctl/command/root.go` 与本次改动文件重叠，实施前须与作者确认处理方式，不得卷入本次提交。
- **`sync.OnceValue` 缓存便携判定**：进程生命周期内只 stat 一次。若用户在应用运行中创建 / 删除 `data/`，需重启才生效。这是刻意取舍——避免每次 `AppDataDir()` 调用都触发 `os.Executable()` + `os.Stat`。
- **便携版 master.key 明文与 DB 同目录**，安全性弱于安装版。这是便携的固有代价，已在 `data/README.txt` 中明示。
