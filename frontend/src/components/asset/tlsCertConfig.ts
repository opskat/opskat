import { createElement } from "react";
import type { FieldDesc } from "@/components/asset/configFields";
import { TLSCertFieldset } from "@/components/asset/TLSCertFieldset";

export type TLSCertSource = "file" | "pem";

/**
 * TLS 证书材料(CA 证书 / 客户端证书 / 客户端私钥)的表单字段,各资产类型的 TLS 分组共用。
 * 三项共用一个来源:本机文件路径,或直接填写的 PEM 内容。两种来源的输入都留在 state 里
 * (切回去还在),保存时只写当前来源的那一组。
 */
export interface TLSCertFormFields {
  tlsCertSource: TLSCertSource;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
  tlsCAPEM: string;
  tlsCertPEM: string;
  /** 本次输入的私钥明文;已存的私钥不回显,其密文在 encryptedTLSKeyPEM。 */
  tlsKeyPEM: string;
  encryptedTLSKeyPEM: string;
}

export const TLS_CERT_DEFAULTS: TLSCertFormFields = {
  tlsCertSource: "pem",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
  tlsCAPEM: "",
  tlsCertPEM: "",
  tlsKeyPEM: "",
  encryptedTLSKeyPEM: "",
};

/** 存进配置的证书材料;各类型自己决定落在哪些 JSON 键上(内置类型见 tlsCertsFromJSON / tlsCertsToJSON)。 */
export interface TLSCertValues {
  caFile?: string;
  certFile?: string;
  keyFile?: string;
  caPEM?: string;
  certPEM?: string;
  /** 密文。 */
  keyPEM?: string;
}

/** 编辑态回填:存的是路径就落在"文件路径"来源,否则是"证书内容"。 */
export function parseTLSCerts(v: TLSCertValues): TLSCertFormFields {
  const fromFile = !!(v.caFile || v.certFile || v.keyFile);
  return {
    tlsCertSource: fromFile ? "file" : TLS_CERT_DEFAULTS.tlsCertSource,
    tlsCAFile: v.caFile || "",
    tlsCertFile: v.certFile || "",
    tlsKeyFile: v.keyFile || "",
    tlsCAPEM: v.caPEM || "",
    tlsCertPEM: v.certPEM || "",
    tlsKeyPEM: "",
    encryptedTLSKeyPEM: v.keyPEM || "",
  };
}

/** 保存/测试共用:只写当前来源的那一组;encryptedKey 由 resolveTLSKey 预解析。 */
export function buildTLSCerts(state: TLSCertFormFields, encryptedKey: string): TLSCertValues {
  const out: TLSCertValues = {};
  if (state.tlsCertSource === "file") {
    if (state.tlsCAFile.trim()) out.caFile = state.tlsCAFile.trim();
    if (state.tlsCertFile.trim()) out.certFile = state.tlsCertFile.trim();
    if (state.tlsKeyFile.trim()) out.keyFile = state.tlsKeyFile.trim();
    return out;
  }
  if (state.tlsCAPEM.trim()) out.caPEM = state.tlsCAPEM.trim();
  if (state.tlsCertPEM.trim()) {
    out.certPEM = state.tlsCertPEM.trim();
    // 私钥只随客户端证书存在:证书清掉了,已存的私钥也不再保留。
    if (encryptedKey) out.keyPEM = encryptedKey;
  }
  return out;
}

/** 私钥密文:本次输入了就加密,否则沿用已存的。保存与测试连接都走它,后端只认密文。 */
export function resolveTLSKey(state: TLSCertFormFields, encrypt: (plaintext: string) => Promise<string>) {
  const typed = state.tlsKeyPEM.trim();
  return typed ? encrypt(typed) : Promise.resolve(state.encryptedTLSKeyPEM);
}

export interface TLSCertJSON {
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
  tls_ca_pem?: string;
  tls_cert_pem?: string;
  tls_key_pem?: string;
}

/** 内置类型(Redis / etcd / Kafka)配置里的证书键。 */
export function tlsCertsFromJSON(cfg: TLSCertJSON): TLSCertFormFields {
  return parseTLSCerts({
    caFile: cfg.tls_ca_file,
    certFile: cfg.tls_cert_file,
    keyFile: cfg.tls_key_file,
    caPEM: cfg.tls_ca_pem,
    certPEM: cfg.tls_cert_pem,
    keyPEM: cfg.tls_key_pem,
  });
}

/** tlsCertsFromJSON 的反向;键序固定:路径三项在前,内容三项在后。 */
export function tlsCertsToJSON(state: TLSCertFormFields, encryptedKey: string): TLSCertJSON {
  const v = buildTLSCerts(state, encryptedKey);
  const out: TLSCertJSON = {};
  if (v.caFile) out.tls_ca_file = v.caFile;
  if (v.certFile) out.tls_cert_file = v.certFile;
  if (v.keyFile) out.tls_key_file = v.keyFile;
  if (v.caPEM) out.tls_ca_pem = v.caPEM;
  if (v.certPEM) out.tls_cert_pem = v.certPEM;
  if (v.keyPEM) out.tls_key_pem = v.keyPEM;
  return out;
}

/** 证书输入区(TLSCertFieldset)的字段描述符;visibleWhen 是它的显示条件(通常是"已启用 TLS")。 */
export function tlsCertFields<S extends TLSCertFormFields>(
  visibleWhen: (s: S) => boolean = () => true
): FieldDesc<S>[] {
  return [
    {
      kind: "custom",
      visibleWhen,
      render: (state, patch) => createElement(TLSCertFieldset, { state, patch: patch as TLSCertFieldsetPatch }),
    },
  ];
}

type TLSCertFieldsetPatch = (p: Partial<TLSCertFormFields>) => void;
