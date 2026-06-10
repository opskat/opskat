// zmodem.js (FGasper) 没有自带类型，也没有 @types 包。这里只声明本项目实际用到的
// 那一小片 API（Sentry / 收发 Session / Offer / Transfer），覆盖 strict 下的类型检查。
// 入口用包根 index.js（导出 Sentry + Session），不用 dist 浏览器 bundle（它依赖 window）。
declare module "zmodem.js" {
  /** 文件元信息：offer.get_details() 返回 / send_offer() 入参。 */
  export interface ZmodemFileDetails {
    name: string;
    size: number;
    mtime?: number | Date | null;
    mode?: number | null;
    files_remaining?: number;
    bytes_remaining?: number;
  }

  /** 接收会话里的一个待接收文件。 */
  export interface ZmodemOffer {
    get_details(): ZmodemFileDetails;
    on(event: "input", handler: (payload: Uint8Array | number[]) => void): void;
    /** 接受并开始接收，Promise 在该文件接收完成时 resolve。 */
    accept(opts?: { on_input?: (payload: Uint8Array | number[]) => void }): Promise<void>;
    skip(): Promise<void> | void;
  }

  /** 发送会话里 send_offer 被对端接受后返回的传输对象。 */
  export interface ZmodemTransfer {
    send(chunk: Uint8Array | number[]): void;
    end(chunk?: Uint8Array | number[]): Promise<void>;
  }

  export interface ZmodemSession {
    type: "receive" | "send";
    on(event: "offer", handler: (offer: ZmodemOffer) => void): void;
    on(event: "session_end", handler: () => void): void;
    /** 接收会话：开始接收。 */
    start(): void;
    /** 发送会话：所有文件发完后关闭。 */
    close(): Promise<void> | void;
    /** 中止整个会话（发 ZABORT）。 */
    abort(): void;
    /** 发送会话：提交一个待发送文件，对端跳过时 resolve 为 undefined。 */
    send_offer(params: ZmodemFileDetails): Promise<ZmodemTransfer | undefined>;
  }

  /** on_detect 回调拿到的检测对象：确认或拒绝一个 ZMODEM 会话。 */
  export interface ZmodemDetection {
    confirm(): ZmodemSession;
    deny(): void;
  }

  export interface ZmodemSentryOptions {
    to_terminal: (octets: Uint8Array | number[]) => void;
    sender: (octets: Uint8Array | number[]) => void;
    on_detect: (detection: ZmodemDetection) => void;
    on_retract: () => void;
  }

  export interface ZmodemSentry {
    consume(input: Uint8Array | number[]): void;
  }

  interface ZmodemSentryConstructor {
    new (options: ZmodemSentryOptions): ZmodemSentry;
  }

  interface ZmodemModule {
    Sentry: ZmodemSentryConstructor;
  }

  const Zmodem: ZmodemModule;
  export default Zmodem;
}
