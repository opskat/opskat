declare module "@novnc/novnc/lib/rfb" {
  export interface RFBOptions {
    credentials?: {
      username?: string;
      password?: string;
      target?: string;
    };
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: RFBOptions);

    scaleViewport: boolean;
    clipViewport: boolean;
    resizeSession: boolean;
    background: string;
    readonly _rfbConnectionState: string;

    approveServer(): void;
    clipboardPasteFrom(text: string): void;
    sendKey(keysym: number, code: string, down?: boolean): void;
    disconnect(): void;
  }
}

declare module "@novnc/novnc" {
  export { default } from "@novnc/novnc/lib/rfb";
}
