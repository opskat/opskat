# VNC 会话传输改走 Wails IPC 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 VNC(`remote_desktop`)会话字节从「Go 起 loopback WebSocket 监听、noVNC 连 `ws://127.0.0.1`」改为「Go 不开任何监听端口,字节走 Wails IPC,noVNC 通过自定义 WS 形状 channel 经 `attach()` 接入」。

**Architecture:** 后端 service 层删掉 loopback WS proxy,`Session` 直接持 `net.Conn`(经 proxychain 拨号),暴露回调注册(`SetCallbacks`)+ 写入(`Write`);Wails `EventsEmit` 只在 binding 层出现,镜像本地终端 `local_ops.go` 的模式。前端新增一个隔离可单测的 `WailsRfbChannel`,把 Go→FE 的 `remote_desktop:data` 事件喂给 `onmessage`、把 noVNC 的 `send` 转成 `WriteRemoteDesktop`。两阶段连接(先订阅事件、后启动读 pump)保证不丢 RFB 握手首包。

**Tech Stack:** Go 1.26 + Wails v2(`wailsRuntime.EventsEmit`)、React 19 + noVNC 1.5.0(`RFB` + `Websock.attach`)、Go test(`net.Pipe`)、vitest(happy-dom)。

## Global Constraints

- **事件名**:Go→FE 数据 `remote_desktop:data:<sessionID>`、关闭 `remote_desktop:closed:<sessionID>`(与 `local:data:` 同风格)。逐字使用。
- **分层**:`internal/service/remote_desktop_svc/` **不得** import Wails runtime;`wailsRuntime.EventsEmit` 只出现在 `internal/app/remote_desktop/`。
- **不引入**:`SessionTransport` 接口 / 注册、WS fallback、背压 / credit-ack 协议(单实现,YAGNI)。
- **不动**:RDP、凭据解析、`proxychain`、指纹校验、file(SFTP)面板、剪贴板 GBK 编解码。
- **生成物**:`frontend/wailsjs/` 是 gitignore 生成物;后端加绑定方法后必须跑 `wails generate module` 才能在前端 import。
- **后端验证用 `golangci-lint`**(非 `go vet`);测试用 `make test` / `go test`。前端测试 `cd frontend && pnpm test`。

---

### Task 1: 后端 service —— 用 net.Conn + 读 pump + 回调替换 loopback WS proxy

**Files:**
- Modify: `internal/service/remote_desktop_svc/manager.go`(删除 `tcpWebSocketProxy` 全块及 `ConnectOptions`、`Session.WebSocketURL`;`Session` 改持 `conn`+回调;新增 `SetCallbacks`/`Write`)
- Test: `internal/service/remote_desktop_svc/manager_test.go`(新建)

**Interfaces:**
- Produces:
  - `func (m *Manager) Connect(ctx context.Context, assetID int64) (*Session, error)`(去掉 `ConnectOptions` 参数)
  - `func (m *Manager) SetCallbacks(sessionID string, onData func([]byte), onClose func()) error`
  - `func (m *Manager) Write(sessionID string, data []byte) error`
  - `Session` 不再有 `WebSocketURL` 字段;JSON 序列化字段为 `id/assetId/assetType/assetName/username/password/fileSshAssetId/fileEnabled/fileStatus/status`
  - 包内(测试可见):`(s *Session) start(onData, onClose)`、`(s *Session) write([]byte) error`、`(s *Session) close()`、字段 `conn net.Conn`

- [ ] **Step 1: 写失败测试** —— `internal/service/remote_desktop_svc/manager_test.go`(新建)

```go
package remote_desktop_svc

import (
	"io"
	"net"
	"testing"
	"time"
)

// 用 net.Pipe 注入假 conn(client 端给 Session,server 端扮演 VNC 服务器),
// 验证:server 写入 → onData 收到;Session.write → server 读到;close → onClose。
func TestSessionPumpForwardsAndWrites(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()

	s := &Session{ID: "t1", conn: client}
	got := make(chan []byte, 1)
	closed := make(chan struct{})
	s.start(func(b []byte) { got <- b }, func() { close(closed) })

	// server → client(session):onData 应收到相同字节
	go func() { _, _ = server.Write([]byte("RFB 003.008\n")) }()
	select {
	case b := <-got:
		if string(b) != "RFB 003.008\n" {
			t.Fatalf("onData = %q, want RFB greeting", b)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onData")
	}

	// session.write → server 应读到相同字节(net.Pipe 同步,需并发读)
	readBack := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(server, buf)
		readBack <- string(buf[:n])
	}()
	if err := s.write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v := <-readBack; v != "hello" {
		t.Fatalf("server read = %q, want hello", v)
	}

	// close 关闭 conn → 读 pump 退出 → onClose 触发一次
	s.close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onClose after close")
	}
}

func TestManagerWriteUnknownSession(t *testing.T) {
	m := &Manager{sessions: map[string]*Session{}}
	if err := m.Write("nope", []byte("x")); err == nil {
		t.Fatal("expected error for unknown session")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/remote_desktop_svc/ -run 'TestSessionPump|TestManagerWriteUnknown' -v`
Expected: 编译失败 —— `s.start` / `s.write` / `Session.conn` 未定义(当前 `Session` 还是 `proxy` 版)。这就是「先失败」的正确状态。

- [ ] **Step 3: 重写 manager.go**

把 `internal/service/remote_desktop_svc/manager.go` 整体替换为下面内容(删除 `tcpWebSocketProxy`、`ConnectOptions`、`WebSocketURL`、`net/http`/`coder/websocket`/`time` 依赖):

```go
package remote_desktop_svc

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"go.uber.org/zap"
)

type Manager struct {
	assetRepo asset_repo.AssetRepo
	resolver  *credential_resolver.Resolver

	mu       sync.Mutex
	sessions map[string]*Session
}

type Session struct {
	ID             string `json:"id"`
	AssetID        int64  `json:"assetId"`
	AssetType      string `json:"assetType"`
	AssetName      string `json:"assetName"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	FileSSHAssetID int64  `json:"fileSshAssetId"`
	FileEnabled    bool   `json:"fileEnabled"`
	FileStatus     string `json:"fileStatus"`
	Status         string `json:"status"`

	conn      net.Conn
	onData    func([]byte)
	onClose   func()
	startOnce sync.Once
	closeOnce sync.Once
}

func NewManager(repo asset_repo.AssetRepo) *Manager {
	if repo == nil {
		repo = asset_repo.Asset()
	}
	return &Manager{
		assetRepo: repo,
		resolver:  credential_resolver.Default(),
		sessions:  make(map[string]*Session),
	}
}

func (m *Manager) Connect(ctx context.Context, assetID int64) (*Session, error) {
	logger.Ctx(ctx).Info("remote desktop connect start", zap.Int64("assetID", assetID))
	session, err := m.connect(ctx, assetID)
	if err != nil {
		logger.Ctx(ctx).Error("remote desktop connect failed", zap.Int64("assetID", assetID), zap.Error(err))
		return nil, err
	}
	logger.Ctx(ctx).Info("remote desktop connected",
		zap.Int64("assetID", assetID), zap.String("sessionID", session.ID))
	return session, nil
}

func (m *Manager) connect(ctx context.Context, assetID int64) (*Session, error) {
	asset, err := m.assetRepo.Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("读取远程桌面资产失败: %w", err)
	}
	if asset.Type != asset_entity.AssetTypeVNC {
		return nil, fmt.Errorf("资产不是远程桌面类型: %s", asset.Type)
	}
	return m.connectVNC(ctx, asset)
}

func (m *Manager) connectVNC(ctx context.Context, asset *asset_entity.Asset) (*Session, error) {
	cfg, err := asset.GetVNCConfig()
	if err != nil {
		return nil, err
	}
	password, err := m.resolver.ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, err
	}
	layers, err := m.resolver.ResolveProxyChain(ctx, cfg.ProxyChain, 5)
	if err != nil {
		return nil, err
	}
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (proxychain.Chain{Layers: layers}).Dial(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("连接 VNC 目标失败: %w", err)
	}
	session := &Session{
		ID:             uuid.NewString(),
		AssetID:        asset.ID,
		AssetType:      asset.Type,
		AssetName:      asset.Name,
		Username:       cfg.Username,
		Password:       password,
		FileSSHAssetID: cfg.FileSSHAssetID,
		FileEnabled:    cfg.FileSSHAssetID > 0,
		FileStatus:     fileStatus(cfg.FileSSHAssetID),
		Status:         "connecting",
		conn:           conn,
	}
	m.store(session)
	return session, nil
}

func (m *Manager) store(session *Session) {
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
}

// SetCallbacks 挂上 Go→FE 的数据/关闭回调,并启动读 pump(幂等)。sessionID 不存在返回错误。
func (m *Manager) SetCallbacks(sessionID string, onData func([]byte), onClose func()) error {
	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("远程桌面会话不存在: %s", sessionID)
	}
	session.start(onData, onClose)
	return nil
}

// Write 把前端(noVNC)发来的字节写入目标连接。
func (m *Manager) Write(sessionID string, data []byte) error {
	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("远程桌面会话不存在: %s", sessionID)
	}
	return session.write(data)
}

func (m *Manager) Disconnect(sessionID string) {
	m.mu.Lock()
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if session != nil {
		session.close()
		// Disconnect 从 Wails 绑定调用,无 ctx,用默认 logger 记录会话关闭。
		logger.Default().Info("remote desktop session closed", zap.String("sessionID", sessionID))
	}
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

func (s *Session) start(onData func([]byte), onClose func()) {
	s.startOnce.Do(func() {
		s.onData = onData
		s.onClose = onClose
		go s.readPump()
	})
}

func (s *Session) readPump() {
	buf := make([]byte, 32768)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 && s.onData != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.onData(chunk)
		}
		if err != nil {
			break
		}
	}
	if s.onClose != nil {
		s.onClose()
	}
	s.close()
}

func (s *Session) write(data []byte) error {
	if s.conn == nil {
		return fmt.Errorf("远程桌面会话未建立连接")
	}
	_, err := s.conn.Write(data)
	return err
}

func (s *Session) close() {
	s.closeOnce.Do(func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}

func fileStatus(id int64) string {
	if id > 0 {
		return "已启用 SSH/SFTP 文件通道"
	}
	return "未配置 SSH/SFTP 文件通道，文件上传下载不可用"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/service/remote_desktop_svc/ -v`
Expected: PASS(含既有 `vnc_conntest_test.go` 全部通过)。

- [ ] **Step 5: lint**

Run: `golangci-lint run --timeout 10m ./internal/service/remote_desktop_svc/...`
Expected: 无 issue(尤其确认 `net/http`、`coder/websocket`、`time` 已不再 import,无 unused)。

- [ ] **Step 6: 提交**

```bash
git add internal/service/remote_desktop_svc/manager.go internal/service/remote_desktop_svc/manager_test.go
git commit -m "♻️ VNC 会话去掉 loopback WS proxy，Session 直接持 net.Conn"
```

---

### Task 2: 后端 binding —— StartRemoteDesktopStream + WriteRemoteDesktop,并重新生成绑定

**Files:**
- Modify: `internal/app/remote_desktop/remote_desktop.go`(`ConnectRemoteDesktop` 去掉 `ConnectOptions{}`;新增 `StartRemoteDesktopStream`、`WriteRemoteDesktop`)
- Test: `internal/app/remote_desktop/remote_desktop_test.go`(追加 2 个用例)

**Interfaces:**
- Consumes(来自 Task 1):`manager.Connect(ctx, id)`、`manager.SetCallbacks(id, onData, onClose) error`、`manager.Write(id, data) error`
- Produces(Wails 绑定,前端 import):
  - `StartRemoteDesktopStream(sessionID string) error`
  - `WriteRemoteDesktop(sessionID string, dataB64 string) error`
  - `ConnectRemoteDesktop(assetID int64) (*Session, error)`(签名不变,内部去掉 options)

- [ ] **Step 1: 写失败测试** —— 追加到 `internal/app/remote_desktop/remote_desktop_test.go`

在文件末尾追加(顶部 import 需要 `go.uber.org/mock/gomock`、`mock_asset_repo`、`remote_desktop_svc`):

```go
func newTestRemoteDesktop(t *testing.T) *RemoteDesktop {
	ctrl := gomock.NewController(t)
	mgr := remote_desktop_svc.NewManager(mock_asset_repo.NewMockAssetRepo(ctrl))
	return &RemoteDesktop{manager: mgr}
}

func TestWriteRemoteDesktopRejectsInvalidBase64(t *testing.T) {
	rd := newTestRemoteDesktop(t)
	if err := rd.WriteRemoteDesktop("s", "not@@base64"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestWriteRemoteDesktopUnknownSession(t *testing.T) {
	rd := newTestRemoteDesktop(t)
	if err := rd.WriteRemoteDesktop("missing", "aGVsbG8="); err == nil {
		t.Fatal("expected unknown-session error")
	}
}
```

对应把文件顶部 import 改为:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/remote_desktop_svc"
)
```

> 说明:`StartRemoteDesktopStream` 的 `EventsEmit` 走 Wails runtime,无法脱离 GUI 单测,放到 Task 5 真机烟测里用日志观测;这里只覆盖 `WriteRemoteDesktop` 的 base64 解码 + 会话查找两条边界。既有 `TestEncodeVNCClipboardText...` 用例保留。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/app/remote_desktop/ -run TestWriteRemoteDesktop -v`
Expected: 编译失败 —— `rd.WriteRemoteDesktop` 未定义。

- [ ] **Step 3: 实现绑定方法**

编辑 `internal/app/remote_desktop/remote_desktop.go`:

顶部 import 增加 `"encoding/base64"` 和 `wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"`。

把 `ConnectRemoteDesktop` 改为(去掉 `ConnectOptions{}`):

```go
func (r *RemoteDesktop) ConnectRemoteDesktop(assetID int64) (*remote_desktop_svc.Session, error) {
	return r.manager.Connect(r.ctx, assetID)
}
```

在 `DisconnectRemoteDesktop` 附近新增:

```go
// StartRemoteDesktopStream 挂上 IPC 回调并启动读 pump。前端必须在 EventsOn 订阅
// remote_desktop:data/closed 之后再调,保证不丢 RFB 握手首包。
func (r *RemoteDesktop) StartRemoteDesktopStream(sessionID string) error {
	return r.manager.SetCallbacks(
		sessionID,
		func(data []byte) {
			wailsRuntime.EventsEmit(r.ctx, "remote_desktop:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
		},
		func() {
			wailsRuntime.EventsEmit(r.ctx, "remote_desktop:closed:"+sessionID, nil)
		},
	)
}

// WriteRemoteDesktop 把前端(noVNC)发来的 base64 字节写入目标连接。
func (r *RemoteDesktop) WriteRemoteDesktop(sessionID, dataB64 string) error {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("解码远程桌面数据失败: %w", err)
	}
	return r.manager.Write(sessionID, data)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/app/remote_desktop/ -v`
Expected: PASS。

- [ ] **Step 5: lint**

Run: `golangci-lint run --timeout 10m ./internal/app/remote_desktop/...`
Expected: 无 issue。

- [ ] **Step 6: 重新生成 Wails 绑定**

Run: `wails generate module`
Expected: 成功;`frontend/wailsjs/go/remote_desktop/RemoteDesktop.d.ts` / `.js` 里出现 `StartRemoteDesktopStream`、`WriteRemoteDesktop`,且 `ConnectRemoteDesktop` 返回类型不再含 `webSocketUrl`。

> `wails generate module` 需要后端能编译(Task 1、2 已完成)。若本机未装 wails CLI:`go install github.com/wailsapp/wails/v2/cmd/wails@latest`。

- [ ] **Step 7: 提交**

```bash
git add internal/app/remote_desktop/remote_desktop.go internal/app/remote_desktop/remote_desktop_test.go
git commit -m "✨ 远程桌面新增 StartRemoteDesktopStream/WriteRemoteDesktop IPC 绑定"
```

---

### Task 3: 前端 WailsRfbChannel(隔离可单测)+ setup.ts 补 binder mock

**Files:**
- Create: `frontend/src/lib/wailsRfbChannel.ts`
- Test: `frontend/src/lib/wailsRfbChannel.test.ts`
- Modify: `frontend/src/__tests__/setup.ts`(给全局 mock 列表补 `remote_desktop` binder)

**Interfaces:**
- Consumes:`EventsOn`/`EventsOff`(`wailsjs/runtime/runtime`)、`WriteRemoteDesktop`(`wailsjs/go/remote_desktop/RemoteDesktop`,Task 2 生成)
- Produces:`class WailsRfbChannel`,含 `binaryType/protocol/readyState/bufferedAmount/onopen/onmessage/onclose/onerror`、`send(data)`、`close()`、`markOpen()`;供 Task 4 的面板 `new RFB(container, channel, opts)` 使用

- [ ] **Step 1: 给 setup.ts 补 remote_desktop binder mock**

编辑 `frontend/src/__tests__/setup.ts`,在其它 `vi.mock("../../wailsjs/go/...")` 旁加一行:

```ts
vi.mock("../../wailsjs/go/remote_desktop/RemoteDesktop", () =>
  mockBinderModule("../../wailsjs/go/remote_desktop/RemoteDesktop")
);
```

- [ ] **Step 2: 写失败测试** —— `frontend/src/lib/wailsRfbChannel.test.ts`(新建)

```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { WriteRemoteDesktop } from "../../wailsjs/go/remote_desktop/RemoteDesktop";
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";

// 捕获 EventsOn 注册的处理器,供测试主动触发。
function captureHandlers() {
  const handlers: Record<string, (payload?: string) => void> = {};
  vi.mocked(EventsOn).mockImplementation(((event: string, h: (p?: string) => void) => {
    handlers[event] = h;
    return () => {};
  }) as never);
  return handlers;
}

describe("WailsRfbChannel", () => {
  beforeEach(() => {
    vi.mocked(EventsOn).mockReset();
    vi.mocked(EventsOff).mockReset();
    vi.mocked(WriteRemoteDesktop).mockReset().mockResolvedValue(undefined as never);
  });

  it("delivers a data event to onmessage as an ArrayBuffer", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const received: ArrayBuffer[] = [];
    channel.onmessage = (e) => received.push(e.data);

    handlers["remote_desktop:data:sess-1"]!(btoa(String.fromCharCode(1, 2, 3)));

    expect(received).toHaveLength(1);
    expect(Array.from(new Uint8Array(received[0]))).toEqual([1, 2, 3]);
  });

  it("encodes send() bytes to base64 for WriteRemoteDesktop", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.send(new Uint8Array([104, 105])); // "hi"
    expect(WriteRemoteDesktop).toHaveBeenCalledWith("sess-1", btoa("hi"));
  });

  it("marks open exactly once and fires onopen", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const onopen = vi.fn();
    channel.onopen = onopen;
    channel.markOpen();
    channel.markOpen();
    expect(onopen).toHaveBeenCalledTimes(1);
    expect(channel.readyState).toBe("open");
  });

  it("fires onclose when the closed event arrives", () => {
    const handlers = captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    const onclose = vi.fn();
    channel.onclose = onclose;
    handlers["remote_desktop:closed:sess-1"]!();
    expect(onclose).toHaveBeenCalledTimes(1);
    expect(channel.readyState).toBe("closed");
  });

  it("close() unsubscribes both events and marks closed", () => {
    captureHandlers();
    const channel = new WailsRfbChannel("sess-1");
    channel.close();
    expect(EventsOff).toHaveBeenCalledWith("remote_desktop:data:sess-1");
    expect(EventsOff).toHaveBeenCalledWith("remote_desktop:closed:sess-1");
    expect(channel.readyState).toBe("closed");
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd frontend && pnpm test -- src/lib/wailsRfbChannel.test.ts`
Expected: FAIL —— 找不到模块 `@/lib/wailsRfbChannel`。

- [ ] **Step 4: 实现 WailsRfbChannel** —— `frontend/src/lib/wailsRfbChannel.ts`(新建)

```ts
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { WriteRemoteDesktop } from "../../wailsjs/go/remote_desktop/RemoteDesktop";

type ReadyState = "connecting" | "open" | "closing" | "closed";

function base64ToArrayBuffer(b64: string): ArrayBuffer {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

function toBase64(data: ArrayBuffer | ArrayBufferView): string {
  const bytes =
    data instanceof ArrayBuffer
      ? new Uint8Array(data)
      : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
  return btoa(binary);
}

/**
 * WebSocket 形状的假 channel,交给 noVNC 的 RFB 构造第二参(经 Websock.attach 接入)。
 * Go→FE 的 remote_desktop:data 事件喂给 onmessage;noVNC 的 send 转成 WriteRemoteDesktop。
 * 不做背压:与 local:data / k8s:log 一致,RFB 又是客户端拉取模型,天然自限速。
 */
export class WailsRfbChannel {
  binaryType = "arraybuffer";
  protocol = "";
  readyState: ReadyState = "connecting";
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: ArrayBuffer }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: ((event: unknown) => void) | null = null;

  private readonly dataEvent: string;
  private readonly closedEvent: string;
  private opened = false;

  constructor(private readonly sessionId: string) {
    this.dataEvent = `remote_desktop:data:${sessionId}`;
    this.closedEvent = `remote_desktop:closed:${sessionId}`;
    EventsOn(this.dataEvent, (b64: string) => {
      if (this.readyState === "closed") return;
      this.onmessage?.({ data: base64ToArrayBuffer(b64) });
    });
    EventsOn(this.closedEvent, () => {
      if (this.readyState === "closed") return;
      this.readyState = "closed";
      this.onclose?.();
    });
  }

  get bufferedAmount(): number {
    // FE→Go 方向量极小(键鼠/剪贴板),恒 0 让 noVNC 不做发送侧节流。
    return 0;
  }

  send(data: ArrayBuffer | ArrayBufferView): void {
    void WriteRemoteDesktop(this.sessionId, toBase64(data));
  }

  // 由面板在 new RFB() 之后调用一次:置 open 并触发 onopen。attach 已同步跑完并以
  // readyState==='connecting' 装好 onopen,故此处单触发一次 _socketOpen,不会重复。
  markOpen(): void {
    if (this.opened || this.readyState === "closed") return;
    this.opened = true;
    this.readyState = "open";
    this.onopen?.();
  }

  close(): void {
    if (this.readyState === "closed") return;
    this.readyState = "closed";
    EventsOff(this.dataEvent);
    EventsOff(this.closedEvent);
  }
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `cd frontend && pnpm test -- src/lib/wailsRfbChannel.test.ts`
Expected: PASS(5 个用例)。

- [ ] **Step 6: lint**

Run: `cd frontend && pnpm lint -- src/lib/wailsRfbChannel.ts`
Expected: 无 error。

- [ ] **Step 7: 提交**

```bash
git add frontend/src/lib/wailsRfbChannel.ts frontend/src/lib/wailsRfbChannel.test.ts frontend/src/__tests__/setup.ts
git commit -m "✨ 新增 WailsRfbChannel:noVNC 走 Wails IPC 的传输 channel"
```

---

### Task 4: 前端面板接入 channel + 放宽 noVNC 类型 + 更新面板测试

**Files:**
- Modify: `frontend/src/types/novnc.d.ts`(构造第二参放宽为 `string | RfbRawChannel`)
- Modify: `frontend/src/components/remote-desktop/RemoteDesktopPanel.tsx`(用 channel 替代 URL + 两阶段 start;`RemoteDesktopSession` 去 `webSocketUrl`)
- Test: `frontend/src/__tests__/RemoteDesktopPanel.test.tsx`(本地 mock 补两个方法、断言两阶段、去 `webSocketUrl`)

**Interfaces:**
- Consumes:`WailsRfbChannel`(Task 3)、`StartRemoteDesktopStream`(Task 2)
- Produces:面板行为不变(渲染 VNC、凭据、剪贴板、指纹、file 面板),仅传输底座换成 IPC

- [ ] **Step 1: 先改面板测试表达新行为** —— `frontend/src/__tests__/RemoteDesktopPanel.test.tsx`

(a) 顶部 import 增加 `StartRemoteDesktopStream`:

```ts
import {
  ConnectRemoteDesktop,
  DisconnectRemoteDesktop,
  EncodeVNCClipboardText,
  StartRemoteDesktopStream,
} from "../../wailsjs/go/remote_desktop/RemoteDesktop";
```

(b) 本地 `vi.mock("../../wailsjs/go/remote_desktop/RemoteDesktop", ...)` 补两个方法:

```ts
vi.mock("../../wailsjs/go/remote_desktop/RemoteDesktop", () => ({
  ConnectRemoteDesktop: vi.fn(),
  DisconnectRemoteDesktop: vi.fn(),
  EncodeVNCClipboardText: vi.fn(),
  StartRemoteDesktopStream: vi.fn(),
  WriteRemoteDesktop: vi.fn(),
}));
```

(c) `beforeEach` 里补一条默认 resolve,并删掉两处 `ConnectRemoteDesktop.mockResolvedValue({...})` 里的 `webSocketUrl: "ws://127.0.0.1:12345",` 行(第 76、196 行):

```ts
vi.mocked(StartRemoteDesktopStream).mockReset().mockResolvedValue(undefined as never);
```

(d) 在第一个用例(server identity prompt)里,`FakeRFB.latest` 就绪后追加一条两阶段断言:

```ts
await waitFor(() => expect(FakeRFB.latest).toBeDefined());
expect(StartRemoteDesktopStream).toHaveBeenCalledWith("vnc-session");
```

- [ ] **Step 2: 运行面板测试确认失败**

Run: `cd frontend && pnpm test -- src/__tests__/RemoteDesktopPanel.test.tsx`
Expected: FAIL —— 面板还在用 `session.webSocketUrl` 且未调 `StartRemoteDesktopStream`,新断言不满足(或渲染因 gating 变化异常)。

- [ ] **Step 3: 放宽 novnc 类型** —— `frontend/src/types/novnc.d.ts`

在 `declare module "@novnc/novnc/lib/rfb"` 内,`RFBOptions` 之后加 `RfbRawChannel`,并把构造第二参放宽:

```ts
  export interface RfbRawChannel {
    binaryType: string;
    protocol: string;
    readyState: string;
    bufferedAmount?: number;
    onopen: (() => void) | null;
    onmessage: ((event: { data: ArrayBuffer }) => void) | null;
    onclose: (() => void) | null;
    onerror: ((event: unknown) => void) | null;
    send(data: ArrayBuffer | ArrayBufferView): void;
    close(): void;
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, source: string | RfbRawChannel, options?: RFBOptions);
```

(其余字段/方法保持不变。)

- [ ] **Step 4: 改面板接入 channel** —— `frontend/src/components/remote-desktop/RemoteDesktopPanel.tsx`

(a) import 增加(顶部):

```ts
import { WailsRfbChannel } from "@/lib/wailsRfbChannel";
```

并把 `remote_desktop/RemoteDesktop` 的 import 补上 `StartRemoteDesktopStream`:

```ts
import {
  ConnectRemoteDesktop,
  DisconnectRemoteDesktop,
  EncodeVNCClipboardText,
  StartRemoteDesktopStream,
} from "../../../wailsjs/go/remote_desktop/RemoteDesktop";
```

(b) `RemoteDesktopSession` 接口删掉 `webSocketUrl: string;` 这一行。

(c) 把第二个 `useEffect`(当前 92–197 行,`if (!session || !session.webSocketUrl ...)` 那块)整体替换为:

```tsx
  useEffect(() => {
    if (!session || !vncContainerRef.current) return;
    let disposed = false;
    let connectionStatePoll: number | undefined;
    const container = vncContainerRef.current;
    container.innerHTML = "";
    setStatus("connecting");
    const channel = new WailsRfbChannel(session.id);
    const markVNCConnected = () => {
      if (disposed) return;
      errorRef.current = "";
      setError("");
      setStatus("connected");
    };
    import("@novnc/novnc/lib/rfb")
      .then(({ default: RFBClient }) => {
        if (disposed || !container) {
          channel.close();
          return;
        }
        const rfb = new RFBClient(container, channel, {
          credentials: { username: session.username || "", password: session.password || "" },
        });
        rfb.scaleViewport = scaleViewportRef.current;
        rfb.clipViewport = true;
        rfb.resizeSession = false;
        rfb.background = "#000";
        rfb.addEventListener("connect", markVNCConnected);
        rfb.addEventListener("desktopname", markVNCConnected);
        rfb.addEventListener("capabilities", markVNCConnected);
        rfb.addEventListener("disconnect", (event) => {
          const e = event as CustomEvent<{ clean?: boolean }>;
          if (e.detail?.clean) {
            if (!disposed) setStatus("closed");
            return;
          }
          const message = errorRef.current || t("remoteDesktop.vncDisconnected");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("securityfailure", (event) => {
          const e = event as CustomEvent<{ status?: number; reason?: string }>;
          if (e.detail?.reason) {
            console.warn("VNC security failure", { status: e.detail?.status, reason: e.detail.reason });
          }
          const message = t("remoteDesktop.vncSecurityFailed");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("credentialsrequired", () => {
          const message = t("remoteDesktop.vncCredentialsRequired");
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        rfb.addEventListener("serververification", (event) => {
          const e = event as CustomEvent<{ publickey?: Uint8Array }>;
          const publicKey = e.detail?.publickey;
          if (!publicKey) {
            const message = t("remoteDesktop.vncServerVerificationFailed");
            errorRef.current = message;
            setError(message);
            setStatus("error");
            return;
          }
          void window.crypto.subtle.digest("SHA-256", new Uint8Array(publicKey)).then((digest) => {
            if (disposed) return;
            const fingerprint = Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0"))
              .join(":")
              .toUpperCase();
            setServerFingerprint(fingerprint);
          });
        });
        rfb.addEventListener("clipboard", (event) => {
          const e = event as CustomEvent<{ text?: string }>;
          ClipboardSetText(decodeVNCClipboardText(e.detail?.text || "")).catch(() => {});
        });
        rfbRef.current = rfb;
        // 两阶段:先 markOpen(触发 onopen → noVNC 就绪),再启动后端读 pump,
        // 保证前端已订阅事件、noVNC 已就绪之后字节才开始流动,不丢 RFB 握手首包。
        channel.markOpen();
        void StartRemoteDesktopStream(session.id).catch((e) => {
          if (disposed) return;
          const message = String(e);
          errorRef.current = message;
          setError(message);
          setStatus("error");
        });
        connectionStatePoll = window.setInterval(() => {
          if (!disposed && rfb._rfbConnectionState === "connected") {
            markVNCConnected();
            if (connectionStatePoll) window.clearInterval(connectionStatePoll);
          }
        }, 250);
        window.setTimeout(() => {
          if (connectionStatePoll) window.clearInterval(connectionStatePoll);
        }, 15000);
      })
      .catch((e) => {
        const message = String(e);
        errorRef.current = message;
        setError(message);
        setStatus("error");
      });
    return () => {
      disposed = true;
      channel.close();
      if (rfbRef.current) {
        try {
          rfbRef.current.disconnect();
        } catch {
          // ignore stale noVNC instance cleanup
        }
        rfbRef.current = null;
      }
      if (connectionStatePoll) window.clearInterval(connectionStatePoll);
      container.innerHTML = "";
    };
  }, [session, t]);
```

> 注:后端会话的断开仍由既有的独立 effect(`DisconnectRemoteDesktop(session.id)`)负责;`channel.close()` 只退订事件。两者职责不重叠。

- [ ] **Step 5: 运行面板测试确认通过**

Run: `cd frontend && pnpm test -- src/__tests__/RemoteDesktopPanel.test.tsx`
Expected: PASS(含新加的 `StartRemoteDesktopStream` 断言与全部既有剪贴板/指纹/file 用例)。

- [ ] **Step 6: 类型检查 + lint**

Run: `cd frontend && pnpm lint`
Expected: 无 error(尤其 `RemoteDesktopSession` 无 `webSocketUrl` 后无残留引用)。

- [ ] **Step 7: 提交**

```bash
git add frontend/src/types/novnc.d.ts frontend/src/components/remote-desktop/RemoteDesktopPanel.tsx frontend/src/__tests__/RemoteDesktopPanel.test.tsx
git commit -m "✨ VNC 面板改用 WailsRfbChannel,两阶段启动会话流"
```

---

### Task 5: 全量校验 + 真机烟测

**Files:** 无(校验任务)

- [ ] **Step 1: 后端全量测试**

Run: `make test`
Expected: PASS。

- [ ] **Step 2: 后端 lint**

Run: `make lint`
Expected: 无 issue。

- [ ] **Step 3: 前端全量测试**

Run: `cd frontend && pnpm test`
Expected: PASS。

- [ ] **Step 4: 前端 lint**

Run: `cd frontend && pnpm lint`
Expected: 无 error。

- [ ] **Step 5: 构建确认(含绑定生成)**

Run: `make build`
Expected: 构建成功(证明 Go/TS 端到端可编译,绑定已生成)。

- [ ] **Step 6: 真机烟测(GUI 无法由 agent 点击,用日志观测)**

按 AGENTS.md「靠观测验证」:`make dev` 起 App → 打开一个 VNC 资产的远程桌面 Tab → 确认画面正常渲染、键鼠可用。同时观测 `logs/opskat.log`:应见 `remote desktop connected`,且**不再有**任何 `websocket tcp proxy` / `ws://127.0.0.1` 相关日志(监听端口已彻底移除)。断开 Tab 后应见 `remote desktop session closed`。

- [ ] **Step 7: 收尾提交(若烟测中有微调)**

```bash
git add -A && git commit -m "✅ VNC IPC 传输端到端校验"
```

---

## Self-Review

**1. Spec coverage(逐节对照 spec):**
- §1 目标(去监听端口)→ Task 1 删 `tcpWebSocketProxy`、connectVNC 直接 `Dial`;Task 5 Step 6 日志确认无 `ws://127.0.0.1`。✓
- §2 可行性(RFB 收 channel)→ Task 4 `new RFBClient(container, channel, opts)` + novnc.d.ts 放宽。✓
- §3 数据流 + 两阶段 → Task 2 `StartRemoteDesktopStream`/事件名、Task 4 markOpen→StartStream 顺序。✓
- §4 文件清单 → Task 1(manager.go)、Task 2(binding)、Task 3(channel + setup.ts)、Task 4(panel + types)。✓
- §5 无背压 → channel 无 credit/ack,`bufferedAmount` 恒 0(注释说明)。✓
- §6 测试 → Task 1 `net.Pipe` 白盒、Task 3 channel vitest。✓
- §7 删除清单 → Task 1 删 `WebSocketURL`/proxy/`ConnectOptions`;Task 4 删前端 `webSocketUrl`。✓

**2. Placeholder scan:** 无 TBD/TODO;所有改动步骤均含完整代码。`StartRemoteDesktopStream` 的 EventsEmit 明确标注「不单测、Task 5 真机观测」——是有意的测试边界说明,非占位。✓

**3. Type consistency:**
- 事件名 `remote_desktop:data:<id>` / `remote_desktop:closed:<id>` 在 Task 2(emit)、Task 3(EventsOn/EventsOff)、Task 4(断言)一致。✓
- `SetCallbacks(sessionID, onData func([]byte), onClose func()) error` 在 Task 1 定义、Task 2 调用一致。✓
- `WailsRfbChannel` 的 `markOpen()`/`close()`/`send()`/`onmessage({data})` 在 Task 3 定义、Task 4 使用一致;`RfbRawChannel`(Task 4 novnc.d.ts)与 channel 实际成员结构兼容。✓
- `Connect(ctx, assetID)`(去 `ConnectOptions`)在 Task 1 定义、Task 2 `ConnectRemoteDesktop` 调用一致。✓
