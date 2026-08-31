declare module "@novnc/novnc" {
  export interface RFBCredentials {
    username?: string;
    password?: string;
    target?: string;
  }

  export interface RFBOptions {
    credentials?: RFBCredentials;
    securityPolicy?: number[][];
  }

  export interface RfbCloseEvent {
    code: number;
    reason: string;
    wasClean: boolean;
  }

  export interface RfbRawChannel {
    binaryType: string;
    protocol: string;
    readyState: string;
    bufferedAmount?: number;
    onopen: (() => void) | null;
    onmessage: ((event: { data: ArrayBuffer }) => void) | null;
    onclose: ((event: RfbCloseEvent) => void) | null;
    onerror: ((event: unknown) => void) | null;
    send(data: ArrayBuffer | ArrayBufferView): void;
    close(): void;
  }

  export interface RFBCredentialsRequiredDetail {
    types: Array<"username" | "password" | "target">;
  }

  export interface RFBNegotiatedSecurityDetail {
    type: number;
    name: string;
    authenticationEncrypted: boolean;
    sessionEncrypted: boolean;
    aesBits?: 128 | 256;
  }

  export interface RFBConnectionFailureDetail {
    code:
      | "policy-rejected"
      | "unsupported-security-type"
      | "authentication-failed"
      | "integrity-failed"
      | "transport-closed";
    message: string;
    securityType?: number;
    offeredTypes?: number[];
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, source: string | RfbRawChannel, options?: RFBOptions);

    scaleViewport: boolean;
    clipViewport: boolean;
    resizeSession: boolean;
    background: string;

    approveServer(): void;
    sendCredentials(credentials: RFBCredentials): void;
    clipboardPasteFrom(text: string): void;
    sendKey(keysym: number, code: string, down?: boolean): void;
    sendCtrlAltDel(): void;
    disconnect(): void;
  }
}
