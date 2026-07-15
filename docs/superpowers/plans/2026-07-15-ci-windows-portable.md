# CI 去重与 Windows 便携版 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把三份重复的 `build-desktop` job 收敛为一个可复用工作流，并产出一个数据随目录搬迁、不污染宿主机的 windows-amd64 便携版 zip。

**Architecture:** 新增叶子包 `internal/pkg/portable`，以「exe 同级存在 `data/` 目录」判定便携模式；`bootstrap.AppDataDir()` / `credential_svc.ResolveMasterKey` / `embedded.DefaultInstallDir` 三处各自接入它。CI 侧把平台矩阵收进 `.github/platforms.json`，构建打包逻辑收进 `.github/workflows/build-desktop.yml`（`workflow_call`），release / nightly / manual 三者降为薄调用方。

**Tech Stack:** Go 1.26（`sync.OnceValue` 可用）、gormigrate v2、go-keyring v0.2.8（含 `MockInit()`）、Wails v2.12.0、GitHub Actions（`workflow_call`）、PowerShell `Compress-Archive`、jq。

**规格：** `docs/superpowers/specs/2026-07-15-ci-windows-portable-design.md`
**分支：** `feature/ci-windows-portable`（已创建，spec 已提交于 `9a310f1b`）

## Global Constraints

- **便携模式判定标准**：可执行文件同级存在名为 `data` 的**目录**。是文件不算，不存在不算。
- **便携数据目录名固定为 `data`**，不可配置。
- **不得引入 `runtime.GOOS == "windows"` 分支来做便携判定**——机制跨平台，加平台分叉违反 AGENTS.md 对类型字符串分叉的约束。
- **便携模式下 master key 只落 `<dataDir>/master.key`**，绝不调用 `keyring.Get` / `keyring.Set`。这是必须项：凭据管理器是机器本地的，换机器后重新生成 key 会导致库内已加密凭据永久解不开。
- **便携模式下不得写 HKCU `Environment` 注册表**（即不得调用 `addToUserPath`）。
- **便携 zip 仅针对 windows-amd64**，windows-arm64 维持仅 installer。
- **artifact 名保持现状不变**：`release-opskat-<platform>` / `nightly-opskat-<platform>` / `manual-opskat-<platform>`。
- **提交信息**：gitmoji + 中文 subject。**不带 issue / PR 编号**（本次未刻意关联 issue）。
- **验证用 `golangci-lint` 而非 `go vet`**。
- 每个 Go 任务结束前 `go build ./...` 与 `go test ./...` 必须绿。

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `internal/pkg/portable/portable.go`（新建） | 便携模式判定的唯一来源。仅依赖标准库，故 `bootstrap` / `credential_svc` / `embedded` 均可导入而不成环。 |
| `internal/pkg/portable/portable_test.go`（新建） | `dirFor` 的单元测试。 |
| `internal/service/credential_svc/keychain.go`（改） | `ResolveMasterKey` 改选项对象，新增 `NoKeychain`。 |
| `internal/service/credential_svc/keychain_test.go`（新建） | master key 三个分支的测试，含「凭据管理器仍为空」断言。 |
| `internal/bootstrap/bootstrap.go`（改） | `AppDataDir()` 接入便携分支；`Init` 传 `NoKeychain`；`RunMigrations` 传 dataDir。 |
| `internal/bootstrap/appdatadir_test.go`（新建） | `AppDataDir` 便携 / 非便携分支测试。 |
| `internal/embedded/embed.go`（改） | `DefaultInstallDir` 便携时返回 exe 目录；`InstallOpsctl` 便携时跳过 `addToUserPath`。 |
| `internal/embedded/embed_test.go`（新建） | `DefaultInstallDir` 分支测试。 |
| `migrations/migrations.go`（改） | `RunMigrations(db, dataDir)`。 |
| `migrations/202603260001_ai_providers.go`（改） | 删除重复的 `appDataDir()`，改用传入的 dataDir。 |
| `migrations/ai_providers_migration_test.go`（新建） | 断言读取传入 dataDir 而非平台目录。 |
| `build/windows/portable-data-readme.txt`（新建） | 便携 zip 内 `data/README.txt` 的源文件。 |
| `.github/platforms.json`（新建） | 构建矩阵的单一事实源。 |
| `.github/workflows/build-desktop.yml`（新建） | `workflow_call`，承接全部构建打包逻辑，含便携 zip。 |
| `.github/workflows/release.yml`（改） | 降为薄调用方。 |
| `.github/workflows/nightly.yml`（改） | 降为薄调用方。 |
| `.github/workflows/manual-build.yml`（改） | 降为薄调用方，删除内联平台 JSON。 |
| `docs/ARCHITECTURE.md`（改） | 同步便携模式到数据目录解析链的描述。 |

**任务顺序原则：** Task 1 无依赖；Task 2/4/5 各自独立且只依赖 Task 1；Task 3 依赖 Task 1、2、5。CI 部分（Task 6/7）不依赖 Go 部分，但便携 zip 只有在 Task 1–3 落地后才真正「便携」，故排在后面。

---

### Task 1: 便携模式判定叶子包

**Files:**
- Create: `internal/pkg/portable/portable.go`
- Test: `internal/pkg/portable/portable_test.go`

**Interfaces:**
- Consumes: 无（叶子包，仅标准库）
- Produces:
  - `portable.Dir() string` —— 便携数据目录绝对路径；非便携返回 `""`。结果进程内缓存一次。
  - 包内 `dirFor(exePath string) string` —— 不导出，仅供本包测试。

- [ ] **Step 1: 写失败测试**

创建 `internal/pkg/portable/portable_test.go`：

```go
package portable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirFor(t *testing.T) {
	t.Run("同级存在 data 目录时返回该目录", func(t *testing.T) {
		base := t.TempDir()
		dataDir := filepath.Join(base, "data")
		if err := os.Mkdir(dataDir, 0o755); err != nil {
			t.Fatalf("准备 data 目录失败: %v", err)
		}

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != dataDir {
			t.Errorf("dirFor() = %q, 期望 %q", got, dataDir)
		}
	})

	t.Run("同级无 data 目录时返回空", func(t *testing.T) {
		base := t.TempDir()

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != "" {
			t.Errorf("dirFor() = %q, 期望空字符串", got)
		}
	})

	t.Run("同级 data 是文件而非目录时返回空", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "data"), []byte("x"), 0o600); err != nil {
			t.Fatalf("准备 data 文件失败: %v", err)
		}

		got := dirFor(filepath.Join(base, "opskat.exe"))

		if got != "" {
			t.Errorf("dirFor() = %q, 期望空字符串（data 是文件不应触发便携模式）", got)
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/pkg/portable/ -run TestDirFor -v`
Expected: FAIL，编译错误 `undefined: dirFor`（包尚不存在时报 `no Go files` 亦可，先建空包再跑）

- [ ] **Step 3: 写最小实现**

创建 `internal/pkg/portable/portable.go`：

```go
// Package portable 判定应用是否运行在便携模式。
//
// 便携模式的约定：可执行文件同级存在 data 目录，则应用的全部状态
// （数据库、master key、配置、日志）都放在该目录内，使整个应用目录
// 可以整体搬迁到 U 盘或另一台机器。
//
// 本包只依赖标准库，因此 bootstrap / credential_svc / embedded 都能
// 导入它而不产生循环引用——credential_svc 无法反向导入 bootstrap，
// 这正是本包独立存在的原因。
package portable

import (
	"os"
	"path/filepath"
	"sync"
)

// dirName 便携数据目录的固定名称，不可配置。
const dirName = "data"

// dirFor 从可执行文件路径推导便携数据目录；非便携返回 ""。
// 从 Dir 拆出是为了可测：os.Executable() 在测试中指向临时测试二进制，无法构造。
func dirFor(exePath string) string {
	dir := filepath.Join(filepath.Dir(exePath), dirName)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// Dir 返回便携数据目录，非便携模式返回 ""。
//
// 结果在进程生命周期内只解析一次：便携与否是安装形态的属性，不会在
// 运行中改变，而 AppDataDir() 调用频繁，每次都 os.Executable() + os.Stat
// 并无意义。代价是运行中新建/删除 data 目录需重启才生效。
var Dir = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// 解析符号链接，使经 symlink 调用的 opsctl 也能定位到真实安装目录。
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return dirFor(exe)
})
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/pkg/portable/ -v`
Expected: PASS，三个子测试全绿

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/pkg/portable/`
Expected: `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/pkg/portable/
git commit -m "$(cat <<'MSG'
✨ 新增便携模式判定叶子包

以「可执行文件同级存在 data 目录」判定便携模式。放在 internal/pkg 下
且只依赖标准库，是为了让 credential_svc 也能导入——它无法反向导入
bootstrap（bootstrap 已导入它，会成环）。
MSG
)"
```

---

### Task 2: master key 便携模式不落凭据管理器

**Files:**
- Modify: `internal/service/credential_svc/keychain.go:22-66`
- Test: `internal/service/credential_svc/keychain_test.go`（新建）
- Modify: `internal/bootstrap/bootstrap.go:88`（调用方，唯一一处）

**Interfaces:**
- Consumes: `portable.Dir() string`（Task 1）—— 本任务中**仅由调用方 `bootstrap.Init` 使用**，`credential_svc` 自身不导入 portable，保持其可测性与无关性。
- Produces:
  - `credential_svc.MasterKeyOptions{Explicit, DataDir string; NoKeychain bool}`
  - `credential_svc.ResolveMasterKey(opts MasterKeyOptions) (string, error)` —— 替换原 `ResolveMasterKey(explicit, dataDir string)`。

> **为什么是最高优先级：** 这个分支守的是「便携目录换机器后，`keyring.Get` 读不到 key → 回落文件 → 文件不存在 → 自动生成新 key → 库内所有已加密凭据永久解不开」的静默数据损坏。

- [ ] **Step 1: 写失败测试**

创建 `internal/service/credential_svc/keychain_test.go`：

```go
package credential_svc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestResolveMasterKey(t *testing.T) {
	t.Run("显式传入时直接返回，不碰文件与凭据管理器", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{Explicit: "explicit-key", DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "explicit-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q", got, "explicit-key")
		}
		if _, err := os.Stat(filepath.Join(dataDir, masterKeyFile)); !os.IsNotExist(err) {
			t.Error("显式传入 key 时不应写 master.key 文件")
		}
	})

	t.Run("便携模式生成 key 时只落文件，凭据管理器保持为空", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir, NoKeychain: true})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got == "" {
			t.Fatal("ResolveMasterKey() 返回空 key")
		}

		data, err := os.ReadFile(filepath.Join(dataDir, masterKeyFile))
		if err != nil {
			t.Fatalf("便携模式应把 key 写入 master.key: %v", err)
		}
		if string(data) != got {
			t.Errorf("master.key 内容 = %q, 期望与返回值 %q 一致", string(data), got)
		}

		// 核心断言：便携模式绝不能把 key 写进凭据管理器，
		// 否则数据目录换机器后读不到 key，库内凭据全部解不开。
		if _, err := keyring.Get(keychainService, keychainAccount); err == nil {
			t.Error("便携模式不应向凭据管理器写入 master key")
		}
	})

	t.Run("便携模式读到已有文件时不回写凭据管理器", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dataDir, masterKeyFile), []byte("existing-key"), 0o600); err != nil {
			t.Fatalf("准备 master.key 失败: %v", err)
		}

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir, NoKeychain: true})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "existing-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q", got, "existing-key")
		}
		if _, err := keyring.Get(keychainService, keychainAccount); err == nil {
			t.Error("便携模式读文件后不应同步到凭据管理器")
		}
	})

	t.Run("非便携模式生成 key 时写入凭据管理器（回归保护）", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}

		stored, err := keyring.Get(keychainService, keychainAccount)
		if err != nil {
			t.Fatalf("非便携模式应把 key 写入凭据管理器: %v", err)
		}
		if stored != got {
			t.Errorf("凭据管理器中 = %q, 期望与返回值 %q 一致", stored, got)
		}
	})

	t.Run("非便携模式优先读凭据管理器（回归保护）", func(t *testing.T) {
		keyring.MockInit()
		dataDir := t.TempDir()
		if err := keyring.Set(keychainService, keychainAccount, "keychain-key"); err != nil {
			t.Fatalf("准备凭据管理器失败: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, masterKeyFile), []byte("file-key"), 0o600); err != nil {
			t.Fatalf("准备 master.key 失败: %v", err)
		}

		got, err := ResolveMasterKey(MasterKeyOptions{DataDir: dataDir})

		if err != nil {
			t.Fatalf("ResolveMasterKey() 出错: %v", err)
		}
		if got != "keychain-key" {
			t.Errorf("ResolveMasterKey() = %q, 期望 %q（凭据管理器优先于文件）", got, "keychain-key")
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/credential_svc/ -run TestResolveMasterKey -v`
Expected: FAIL，编译错误 `undefined: MasterKeyOptions`

- [ ] **Step 3: 改实现**

把 `internal/service/credential_svc/keychain.go` 中 `ResolveMasterKey` 整个函数（第 22–66 行，含其上方注释块）替换为：

```go
// MasterKeyOptions 是 ResolveMasterKey 的入参。
type MasterKeyOptions struct {
	// Explicit 来自 CLI --master-key 或 OPSKAT_MASTER_KEY，非空则直接采用。
	Explicit string
	// DataDir master key 文件所在目录。
	DataDir string
	// NoKeychain 便携模式下为 true：master key 只落 <DataDir>/master.key，
	// 不读也不写 OS 凭据管理器。凭据管理器是机器本地的，便携目录换到另一台
	// 机器后会读不到 key 而重新生成，导致库内已加密凭据永久解不开。
	NoKeychain bool
}

// ResolveMasterKey 按优先级获取 master key:
//  1. opts.Explicit（CLI --master-key / 环境变量）
//  2. OS Keychain（opts.NoKeychain 为 true 时跳过）
//  3. 文件回退 (<DataDir>/master.key)
//
// 如果所有来源都没有，自动生成并存储。
func ResolveMasterKey(opts MasterKeyOptions) (string, error) {
	if opts.Explicit != "" {
		return opts.Explicit, nil
	}

	// 尝试从 Keychain 读取
	if !opts.NoKeychain {
		key, err := keyring.Get(keychainService, keychainAccount)
		if err == nil && key != "" {
			return key, nil
		}
	}

	// 尝试从文件读取
	filePath := filepath.Join(opts.DataDir, masterKeyFile)
	data, err := os.ReadFile(filePath) //nolint:gosec // path from app data directory
	if err == nil && len(data) > 0 {
		key := string(data)
		// 尝试同步到 Keychain（best-effort）
		if !opts.NoKeychain {
			if err := keyring.Set(keychainService, keychainAccount, key); err != nil {
				logger.Default().Warn("sync master key to keychain", zap.Error(err))
			}
		}
		return key, nil
	}

	// 自动生成新的 master key
	key, err := generateMasterKey()
	if err != nil {
		return "", fmt.Errorf("生成 master key 失败: %w", err)
	}

	// 便携模式直接落文件；否则优先 Keychain，不可用时回退文件
	if opts.NoKeychain {
		if err := os.WriteFile(filePath, []byte(key), 0600); err != nil {
			return "", fmt.Errorf("存储 master key 失败: %w", err)
		}
		return key, nil
	}
	if err := keyring.Set(keychainService, keychainAccount, key); err != nil {
		// Keychain 不可用，回退到文件存储
		if writeErr := os.WriteFile(filePath, []byte(key), 0600); writeErr != nil {
			return "", fmt.Errorf("存储 master key 失败（Keychain: %v, 文件: %w）", err, writeErr)
		}
	}

	return key, nil
}
```

- [ ] **Step 4: 更新唯一调用方**

`internal/bootstrap/bootstrap.go` 第 88 行，把：

```go
	masterKey, err := credential_svc.ResolveMasterKey(opts.MasterKey, dataDir)
```

改为（本步先不接便携，Task 3 再补 `NoKeychain`；此处只做签名迁移，保持行为不变）：

```go
	masterKey, err := credential_svc.ResolveMasterKey(credential_svc.MasterKeyOptions{
		Explicit: opts.MasterKey,
		DataDir:  dataDir,
	})
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go build ./... && go test ./internal/service/credential_svc/ -run TestResolveMasterKey -v`
Expected: 编译通过；五个子测试全 PASS

- [ ] **Step 6: 全量回归 + Lint**

Run: `go test ./... && golangci-lint run ./internal/service/credential_svc/ ./internal/bootstrap/`
Expected: 全绿；`0 issues.`

- [ ] **Step 7: Commit**

```bash
git add internal/service/credential_svc/keychain.go internal/service/credential_svc/keychain_test.go internal/bootstrap/bootstrap.go
git commit -m "$(cat <<'MSG'
♻️ ResolveMasterKey 改选项对象并支持跳过凭据管理器

新增 NoKeychain 供便携模式使用：master key 只落 <dataDir>/master.key。
凭据管理器是机器本地的，便携目录换机器后读不到 key 会重新生成，
导致库内已加密凭据永久解不开——这是静默数据损坏，不是降级。

本次仅迁移签名，调用方行为不变；便携模式接入见后续提交。
MSG
)"
```

---

### Task 3: `AppDataDir` 接入便携模式

**Files:**
- Modify: `internal/bootstrap/bootstrap.go:56-73`（`AppDataDir`）、`:88`（`Init` 传 `NoKeychain`）
- Test: `internal/bootstrap/appdatadir_test.go`（新建）

**Interfaces:**
- Consumes: `portable.Dir()`（Task 1）、`credential_svc.MasterKeyOptions`（Task 2）
- Produces: `bootstrap.AppDataDir()` 行为变更——便携时返回便携目录。签名不变，故 `cmd/opsctl/command/` 下 6 处直接调用者（`ext.go` / `batch.go` / `handler.go` / `approval.go` / `grant.go` / `sshproxy.go`）与 `ResolvedDataDir()` 自动便携化，无需改动。

- [ ] **Step 1: 写失败测试**

创建 `internal/bootstrap/appdatadir_test.go`：

```go
package bootstrap

import (
	"runtime"
	"strings"
	"testing"
)

func TestAppDataDir(t *testing.T) {
	t.Run("便携模式返回便携目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "/tmp/opskat-portable/data" }

		if got := AppDataDir(); got != "/tmp/opskat-portable/data" {
			t.Errorf("AppDataDir() = %q, 期望 %q", got, "/tmp/opskat-portable/data")
		}
	})

	t.Run("非便携模式返回平台默认目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "" }

		got := AppDataDir()

		if got == "" {
			t.Fatal("AppDataDir() 返回空")
		}
		if !strings.HasSuffix(got, "opskat") {
			t.Errorf("AppDataDir() = %q, 期望以 opskat 结尾", got)
		}
		switch runtime.GOOS {
		case "darwin":
			if !strings.Contains(got, "Application Support") {
				t.Errorf("AppDataDir() = %q, darwin 期望包含 Application Support", got)
			}
		case "linux":
			if !strings.Contains(got, ".config") {
				t.Errorf("AppDataDir() = %q, linux 期望包含 .config", got)
			}
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/bootstrap/ -run TestAppDataDir -v`
Expected: FAIL，编译错误 `undefined: portableDir`

- [ ] **Step 3: 改实现**

在 `internal/bootstrap/bootstrap.go` 的 import 块加入：

```go
	"github.com/opskat/opskat/internal/pkg/portable"
```

把 `AppDataDir`（第 56–73 行）替换为：

```go
// portableDir 解析便携数据目录，便携模式外返回 ""。变量而非直接调用，
// 是为了让 AppDataDir 的两个分支可测（os.Executable() 在测试中指向
// 临时测试二进制，无法构造 data 目录）。
var portableDir = portable.Dir

// AppDataDir 返回应用数据目录。
// 便携模式（可执行文件同级存在 data 目录）优先，否则用平台默认目录。
func AppDataDir() string {
	if dir := portableDir(); dir != "" {
		return dir
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "opskat")
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			home, _ := os.UserHomeDir()
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opskat")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "opskat")
	}
}
```

- [ ] **Step 4: `Init` 传 `NoKeychain`**

`internal/bootstrap/bootstrap.go` 中 Task 2 Step 4 改过的那段，补上 `NoKeychain`：

```go
	masterKey, err := credential_svc.ResolveMasterKey(credential_svc.MasterKeyOptions{
		Explicit:   opts.MasterKey,
		DataDir:    dataDir,
		NoKeychain: portableDir() != "",
	})
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/bootstrap/ -run TestAppDataDir -v`
Expected: PASS，两个子测试全绿

- [ ] **Step 6: 全量回归 + Lint**

Run: `go test ./... && golangci-lint run ./internal/bootstrap/`
Expected: 全绿（`main_test.go` 的 `OPSKAT_DATA_DIR` 用例须保持 PASS）；`0 issues.`

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/
git commit -m "$(cat <<'MSG'
✨ AppDataDir 支持便携模式

可执行文件同级存在 data 目录时，数据目录取该目录，且 master key 不
落凭据管理器。落点选 AppDataDir 而非新建解析函数，是因为它就是平台
默认数据目录的生产者，便携目录只是它的一个变体——这样 opsctl 下 6 处
直接调用者与 ResolvedDataDir 都自动便携化，无需逐个改。
MSG
)"
```

---

### Task 4: opsctl 安装目录便携化

**Files:**
- Modify: `internal/embedded/embed.go:18-62`
- Test: `internal/embedded/embed_test.go`（新建）

**Interfaces:**
- Consumes: `portable.Dir()`（Task 1）
- Produces: `embedded.DefaultInstallDir()` 行为变更——便携时返回 exe 所在目录；`InstallOpsctl` 便携时跳过 `addToUserPath`。签名均不变，`internal/app/system/settings.go` 的 4 处调用无需改动。

> `addToUserPath` 会写 HKCU `Environment` 注册表并广播 `WM_SETTINGCHANGE`（见 `internal/embedded/path_windows.go`）。便携模式必须跳过：这是实打实的宿主机污染，且 U 盘盘符会变，写入 PATH 亦无意义。

- [ ] **Step 1: 写失败测试**

创建 `internal/embedded/embed_test.go`：

```go
package embedded

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultInstallDir(t *testing.T) {
	t.Run("便携模式返回可执行文件所在目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return filepath.Join("/tmp/opskat-portable", "data") }

		got := DefaultInstallDir()

		if got != "/tmp/opskat-portable" {
			t.Errorf("DefaultInstallDir() = %q, 期望 %q（data 目录的父目录，即 exe 同级）", got, "/tmp/opskat-portable")
		}
	})

	t.Run("非便携模式返回平台默认安装目录", func(t *testing.T) {
		orig := portableDir
		t.Cleanup(func() { portableDir = orig })
		portableDir = func() string { return "" }

		got := DefaultInstallDir()

		if got == "" {
			t.Fatal("DefaultInstallDir() 返回空")
		}
		if runtime.GOOS != "windows" && !strings.HasSuffix(got, filepath.Join(".local", "bin")) {
			t.Errorf("DefaultInstallDir() = %q, 非 windows 期望以 .local/bin 结尾", got)
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/embedded/ -run TestDefaultInstallDir -v`
Expected: FAIL，编译错误 `undefined: portableDir`

- [ ] **Step 3: 改实现**

在 `internal/embedded/embed.go` 的 import 块加入：

```go
	"github.com/opskat/opskat/internal/pkg/portable"
```

把 `DefaultInstallDir`（第 18–31 行）替换为：

```go
// portableDir 解析便携数据目录，便携模式外返回 ""。变量而非直接调用，是为了可测。
var portableDir = portable.Dir

// DefaultInstallDir 返回默认安装目录。
// 便携模式下与 opskat.exe 同级（即便携 data 目录的父目录），使 opsctl 与
// 应用认到同一个数据目录；否则 Windows 上与数据目录统一为 %LOCALAPPDATA%\opskat。
func DefaultInstallDir() string {
	if dir := portableDir(); dir != "" {
		return filepath.Dir(dir)
	}
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			home, _ := os.UserHomeDir()
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(localAppData, "opskat")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin")
}
```

把 `InstallOpsctl` 末尾的 PATH 段（第 56–59 行）替换为：

```go
	// 便携模式不改宿主机 PATH：写 HKCU Environment 是实打实的污染，
	// 且便携目录的盘符会变，写进 PATH 也没有意义。
	if portableDir() != "" {
		return targetPath, nil
	}

	// Windows: 将安装目录添加到用户 PATH
	if err := addToUserPath(targetDir); err != nil {
		return targetPath, fmt.Errorf("installed successfully but failed to add to PATH: %w", err)
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/embedded/ -v`
Expected: PASS，两个子测试全绿

- [ ] **Step 5: 全量回归 + Lint**

Run: `go build ./... && go test ./... && golangci-lint run ./internal/embedded/`
Expected: 全绿；`0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/embedded/
git commit -m "$(cat <<'MSG'
✨ 便携模式下 opsctl 装到应用同级且不改 PATH

DefaultInstallDir 便携时返回 opskat.exe 同级目录，使 opsctl 与应用
认到同一个 data 目录；InstallOpsctl 便携时跳过 addToUserPath——它写
HKCU Environment 注册表，是宿主机污染，且便携目录盘符会变。
MSG
)"
```

---

### Task 5: 迁移改用传入的数据目录

**Files:**
- Modify: `migrations/migrations.go:9`
- Modify: `migrations/202603260001_ai_providers.go:15-102`
- Modify: `internal/bootstrap/bootstrap.go:116`（唯一调用方）
- Test: `migrations/ai_providers_migration_test.go`（新建）

**Interfaces:**
- Consumes: 无
- Produces: `migrations.RunMigrations(db *gorm.DB, dataDir string) error` —— 替换原 `RunMigrations(db *gorm.DB) error`。

> **为什么改：** `202603260001` 读 `<appDataDir>/config.json` 把旧 OpenAI 配置导入 DB，而它用的是自己复制的一份平台目录逻辑（注释称为避免循环引用）。便携版在他人机器上运行时会读**宿主机**的 `%LOCALAPPDATA%\opskat\config.json`，把宿主用户的 API key 导进便携库。改为传参同时修好两件事：便携版不再吸宿主机凭据，且该迁移开始尊重 `--data-dir`。

- [ ] **Step 1: 写失败测试**

创建 `migrations/ai_providers_migration_test.go`：

```go
package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 返回一个空库。表由 RunMigrations 自己建——不要预建
// conversations：migration202603220001 用的是 CREATE TABLE IF NOT EXISTS，
// 预建会留下一张缺列的桩表，让后续迁移拿到错误的结构。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	return db
}

func TestMigrateConfigToDB(t *testing.T) {
	t.Run("从传入的 dataDir 读取 config.json 并导入", func(t *testing.T) {
		db := newTestDB(t)
		dataDir := t.TempDir()
		cfg := `{"ai_provider_type":"openai","ai_api_base":"https://api.example.com","ai_api_key":"sk-test","ai_model":"gpt-4"}`
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(cfg), 0o600); err != nil {
			t.Fatalf("准备 config.json 失败: %v", err)
		}

		if err := RunMigrations(db, dataDir); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var apiBase string
		if err := db.Raw("SELECT api_base FROM ai_providers WHERE type = ?", "openai").Scan(&apiBase).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if apiBase != "https://api.example.com" {
			t.Errorf("api_base = %q, 期望 %q", apiBase, "https://api.example.com")
		}
	})

	t.Run("dataDir 内无 config.json 时不导入任何记录", func(t *testing.T) {
		db := newTestDB(t)

		// 传入空目录：即便宿主机 ~/.config/opskat/config.json 存在，也不应被读取。
		if err := RunMigrations(db, t.TempDir()); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&count).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if count != 0 {
			t.Errorf("ai_providers 记录数 = %d, 期望 0（不得回退读平台目录）", count)
		}
	})

	t.Run("非 openai 类型不导入", func(t *testing.T) {
		db := newTestDB(t)
		dataDir := t.TempDir()
		cfg := `{"ai_provider_type":"local_cli","ai_api_base":"http://localhost"}`
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(cfg), 0o600); err != nil {
			t.Fatalf("准备 config.json 失败: %v", err)
		}

		if err := RunMigrations(db, dataDir); err != nil {
			t.Fatalf("RunMigrations() 出错: %v", err)
		}

		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM ai_providers").Scan(&count).Error; err != nil {
			t.Fatalf("查询 ai_providers 失败: %v", err)
		}
		if count != 0 {
			t.Errorf("ai_providers 记录数 = %d, 期望 0（local_cli 不再支持）", count)
		}
	})
}
```

> `github.com/glebarez/sqlite` 已是 go.mod 的直接依赖（v1.11.0），`internal/repository/snippet_repo/snippet_impl_test.go` 就用它开测试库——沿用同一驱动，无需新增依赖。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./migrations/ -run TestMigrateConfigToDB -v`
Expected: FAIL，编译错误 `too many arguments in call to RunMigrations`

- [ ] **Step 3: 改 `RunMigrations` 签名**

`migrations/migrations.go`：

```go
// RunMigrations 执行数据库迁移。
// dataDir 是应用实际使用的数据目录（可能被 --data-dir 覆盖或为便携目录），
// 供需要读取磁盘旧配置的迁移使用。
func RunMigrations(db *gorm.DB, dataDir string) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, []*gormigrate.Migration{
		migration202603220001(),
		migration202603260001(dataDir),
		migration202603270001(),
		migration202603290001(),
		migration202603300001(),
		migration202603300002(),
		migration202603310001(),
		migration202604050001(),
		migration202604140001(),
		migration202604160001(),
		migration202604170001(),
		migration202604220001(),
		migration202604230001(),
		migration202604270001(),
		migration202605010001(),
		migration202605060001(),
		migration202605070001(),
		migration202605120001(),
		migration202605260001(),
	})
	return m.Migrate()
}
```

- [ ] **Step 4: 改迁移本身，删掉重复的 `appDataDir`**

`migrations/202603260001_ai_providers.go`：

1. 函数签名改为 `func migration202603260001(dataDir string) *gormigrate.Migration`
2. 第 40 行 `migrateConfigToDB(tx)` 改为 `migrateConfigToDB(tx, dataDir)`
3. `migrateConfigToDB` 签名改为 `func migrateConfigToDB(tx *gorm.DB, dataDir string)`，并**删除**其内第一行 `dataDir := appDataDir()`
4. **整个删除**文件末尾的 `appDataDir()` 函数（含其 `// appDataDir 获取应用数据目录（migration 中不依赖 bootstrap 包避免循环引用）` 注释）
5. 从 import 块删除因此不再使用的 `"runtime"`（`os` / `path/filepath` 仍被 `migrateConfigToDB` 使用，保留）

改完后 `migrateConfigToDB` 开头应为：

```go
// migrateConfigToDB 从 dataDir 下的 config.json 读取旧 AI 配置并迁移到数据库。
func migrateConfigToDB(tx *gorm.DB, dataDir string) {
	configPath := filepath.Join(dataDir, "config.json")

	data, err := os.ReadFile(configPath) //nolint:gosec // configPath 来自已解析的数据目录，非用户输入
	if err != nil {
		return
	}
```

- [ ] **Step 5: 更新唯一调用方**

`internal/bootstrap/bootstrap.go` 第 116 行：

```go
	if err := migrations.RunMigrations(db.Default(), dataDir); err != nil {
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go build ./... && go test ./migrations/ -v`
Expected: 编译通过；三个子测试全 PASS

- [ ] **Step 7: 全量回归 + Lint**

Run: `go test ./... && golangci-lint run ./migrations/ ./internal/bootstrap/`
Expected: 全绿；`0 issues.`

- [ ] **Step 8: Commit**

```bash
git add migrations/ internal/bootstrap/bootstrap.go
git commit -m "$(cat <<'MSG'
🐛 AI 配置迁移改用实际数据目录，删除重复的平台路径逻辑

202603260001 原先用自己复制的一份 appDataDir()，读的是平台默认目录。
便携版跑在他人机器上时会读宿主机的 config.json，把宿主用户的 API key
导进便携库；--data-dir 覆盖也一直被它忽略。改为由 RunMigrations 传入
实际数据目录，同时删掉那份复制的逻辑。
MSG
)"
```

---

### Task 6: 便携模式本地端到端验证

**Files:**
- Create: `build/windows/portable-data-readme.txt`

**Interfaces:**
- Consumes: Task 1–5 的全部改动
- Produces: `build/windows/portable-data-readme.txt` —— 供 Task 7 的 CI 打包步骤复制为 zip 内的 `data/README.txt`。

> AGENTS.md 要求以可观察副作用验证，不能只靠断言。便携机制不含平台分支，故 macOS/Linux 上即可真跑，验证的是「改 AppDataDir 后 opsctl 全链路自动便携」这一主张。

- [ ] **Step 1: 创建 README 源文件**

创建 `build/windows/portable-data-readme.txt`（内容与 spec 5.5 节逐字一致）：

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

- [ ] **Step 2: 构建 opsctl 并造出便携布局**

```bash
make build-cli
mkdir -p ./build/bin/data
```

- [ ] **Step 3: 观察便携模式生效**

```bash
./build/bin/opsctl list
ls -la ./build/bin/data/
```

Expected: `./build/bin/data/` 下出现 `opskat.db`、`master.key`、`logs/`。
若 `data/` 为空而数据落到了 `~/Library/Application Support/opskat`（macOS）或 `~/.config/opskat`（Linux），说明 Task 3 未生效——停下排查，不要继续。

- [ ] **Step 4: 观察 master key 未落钥匙串**

```bash
cat ./build/bin/data/master.key | head -c 20; echo
```

Expected: 打印出 base64 片段，证明 key 在文件里。
（不要用 `security find-generic-password -s opskat` 判断钥匙串为空——开发机上很可能已有历史条目，那不是本次写入的。文件存在即证明便携分支走通。）

- [ ] **Step 5: 观察删除 data 后回落**

```bash
rm -rf ./build/bin/data
./build/bin/opsctl list
ls ./build/bin/ | grep -c data || echo "data 目录未被重建（符合预期）"
```

Expected: 命令正常执行；`./build/bin/data` 未被重建，数据回落到平台默认目录。

- [ ] **Step 6: 清理并 Commit**

```bash
rm -rf ./build/bin/data
git add build/windows/portable-data-readme.txt
git commit -m "$(cat <<'MSG'
📝 新增便携版 data/README.txt 源文件

说明便携模式的数据位置、删除该目录的后果，以及 master key 明文与
数据库同目录的安全取舍——便携版与安装版在这一点上行为不同，用户
需要知道。
MSG
)"
```

---

### Task 7: CI 去重为可复用工作流并打包便携版

**Files:**
- Create: `.github/platforms.json`
- Create: `.github/workflows/build-desktop.yml`
- Modify: `.github/workflows/release.yml`（删除 `build-desktop` job，改为调用）
- Modify: `.github/workflows/nightly.yml`（同上）
- Modify: `.github/workflows/manual-build.yml`（同上，并删除内联平台 JSON）

**Interfaces:**
- Consumes: `build/windows/portable-data-readme.txt`（Task 6）
- Produces: 可复用工作流 `./.github/workflows/build-desktop.yml`，输入 `version` / `commit-id` / `ref` / `artifact-prefix` / `platforms` / `if-no-files-found`。

- [ ] **Step 1: 创建平台矩阵单一事实源**

创建 `.github/platforms.json`：

```json
[
  {
    "os": "macos-latest",
    "goos": "darwin",
    "goarch": "arm64",
    "platform": "darwin-arm64"
  },
  {
    "os": "macos-latest",
    "goos": "darwin",
    "goarch": "amd64",
    "platform": "darwin-amd64"
  },
  {
    "os": "ubuntu-22.04",
    "goos": "linux",
    "goarch": "amd64",
    "platform": "linux-amd64",
    "webkit_pkg": "libwebkit2gtk-4.0-dev"
  },
  {
    "os": "ubuntu-24.04-arm",
    "goos": "linux",
    "goarch": "arm64",
    "platform": "linux-arm64",
    "webkit_pkg": "libwebkit2gtk-4.1-dev",
    "extra_tags": ",webkit2_41"
  },
  {
    "os": "windows-latest",
    "goos": "windows",
    "goarch": "amd64",
    "platform": "windows-amd64"
  },
  {
    "os": "windows-latest",
    "goos": "windows",
    "goarch": "arm64",
    "platform": "windows-arm64"
  }
]
```

- [ ] **Step 2: 创建可复用工作流**

创建 `.github/workflows/build-desktop.yml`：

```yaml
name: Build Desktop

on:
  workflow_call:
    inputs:
      version:
        description: "版本号，注入 ldflags"
        required: true
        type: string
      commit-id:
        description: "commit SHA，注入 buildinfo.CommitID"
        required: true
        type: string
      ref:
        description: "checkout 目标；空则用触发 ref"
        required: false
        type: string
        default: ""
      artifact-prefix:
        description: "artifact 名前缀，如 release / nightly / manual"
        required: true
        type: string
      platforms:
        description: "逗号分隔的平台过滤，如 windows-amd64,linux-amd64；空则全部"
        required: false
        type: string
        default: ""
      if-no-files-found:
        description: "上传时找不到文件的处理方式：warn / error / ignore"
        required: false
        type: string
        default: warn

jobs:
  matrix:
    name: Resolve matrix
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.resolve.outputs.matrix }}
    steps:
      - uses: actions/checkout@v6

      - name: Resolve platform matrix
        id: resolve
        env:
          PLATFORMS: ${{ inputs.platforms }}
        run: |
          set -euo pipefail
          if [ -z "$PLATFORMS" ]; then
            MATRIX=$(jq -c '{include: .}' .github/platforms.json)
          else
            SELECTED=$(printf '%s' "$PLATFORMS" | tr ',' '\n' | jq -R . | jq -s .)
            MATRIX=$(jq -c --argjson requested "$SELECTED" \
              '{include: [$requested[] as $p | .[] | select(.platform == $p)]}' \
              .github/platforms.json)
          fi
          COUNT=$(jq '.include | length' <<< "$MATRIX")
          if [ "$COUNT" -eq 0 ]; then
            echo "::error::No platform matched: '${PLATFORMS}'"
            exit 1
          fi
          echo "Selected: $(jq -r '.include[].platform' <<< "$MATRIX" | paste -sd ',' -)"
          echo "matrix=${MATRIX}" >> "$GITHUB_OUTPUT"

  build:
    name: Desktop (${{ matrix.platform }})
    needs: [matrix]
    runs-on: ${{ matrix.os }}
    env:
      VERSION: ${{ inputs.version }}
      COMMIT_ID: ${{ inputs.commit-id }}
      NODE_OPTIONS: --max-old-space-size=4096
    strategy:
      fail-fast: false
      matrix: ${{ fromJson(needs.matrix.outputs.matrix) }}
    steps:
      - uses: actions/checkout@v6
        with:
          ref: ${{ inputs.ref }}

      - name: Free disk space (macOS)
        if: matrix.goos == 'darwin'
        run: |
          rm -rf ~/Library/Android/sdk || true
          rm -rf ~/Library/Developer/CoreSimulator || true
          rm -rf ~/Library/Caches/Homebrew || true
          rm -rf /Users/runner/hostedtoolcache || true
          sudo rm -rf /usr/local/share/powershell || true
          sudo rm -rf /usr/local/lib/node_modules || true
          sudo rm -rf /usr/local/share/chromium || true

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod

      - uses: pnpm/action-setup@v5
        with:
          version: 10

      - uses: actions/setup-node@v6
        with:
          node-version: "22"
          cache: "pnpm"
          cache-dependency-path: frontend/pnpm-lock.yaml

      - name: Install frontend dependencies
        run: cd frontend && pnpm install --frozen-lockfile

      - name: Install Wails CLI
        run: go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0

      - name: Install Linux dependencies
        if: matrix.goos == 'linux'
        run: sudo apt-get update && sudo apt-get install -y libgtk-3-dev ${{ matrix.webkit_pkg || 'libwebkit2gtk-4.0-dev' }}

      - name: Install NSIS
        if: matrix.goos == 'windows'
        shell: pwsh
        run: |
          choco install nsis -y
          echo "${env:ProgramFiles(x86)}\NSIS" >> $env:GITHUB_PATH

      - name: Build embedded opsctl
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: go build -ldflags="-s -w -X github.com/cago-frame/cago/configs.Version=${{ env.VERSION }} -X github.com/opskat/opskat/internal/buildinfo.CommitID=${{ env.COMMIT_ID }}" -o ./internal/embedded/opsctl_bin ./cmd/opsctl/

      - name: Build desktop app
        if: matrix.goos != 'windows'
        run: wails build -platform ${{ matrix.goos }}/${{ matrix.goarch }} -ldflags="-s -w -X github.com/cago-frame/cago/configs.Version=${{ env.VERSION }} -X github.com/opskat/opskat/internal/buildinfo.CommitID=${{ env.COMMIT_ID }}" -tags "embed_opsctl${{ matrix.extra_tags }}"

      - name: Build desktop app (Windows)
        if: matrix.goos == 'windows'
        run: wails build -nsis -platform ${{ matrix.goos }}/${{ matrix.goarch }} -ldflags="-s -w -X github.com/cago-frame/cago/configs.Version=${{ env.VERSION }} -X github.com/opskat/opskat/internal/buildinfo.CommitID=${{ env.COMMIT_ID }}" -tags embed_opsctl

      - name: Import Apple certificate
        if: matrix.goos == 'darwin'
        env:
          APPLE_CERTIFICATE_P12: ${{ secrets.APPLE_CERTIFICATE_P12 }}
          APPLE_CERTIFICATE_PASSWORD: ${{ secrets.APPLE_CERTIFICATE_PASSWORD }}
        run: |
          if [ -z "$APPLE_CERTIFICATE_P12" ]; then
            echo "No Apple certificate configured, skipping"
            exit 0
          fi
          KEYCHAIN_PATH=$RUNNER_TEMP/app-signing.keychain-db
          KEYCHAIN_PASSWORD=$(openssl rand -base64 32)
          security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
          security set-keychain-settings -lut 21600 "$KEYCHAIN_PATH"
          security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
          echo "$APPLE_CERTIFICATE_P12" | base64 --decode > $RUNNER_TEMP/certificate.p12
          security import $RUNNER_TEMP/certificate.p12 -P "$APPLE_CERTIFICATE_PASSWORD" -A -t cert -f pkcs12 -k "$KEYCHAIN_PATH"
          security set-key-partition-list -S apple-tool:,apple: -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
          security list-keychain -d user -s "$KEYCHAIN_PATH"
          IDENTITY=$(security find-identity -v -p codesigning "$KEYCHAIN_PATH" | grep "Developer ID Application" | head -1 | sed 's/.*"\(.*\)".*/\1/')
          echo "SIGNING_IDENTITY=$IDENTITY" >> $GITHUB_ENV

      - name: Sign macOS app
        if: matrix.goos == 'darwin'
        run: |
          if [ -n "$SIGNING_IDENTITY" ]; then
            codesign --force --deep --sign "$SIGNING_IDENTITY" --options runtime --entitlements build/darwin/entitlements.plist build/bin/opskat.app
          else
            codesign --force --deep --sign - build/bin/opskat.app
          fi

      - name: Package (macOS dmg)
        if: matrix.goos == 'darwin'
        run: |
          DMG_SRC=$(mktemp -d)
          cp -R build/bin/opskat.app "$DMG_SRC/"
          ln -s /Applications "$DMG_SRC/Applications"
          hdiutil create -volname "Opskat" -srcfolder "$DMG_SRC" -ov -format UDZO opskat-${VERSION}-${{ matrix.platform }}.dmg
          rm -rf "$DMG_SRC"

      - name: Notarize macOS DMG
        if: matrix.goos == 'darwin'
        env:
          APPLE_ID: ${{ secrets.APPLE_ID }}
          APPLE_ID_PASSWORD: ${{ secrets.APPLE_ID_PASSWORD }}
          APPLE_TEAM_ID: ${{ secrets.APPLE_TEAM_ID }}
        run: |
          if [ -z "$SIGNING_IDENTITY" ]; then
            echo "No signing identity, skipping notarization"
            exit 0
          fi
          xcrun notarytool submit opskat-${VERSION}-${{ matrix.platform }}.dmg \
            --apple-id "$APPLE_ID" \
            --password "$APPLE_ID_PASSWORD" \
            --team-id "$APPLE_TEAM_ID" \
            --wait
          xcrun stapler staple opskat-${VERSION}-${{ matrix.platform }}.dmg

      - name: Package (Linux deb)
        if: matrix.goos == 'linux'
        run: |
          PKG_ROOT=pkg-deb
          mkdir -p ${PKG_ROOT}/DEBIAN
          mkdir -p ${PKG_ROOT}/usr/bin
          mkdir -p ${PKG_ROOT}/usr/share/applications
          mkdir -p ${PKG_ROOT}/usr/share/icons/hicolor/256x256/apps
          cp build/bin/opskat ${PKG_ROOT}/usr/bin/opskat
          cp build/appicon.png ${PKG_ROOT}/usr/share/icons/hicolor/256x256/apps/opskat.png
          cat > ${PKG_ROOT}/DEBIAN/control << CTRL
          Package: opskat
          Version: $(echo "${VERSION#v}" | tr '-' '~')
          Section: utils
          Priority: optional
          Architecture: ${{ matrix.goarch }}
          Maintainer: opskat
          Description: Opskat desktop application
          CTRL
          sed -i 's/^          //' ${PKG_ROOT}/DEBIAN/control
          cat > ${PKG_ROOT}/usr/share/applications/opskat.desktop << DESK
          [Desktop Entry]
          Name=Opskat
          Exec=/usr/bin/opskat
          Icon=opskat
          Type=Application
          Categories=Utility;
          DESK
          sed -i 's/^          //' ${PKG_ROOT}/usr/share/applications/opskat.desktop
          dpkg-deb --build ${PKG_ROOT} opskat-${VERSION}-${{ matrix.platform }}.deb

      - name: Package (Windows installer)
        if: matrix.goos == 'windows'
        shell: pwsh
        run: Move-Item "build\bin\opskat-${{ matrix.goarch }}-installer.exe" "opskat-$($env:VERSION)-${{ matrix.platform }}-installer.exe"

      - name: Package (Windows portable)
        if: matrix.goos == 'windows' && matrix.goarch == 'amd64'
        shell: pwsh
        run: |
          $stage = "portable/opskat-$($env:VERSION)-${{ matrix.platform }}"
          New-Item -ItemType Directory -Force -Path "$stage/data" | Out-Null
          Copy-Item build/bin/opskat.exe "$stage/opskat.exe"
          Copy-Item internal/embedded/opsctl_bin "$stage/opsctl.exe"
          Copy-Item build/windows/portable-data-readme.txt "$stage/data/README.txt"
          Compress-Archive -Path $stage -DestinationPath "opskat-$($env:VERSION)-${{ matrix.platform }}-portable.zip"

      - uses: actions/upload-artifact@v7
        with:
          name: ${{ inputs.artifact-prefix }}-opskat-${{ matrix.platform }}
          path: opskat-${{ inputs.version }}-${{ matrix.platform }}*
          if-no-files-found: ${{ inputs.if-no-files-found }}
```

> **三点为什么：**
> - `opskat.exe` 是 `-nsis` 已顺带产出但原先被丢弃的产物；`opsctl_bin` 已按 matrix 的 `GOOS`/`GOARCH` 编成 windows-amd64。便携版零额外构建耗时。
> - 暂存目录在 `portable/` 下，不会被根目录上传 glob `opskat-<version>-<platform>*` 误捞；zip 落在根目录，被同一 glob 自动捞到，故上传步骤与下游 checksums job 均无需改动。
> - `Compress-Archive -Path $stage`（不带 `\*`）使 zip 内含顶层文件夹，解压不散落。

- [ ] **Step 3: 改造 release.yml**

删除整个 `build-desktop` job（第 14–204 行）及顶部的 `env:` 块（第 8–11 行，其中 `VERSION`/`COMMIT_ID`/`NODE_OPTIONS` 已下沉到可复用工作流），替换为：

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

jobs:
  build-desktop:
    uses: ./.github/workflows/build-desktop.yml
    with:
      version: ${{ github.ref_name }}
      commit-id: ${{ github.sha }}
      artifact-prefix: release
    secrets: inherit

  release:
    name: Create Release
    needs: [build-desktop]
    # ...以下 release job 原样保留，不改动
```

- [ ] **Step 4: 改造 nightly.yml**

删除整个 `build-desktop` job（第 87–283 行）及顶部 `env:` 块（第 15–16 行），替换为：

```yaml
  build-desktop:
    needs: [prepare]
    if: ${{ always() && !failure() && !cancelled() && needs.prepare.outputs.skip != 'true' }}
    uses: ./.github/workflows/build-desktop.yml
    with:
      version: ${{ needs.prepare.outputs.version }}
      commit-id: ${{ needs.prepare.outputs.sha }}
      ref: nightly
      artifact-prefix: nightly
    secrets: inherit
```

`sync` / `prepare` / `release` 三个 job 原样保留。

- [ ] **Step 5: 改造 manual-build.yml**

`prepare` job：删除 `Build platform matrix` 步骤（第 82–169 行）与 outputs 中的 `matrix`，把 `platforms` 输出改为逗号串。`prepare` job 的 outputs 与新步骤：

```yaml
    outputs:
      version: ${{ steps.version.outputs.version }}
      sha: ${{ steps.version.outputs.sha }}
      platforms: ${{ steps.platforms.outputs.platforms }}
```

`Compute version` 步骤原样保留，其后的 `Build platform matrix` 步骤整体替换为：

```yaml
      - name: Select platforms
        id: platforms
        env:
          BUILD_DARWIN_ARM64: ${{ inputs.darwin_arm64 }}
          BUILD_DARWIN_AMD64: ${{ inputs.darwin_amd64 }}
          BUILD_LINUX_AMD64: ${{ inputs.linux_amd64 }}
          BUILD_LINUX_ARM64: ${{ inputs.linux_arm64 }}
          BUILD_WINDOWS_AMD64: ${{ inputs.windows_amd64 }}
          BUILD_WINDOWS_ARM64: ${{ inputs.windows_arm64 }}
        run: |
          set -euo pipefail
          SELECTED=()
          [ "$BUILD_DARWIN_ARM64" = "true" ]  && SELECTED+=("darwin-arm64")  || true
          [ "$BUILD_DARWIN_AMD64" = "true" ]  && SELECTED+=("darwin-amd64")  || true
          [ "$BUILD_LINUX_AMD64" = "true" ]   && SELECTED+=("linux-amd64")   || true
          [ "$BUILD_LINUX_ARM64" = "true" ]   && SELECTED+=("linux-arm64")   || true
          [ "$BUILD_WINDOWS_AMD64" = "true" ] && SELECTED+=("windows-amd64") || true
          [ "$BUILD_WINDOWS_ARM64" = "true" ] && SELECTED+=("windows-arm64") || true
          if [ "${#SELECTED[@]}" -eq 0 ]; then
            echo "::error::Select at least one platform to build."
            exit 1
          fi
          PLATFORMS=$(printf '%s\n' "${SELECTED[@]}" | paste -sd ',' -)
          echo "Selected platforms: ${PLATFORMS}"
          echo "platforms=${PLATFORMS}" >> "$GITHUB_OUTPUT"
```

`build-desktop` job（第 171–340 行）整体替换为：

```yaml
  build-desktop:
    needs: [prepare]
    uses: ./.github/workflows/build-desktop.yml
    with:
      version: ${{ needs.prepare.outputs.version }}
      commit-id: ${{ needs.prepare.outputs.sha }}
      ref: ${{ needs.prepare.outputs.sha }}
      artifact-prefix: manual
      platforms: ${{ needs.prepare.outputs.platforms }}
      if-no-files-found: error
    secrets: inherit
```

顶部 `env:` 块（第 41–42 行）删除；`checksums` job 原样保留。

- [ ] **Step 6: 静态检查工作流语法**

```bash
command -v actionlint >/dev/null || go install github.com/rhysd/actionlint/cmd/actionlint@latest
actionlint
```

Expected: 无输出（通过）。若报 `secrets: inherit` 相关告警，确认三个调用方均已写 `secrets: inherit`。

- [ ] **Step 7: 本地校验矩阵解析逻辑**

不用等 CI，先在本地验证 jq 表达式：

```bash
jq -c '{include: .}' .github/platforms.json | jq '.include | length'
```
Expected: `6`

```bash
printf 'windows-amd64' | tr ',' '\n' | jq -R . | jq -s . > /tmp/sel.json
jq -c --argjson requested "$(cat /tmp/sel.json)" \
  '{include: [$requested[] as $p | .[] | select(.platform == $p)]}' \
  .github/platforms.json
```
Expected: 只含 windows-amd64 一项的 `{"include":[{...}]}`

- [ ] **Step 8: Commit**

```bash
git add .github/
git commit -m "$(cat <<'MSG'
🔧 构建工作流去重并新增 Windows 便携版

release / nightly / manual 三份 build-desktop job 有约 200 行逐字重复，
差异只有 checkout ref、版本来源和 artifact 名前缀。收敛为可复用工作流，
平台矩阵收进 .github/platforms.json 作为单一事实源。

顺带产出 windows-amd64 便携版 zip：opskat.exe 本就是 -nsis 顺带产出
但被丢弃的产物，opsctl_bin 也已按 matrix 编好，故零额外构建耗时。
MSG
)"
```

---

### Task 8: 同步架构文档

**Files:**
- Modify: `docs/ARCHITECTURE.md:74`

**Interfaces:**
- Consumes: Task 1–7 的全部改动
- Produces: 无（文档）

> 先读 `docs/DOC-MAINTENANCE.md`——AGENTS.md 要求编辑任何贡献者文档前必须读它（文档集组织规则 + 对当前分支的反漂移核对）。

- [ ] **Step 1: 读文档维护规则**

```bash
cat docs/DOC-MAINTENANCE.md
```

- [ ] **Step 2: 更新数据目录解析链的描述**

`docs/ARCHITECTURE.md` 第 74 行现为：

> `main.go` is the composition root. In order, it: resolves the data dir / master key (env overrides `OPSKAT_DATA_DIR`, `OPSKAT_MASTER_KEY`; `OPSKAT_E2E` relaxes the single-instance lock for the e2e harness); runs `bootstrap.Init` ...

把括号内的解析链描述改为把便携模式纳入优先级，改后该分句为：

> resolves the data dir / master key (`OPSKAT_DATA_DIR` / `OPSKAT_MASTER_KEY` override; otherwise `bootstrap.AppDataDir()` returns the **portable** dir — `data/` next to the executable, see `internal/pkg/portable` — and falls back to the per-platform dir. In portable mode the master key stays in `<dataDir>/master.key` instead of the OS keychain, so the folder can be moved between machines; `OPSKAT_E2E` relaxes the single-instance lock for the e2e harness)

- [ ] **Step 3: 核对该行其余事实仍成立**

按 DOC-MAINTENANCE 的反漂移要求，确认第 74 行提到的 `bootstrap.Init` 职责描述（DB open、master-key resolve、repository registration、migrations）仍与代码一致——Task 5 改了 `RunMigrations` 签名但未改职责，故该描述无需变更。确认后无须改动。

- [ ] **Step 4: Commit**

```bash
git add docs/ARCHITECTURE.md
git commit -m "$(cat <<'MSG'
📝 架构文档同步便携模式数据目录解析

AppDataDir 现在优先返回便携目录（exe 同级 data/），且便携模式下
master key 留在 <dataDir>/master.key 而非 OS 钥匙串。
MSG
)"
```

---

### Task 9: CI 端到端验证（需要人工触发）

**Files:** 无（验证任务）

**Interfaces:**
- Consumes: Task 7 产出的工作流
- Produces: 便携 zip 结构的实证

> **本任务无法由 agent 独自完成**——需要把分支推到远端并在 GitHub UI 触发 workflow。在本任务通过前，**不得声称 CI 改动可用**。

- [ ] **Step 1: 推分支**

```bash
git push -u origin feature/ci-windows-portable
```

- [ ] **Step 2: 触发 manual-build，仅勾选 windows-amd64**

```bash
gh workflow run manual-build.yml --ref feature/ci-windows-portable \
  -f darwin_arm64=false -f darwin_amd64=false \
  -f linux_amd64=false -f linux_arm64=false \
  -f windows_amd64=true -f windows_arm64=false
```

- [ ] **Step 3: 等待并观察结果**

```bash
gh run list --workflow=manual-build.yml --branch=feature/ci-windows-portable --limit 1
gh run watch $(gh run list --workflow=manual-build.yml --branch=feature/ci-windows-portable --limit 1 --json databaseId --jq '.[0].databaseId')
```

Expected: `matrix` job 只解析出 windows-amd64；`build` job 成功。

- [ ] **Step 4: 下载 artifact 并核对 zip 结构**

```bash
cd /tmp && rm -rf portable-check && mkdir portable-check && cd portable-check
gh run download $(gh run list --workflow=manual-build.yml --branch=feature/ci-windows-portable --limit 1 --json databaseId --jq '.[0].databaseId') -n manual-opskat-windows-amd64
unzip -l *-portable.zip
```

Expected: 列出如下三项（`<v>` 为版本号）：
```
opskat-<v>-windows-amd64/opskat.exe
opskat-<v>-windows-amd64/opsctl.exe
opskat-<v>-windows-amd64/data/README.txt
```

若 `data/README.txt` 缺失，说明 `Compress-Archive` 未纳入子目录——排查后重跑，不要跳过。

- [ ] **Step 5: 核对 installer 仍在**

```bash
ls
```

Expected: 同时存在 `opskat-<v>-windows-amd64-installer.exe` 与 `opskat-<v>-windows-amd64-portable.zip`——便携版是新增，不是替换。

- [ ] **Step 6: 记录验证结果**

把 `unzip -l` 的实际输出贴进 PR 描述，作为 CI 半边成立的证据。

---

## 完成后

全部任务通过后，用 `superpowers:finishing-a-development-branch` 决定合并方式。PR 描述须包含：

1. Task 6 的本地便携验证输出（`ls -la ./build/bin/data/`）
2. Task 9 的 `unzip -l` 输出
3. 明确说明「`cmd/opsctl/` 下 6 处 `--data-dir` 被忽略的既有缺陷」未在本次修复，已另开 issue

## 已知风险

- **`sync.OnceValue` 缓存便携判定**：进程内只 stat 一次。运行中创建/删除 `data/` 需重启才生效。刻意取舍，见 Task 1 Step 3 的注释。
- **便携版 master.key 明文与 DB 同目录**，安全性弱于安装版。便携的固有代价，已在 `data/README.txt` 中明示。
- **Task 5 的测试跑的是全部迁移**（`RunMigrations` 无法只跑一个）。若后续新增迁移依赖磁盘状态，该测试可能变脆——届时应给迁移传入所需状态，而非在测试里造平台目录。
