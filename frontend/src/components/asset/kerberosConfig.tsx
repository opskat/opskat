import type { FieldDesc } from "@/components/asset/configFields";
import { KerberosPasswordInput } from "@/components/asset/KerberosPasswordInput";

/** 认证方式:"" 是口令(默认,不写进配置),"kerberos" 走宿主取票。 */
export type AuthMethod = "" | "kerberos";
export type KerberosCredentialSource = "password" | "keytab";

/**
 * Kerberos 认证的表单字段,只对声明了 Kerberos 字段组(kerberos)的驱动有意义。
 * 口令与 keytab 路径两种凭据的输入都留在 state 里(切回去还在),保存时只写当前来源的那一项。
 */
export interface KerberosFormFields {
  authMethod: AuthMethod;
  krbPrincipal: string;
  krbCredentialSource: KerberosCredentialSource;
  /** 本次输入的口令明文;已存的口令不回显,其密文在 encryptedKrbPassword。 */
  krbPassword: string;
  encryptedKrbPassword: string;
  krbKeytabFile: string;
  /** 逗号分隔的 host[:port],缺省端口 88 由宿主补。 */
  krbKDC: string;
  krbSPN: string;
}

export const KERBEROS_DEFAULTS: KerberosFormFields = {
  authMethod: "",
  krbPrincipal: "",
  krbCredentialSource: "password",
  krbPassword: "",
  encryptedKrbPassword: "",
  krbKeytabFile: "",
  krbKDC: "",
  krbSPN: "",
};

/** DatabaseConfig 里的 Kerberos 键(与 Go 侧 asset_entity.DatabaseConfig 同名)。 */
export interface KerberosJSON {
  auth_method?: string;
  krb_principal?: string;
  /** 密文。 */
  krb_password?: string;
  krb_keytab_file?: string;
  krb_kdc?: string;
  krb_spn?: string;
}

/** 编辑态回填:存的是 keytab 路径就落在 keytab 来源;口令只带回密文。 */
export function kerberosFromJSON(cfg: KerberosJSON): KerberosFormFields {
  return {
    authMethod: cfg.auth_method === "kerberos" ? "kerberos" : "",
    krbPrincipal: cfg.krb_principal || "",
    krbCredentialSource: cfg.krb_keytab_file ? "keytab" : "password",
    krbPassword: "",
    encryptedKrbPassword: cfg.krb_password || "",
    krbKeytabFile: cfg.krb_keytab_file || "",
    krbKDC: cfg.krb_kdc || "",
    krbSPN: cfg.krb_spn || "",
  };
}

/** KDC 输入拆成地址列表:逗号、分号或空白分隔,空项丢弃。 */
function kdcAddresses(input: string): string[] {
  return input.split(/[\s,;]+/).filter(Boolean);
}

/** 保存/测试共用:只写当前凭据来源的那一项;encryptedPassword 由 resolveKrbPassword 预解析。 */
export function kerberosToJSON(state: KerberosFormFields, encryptedPassword: string): KerberosJSON {
  const out: KerberosJSON = { auth_method: "kerberos", krb_principal: state.krbPrincipal.trim() };
  if (state.krbCredentialSource === "keytab") {
    if (state.krbKeytabFile.trim()) out.krb_keytab_file = state.krbKeytabFile.trim();
  } else if (encryptedPassword) {
    out.krb_password = encryptedPassword;
  }
  out.krb_kdc = kdcAddresses(state.krbKDC).join(",");
  if (state.krbSPN.trim()) out.krb_spn = state.krbSPN.trim();
  return out;
}

/** Kerberos 口令密文:本次输入了就加密,否则沿用已存的。保存与测试连接都走它,后端只认密文。 */
export function resolveKrbPassword(state: KerberosFormFields, encrypt: (plaintext: string) => Promise<string>) {
  return state.krbPassword ? encrypt(state.krbPassword) : Promise.resolve(state.encryptedKrbPassword);
}

/** 选了 Kerberos 且设置不全时,返回要提示的 i18n 键(点名字段);完整则返回空串。 */
export function kerberosInvalidReason(state: KerberosFormFields): string {
  if (!/^[^@\s]+@[^@\s]+$/.test(state.krbPrincipal.trim())) return "asset.formKrbPrincipal";
  if (kdcAddresses(state.krbKDC).length === 0) return "asset.formKrbKDC";
  const hasCredential =
    state.krbCredentialSource === "keytab"
      ? !!state.krbKeytabFile.trim()
      : !!state.krbPassword || !!state.encryptedKrbPassword;
  return hasCredential ? "" : "asset.formKrbCredential";
}

/** Kerberos 字段组的表单控件:认证方式切换在前,其余字段只在选了 Kerberos 后显示。 */
export function kerberosFields<S extends KerberosFormFields>(): FieldDesc<S>[] {
  const on = (s: S) => s.authMethod === "kerberos";
  return [
    {
      kind: "segmented",
      key: "authMethod",
      label: "asset.authMethod",
      options: [
        { value: "", label: "asset.authPassword", testid: "krb-auth-password" },
        { value: "kerberos", label: "asset.authKerberos", testid: "krb-auth-kerberos" },
      ],
    },
    {
      kind: "text",
      key: "krbPrincipal",
      label: "asset.krbPrincipal",
      placeholder: "user@EXAMPLE.COM",
      testid: "krb-principal",
      required: true,
      visibleWhen: on,
    },
    {
      kind: "segmented",
      key: "krbCredentialSource",
      label: "asset.krbCredential",
      options: [
        { value: "password", label: "asset.krbCredentialPassword", testid: "krb-credential-password" },
        { value: "keytab", label: "asset.krbCredentialKeytab", testid: "krb-credential-keytab" },
      ],
      visibleWhen: on,
    },
    {
      kind: "custom",
      visibleWhen: (s) => on(s) && s.krbCredentialSource === "password",
      render: (s, patch) => (
        <KerberosPasswordInput state={s} patch={patch as (p: Partial<KerberosFormFields>) => void} />
      ),
    },
    {
      kind: "text",
      key: "krbKeytabFile",
      label: "asset.krbKeytabFile",
      placeholder: "/path/to/user.keytab",
      testid: "krb-keytab-file",
      visibleWhen: (s) => on(s) && s.krbCredentialSource === "keytab",
    },
    {
      kind: "text",
      key: "krbKDC",
      label: "asset.krbKDC",
      placeholder: "kdc1.example.com, kdc2.example.com:88",
      testid: "krb-kdc",
      required: true,
      visibleWhen: on,
    },
    {
      kind: "text",
      key: "krbSPN",
      label: "asset.krbSPN",
      placeholder: "service/host",
      testid: "krb-spn",
      visibleWhen: on,
    },
  ];
}
