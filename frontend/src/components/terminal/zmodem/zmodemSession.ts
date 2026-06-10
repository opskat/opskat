import Zmodem, { type ZmodemSentry, type ZmodemDetection, type ZmodemSession, type ZmodemOffer } from "zmodem.js";
import {
  ZmodemBeginDownload,
  ZmodemAppendChunk,
  ZmodemFinishDownload,
  ZmodemAbortDownload,
  ZmodemPickUploadFiles,
  ZmodemReadChunk,
  ZmodemFinishUpload,
  ZmodemAbortUpload,
} from "../../../../wailsjs/go/ssh/SSH";
import { bytesToBase64 } from "@/lib/terminalEncode";
import { useSFTPStore, type SFTPTransferTarget } from "@/stores/sftpStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { notifySuccess } from "@/lib/notify";
import i18n from "@/i18n";

// 每次从后端拉取/下发的分块大小。ZMODEM 子包不大，瓶颈在跨 Wails 边界的 base64，
// 8KB 兼顾内存与事件量。
const UPLOAD_CHUNK = 8 * 1024;

export interface ZmodemController {
  /** 把一段终端入站字节喂给 Sentry：非 ZMODEM 透传到终端，ZMODEM 帧被截获驱动协议。 */
  consume: (bytes: Uint8Array) => void;
  /** 是否有 ZMODEM 会话进行中（terminalRegistry 据此抑制键盘输入）。 */
  isActive: () => boolean;
  /** 主动中止当前会话与在途文件（Ctrl-C / 取消按钮）。 */
  abort: () => void;
  /** 终端销毁/会话关闭时调用：中止并清理。 */
  dispose: () => void;
}

export interface ZmodemControllerOptions {
  sessionId: string;
  write: (sessionId: string, dataB64: string) => Promise<void>;
  toTerminal: (bytes: Uint8Array) => void;
}

function toUint8(octets: Uint8Array | number[]): Uint8Array {
  return octets instanceof Uint8Array ? octets : new Uint8Array(octets);
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function createZmodemController(opts: ZmodemControllerOptions): ZmodemController {
  const { sessionId, write, toTerminal } = opts;

  let active = false;
  let currentSession: ZmodemSession | null = null;
  // 当前在途文件的后端清理回调（下载→AbortDownload 删残file / 上传→AbortUpload 关句柄）。
  let currentAbort: (() => void) | null = null;

  const sentry: ZmodemSentry = new Zmodem.Sentry({
    to_terminal: (octets) => toTerminal(toUint8(octets)),
    sender: (octets) => {
      write(sessionId, bytesToBase64(toUint8(octets))).catch(console.error);
    },
    on_retract: () => {
      // 握手被收回（探测到的 ZMODEM 其实不是）：无在途状态可清，留待下次。
    },
    on_detect: (detection) => handleDetect(detection),
  });

  function consume(bytes: Uint8Array) {
    try {
      sentry.consume(bytes);
    } catch (e) {
      // ZMODEM 解析异常：兜底把原始字节写回终端，避免终端因协议错误而卡死。
      console.error("zmodem sentry consume error", e);
      toTerminal(bytes);
    }
  }

  function resolveTarget(): SFTPTransferTarget {
    const { tabData } = useTerminalStore.getState();
    const tabId = Object.keys(tabData).find((id) => Boolean(tabData[id]?.panes[sessionId])) ?? sessionId;
    return { tabId, sessionId };
  }

  function cleanup() {
    active = false;
    currentSession = null;
    currentAbort = null;
  }

  function abort() {
    // 先捕获再清空，避免 session.abort() 同步触发 session_end→cleanup 把 currentAbort 抢先置空，
    // 导致后端残file 清理被跳过。
    const backendAbort = currentAbort;
    const session = currentSession;
    currentAbort = null;
    if (session) {
      try {
        session.abort();
      } catch (e) {
        console.error("zmodem session abort error", e);
      }
    }
    if (backendAbort) backendAbort();
  }

  function handleDetect(detection: ZmodemDetection) {
    let session: ZmodemSession;
    try {
      session = detection.confirm();
    } catch (e) {
      console.error("zmodem confirm failed", e);
      return;
    }
    active = true;
    currentSession = session;

    // 传输开始即打开文件管理面板，复用其中的 TransferSection 显示进度。
    const target = resolveTarget();
    useSFTPStore.getState().openFileManager(target.tabId);

    session.on("session_end", () => cleanup());

    if (session.type === "receive") {
      startReceive(session, target);
    } else {
      void startSend(session, target);
    }
  }

  // --- 下载：远端 sz ---

  function startReceive(session: ZmodemSession, target: SFTPTransferTarget) {
    session.on("offer", (offer) => {
      void handleOffer(offer, target);
    });
    session.start();
  }

  async function handleOffer(offer: ZmodemOffer, target: SFTPTransferTarget) {
    const details = offer.get_details();
    const name = details.name;
    const size = Number(details.size ?? 0);

    let transferId = "";
    try {
      // 弹原生 Save 对话框并创建文件；用户取消时返回空串。
      transferId = await ZmodemBeginDownload(sessionId, name, size);
    } catch (e) {
      console.error("zmodem begin download failed", e);
    }
    if (!transferId) {
      await offer.skip();
      return;
    }

    useSFTPStore.getState().subscribeExternalTransfer(transferId, target, "download", () => abort());
    currentAbort = () => {
      ZmodemAbortDownload(transferId).catch(console.error);
    };

    offer.on("input", (payload) => {
      ZmodemAppendChunk(transferId, bytesToBase64(toUint8(payload))).catch(console.error);
    });

    try {
      await offer.accept();
      await ZmodemFinishDownload(transferId);
      notifySuccess(i18n.t("zmodem.downloadComplete", { name }));
    } catch (e) {
      console.error("zmodem receive offer failed", e);
      await ZmodemAbortDownload(transferId).catch(console.error);
    } finally {
      currentAbort = null;
    }
  }

  // --- 上传：远端 rz ---

  async function startSend(session: ZmodemSession, target: SFTPTransferTarget) {
    let files: Array<{ transferId: string; name: string; size: number; mtime: number }> = [];
    try {
      // 弹原生多选对话框并逐个打开句柄；用户取消返回空列表。
      files = await ZmodemPickUploadFiles(sessionId);
    } catch (e) {
      console.error("zmodem pick upload files failed", e);
    }
    if (!files || files.length === 0) {
      try {
        await session.close();
      } catch (e) {
        console.error("zmodem session close error", e);
      }
      return;
    }

    for (const f of files) {
      await sendOne(session, f, target);
    }

    try {
      await session.close();
    } catch (e) {
      console.error("zmodem session close error", e);
    }
  }

  async function sendOne(
    session: ZmodemSession,
    f: { transferId: string; name: string; size: number; mtime: number },
    target: SFTPTransferTarget
  ) {
    const transferId = f.transferId;
    let xfer;
    try {
      xfer = await session.send_offer({
        name: f.name,
        size: Number(f.size ?? 0),
        mtime: f.mtime ? Number(f.mtime) : undefined,
      });
    } catch (e) {
      console.error("zmodem send_offer failed", e);
      await ZmodemAbortUpload(transferId).catch(console.error);
      return;
    }
    if (!xfer) {
      // 对端跳过该文件：关闭句柄，不计入传输列表。
      await ZmodemAbortUpload(transferId).catch(console.error);
      return;
    }

    useSFTPStore.getState().subscribeExternalTransfer(transferId, target, "upload", () => abort());
    currentAbort = () => {
      ZmodemAbortUpload(transferId).catch(console.error);
    };

    try {
      let eof = false;
      while (!eof) {
        const chunk = await ZmodemReadChunk(transferId, UPLOAD_CHUNK);
        const bytes = base64ToBytes(chunk.data);
        if (bytes.length > 0) xfer.send(bytes);
        eof = chunk.eof;
      }
      await xfer.end();
      await ZmodemFinishUpload(transferId);
      notifySuccess(i18n.t("zmodem.uploadComplete", { name: f.name }));
    } catch (e) {
      console.error("zmodem send file failed", e);
      await ZmodemAbortUpload(transferId).catch(console.error);
    } finally {
      currentAbort = null;
    }
  }

  return {
    consume,
    isActive: () => active,
    abort,
    dispose: () => {
      abort();
      cleanup();
    },
  };
}
