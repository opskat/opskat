import type { custom_type_entity } from "../../../wailsjs/go/models";
import {
  CONNECTION_DEFAULTS,
  buildProxyChainJSON,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyChainJSON,
} from "./proxyConfig";
import { TLS_FILE_DEFAULTS, type TlsFileFormFields } from "./tlsFields";

/** 自定义类型的执行方式(与后端 custom_type_entity.ExecModeHTTP 的值一致)。 */
export const EXEC_MODE_HTTP = "http";

/** 通用资产存储的字段值(后端 asset_entity.GenericValue):非密钥为明文,密钥为密文或托管凭据引用。 */
export interface GenericValueJSON {
  value?: string;
  credential_id?: number;
}

/** 通用资产 Asset.Config(后端 asset_entity.GenericConfig,GenericConnection 平铺)。 */
export interface GenericConfigJSON {
  custom_type?: string;
  values?: Record<string, GenericValueJSON>;
  proxy_chain?: ProxyChainJSON;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
}

export interface GenericSecretState {
  source: "inline" | "managed";
  /** 本次新输入的明文;空 = 沿用 storedCipher。 */
  password: string;
  credentialId: number;
  /** 编辑态已存的直接输入密文;不回显。 */
  storedCipher: string;
}

export interface GenericFormState extends ConnectionFormFields, TlsFileFormFields {
  /** 非密钥字段的当前值。 */
  values: Record<string, string>;
  /** 字段是否是显式填写的值(编辑态已存在,或用户改动过);未显式填写且等于默认值时不写入,解析时取默认值。 */
  explicit: Record<string, boolean>;
  secrets: Record<string, GenericSecretState>;
}

export function parseGenericConfig(configJSON?: string): GenericConfigJSON {
  try {
    return JSON.parse(configJSON || "{}") as GenericConfigJSON;
  } catch {
    return {};
  }
}

/** 按类型字段结构初始化表单;editConfig 给出时回填已存的值与连接配置。 */
export function initGenericState(
  ct: custom_type_entity.CustomType,
  editConfig?: GenericConfigJSON,
  assetTunnelId = 0
): GenericFormState {
  const stored = editConfig?.values ?? {};
  const values: Record<string, string> = {};
  const explicit: Record<string, boolean> = {};
  const secrets: Record<string, GenericSecretState> = {};
  for (const f of ct.fields ?? []) {
    const v = stored[f.name];
    explicit[f.name] = v !== undefined;
    if (f.secret) {
      secrets[f.name] = {
        source: v?.credential_id ? "managed" : "inline",
        password: "",
        credentialId: v?.credential_id ?? 0,
        storedCipher: v?.credential_id ? "" : (v?.value ?? ""),
      };
    } else {
      values[f.name] = v !== undefined ? (v.value ?? "") : (f.default ?? "");
    }
  }
  const connection = editConfig
    ? parseConnectionFields(undefined, assetTunnelId, editConfig.proxy_chain)
    : { ...CONNECTION_DEFAULTS };
  return {
    values,
    explicit,
    secrets,
    ...connection,
    tlsInsecure: editConfig?.tls_insecure ?? TLS_FILE_DEFAULTS.tlsInsecure,
    tlsServerName: editConfig?.tls_server_name ?? TLS_FILE_DEFAULTS.tlsServerName,
    tlsCAFile: editConfig?.tls_ca_file ?? TLS_FILE_DEFAULTS.tlsCAFile,
    tlsCertFile: editConfig?.tls_cert_file ?? TLS_FILE_DEFAULTS.tlsCertFile,
    tlsKeyFile: editConfig?.tls_key_file ?? TLS_FILE_DEFAULTS.tlsKeyFile,
  };
}

function secretHasValue(s: GenericSecretState): boolean {
  return s.source === "managed" ? s.credentialId > 0 : !!s.password || !!s.storedCipher;
}

/** 值为空的必填字段名,按字段结构顺序。 */
export function genericMissingFields(ct: custom_type_entity.CustomType, state: GenericFormState): string[] {
  return (ct.fields ?? [])
    .filter((f) => f.required)
    .filter((f) => (f.secret ? !secretHasValue(state.secrets[f.name]) : !state.values[f.name]?.trim()))
    .map((f) => f.name);
}

function includePlain(f: custom_type_entity.Field, state: GenericFormState): boolean {
  return state.explicit[f.name] || state.values[f.name] !== (f.default ?? "");
}

function connectionJSON(
  ct: custom_type_entity.CustomType,
  state: GenericFormState,
  proxyChainSecrets?: Record<string, { password?: string; token?: string }>
): Omit<GenericConfigJSON, "custom_type" | "values"> {
  if (ct.execMode !== EXEC_MODE_HTTP) return {};
  const out: Omit<GenericConfigJSON, "custom_type" | "values"> = {};
  const chain = buildProxyChainJSON(state.proxyChainLayers, proxyChainSecrets);
  if (chain) out.proxy_chain = chain;
  if (state.tlsInsecure) out.tls_insecure = true;
  if (state.tlsServerName.trim()) out.tls_server_name = state.tlsServerName.trim();
  if (state.tlsCAFile.trim()) out.tls_ca_file = state.tlsCAFile.trim();
  if (state.tlsCertFile.trim()) out.tls_cert_file = state.tlsCertFile.trim();
  if (state.tlsKeyFile.trim()) out.tls_key_file = state.tlsKeyFile.trim();
  return out;
}

/** HTTP 方式按表单选的 SSH 隧道;命令方式不走网络,恒 0。 */
export function genericTunnelId(ct: custom_type_entity.CustomType, state: GenericFormState): number {
  return ct.execMode === EXEC_MODE_HTTP && state.connectionType === "jumphost" ? state.sshTunnelId : 0;
}

/** 保存序列化:非密钥写明文,直接输入的密钥加密(未改动沿用已存密文),托管凭据写引用。 */
export async function buildGenericConfig(
  ct: custom_type_entity.CustomType,
  state: GenericFormState,
  encrypt: (plain: string) => Promise<string>,
  proxyChainSecrets: Record<string, { password?: string; token?: string }>
): Promise<string> {
  const values: Record<string, GenericValueJSON> = {};
  for (const f of ct.fields ?? []) {
    if (f.secret) {
      const s = state.secrets[f.name];
      if (s.source === "managed") {
        if (s.credentialId > 0) values[f.name] = { credential_id: s.credentialId };
      } else if (s.password) {
        values[f.name] = { value: await encrypt(s.password) };
      } else if (s.storedCipher) {
        values[f.name] = { value: s.storedCipher };
      }
    } else if (includePlain(f, state)) {
      values[f.name] = { value: state.values[f.name] };
    }
  }
  const cfg: GenericConfigJSON = { custom_type: ct.slug, values, ...connectionJSON(ct, state, proxyChainSecrets) };
  return JSON.stringify(cfg);
}

/**
 * 测试连接输入(后端 helper.GenericConnTestInput):值与 put_asset 同形,密钥给明文或
 * {credential_id};编辑态未改动的直接输入密钥不给出,由后端沿用已存的值。
 */
export function buildGenericTestInput(
  ct: custom_type_entity.CustomType,
  state: GenericFormState,
  assetId?: number
): string {
  const values: Record<string, string | { credential_id: number }> = {};
  for (const f of ct.fields ?? []) {
    if (f.secret) {
      const s = state.secrets[f.name];
      if (s.source === "managed") {
        if (s.credentialId > 0) values[f.name] = { credential_id: s.credentialId };
      } else if (s.password) {
        values[f.name] = s.password;
      }
    } else if (includePlain(f, state)) {
      values[f.name] = state.values[f.name];
    }
  }
  const input: Record<string, unknown> = { custom_type: ct.slug, values };
  if (assetId) input.asset_id = assetId;
  const tunnel = genericTunnelId(ct, state);
  if (tunnel) input.ssh_tunnel_id = tunnel;
  Object.assign(
    input,
    connectionJSON(
      ct,
      state,
      Object.fromEntries(state.proxyChainLayers.map((l) => [l.id, { password: l.password, token: l.token }]))
    )
  );
  return JSON.stringify(input);
}
