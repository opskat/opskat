// VNC 客户端里不依赖 noVNC 运行时的部分：失败码、文案 key、错误类型与协商结果类型。
// 表单的测试连接在用户点下之前只需要这些，noVNC 本体（lib/vncClient）等真正连接时再加载。
import type { RFBConnectionFailureDetail, RFBNegotiatedSecurityDetail } from "@novnc/novnc";

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
