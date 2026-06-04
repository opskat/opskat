# 本地终端资产 (local) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `local` 资产类型,让用户在 OpsKat 内打开本机 shell(PowerShell/cmd/WSL/bash/zsh)的终端 Tab;不接 AI。

**Architecture:** 完全基于 opskat 自身的 `serial` 资产模式 + opskat 原生的跨平台 PTY 层(`localterm_svc` 包内 build-tag 文件,照 `serial_svc/hwflow_*.go` 约定)。后端:`assettype` 注册 handler → `localterm_svc.Manager/Session`(包 `ptyProcess`)→ `app/local` binder → Wails 事件。前端:`assetTypes/` 注册表镜像新增 + 终端 transport 二元分支(`isSerial`)收敛成 `TRANSPORTS` 映射表。

**Tech Stack:** Go 1.25 + Wails v2;`github.com/creack/pty`(Unix)+ `github.com/UserExistsError/conpty`(Windows);React 19 + TS + Zustand;goconvey/testify(Go)、vitest(前端)。

参考 spec:`docs/superpowers/specs/2026-06-03-local-terminal-asset-design.md`

---

## Phase 1 — 跨平台 PTY 层

### Task 1: PTY 接口与平台实现

**Files:**
- Create: `internal/service/localterm_svc/pty.go`
- Create: `internal/service/localterm_svc/pty_unix.go`
- Create: `internal/service/localterm_svc/pty_windows.go`
- Test: `internal/service/localterm_svc/pty_unix_test.go`
- Modify: `go.mod` / `go.sum`(经由 `go get`,勿手改)

- [ ] **Step 1: 加依赖**

Run:
```bash
go get github.com/creack/pty@latest
go get github.com/UserExistsError/conpty@v0.1.4
```
Expected: `go.mod` 出现这两行 require;`go.sum` 更新。

- [ ] **Step 2: 写包内接口 `pty.go`**

```go
// Package localterm_svc 管理本地 shell(PTY)终端会话。
package localterm_svc

// ptyProcess 是一个本地 shell 进程 + 其伪终端(PTY)的最小抽象。
// 平台实现见 pty_unix.go(creack/pty)与 pty_windows.go(conpty)。
type ptyProcess interface {
	Read(p []byte) (int, error)        // 读 PTY 输出(stdout+stderr 合流);进程退出后返回 EOF
	Write(p []byte) (int, error)       // 写 PTY 输入(用户键入)
	Resize(cols, rows int) error       // 调整窗口尺寸
	Close() error                      // 关闭 PTY 并回收子进程(实现内部带超时,避免僵尸)
}

// ptySpec 描述要启动的本地 shell。
type ptySpec struct {
	Shell string   // 为空时由平台实现按 OS 兜底
	Args  []string // shell 参数,如 ["-d","Ubuntu"]
	Cwd   string   // 工作目录,空则继承当前进程
	Cols  int      // 初始列(<=0 取 80)
	Rows  int      // 初始行(<=0 取 24)
}

// startPTYFn 是 startPTY 的可替换入口,测试用 fake 注入。
var startPTYFn = startPTY

func clampSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return cols, rows
}
```

- [ ] **Step 3: 写 Unix 实现 `pty_unix.go`**

```go
//go:build !windows

package localterm_svc

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type unixPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func startPTY(spec ptySpec) (ptyProcess, error) {
	shell := spec.Shell
	if shell == "" {
		shell = defaultShell()
	}
	cmd := exec.Command(shell, spec.Args...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	cols, rows := clampSize(spec.Cols, spec.Rows)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{f: f, cmd: cmd}, nil
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *unixPTY) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *unixPTY) Close() error {
	// 先给前台进程组发 SIGHUP 触发 shell 退出,再关 PTY master。
	_ = p.cmd.Process.Signal(syscall.SIGHUP)
	err := p.f.Close()
	// 后台带超时回收,避免僵尸进程,也避免 Close 阻塞调用方。
	go func() {
		done := make(chan struct{})
		go func() { _ = p.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	}()
	return err
}
```

- [ ] **Step 4: 写 Windows 实现 `pty_windows.go`**

> ⚠️ 在 Windows 上对照 `github.com/UserExistsError/conpty@v0.1.4` 的 godoc 核一遍签名(`Start` / `ConPtyDimensions` / `ConPtyWorkDir` / `Resize` / `Wait` / `IsConPtySupported`),不同小版本可能略有出入。

```go
//go:build windows

package localterm_svc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/UserExistsError/conpty"
)

type winPTY struct {
	cpty *conpty.ConPty
}

// windowsDefaultShell 按 pwsh → powershell → cmd 兜底。
func windowsDefaultShell() string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	if c := os.Getenv("COMSPEC"); c != "" {
		return c
	}
	return "cmd.exe"
}

func startPTY(spec ptySpec) (ptyProcess, error) {
	if !conpty.IsConPtySupported() {
		return nil, errors.New("当前系统不支持 ConPTY(需 Windows 10 1809+)")
	}
	shell := spec.Shell
	if shell == "" {
		shell = windowsDefaultShell()
	}
	cmdline := windowsCommandLine(shell, spec.Args)

	cols, rows := clampSize(spec.Cols, spec.Rows)
	opts := []conpty.ConPtyOption{conpty.ConPtyDimensions(cols, rows)}
	if spec.Cwd != "" {
		opts = append(opts, conpty.ConPtyWorkDir(spec.Cwd))
	}
	cpty, err := conpty.Start(cmdline, opts...)
	if err != nil {
		return nil, err
	}
	return &winPTY{cpty: cpty}, nil
}

func (p *winPTY) Read(b []byte) (int, error)  { return p.cpty.Read(b) }
func (p *winPTY) Write(b []byte) (int, error) { return p.cpty.Write(b) }

func (p *winPTY) Resize(cols, rows int) error {
	cols, rows = clampSize(cols, rows)
	return p.cpty.Resize(cols, rows)
}

func (p *winPTY) Close() error {
	// conpty.Close 关闭伪控制台句柄并触发子进程退出;Wait 回收。
	go func() { _, _ = p.cpty.Wait(context.Background()) }()
	return p.cpty.Close()
}
```

- [ ] **Step 5: 写 Unix 集成测试 `pty_unix_test.go`**

```go
//go:build !windows

package localterm_svc

import (
	"bufio"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartPTYEchoesInput(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	defer proc.Close()

	_, err = proc.Write([]byte("echo hello-pty\n"))
	require.NoError(t, err)

	// 读输出直到看到 echo 结果或超时。
	out := make(chan string, 1)
	go func() {
		r := bufio.NewReader(readerFunc(proc.Read))
		var sb strings.Builder
		buf := make([]byte, 1024)
		for {
			n, e := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), "hello-pty") {
					out <- sb.String()
					return
				}
			}
			if e != nil {
				out <- sb.String()
				return
			}
		}
	}()

	select {
	case s := <-out:
		require.Contains(t, s, "hello-pty")
	case <-time.After(5 * time.Second):
		t.Fatal("PTY 未在超时内回显输入")
	}
}

func TestStartPTYResizeNoError(t *testing.T) {
	proc, err := startPTY(ptySpec{Shell: "/bin/sh"})
	require.NoError(t, err)
	defer proc.Close()
	require.NoError(t, proc.Resize(120, 40))
}

// readerFunc 把 proc.Read 适配成 io.Reader。
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/service/localterm_svc/ -run TestStartPTY -v`
Expected: PASS(在 macOS/Linux 上)。

- [ ] **Step 7: Commit**

```bash
git add internal/service/localterm_svc/pty.go internal/service/localterm_svc/pty_unix.go internal/service/localterm_svc/pty_windows.go internal/service/localterm_svc/pty_unix_test.go go.mod go.sum
git commit -m "✨ 本地终端 PTY 跨平台引擎 #70"
```

---

## Phase 2 — 资产实体 LocalConfig

### Task 2: asset_entity 增加 local 类型

**Files:**
- Modify: `internal/model/entity/asset_entity/asset.go`
- Test: `internal/model/entity/asset_entity/asset_local_test.go`

- [ ] **Step 1: 写失败测试 `asset_local_test.go`**

```go
package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalConfigRoundTrip(t *testing.T) {
	a := &Asset{Type: AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{
		Shell: "/bin/zsh", Args: []string{"-l"}, Cwd: "/tmp",
	}))
	cfg, err := a.GetLocalConfig()
	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh", cfg.Shell)
	assert.Equal(t, []string{"-l"}, cfg.Args)
	assert.Equal(t, "/tmp", cfg.Cwd)
	assert.True(t, a.IsLocal())
}

func TestLocalAssetCanConnectWhenActive(t *testing.T) {
	a := &Asset{Type: AssetTypeLocal, Status: StatusActive}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{}))
	assert.True(t, a.CanConnect(), "本地资产无需 host/port,激活即可连")
}

func TestLocalValidateAllowsEmptyShell(t *testing.T) {
	a := &Asset{Name: "my-shell", Type: AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{}))
	assert.NoError(t, a.Validate(), "shell 可空(运行时按 OS 兜底)")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/model/entity/asset_entity/ -run TestLocal -v`
Expected: FAIL(`AssetTypeLocal` / `LocalConfig` / `SetLocalConfig` 未定义)。

- [ ] **Step 3: 加常量**(`asset.go` 第 16-25 行常量块,在 `AssetTypeEtcd` 后追加)

```go
	AssetTypeSerial   = "serial"
	AssetTypeEtcd     = "etcd"
	AssetTypeLocal    = "local"
```

- [ ] **Step 4: 加 LocalConfig 结构 + PasswordSource**(`asset.go`,放在 `SerialConfig` 定义之后,约第 276 行后)

```go
// LocalConfig 本地终端(local)类型的特定配置。无 host/port/凭证。
type LocalConfig struct {
	Shell string   `json:"shell,omitempty"` // 为空时运行时按 OS 兜底
	Args  []string `json:"args,omitempty"`  // shell 参数
	Cwd   string   `json:"cwd,omitempty"`   // 工作目录
}

// LocalConfig PasswordSource implementation(本地终端无密码,返回空)
func (c *LocalConfig) GetCredentialID() int64 { return 0 }
func (c *LocalConfig) GetPassword() string    { return "" }
```

- [ ] **Step 5: 加充血方法**(`asset.go`,放在 `IsSerial`/`IsEtcd` 附近 + `GetSerialConfig`/`SetSerialConfig` 之后)

```go
// IsLocal 判断是否本地终端类型
func (a *Asset) IsLocal() bool {
	return a.Type == AssetTypeLocal
}

// GetLocalConfig 解析本地终端配置
func (a *Asset) GetLocalConfig() (*LocalConfig, error) {
	if !a.IsLocal() {
		return nil, errors.New("资产不是本地终端类型")
	}
	return jsonfield.Unmarshal[LocalConfig](a.Config, "本地终端配置")
}

// SetLocalConfig 序列化本地终端配置到 Config 字段
func (a *Asset) SetLocalConfig(cfg *LocalConfig) error {
	s, err := jsonfield.Marshal(cfg, "本地终端配置")
	if err != nil {
		return err
	}
	a.Config = s
	return nil
}

// validateLocal 校验本地终端配置(shell 可空,配置只需能解析)
func (a *Asset) validateLocal() error {
	if _, err := a.GetLocalConfig(); err != nil {
		return fmt.Errorf("本地终端配置无效: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: 接入 Validate 分发 switch**(`asset.go` 约第 657-660 行,`case AssetTypeSerial` 后)

```go
	case AssetTypeSerial:
		return a.validateSerial()
	case AssetTypeLocal:
		return a.validateLocal()
	case AssetTypeEtcd:
		return a.validateEtcd()
```

- [ ] **Step 7: 接入 CanConnect switch**(`asset.go` `CanConnect` 内,约第 935 行 `case AssetTypeSerial` 前后,本地恒可连)

```go
	case AssetTypeLocal:
		return true
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/model/entity/asset_entity/ -run TestLocal -v`
Expected: PASS。

- [ ] **Step 9: Commit**

```bash
git add internal/model/entity/asset_entity/asset.go internal/model/entity/asset_entity/asset_local_test.go
git commit -m "✨ asset_entity 增加 local 终端类型 #70"
```

---

## Phase 3 — assettype handler

### Task 3: localHandler

**Files:**
- Create: `internal/assettype/local.go`
- Test: `internal/assettype/local_test.go`

- [ ] **Step 1: 写失败测试 `local_test.go`**

```go
package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalHandler_Registered(t *testing.T) {
	h, ok := Get("local")
	require.True(t, ok, "local handler should be registered in init()")
	assert.Equal(t, "local", h.Type())
	assert.Equal(t, 0, h.DefaultPort())
}

func TestLocalHandler_ValidateCreateArgsAllowsEmpty(t *testing.T) {
	h := &localHandler{}
	assert.NoError(t, h.ValidateCreateArgs(map[string]any{}), "shell 可空")
}

func TestLocalHandler_ApplyCreateArgs(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"shell": "/bin/zsh",
		"args":  []any{"-l"},
		"cwd":   "/tmp",
	})
	require.NoError(t, err)
	cfg, err := a.GetLocalConfig()
	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh", cfg.Shell)
	assert.Equal(t, []string{"-l"}, cfg.Args)
	assert.Equal(t, "/tmp", cfg.Cwd)
}

func TestLocalHandler_ApplyUpdateArgs_PartialFields(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&asset_entity.LocalConfig{Shell: "/bin/bash", Cwd: "/old"}))
	err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"cwd": "/new"})
	require.NoError(t, err)
	cfg, _ := a.GetLocalConfig()
	assert.Equal(t, "/bin/bash", cfg.Shell, "未传字段应保留")
	assert.Equal(t, "/new", cfg.Cwd)
}

func TestLocalHandler_SafeViewNoSecrets(t *testing.T) {
	h := &localHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&asset_entity.LocalConfig{Shell: "/bin/zsh", Cwd: "/tmp"}))
	view := h.SafeView(a)
	require.NotNil(t, view)
	assert.Contains(t, view, "shell")
	assert.Contains(t, view, "cwd")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/assettype/ -run TestLocalHandler -v`
Expected: FAIL(`localHandler` 未定义)。

- [ ] **Step 3: 写 handler `local.go`**(镜像 `serial.go`)

```go
package assettype

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type localHandler struct{}

func init() {
	Register(&localHandler{})
}

func (h *localHandler) Type() string     { return asset_entity.AssetTypeLocal }
func (h *localHandler) DefaultPort() int { return 0 }

func (h *localHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetLocalConfig()
	if err != nil || cfg == nil {
		return nil
	}
	return map[string]any{
		"shell": cfg.Shell,
		"args":  cfg.Args,
		"cwd":   cfg.Cwd,
	}
}

// ResolvePassword 本地终端无密码,返回空。
func (h *localHandler) ResolvePassword(_ context.Context, _ *asset_entity.Asset) (string, error) {
	return "", nil
}

// DefaultPolicy 仅为满足接口;本次不接 AI,策略不参与拦截。
func (h *localHandler) DefaultPolicy() any { return asset_entity.DefaultCommandPolicy() }

// ValidateCreateArgs 本地终端无必填字段(shell 可空,运行时按 OS 兜底)。
func (h *localHandler) ValidateCreateArgs(_ map[string]any) error { return nil }

func (h *localHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	return a.SetLocalConfig(&asset_entity.LocalConfig{
		Shell: ArgString(args, "shell"),
		Args:  ArgStringSlice(args, "args"),
		Cwd:   ArgString(args, "cwd"),
	})
}

func (h *localHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetLocalConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &asset_entity.LocalConfig{}
	}
	if v := ArgString(args, "shell"); v != "" {
		cfg.Shell = v
	}
	if v := ArgStringSlice(args, "args"); v != nil {
		cfg.Args = v
	}
	if v := ArgString(args, "cwd"); v != "" {
		cfg.Cwd = v
	}
	return a.SetLocalConfig(cfg)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/assettype/ -run TestLocalHandler -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/assettype/local.go internal/assettype/local_test.go
git commit -m "✨ 注册 local 资产类型 handler #70"
```

---

## Phase 4 — localterm_svc Manager / Session / Shell 探测

### Task 4: 会话管理器

**Files:**
- Create: `internal/service/localterm_svc/localterm.go`
- Test: `internal/service/localterm_svc/localterm_test.go`

- [ ] **Step 1: 写失败测试 `localterm_test.go`**(注入 fake `ptyProcess`)

```go
package localterm_svc

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProc struct {
	mu        sync.Mutex
	writes    [][]byte
	resizes   [][2]int
	closed    bool
	closeN    int
	readCh    chan []byte // 推送 fake 输出
	readErr   error
}

func newFakeProc() *fakeProc { return &fakeProc{readCh: make(chan []byte, 16)} }

func (p *fakeProc) Read(b []byte) (int, error) {
	chunk, ok := <-p.readCh
	if !ok {
		return 0, io.EOF
	}
	n := copy(b, chunk)
	return n, nil
}

func (p *fakeProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	p.writes = append(p.writes, cp)
	return len(b), nil
}

func (p *fakeProc) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}

func (p *fakeProc) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.closeN++
		close(p.readCh)
	}
	return nil
}

func withFakeStartPTY(t *testing.T, proc *fakeProc) {
	orig := startPTYFn
	startPTYFn = func(ptySpec) (ptyProcess, error) { return proc, nil }
	t.Cleanup(func() { startPTYFn = orig })
}

func TestManagerConnectAndStreamData(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)

	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 7, Cols: 80, Rows: 24})
	require.NoError(t, err)

	got := make(chan []byte, 4)
	mgr.SetCallbacks(sid, func(b []byte) { got <- b }, nil)

	proc.readCh <- []byte("hello")
	select {
	case b := <-got:
		assert.Equal(t, "hello", string(b))
	case <-time.After(time.Second):
		t.Fatal("未收到 onData 回调")
	}
}

func TestSessionWriteAndResize(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 1})
	require.NoError(t, err)
	mgr.SetCallbacks(sid, func([]byte) {}, nil)

	sess, ok := mgr.GetSession(sid)
	require.True(t, ok)
	require.NoError(t, sess.Write([]byte("ls\n")))
	require.NoError(t, sess.Resize(120, 40))

	proc.mu.Lock()
	defer proc.mu.Unlock()
	assert.Equal(t, [][]byte{[]byte("ls\n")}, proc.writes)
	assert.Equal(t, [][2]int{{120, 40}}, proc.resizes)
}

func TestReadEOFTriggersClosedCallback(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 2})
	require.NoError(t, err)

	closed := make(chan string, 1)
	mgr.SetCallbacks(sid, func([]byte) {}, func(s string) { closed <- s })

	// 模拟 shell 退出:关闭 readCh → Read 返回 EOF。
	proc.Close()

	select {
	case s := <-closed:
		assert.Equal(t, sid, s)
	case <-time.After(time.Second):
		t.Fatal("EOF 未触发 onClosed")
	}
	_, ok := mgr.GetSession(sid)
	assert.False(t, ok, "会话应已从 manager 移除")
}

func TestDisconnectClosesProc(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 3})
	require.NoError(t, err)
	mgr.SetCallbacks(sid, func([]byte) {}, nil)

	mgr.Disconnect(sid)
	require.Eventually(t, func() bool {
		proc.mu.Lock()
		defer proc.mu.Unlock()
		return proc.closeN == 1
	}, time.Second, 5*time.Millisecond)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/localterm_svc/ -run 'TestManager|TestSession|TestRead|TestDisconnect' -v`
Expected: FAIL(`NewManager` / `ConnectConfig` 未定义)。

- [ ] **Step 3: 写 Manager/Session `localterm.go`**

```go
package localterm_svc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

var errSessionClosed = errors.New("session is closed")

const callbackSetupGracePeriod = 5 * time.Second

// ConnectConfig 本地终端连接配置。
type ConnectConfig struct {
	AssetID int64
	Shell   string
	Args    []string
	Cwd     string
	Cols    int
	Rows    int
}

// Session 表示一个活跃的本地终端会话。
type Session struct {
	ID      string
	AssetID int64
	proc    ptyProcess

	writeMu sync.Mutex
	mu      sync.Mutex

	closed        bool
	readerStarted bool
	closedCh      chan struct{}
	readerReadyCh chan struct{}

	onData   func(data []byte)
	onClosed func(sessionID string)
}

// Write 向 PTY 写入用户输入。
func (s *Session) Write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	proc := s.proc
	s.mu.Unlock()
	_, err := proc.Write(data)
	return err
}

// Resize 调整 PTY 窗口尺寸。
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	proc := s.proc
	s.mu.Unlock()
	return proc.Resize(cols, rows)
}

func (s *Session) ensureClosedChLocked() chan struct{} {
	if s.closedCh == nil {
		s.closedCh = make(chan struct{})
	}
	return s.closedCh
}

func (s *Session) ensureReaderReadyChLocked() chan struct{} {
	if s.readerReadyCh == nil {
		s.readerReadyCh = make(chan struct{})
	}
	return s.readerReadyCh
}

func (s *Session) closeLocked() (ptyProcess, func(string), string, bool) {
	if s.closed {
		return nil, nil, "", false
	}
	close(s.ensureClosedChLocked())
	s.closed = true
	return s.proc, s.onClosed, s.ID, true
}

// Close 关闭会话(关 PTY + 回调)。
func (s *Session) Close() {
	s.mu.Lock()
	proc, onClosed, sessionID, ok := s.closeLocked()
	s.mu.Unlock()
	if !ok {
		return
	}
	if err := proc.Close(); err != nil {
		logger.Default().Warn("close local pty", zap.String("sessionID", sessionID), zap.Error(err))
	}
	if onClosed != nil {
		go onClosed(sessionID)
	}
}

// IsClosed 是否已关闭。
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Manager 管理所有本地终端会话。
type Manager struct {
	sessions sync.Map // map[string]*Session
	counter  int64
	mu       sync.Mutex
}

// NewManager 创建本地终端会话管理器。
func NewManager() *Manager { return &Manager{} }

// Connect 启动一个本地 shell,返回 sessionID。调用方随后用 SetCallbacks 挂回调。
func (m *Manager) Connect(cfg ConnectConfig) (string, error) {
	proc, err := startPTYFn(ptySpec{
		Shell: cfg.Shell, Args: cfg.Args, Cwd: cfg.Cwd, Cols: cfg.Cols, Rows: cfg.Rows,
	})
	if err != nil {
		return "", fmt.Errorf("start local pty: %w", err)
	}

	m.mu.Lock()
	m.counter++
	sessionID := fmt.Sprintf("local-%d", m.counter)
	m.mu.Unlock()

	sess := &Session{ID: sessionID, AssetID: cfg.AssetID, proc: proc}
	m.sessions.Store(sessionID, sess)
	m.watchCallbackSetup(sess, callbackSetupGracePeriod)

	logger.Default().Info("local terminal started",
		zap.String("sessionID", sessionID), zap.Int64("assetID", cfg.AssetID), zap.String("shell", cfg.Shell))
	return sessionID, nil
}

// SetCallbacks 挂数据/关闭回调,回调就绪后才启动 reader,避免首屏输出丢失。
func (m *Manager) SetCallbacks(sessionID string, onData func([]byte), onClosed func(string)) {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return
	}
	startReader := false
	sess.mu.Lock()
	sess.onData = onData
	sess.onClosed = onClosed
	if !sess.readerStarted && !sess.closed {
		close(sess.ensureReaderReadyChLocked())
		sess.readerStarted = true
		startReader = true
	}
	sess.mu.Unlock()
	if startReader {
		go m.readOutput(sess)
	}
}

func (m *Manager) watchCallbackSetup(sess *Session, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	sess.mu.Lock()
	if sess.closed || sess.readerStarted {
		sess.mu.Unlock()
		return
	}
	readyCh := sess.ensureReaderReadyChLocked()
	closedCh := sess.ensureClosedChLocked()
	sessionID := sess.ID
	sess.mu.Unlock()

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-readyCh:
		case <-closedCh:
		case <-timer.C:
			if _, ok := m.sessions.Load(sessionID); ok {
				logger.Default().Warn("close local session without callbacks",
					zap.String("sessionID", sessionID), zap.Duration("timeout", timeout))
				m.closeSession(sessionID)
			}
		}
	}()
}

// readOutput 持续读 PTY 输出并回调。一次 Read 最多 32KB,天然合并突发输出。
func (m *Manager) readOutput(sess *Session) {
	defer func() {
		m.sessions.Delete(sess.ID)
		sess.Close()
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := sess.proc.Read(buf)
		if n > 0 {
			sess.mu.Lock()
			handler := sess.onData
			sess.mu.Unlock()
			if handler != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				handler(chunk)
			}
		}
		if err != nil {
			return // EOF(shell 退出)或读错误 → 关闭会话
		}
	}
}

// GetSession 获取活跃会话。
func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	sess := v.(*Session)
	if sess.IsClosed() {
		m.sessions.Delete(sessionID)
		return nil, false
	}
	return sess, true
}

// Disconnect 断开会话。
func (m *Manager) Disconnect(sessionID string) { m.closeSession(sessionID) }

// CloseAll 关闭所有会话。
func (m *Manager) CloseAll() {
	var ids []string
	m.sessions.Range(func(k, _ any) bool { ids = append(ids, k.(string)); return true })
	for _, id := range ids {
		m.closeSession(id)
	}
}

func (m *Manager) closeSession(sessionID string) {
	v, ok := m.sessions.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	v.(*Session).Close()
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/service/localterm_svc/ -v`
Expected: PASS(含 Phase 1 的 PTY 测试)。

- [ ] **Step 5: Commit**

```bash
git add internal/service/localterm_svc/localterm.go internal/service/localterm_svc/localterm_test.go
git commit -m "✨ 本地终端会话管理器 #70"
```

### Task 4b: Shell 探测(DetectShells)

**Files:**
- Create: `internal/service/localterm_svc/shells.go`
- Create: `internal/service/localterm_svc/shells_unix.go`
- Create: `internal/service/localterm_svc/shells_windows.go`
- Test: `internal/service/localterm_svc/shells_unix_test.go`

- [ ] **Step 1: 公共类型 `shells.go`**

```go
package localterm_svc

// ShellInfo 描述一个可供用户选择的本地 shell 预设。
// Args 让 "shell+固定参数" 的预设(如 WSL 发行版、Git Bash 登录)可一键选中。
type ShellInfo struct {
	Name string   `json:"name"`           // 展示名,如 "zsh" / "WSL: Ubuntu" / "Git Bash"
	Path string   `json:"path"`           // 可执行文件路径
	Args []string `json:"args,omitempty"` // 启动参数,如 ["-d","Ubuntu"]
}
```

- [ ] **Step 2: Unix 探测 `shells_unix.go`**

```go
//go:build !windows

package localterm_svc

import (
	"bufio"
	"os"
	"strings"
)

// DetectShells 探测本机可用 shell:$SHELL(默认优先)+ /etc/shells(系统权威清单)。
func DetectShells() []ShellInfo {
	seen := map[string]bool{}
	var out []ShellInfo
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			return
		}
		seen[path] = true
		name := path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			name = path[i+1:]
		}
		out = append(out, ShellInfo{Name: name, Path: path})
	}

	add(os.Getenv("SHELL"))

	if f, err := os.Open("/etc/shells"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			add(line)
		}
	}
	return out
}
```

- [ ] **Step 3: Windows 探测 `shells_windows.go`**

```go
//go:build windows

package localterm_svc

import (
	"os"
	"os/exec"
	"strings"
)

// DetectShells 探测本机:pwsh/powershell/cmd + Git Bash + WSL 发行版。
func DetectShells() []ShellInfo {
	var out []ShellInfo

	for _, name := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, ShellInfo{Name: name, Path: p})
		}
	}

	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, ShellInfo{Name: "Git Bash", Path: p, Args: []string{"--login", "-i"}})
			break
		}
	}

	if wsl, err := exec.LookPath("wsl.exe"); err == nil {
		for _, distro := range listWSLDistros(wsl) {
			out = append(out, ShellInfo{Name: "WSL: " + distro, Path: wsl, Args: []string{"-d", distro}})
		}
	}
	return out
}

// listWSLDistros 跑 `wsl -l -q` 列出已装发行版。wsl 输出是 UTF-16LE,
// v1 用"去 NUL + 去 CR + 按行切"解析(ASCII 名字够用;非 ASCII 后续再上正规 UTF-16 解码)。
func listWSLDistros(wslPath string) []string {
	raw, err := exec.Command(wslPath, "-l", "-q").Output()
	if err != nil {
		return nil
	}
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b != 0x00 && b != '\r' {
			cleaned = append(cleaned, b)
		}
	}
	var distros []string
	for _, line := range strings.Split(string(cleaned), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			distros = append(distros, line)
		}
	}
	return distros
}
```

- [ ] **Step 4: Unix 探测测试 `shells_unix_test.go`**

```go
//go:build !windows

package localterm_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectShellsReturnsExistingShells(t *testing.T) {
	shells := DetectShells()
	// /bin/sh 几乎一定在 /etc/shells 或作为兜底存在;至少不应 panic 且项的 Path 非空。
	for _, s := range shells {
		assert.NotEmpty(t, s.Path)
		assert.NotEmpty(t, s.Name)
	}
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/service/localterm_svc/ -run TestDetectShells -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/service/localterm_svc/shells.go internal/service/localterm_svc/shells_unix.go internal/service/localterm_svc/shells_windows.go internal/service/localterm_svc/shells_unix_test.go
git commit -m "✨ 本地 shell 探测(/etc/shells + WSL 枚举) #70"
```

---

## Phase 5 — app/local binder

### Task 5: Wails binder

**Files:**
- Create: `internal/app/local/local.go`
- Create: `internal/app/local/local_ops.go`

> 镜像 `internal/app/serial/{serial.go,serial_ops.go}`。binder 逻辑薄(parse → service → emit),无需单测;由 Phase 9 手动冒烟。

- [ ] **Step 1: 写 binder `local.go`**(镜像 `serial.go`)

```go
// Package local 实现 local binder:本地终端连接、读写、尺寸调整。
package local

import (
	"context"

	"github.com/opskat/opskat/internal/service/localterm_svc"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Local binder。
type Local struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	manager *localterm_svc.Manager
}

// New 构造 local binder。
func New(appCtx context.Context, lang LangProvider, mgr *localterm_svc.Manager) *Local {
	return &Local{appCtx: appCtx, lang: lang, manager: mgr}
}

// Startup 保存 Wails ctx。
func (l *Local) Startup(ctx context.Context) { l.ctx = ctx }

// Cleanup 关闭所有本地终端。
func (l *Local) Cleanup() {
	if l.manager != nil {
		l.manager.CloseAll()
	}
}
```

- [ ] **Step 2: 写 ops `local_ops.go`**(镜像 `serial_ops.go`,事件前缀 `local:`)

```go
package local

import (
	"encoding/base64"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/localterm_svc"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// LocalConnectRequest 前端本地终端连接请求。
type LocalConnectRequest struct {
	AssetID int64 `json:"assetId"`
	Cols    int   `json:"cols"`
	Rows    int   `json:"rows"`
}

// LocalConnectEvent 本地终端异步连接事件。
type LocalConnectEvent struct {
	Type      string `json:"type"`                // "progress" | "connected" | "error"
	Message   string `json:"message,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ConnectLocalAsync 异步启动本地终端,立即返回 connectionId。
func (l *Local) ConnectLocalAsync(req LocalConnectRequest) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(l.ctx, l.lang.Lang()), req.AssetID)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "资产不存在", "asset not found"), err)
	}
	if !asset.IsLocal() {
		return "", fmt.Errorf("%s", i18n.Pick(l.lang.Lang(), "资产不是本地终端类型", "asset is not a local type"))
	}
	cfg, err := asset.GetLocalConfig()
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "解析本地终端配置失败", "parse local config failed"), err)
	}

	connectionID := fmt.Sprintf("local-conn-%d", req.AssetID)
	eventName := "local:connect:" + connectionID
	emit := func(e LocalConnectEvent) { wailsRuntime.EventsEmit(l.ctx, eventName, e) }

	go func() {
		emit(LocalConnectEvent{Type: "progress", Message: i18n.Pick(l.lang.Lang(), "正在启动本地终端...", "Starting local terminal...")})

		sessionID, err := l.manager.Connect(localterm_svc.ConnectConfig{
			AssetID: req.AssetID,
			Shell:   cfg.Shell,
			Args:    cfg.Args,
			Cwd:     cfg.Cwd,
			Cols:    req.Cols,
			Rows:    req.Rows,
		})
		if err != nil {
			emit(LocalConnectEvent{Type: "error", Error: err.Error()})
			return
		}

		l.manager.SetCallbacks(
			sessionID,
			func(data []byte) {
				wailsRuntime.EventsEmit(l.ctx, "local:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
			},
			func(sid string) {
				wailsRuntime.EventsEmit(l.ctx, "local:closed:"+sid, nil)
			},
		)
		emit(LocalConnectEvent{Type: "connected", SessionID: sessionID})
	}()

	return connectionID, nil
}

// WriteLocal 向本地终端写入数据(base64)。
func (l *Local) WriteLocal(sessionID string, dataB64 string) error {
	sess, ok := l.manager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("%s: %s", i18n.Pick(l.lang.Lang(), "本地终端会话不存在", "local session not found"), sessionID)
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.Pick(l.lang.Lang(), "解码数据失败", "decode data failed"), err)
	}
	return sess.Write(data)
}

// ResizeLocalTerminal 调整本地终端尺寸。
func (l *Local) ResizeLocalTerminal(sessionID string, cols, rows int) error {
	sess, ok := l.manager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("%s: %s", i18n.Pick(l.lang.Lang(), "本地终端会话不存在", "local session not found"), sessionID)
	}
	return sess.Resize(cols, rows)
}

// DisconnectLocal 断开本地终端。
func (l *Local) DisconnectLocal(sessionID string) {
	l.manager.Disconnect(sessionID)
}

// ListLocalShells 委托 localterm_svc 探测本机可用 shell(/etc/shells、WSL 发行版等),供前端下拉预设。
func (l *Local) ListLocalShells() ([]localterm_svc.ShellInfo, error) {
	return localterm_svc.DetectShells(), nil
}
```

> ⚠️ `connectionID` 不能用固定字符串——并发/重连会撞。这里用 `local-conn-<assetID>`;若需要严格唯一,加一个 binder 级 `atomic.Int64` 计数器(参考 serial binder 的 `connCounter`)。**实现时改用计数器**:在 `Local` 结构体加 `connCounter atomic.Int64`,`connectionID := fmt.Sprintf("local-conn-%d", l.connCounter.Add(1))`。

- [ ] **Step 3: 按上面 ⚠️ 改用 `connCounter`**(`local.go` 加字段 + import `sync/atomic`;`local_ops.go` 用 `l.connCounter.Add(1)`)

`local.go`:
```go
import (
	"context"
	"sync/atomic"

	"github.com/opskat/opskat/internal/service/localterm_svc"
)

type Local struct {
	appCtx      context.Context
	ctx         context.Context
	lang        LangProvider
	manager     *localterm_svc.Manager
	connCounter atomic.Int64
}
```
`local_ops.go`(替换那一行):
```go
	connectionID := fmt.Sprintf("local-conn-%d", l.connCounter.Add(1))
```

- [ ] **Step 4: 编译**

Run: `go build ./internal/app/local/...`
Expected: 编译通过(此时还没在 main.go 注册,wailsjs 绑定也未生成——下一 Phase)。

- [ ] **Step 5: Commit**

```bash
git add internal/app/local/
git commit -m "✨ local binder(本地终端 IPC) #70"
```

---

## Phase 6 — main.go 接线 + 生成 Wails 绑定

### Task 6: 注册 binder 并生成前端绑定

**Files:**
- Modify: `main.go`
- Generated: `frontend/wailsjs/go/local/Local.{d.ts,js}`(由 wails 生成,勿手写)

- [ ] **Step 1: main.go 构造 manager**(约第 103 行,`serialMgr := serial_svc.NewManager()` 之后)

```go
	serialMgr := serial_svc.NewManager()
	localMgr := localterm_svc.NewManager()
```
并在文件顶部 import 块加 `"github.com/opskat/opskat/internal/service/localterm_svc"` 和 `"github.com/opskat/opskat/internal/app/local"`。

- [ ] **Step 2: 构造 binder**(约第 125 行,`serialB := serial.New(...)` 之后)

```go
	serialB := serial.New(appCtx, sys, serialMgr)
	localB := local.New(appCtx, sys, localMgr)
```

- [ ] **Step 3: 加入 binders 生命周期切片**(约第 138 行)

```go
	binders := []Lifecycle{sys, sshB, queryB, redisB, etcdB, kafkaB, k8sB, serialB, localB, aiB, opsctlB, extB, extEditB}
```

- [ ] **Step 4: 加入 Wails Bind 列表**

找到 `wails.Run(&options.App{...})` 里的 `Bind: []interface{}{...}` 列表(serialB 出现处),在 `serialB` 后加 `localB`。

Run: `grep -n "serialB" main.go`
Expected: 看到 Bind 列表里也有 serialB,在其后补 `localB,`。

- [ ] **Step 5: 生成 Wails 绑定**

Run: `wails generate module` (或 `make dev` 触发一次生成后 Ctrl-C)
Expected: 出现 `frontend/wailsjs/go/local/Local.d.ts` 与 `Local.js`,导出 `ConnectLocalAsync/WriteLocal/ResizeLocalTerminal/DisconnectLocal/ListLocalShells`。

- [ ] **Step 6: 后端整体编译 + 测试**

Run: `go build ./... && go test ./internal/...`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add main.go frontend/wailsjs/go/local/ frontend/wailsjs/go/models.ts
git commit -m "🔧 注册 local binder 并生成前端绑定 #70"
```

---

## Phase 7 — 前端资产类型注册(镜像 serial)

### Task 7: assetTypes 注册 + 表单 + 详情卡 + i18n

**Files:**
- Create: `frontend/src/lib/assetTypes/local.ts`
- Modify: `frontend/src/lib/assetTypes/options.ts`
- Modify: `frontend/src/lib/assetTypes/index.ts`
- Create: `frontend/src/components/asset/detail/LocalDetailInfoCard.tsx`
- Create: `frontend/src/components/asset/LocalConfigSection.tsx`
- Modify: `frontend/src/components/asset/AssetForm.tsx`
- Modify: `frontend/src/i18n/locales/en/common.json`, `frontend/src/i18n/locales/zh-CN/common.json`
- Modify: `frontend/src/lib/assetTypes/__tests__/registry.test.ts`

- [ ] **Step 1: 类型选项 `options.ts`**(在 serial 项之后,约第 83 行后加;顶部 import 加 `SquareTerminal`)

```typescript
  {
    value: "local",
    aliases: ["local", "shell", "terminal"],
    label: "nav.local",
    labelIsI18nKey: true,
    icon: SquareTerminal,
    group: "builtin",
  },
```

- [ ] **Step 2: 注册 `local.ts`**(镜像 `serial.ts`)

```typescript
import { SquareTerminal } from "lucide-react";
import { registerAssetType } from "./registry";
import { LocalDetailInfoCard } from "@/components/asset/detail/LocalDetailInfoCard";

registerAssetType({
  type: "local",
  icon: SquareTerminal,
  canConnect: true,
  canConnectInNewTab: true,
  connectAction: "terminal",
  DetailInfoCard: LocalDetailInfoCard,
  policy: undefined,
});
```
> 对照 `serial.ts` 的 `registerAssetType` 实际字段补齐(尤其 `policy` 的形态;serial 若无策略 UI 就传与 serial 相同的值)。

- [ ] **Step 3: 汇总 import `index.ts`**(serial import 旁)

```typescript
import "./serial";
import "./local";
```

- [ ] **Step 4: 详情卡 `LocalDetailInfoCard.tsx`**(镜像 `SerialDetailInfoCard.tsx`)

```tsx
import { useTranslation } from "react-i18next";

interface LocalConfig {
  shell?: string;
  args?: string[];
  cwd?: string;
}

export function LocalDetailInfoCard({ config }: { config: string }) {
  const { t } = useTranslation();
  let cfg: LocalConfig = {};
  try {
    cfg = JSON.parse(config || "{}");
  } catch {
    /* ignore */
  }
  return (
    <div className="space-y-1 text-sm">
      <div>
        <span className="text-muted-foreground">{t("asset.localShell")}: </span>
        {cfg.shell || t("asset.localDefaultShell")}
      </div>
      {cfg.cwd && (
        <div>
          <span className="text-muted-foreground">{t("asset.localCwd")}: </span>
          {cfg.cwd}
        </div>
      )}
    </div>
  );
}
```
> 对照 `SerialDetailInfoCard.tsx` 的 props 签名(是 `{ config }` 还是 `{ asset }`)调整。

- [ ] **Step 5: 表单段 `LocalConfigSection.tsx`**(镜像 `SerialConfigSection.tsx`,用 `ListLocalShells`)

```tsx
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
import { ListLocalShells } from "../../../wailsjs/go/local/Local";

interface ShellInfo {
  name: string;
  path: string;
  args?: string[];
}

interface Props {
  shell: string;
  setShell: (v: string) => void;
  args: string;
  setArgs: (v: string) => void;
  cwd: string;
  setCwd: (v: string) => void;
}

export function LocalConfigSection({ shell, setShell, args, setArgs, cwd, setCwd }: Props) {
  const { t } = useTranslation();
  const [shells, setShells] = useState<ShellInfo[]>([]);

  useEffect(() => {
    ListLocalShells()
      .then((list) => setShells((list as ShellInfo[]) || []))
      .catch(() => setShells([]));
  }, []);

  // 探测下拉是"快速填充"动作:选中即把 shell/args 两个可编辑框一起填好。
  // value 用索引(WSL 多个发行版共用 wsl.exe 路径,path 不唯一)。
  const onSelectPreset = (val: string) => {
    if (val === "__default__") {
      setShell("");
      setArgs("");
      return;
    }
    const s = shells[Number(val)];
    if (s) {
      setShell(s.path);
      setArgs((s.args || []).join(" "));
    }
  };

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label>{t("asset.localShell")}</Label>
        <Select onValueChange={onSelectPreset}>
          <SelectTrigger>
            <SelectValue placeholder={t("asset.localShellPreset")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__default__">{t("asset.localDefaultShell")}</SelectItem>
            {shells.map((s, i) => (
              <SelectItem key={`${s.path}-${i}`} value={String(i)}>
                {s.name}
                {s.args && s.args.length ? ` (${s.path} ${s.args.join(" ")})` : ` (${s.path})`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input value={shell} onChange={(e) => setShell(e.target.value)} placeholder={t("asset.localShellPlaceholder")} />
      </div>
      <div className="space-y-2">
        <Label>{t("asset.localArgs")}</Label>
        <Input value={args} onChange={(e) => setArgs(e.target.value)} placeholder="-l" />
      </div>
      <div className="space-y-2">
        <Label>{t("asset.localCwd")}</Label>
        <Input value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="/home/user" />
      </div>
    </div>
  );
}
```
> 对照 `SerialConfigSection.tsx` 实际使用的 `@opskat/ui` 组件名/导入路径校正。下拉是预设快填,选中后 shell/args 仍可手改;`args` 用空格分隔,保存时 split 成数组。`ListLocalShells` 返回的是 wails 生成的 `localterm_svc.ShellInfo`,这里用本地 `ShellInfo` 接口收下即可。

- [ ] **Step 6: AssetForm 接线**(`AssetForm.tsx`)

6a. import(第 48 行 SerialConfigSection 旁):
```typescript
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";
```
6b. AssetType union(第 207 行)加 `"local"`:
```typescript
type AssetType = "ssh" | "database" | "redis" | "mongodb" | "kafka" | "k8s" | "serial" | "etcd" | "local" | (string & {});
```
6c. 状态(serial 字段旁,第 426-432 行附近):
```typescript
const [localShell, setLocalShell] = useState("");
const [localArgs, setLocalArgs] = useState("");
const [localCwd, setLocalCwd] = useState("");
```
6d. loadConfig(serial 的 `loadSerialConfig` 旁):
```typescript
const loadLocalConfig = (asset: asset_entity.Asset) => {
  try {
    const cfg = JSON.parse(asset.Config || "{}");
    setLocalShell(cfg.shell || "");
    setLocalArgs(Array.isArray(cfg.args) ? cfg.args.join(" ") : "");
    setLocalCwd(cfg.cwd || "");
  } catch {
    setLocalShell("");
    setLocalArgs("");
    setLocalCwd("");
  }
};
```
找到按 `assetType` 调用 `loadSerialConfig` 的分发处,补 `else if (asset.Type === "local") loadLocalConfig(asset);`。
6e. 保存(第 1547-1556 行 serial 分支旁):
```typescript
} else if (assetType === "local") {
  const localConfig: Record<string, unknown> = {};
  if (localShell) localConfig.shell = localShell;
  const argList = localArgs.trim().split(/\s+/).filter(Boolean);
  if (argList.length) localConfig.args = argList;
  if (localCwd) localConfig.cwd = localCwd;
  config = JSON.stringify(localConfig);
```
6f. 渲染(第 2091-2102 行 SerialConfigSection 旁):
```tsx
{assetType === "local" && (
  <LocalConfigSection
    shell={localShell}
    setShell={setLocalShell}
    args={localArgs}
    setArgs={setLocalArgs}
    cwd={localCwd}
    setCwd={setLocalCwd}
  />
)}
```

- [ ] **Step 7: i18n**(两个 common.json 都加)

`nav` 段:
```json
"local": "Local Terminal"   // zh-CN: "本地终端"
```
`asset` 段:
```json
"localShell": "Shell",                         // zh-CN: "Shell"
"localShellPreset": "Detected shells…",        // zh-CN: "已探测到的 shell…"
"localShellPlaceholder": "e.g. /bin/zsh, pwsh.exe",  // zh-CN: "如 /bin/zsh、pwsh.exe"
"localDefaultShell": "System default",         // zh-CN: "系统默认"
"localArgs": "Arguments",                       // zh-CN: "启动参数"
"localCwd": "Working Directory"                 // zh-CN: "工作目录"
```

- [ ] **Step 8: 扩展注册表测试 `registry.test.ts`**

在 builtin 列表断言里加入 `local`;新增:
```typescript
expect(getAssetType("local")!.connectAction).toBe("terminal");
```

- [ ] **Step 9: 跑前端测试 + lint**

Run: `cd frontend && pnpm test -- registry && pnpm lint`
Expected: PASS。

- [ ] **Step 10: Commit**

```bash
git add frontend/src/lib/assetTypes/ frontend/src/components/asset/ frontend/src/i18n/
git commit -m "✨ 前端注册 local 资产类型与表单 #70"
```

---

## Phase 8 — 前端终端 transport 映射表重构

### Task 8: 把 isSerial 二元分支收敛成 TRANSPORTS 表,并接入 local

**Files:**
- Modify: `frontend/src/stores/terminalStore.ts`
- Modify: `frontend/src/components/terminal/terminalRegistry.ts`
- Modify: `frontend/src/components/terminal/Terminal.tsx`
- Modify: `frontend/src/__tests__/terminalRegistry.test.ts`

- [ ] **Step 1: 写/扩展失败测试 `terminalRegistry.test.ts`**

补一个断言:`local` transport 解析出的 eventPrefix 为 `local`、writeFn 为 `WriteLocal`。若现有测试通过 mock wails 模块断言 write 调用,则新增 local 用例;并 import `TRANSPORTS` 断言三键齐全:
```typescript
import { TRANSPORTS } from "@/stores/terminalStore";

it("TRANSPORTS 覆盖 ssh/serial/local 且字段齐全", () => {
  for (const key of ["ssh", "serial", "local"] as const) {
    const t = TRANSPORTS[key];
    expect(t.eventPrefix).toBe(key);
    expect(typeof t.write).toBe("function");
    expect(typeof t.resize).toBe("function");
    expect(typeof t.connectAsync).toBe("function");
    expect(typeof t.disconnect).toBe("function");
    expect(typeof t.canSplit).toBe("boolean");
  }
  expect(TRANSPORTS.ssh.canSplit).toBe(true);
  expect(TRANSPORTS.serial.canSplit).toBe(false);
  expect(TRANSPORTS.local.canSplit).toBe(false);
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && pnpm test -- terminalRegistry`
Expected: FAIL(`TRANSPORTS` 未导出)。

- [ ] **Step 3: 在 `terminalStore.ts` 顶部建 TRANSPORTS 表**

3a. import(第 13 行 serial import 旁):
```typescript
import { WriteLocal, ConnectLocalAsync, DisconnectLocal, ResizeLocalTerminal } from "../../wailsjs/go/local/Local";
import { ResizeSerialTerminal } from "../../wailsjs/go/serial/Serial";
import { ResizeSSH } from "../../wailsjs/go/ssh/SSH";
```
3b. 改类型 + 建表(替换第 26 行 `export type TerminalTransport = "ssh" | "serial";` 起到 `writeSessionInput` 结束,即第 26-44 行):
```typescript
export type TerminalTransport = "ssh" | "serial" | "local";

interface TransportSpec {
  connectAsync: (assetId: number, opts: { cols: number; rows: number; password: string }) => Promise<string>;
  write: (sessionId: string, dataB64: string) => Promise<void>;
  resize: (sessionId: string, cols: number, rows: number) => Promise<void>;
  disconnect: (sessionId: string) => void;
  eventPrefix: string;
  canSplit: boolean;
  hasDirectorySync: boolean; // 仅 ssh 有 cwd 跟随
}

export const TRANSPORTS: Record<TerminalTransport, TransportSpec> = {
  ssh: {
    connectAsync: (assetId, { cols, rows, password }) =>
      ConnectSSHAsync(new ssh_models.SSHConnectRequest({ assetId, password, key: "", cols, rows })),
    write: WriteSSH,
    resize: ResizeSSH,
    disconnect: DisconnectSSH,
    eventPrefix: "ssh",
    canSplit: true,
    hasDirectorySync: true,
  },
  serial: {
    connectAsync: (assetId) => ConnectSerialAsync({ assetId }),
    write: WriteSerial,
    resize: ResizeSerialTerminal,
    disconnect: DisconnectSerial,
    eventPrefix: "serial",
    canSplit: false,
    hasDirectorySync: false,
  },
  local: {
    connectAsync: (assetId, { cols, rows }) => ConnectLocalAsync({ assetId, cols, rows }),
    write: WriteLocal,
    resize: ResizeLocalTerminal,
    disconnect: DisconnectLocal,
    eventPrefix: "local",
    canSplit: false,
    hasDirectorySync: false,
  },
};

export function transportForAsset(assetType: string): TerminalTransport {
  if (assetType === "serial") return "serial";
  if (assetType === "local") return "local";
  return "ssh";
}

function inferTransportFromSessionId(sessionId: string): TerminalTransport {
  if (sessionId.startsWith("serial-")) return "serial";
  if (sessionId.startsWith("local-")) return "local";
  return "ssh";
}

function disconnectSession(sessionId: string, transport: TerminalTransport): void {
  TRANSPORTS[transport].disconnect(sessionId);
}

function writeSessionInput(sessionId: string, transport: TerminalTransport, dataB64: string): Promise<void> {
  return TRANSPORTS[transport].write(sessionId, dataB64);
}
```

- [ ] **Step 4: 改 `ConnectionState.transport` 类型**(第 110 行)

```typescript
  transport: TerminalTransport;
```

- [ ] **Step 5: 改 `setupConnectionListener` 形参类型**(第 249 行)

```typescript
  transport: TerminalTransport = "ssh"
```

- [ ] **Step 6: 重写 `connect()` 的 transport 分支**(第 444-568 行)

把 `const isSerial = asset.Type === "serial";` 改成:
```typescript
      const transport = transportForAsset(asset.Type);
      const spec = TRANSPORTS[transport];
```
连接调用(第 448-459 行)替换为:
```typescript
      connectionId = await spec.connectAsync(assetId, { cols: 80, rows: 24, password });
```
其余把 `isSerial ? "serial" : "ssh"` 全部替换为 `transport`;`if (!isSerial) registerSessionSyncListener(...)`(第 546、689 行)改为 `if (spec.hasDirectorySync) registerSessionSyncListener(...)`;pendingInput 写入的 `isSerial ? "serial" : "ssh"`(第 554 行)改 `transport`。

- [ ] **Step 7: 重写 `reconnect()` 的 transport 分支**(第 594-695 行)

```typescript
    const asset = useAssetStore.getState().assets.find((a) => a.ID === meta.assetId);
    const transport: TerminalTransport =
      pane?.transport ?? (asset ? transportForAsset(asset.Type) : inferTransportFromSessionId(sessionId));
    const spec = TRANSPORTS[transport];
```
连接调用(第 604-614 行)替换为:
```typescript
    const connectPromise = spec.connectAsync(meta.assetId, { cols: 80, rows: 24, password: "" });
```
其余 `isSerial ? "serial" : "ssh"` → `transport`;`if (!isSerial) registerSessionSyncListener(...)`(第 689 行)→ `if (spec.hasDirectorySync) ...`;`setupConnectionListener(..., isSerial ? "serial" : "ssh")`(第 694 行)→ `..., transport`。

- [ ] **Step 8: splitPane 守卫改用 canSplit**(第 892 行)

```typescript
    const activeTransport = data.panes[data.activePaneId]?.transport ?? "ssh";
    if (!TRANSPORTS[activeTransport].canSplit) return;
```

- [ ] **Step 9: terminalRegistry.ts 改用 TRANSPORTS**

替换第 6-7 行 import 为:
```typescript
import { TRANSPORTS, type TerminalTransport } from "@/stores/terminalStore";
```
替换第 40 行 `transport?: "ssh" | "serial";` 为 `transport?: TerminalTransport;`。
替换第 71-73 行为:
```typescript
  const transport: TerminalTransport =
    init.transport ?? (sessionId.startsWith("serial-") ? "serial" : sessionId.startsWith("local-") ? "local" : "ssh");
  const spec = TRANSPORTS[transport];
  const writeFn = spec.write;
  const eventPrefix = spec.eventPrefix;
```

- [ ] **Step 10: Terminal.tsx 改用 TRANSPORTS**

替换第 5-7 行 import 为:
```typescript
import { useTerminalStore, TRANSPORTS } from "@/stores/terminalStore";
```
(删掉 `WriteSSH/WriteSerial/ResizeSerialTerminal/ResizeSSH` 的直接 import;注意第 9 行原本就 import 了 `useTerminalStore`,合并为一行)
删除第 65 行 `const isSerial = transport === "serial";`。
第 82-83 行 paste:
```typescript
        const writeFn = TRANSPORTS[transport].write;
```
第 121-122、149-150 行 resize:
```typescript
        const resizeFn = TRANSPORTS[transport].resize;
```
第 252 行 split 菜单 disabled:
```tsx
<ContextMenuItem onClick={() => splitPane(tabId, "horizontal")} disabled={!paneConnected || !TRANSPORTS[transport].canSplit}>
```
(同理处理纵向 split 那一项)
更新这些 `useCallback`/`useEffect` 的依赖数组:把 `isSerial` 换成 `transport`。

- [ ] **Step 11: 跑前端测试 + lint + 类型检查**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build`
Expected: PASS(`pnpm build` 含 tsc 类型检查,确保没有遗漏的 `isSerial`)。

- [ ] **Step 12: Commit**

```bash
git add frontend/src/stores/terminalStore.ts frontend/src/components/terminal/ frontend/src/__tests__/terminalRegistry.test.ts
git commit -m "♻️ 终端 transport 收敛为映射表并接入 local #70"
```

---

## Phase 9 — 集成验证

### Task 9: 全量校验 + 手动冒烟

**Files:** 无(纯验证)

- [ ] **Step 1: 后端全量**

Run: `make test && make lint`
Expected: PASS。

- [ ] **Step 2: 前端全量**

Run: `cd frontend && pnpm test && pnpm lint && pnpm build`
Expected: PASS。

- [ ] **Step 3: 手动冒烟(mac/linux)**

Run: `make dev`
操作:新建资产 → 类型选「本地终端」→ shell 留空(系统默认)→ 保存 → 双击打开 → 应进入本机 shell。验证:
- 提示符正常,能跑 `ls` / `echo $SHELL`;
- `vim` 全屏后改窗口大小,内容随之 reflow(Resize 生效);
- 输入 `exit` → 终端显示会话已关闭、可按 Enter 重连(重新 spawn);
- 右键菜单「分屏」对本地终端为禁用(canSplit=false);
- ssh/serial 资产打开终端行为无回归(连接、写入、resize、ssh 分屏)。

- [ ] **Step 4: (有 Windows 环境时)冒烟**

打开本地终端 → 默认应进 PowerShell;跑 `dir`;改窗口大小验证 Resize;`exit` 验证关闭/重连。若系统 < Win10 1809,应看到明确错误 toast 而非崩溃。

- [ ] **Step 5: 自检 — 残留 isSerial 扫描**

Run: `grep -rn "isSerial\|=== \"serial\"" frontend/src --include=*.ts --include=*.tsx`
Expected: 仅剩注释或 `transportForAsset`/`inferTransportFromSessionId` 内的合法判断;无散落的能力分支。若有遗漏,补成 `TRANSPORTS[...]` 查表。

- [ ] **Step 6: 最终提交(如有零碎修复)**

```bash
git add -A
git commit -m "✅ 本地终端资产集成校验与收尾 #70"
```

---

## Self-Review 记录

- **Spec 覆盖**:PTY 层(Task1)、实体(Task2)、handler(Task3)、service(Task4)、shell 探测/WSL 枚举(Task4b)、binder(Task5)、wiring(Task6)、前端类型注册+表单预设(Task7)、transport 重构(Task8)、验证(Task9)——覆盖 spec §3–§8 全部条目。免迁移(§6)已确认无 schema 改动。
- **作用域**:不接 AI(无 `internal/ai` 改动)、local 不支持 split(v1)、`LocalConfig={Shell,Args,Cwd}`——与 spec 一致。
- **类型一致**:`ptyProcess{Read,Write,Resize,Close}`、`ConnectConfig{AssetID,Shell,Args,Cwd,Cols,Rows}`、`TransportSpec{connectAsync,write,resize,disconnect,eventPrefix,canSplit,hasDirectorySync}` 在各 Task 间一致。
- **已知需在实现时核对**:① conpty v0.1.4 实际 API(Task1 Step4);② `registerAssetType`/`SerialConfigSection`/`SerialDetailInfoCard` 的真实字段与 props(Task7);③ `AssetForm.tsx` 行号会随改动漂移,按"serial 旁"定位而非死认行号。
