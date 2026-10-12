import type { FieldDesc } from "@/components/asset/configFields";

/** 通用资产 HTTP 连接的 TLS 表单字段(证书按本机文件路径给出)。 */
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
