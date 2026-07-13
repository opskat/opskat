# VNC 会话传输改走 Wails IPC 设计

- 分支: `codex/vnc-rdp-222`
- 日期: 2026-07-13
- 范围: **只改 VNC(`remote_desktop`)这条会话字节通道**。把「Go 起 loopback WebSocket 监听 → noVNC 连 `ws://127.0.0.1:port`」换成「Go 不开监听端口,字节走 Wails IPC,noVNC 用自定义 channel 经 `attach()` 接入」。

## 1. 背景与目标

当前分支上 VNC 已经端到端可用,走的是 **loopback WS 桥**:`remote_desktop_svc.Manager` 里的 `tcpWebSocketProxy` 在 `127.0.0.1:0` 起一个 `http.Server`,`coder/websocket` 接入后经 `proxychain` 拨号到目标,双向透传;`Session.WebSocketURL` 返回给前端,`RemoteDesktopPanel.tsx` 用 `new RFB(container, session.webSocketUrl, …)` 让 noVNC 直连这个 WS。

本次目标:**彻底不开监听端口**,会话字节改用 Wails IPC 搬运。动机是不想在本机开一个监听 socket(硬化诉求);功能行为对用户不变。

**非目标(本次不做):**
- 传输层接口/注册化。改造后只剩 IPC 一条实现,按 AGENTS.md 的 YAGNI(单实现不引接口),直接写成一个干净的 IPC 传输,不留 `SessionTransport` interface。真需要第二种传输再提接口。
- 保留 loopback WS 作为 fallback。直接删。
- 背压 / credit-ack 协议(理由见 §5)。
- RDP。只动 `remote_desktop` 的 VNC 路径。

## 2. 可行性结论(已对 noVNC 1.5.0 源码核实)

- `rfb.js:133-137`:构造第二参 `typeof urlOrChannel === "string"` 才当 URL;否则存为 `_rawChannel`。
- `rfb.js:617-628`:连接时 URL 走 `_sock.open(url)`,channel 走 `_sock.attach(this._rawChannel)`;`attach` 后若 `readyState==='closed'` 报错、`==='open'` 立即 `_socketOpen()`。
- `websock.js:55`:raw channel 需要的属性 `rawChannelProps = ["send","close","binaryType","onerror","onmessage","onopen","protocol","readyState"]`。
- `websock.js:288+`:`attach()` 会用 noVNC 自己的处理器**覆盖** channel 的 `onmessage/onopen/onclose/onerror`,并置 `binaryType="arraybuffer"`;`readyState` getter 用 `ReadyStates.*.includes()` 映射,字符串(`'open'`)或数字(`1`)都认。

结论:交给 `new RFB(container, wailsChannel, opts)` 一个实现了上述 8 个属性的普通对象即可,noVNC 会像驱动 WebSocket 一样驱动它,**无需 fork**。

## 3. 数据流与两阶段连接

```
noVNC ⇄ WailsRfbChannel(id)                              ← 前端,实现 rawChannelProps
   Go→FE:  EventsEmit("remote_desktop:data:"+id, base64)   → channel.onmessage({data: ArrayBuffer})
   FE→Go:  WriteRemoteDesktop(id, base64)                  → net.Conn.Write
   Go→FE:  EventsEmit("remote_desktop:closed:"+id)         → channel.onclose()
Go: net.Conn（经 proxychain 拨号,无 listener）
```

**关键正确性点 —— 两阶段连接,避免丢握手字节。** RFB 是服务端先说话(先发 ProtocolVersion)。若前端还没 `EventsOn` 订阅、Go 就开始 pump,首包会作为事件丢失,握手直接坏掉。因此拆成两步,保证「订阅早于 pump」:

1. `ConnectRemoteDesktop(assetID)`:解析凭据 + proxychain,**经 proxychain 拨号**得 `net.Conn`,存 session,返回 `Session{id, credentials, file…}`。**不启动 pump**。拨号/凭据错误在此同步返回(与现状一致,前端 catch 后展示)。拨号后到 pump 启动前,服务端 greeting 暂存在内核 socket 接收缓冲区,不丢。
2. 前端拿到 session:
   - `new WailsRfbChannel(id)` —— 构造即 `EventsOn` 订阅 `data`/`closed`,`readyState='connecting'`。
   - `new RFB(container, channel, {credentials})` —— noVNC `attach`,装好自己的 `onmessage/onopen/…`,处于 connecting 等 `onopen`。
   - `channel.markOpen()` —— 置 `readyState='open'` 并**触发一次** `onopen`(延后一拍确保 `attach` 已跑,避免 `_socketOpen` 双触发);noVNC 进入 ProtocolVersion 态,准备收字节。
   - `await StartRemoteDesktopStream(id)` —— 后端此刻才 `SetCallbacks` 挂回调、起读 pump。此时前端已订阅、noVNC 已就绪,零丢包。

`onopen` 先于 pump 启动触发,`EventsOn` 又先于 `StartRemoteDesktopStream`,双重保证首包不丢、且不会在握手前处理数据。

## 4. 文件清单与改动

镜像本地终端 `internal/app/local/local_ops.go` 的既有模式:service 暴露回调注册,`wailsRuntime.EventsEmit` 只在 binding 层出现(保持 bindings→service 分层,service 不 import Wails)。

### 后端

| 文件 | 改动 |
|------|------|
| `internal/service/remote_desktop_svc/manager.go` | **删** `tcpWebSocketProxy` 整块(~80 行)及 `net/http`、`coder/websocket` 依赖;**删** `Session.WebSocketURL`。`Session` 改持 `conn net.Conn` + `onData func([]byte)` / `onClose func()` + `done`/`once`。`connectVNC` 经 proxychain 拨号得 conn 存入 session(不再起 proxy)。新增 `SetCallbacks(id, onData, onClose)`:挂回调并启动读 pump goroutine(32KB buf,读 conn→`onData`,EOF/err→`onClose`);`Write(id, data)`:写 conn。`Disconnect`/`close` 关 conn + 结束 pump。为可测,加一个接受 `net.Conn` 的**包内**构造缝(见 §6)。 |
| `internal/app/remote_desktop/remote_desktop.go` | `ConnectRemoteDesktop` 返回 session(去掉 URL)。新增 `StartRemoteDesktopStream(sessionID)` → `manager.SetCallbacks(id, onData=EventsEmit "remote_desktop:data:"+id base64, onClose=EventsEmit "remote_desktop:closed:"+id)`。新增 `WriteRemoteDesktop(sessionID, dataB64)` → base64 decode → `manager.Write`。`DisconnectRemoteDesktop` 不变。 |

### 前端

| 文件 | 改动 |
|------|------|
| `frontend/src/lib/wailsRfbChannel.ts`（新增) | 一个隔离、可单测的 class,实现 `{send, close, binaryType, protocol, readyState, onopen, onmessage, onclose, onerror}`。构造 `EventsOn("remote_desktop:data:"+id, b64→ this.onmessage({data: base64→ArrayBuffer}))`、`"remote_desktop:closed:"+id → this.onclose()`;`readyState` 初始 `'connecting'`。`send(data)` → base64 → `WriteRemoteDesktop(id, …)`。`close()` → `EventsOff` 退订 + `DisconnectRemoteDesktop(id)` + `readyState='closed'`。`markOpen()`:延后一拍置 `'open'` 并触发一次 `onopen`。 |
| `frontend/src/components/remote-desktop/RemoteDesktopPanel.tsx` | 第二个 effect:从「gate `session.webSocketUrl` + `new RFB(url)`」改为「`new WailsRfbChannel(id)` → `new RFB(channel, {credentials})` → `markOpen()` → `await StartRemoteDesktopStream(id)`」;cleanup 里 `channel.close()`。`RemoteDesktopSession` 去掉 `webSocketUrl`(gate 改用 `session.id`)。凭据、剪贴板、指纹校验、file 面板逻辑不动。 |
| `frontend/src/types/novnc.d.ts` | `RFB` 构造第二参 `url: string` 放宽为 `string \| RawChannel`(补一个 `RawChannel` 接口类型)。 |

> `wailsjs/` 是 gitignore 的生成物;新增的 `StartRemoteDesktopStream`/`WriteRemoteDesktop` 由 `wails generate` 产出,开发时先跑一遍。

## 5. 背压 —— 不做(沿用既有约定)

本地 PTY(`local:data:`,`yes` 也能刷屏)、k8s 日志(`k8s:log:`)、rdp 事件(`rdp:event:`)全部是「base64 over events、无显式背压」且长期工作正常。RFB 又是**客户端拉取**模型(client 发 `FramebufferUpdateRequest`,server 一问一答),比 PTY 更不易灌爆事件队列。按 reuse-first + 「先实测再优化」,v1 直接镜像 `local_ops.go`,**不引入 credit/ack 协议**。若日后日志显示事件队列膨胀(如开启 continuous-updates),再补窗口化 ack。

FE→Go 方向(键鼠/剪贴板)量极小,channel 的 `bufferedAmount` 恒返回 0 即可,noVNC 不会因此卡送。

**假设**:Wails 事件经 WebView 桥单通道按序、可靠投递(本机 IPC,非网络),故不做重排/丢失处理。

## 6. 测试(TDD,先写失败用例)

- **Go(service)**:给 manager 加一个接受 `net.Conn` 的包内构造缝,用 `net.Pipe()` 注入假 conn。断言:① pipe 一端写 → `onData` 收到相同字节;② `Write` → pipe 另一端读到相同字节;③ `Disconnect` 后 pump goroutine 退出、`onClose` 触发。不依赖真实网络。
- **前端(vitest)**:mock `wailsjs/runtime`(`EventsOn`/`EventsOff`)与绑定方法(`WriteRemoteDesktop`/`DisconnectRemoteDesktop`)。断言:① `remote_desktop:data:<id>` 事件 → `onmessage` 收到正确 `ArrayBuffer`;② `send(bytes)` → `WriteRemoteDesktop` 收到正确 base64;③ `remote_desktop:closed:<id>` → `onclose`;④ `close()` → 调 `EventsOff` 退订并 `DisconnectRemoteDesktop`。channel 抽成独立单元的最大价值即此层可脱离 noVNC/Wails 单测。

## 7. 删除清单 / 边界

- **删**:`tcpWebSocketProxy` 及其 `net/http`+`coder/websocket` 依赖、`Session.WebSocketURL`、前端 `RemoteDesktopSession.webSocketUrl` 及其 gating。
- **不引入**:`SessionTransport` 接口/注册、WS fallback、背压协议。
- **不动**:RDP、凭据解析、proxychain、指纹校验、file(SFTP)面板、剪贴板 GBK 编解码。
