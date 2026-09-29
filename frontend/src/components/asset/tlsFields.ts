import type { FieldDesc } from "@/components/asset/configFields";

/** TLS 证书相关的共享表单字段(Redis / etcd / Kafka / 通用资产 HTTP 连接共用)。 */
export interface TlsFileFormFields {
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
}

export const TLS_FILE_DEFAULTS: TlsFileFormFields = {
  tlsInsecure: false,
  tlsServerName: "",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
};

/** 跳过校验 / ServerName / CA / 客户端证书 / 客户端私钥;visibleWhen 省略 = 总是显示。 */
export function tlsFileFields<S extends TlsFileFormFields>(opts: {
  serverNamePlaceholder: string;
  visibleWhen?: (s: S) => boolean;
}): FieldDesc<S>[] {
  const { serverNamePlaceholder, visibleWhen } = opts;
  return [
    { kind: "switch", key: "tlsInsecure", label: "asset.tlsInsecure", visibleWhen },
    {
      kind: "text",
      key: "tlsServerName",
      label: "asset.tlsServerName",
      placeholder: serverNamePlaceholder,
      visibleWhen,
    },
    { kind: "text", key: "tlsCAFile", label: "asset.tlsCAFile", placeholder: "/path/to/ca.pem", visibleWhen },
    {
      kind: "text",
      key: "tlsCertFile",
      label: "asset.tlsCertFile",
      placeholder: "/path/to/client.crt",
      visibleWhen,
    },
    { kind: "text", key: "tlsKeyFile", label: "asset.tlsKeyFile", placeholder: "/path/to/client.key", visibleWhen },
  ];
}

/** 带「TLS 加密」开关的一组:开关打开后才显示证书字段(Redis / etcd / Kafka 的 TLS 标签页)。 */
export function tlsToggleFields<S extends TlsFileFormFields & { tls: boolean }>(
  serverNamePlaceholder: string
): FieldDesc<S>[] {
  return [
    { kind: "switch", key: "tls", label: "asset.tls" },
    ...tlsFileFields<S>({ serverNamePlaceholder, visibleWhen: (s) => s.tls }),
  ];
}
