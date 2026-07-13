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
    data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
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
