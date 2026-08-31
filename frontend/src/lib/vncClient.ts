import RFB, {
  type RFBConnectionFailureDetail,
  type RFBCredentialsRequiredDetail,
  type RFBNegotiatedSecurityDetail,
  type RfbRawChannel,
} from "@novnc/novnc";

export type VNCNegotiatedSecurity = RFBNegotiatedSecurityDetail;

export type VNCServerKeyCheck = {
  state: "first_use" | "match" | "changed";
  host: string;
  port: number;
  oldFingerprint?: string;
  newFingerprint: string;
};

export type VNCClientFailureCode =
  | RFBConnectionFailureDetail["code"]
  | "unsatisfied-credentials"
  | "server-key-invalid"
  | "server-key-check-failed"
  | "server-key-trust-failed"
  | "server-key-rejected"
  | "cancelled";

const VNC_FAILURE_MESSAGE_KEYS: Record<VNCClientFailureCode, string> = {
  "policy-rejected": "vnc.failure.policyRejected",
  "unsupported-security-type": "vnc.failure.unsupportedSecurityType",
  "authentication-failed": "vnc.failure.authenticationFailed",
  "integrity-failed": "vnc.failure.integrityFailed",
  "transport-closed": "vnc.failure.transportClosed",
  "unsatisfied-credentials": "vnc.failure.unsatisfiedCredentials",
  "server-key-invalid": "vnc.failure.serverKeyInvalid",
  "server-key-check-failed": "vnc.failure.serverKeyCheckFailed",
  "server-key-trust-failed": "vnc.failure.serverKeyTrustFailed",
  "server-key-rejected": "vnc.failure.serverKeyRejected",
  cancelled: "vnc.failure.cancelled",
};

export function vncClientFailureMessageKey(code: VNCClientFailureCode): string {
  return VNC_FAILURE_MESSAGE_KEYS[code];
}

export class VNCClientError extends Error {
  readonly cause?: unknown;

  constructor(
    readonly code: VNCClientFailureCode,
    readonly securityType?: number,
    cause?: unknown
  ) {
    super(code);
    this.name = "VNCClientError";
    this.cause = cause;
  }
}

export interface VNCClientSource extends RfbRawChannel {
  close(): void;
}

export interface StartVNCClientOptions {
  target: HTMLElement;
  source: VNCClientSource;
  sessionId: string;
  username?: string;
  password?: string;
  securityPolicy: number[][];
  checkServerKey: (
    sessionId: string,
    publicKeyB64: string
  ) => Promise<Omit<VNCServerKeyCheck, "state"> & { state: string }>;
  trustServerKey: (sessionId: string, publicKeyB64: string, replace: boolean) => Promise<void>;
  requestServerTrust: (check: VNCServerKeyCheck) => Promise<boolean>;
  openSource: () => void;
  startTransport: () => Promise<void>;
  onNegotiatedSecurity?: (security: VNCNegotiatedSecurity) => void;
  onConnected?: () => void;
  onDisconnected?: (clean: boolean) => void;
  onFailure?: (error: VNCClientError) => void;
  onClipboard?: (text: string) => void;
}

export interface VNCClientHandle {
  readonly rfb: RFB;
  readonly result: Promise<VNCNegotiatedSecurity>;
  cleanup(): void;
}

function publicKeyBase64(publicKey: Uint8Array): string {
  let binary = "";
  for (const value of publicKey) binary += String.fromCharCode(value);
  return btoa(binary);
}

function requestedCredentials(
  requested: RFBCredentialsRequiredDetail["types"],
  username: string | undefined,
  password: string | undefined
): { username: string; password?: string } | null {
  if (requested.includes("target")) return null;
  if (requested.includes("password") && !password) return null;
  return { username: username ?? "", ...(password === undefined ? {} : { password }) };
}

export function startVNCClient(options: StartVNCClientOptions): VNCClientHandle {
  let active = true;
  let settled = false;
  let failureReported = false;
  let negotiated: VNCNegotiatedSecurity | undefined;
  let verificationPending = false;
  let queuedCredentialTypes: RFBCredentialsRequiredDetail["types"] | undefined;
  let resolveResult!: (value: VNCNegotiatedSecurity) => void;
  let rejectResult!: (reason: VNCClientError) => void;
  const result = new Promise<VNCNegotiatedSecurity>((resolve, reject) => {
    resolveResult = resolve;
    rejectResult = reject;
  });
  // A handle may be cancelled before its owner starts awaiting result (for example,
  // while a trust prompt is open). Keep that expected cancellation from becoming
  // an unhandled rejection without changing the original promise's rejected state.
  void result.catch(() => undefined);
  const rfb = new RFB(options.target, options.source, { securityPolicy: options.securityPolicy });

  const listeners: Array<[string, EventListener]> = [];
  const listen = (type: string, listener: EventListener) => {
    listeners.push([type, listener]);
    rfb.addEventListener(type, listener);
  };
  const stop = () => {
    if (!active) return;
    active = false;
    for (const [type, listener] of listeners) rfb.removeEventListener(type, listener);
    rfb.disconnect();
    options.source.close();
  };
  const fail = (error: VNCClientError) => {
    if (!active || failureReported) return;
    failureReported = true;
    options.onFailure?.(error);
    if (!settled) {
      settled = true;
      rejectResult(error);
    }
    stop();
  };
  const supplyCredentials = (types: RFBCredentialsRequiredDetail["types"]) => {
    if (!active) return;
    if (verificationPending) {
      queuedCredentialTypes = types;
      return;
    }
    const credentials = requestedCredentials(types, options.username, options.password);
    if (!credentials) {
      fail(new VNCClientError("unsatisfied-credentials"));
      return;
    }
    rfb.sendCredentials(credentials);
  };
  const approveVerifiedServer = () => {
    if (!active) return;
    rfb.approveServer();
    verificationPending = false;
    if (queuedCredentialTypes) {
      const types = queuedCredentialTypes;
      queuedCredentialTypes = undefined;
      supplyCredentials(types);
    }
  };

  listen("serververification", ((event: CustomEvent<{ publickey?: Uint8Array }>) => {
    const publicKey = event.detail?.publickey;
    if (!publicKey?.length) {
      fail(new VNCClientError("server-key-invalid"));
      return;
    }
    verificationPending = true;
    const encodedKey = publicKeyBase64(publicKey);
    void options
      .checkServerKey(options.sessionId, encodedKey)
      .then(async (checked) => {
        if (!active) return;
        if (checked.state !== "first_use" && checked.state !== "match" && checked.state !== "changed") {
          fail(new VNCClientError("server-key-check-failed"));
          return;
        }
        const check: VNCServerKeyCheck = { ...checked, state: checked.state };
        if (check.state === "match") {
          approveVerifiedServer();
          return;
        }
        const trusted = await options.requestServerTrust(check);
        if (!active) return;
        if (!trusted) {
          fail(new VNCClientError("server-key-rejected"));
          return;
        }
        try {
          await options.trustServerKey(options.sessionId, encodedKey, check.state === "changed");
        } catch (error) {
          fail(new VNCClientError("server-key-trust-failed", undefined, error));
          return;
        }
        approveVerifiedServer();
      })
      .catch((error) => fail(new VNCClientError("server-key-check-failed", undefined, error)));
  }) as EventListener);

  listen("credentialsrequired", ((event: CustomEvent<RFBCredentialsRequiredDetail>) => {
    supplyCredentials(event.detail?.types ?? []);
  }) as EventListener);

  listen("negotiatedsecurity", ((event: CustomEvent<VNCNegotiatedSecurity>) => {
    if (!active) return;
    negotiated = event.detail;
    options.onNegotiatedSecurity?.(event.detail);
  }) as EventListener);

  listen("connectionfailure", ((event: CustomEvent<RFBConnectionFailureDetail>) => {
    fail(new VNCClientError(event.detail.code, event.detail.securityType));
  }) as EventListener);

  listen("connect", (() => {
    if (!active) return;
    options.onConnected?.();
    if (!settled) {
      if (!negotiated) {
        fail(new VNCClientError("transport-closed"));
        return;
      }
      settled = true;
      resolveResult(negotiated);
    }
  }) as EventListener);

  listen("disconnect", ((event: CustomEvent<{ clean?: boolean }>) => {
    if (!active) return;
    const clean = !!event.detail?.clean;
    options.onDisconnected?.(clean);
    if (!settled) fail(new VNCClientError("transport-closed"));
  }) as EventListener);

  listen("clipboard", ((event: CustomEvent<{ text?: string }>) => {
    if (active) options.onClipboard?.(event.detail?.text ?? "");
  }) as EventListener);

  options.openSource();
  void options.startTransport().catch((error) => {
    fail(new VNCClientError("transport-closed", undefined, error));
  });

  return {
    rfb,
    result,
    cleanup() {
      if (!active) return;
      if (!settled) {
        settled = true;
        rejectResult(new VNCClientError("cancelled"));
      }
      stop();
    },
  };
}
