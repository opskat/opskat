import type { CredentialFragment } from "./credentialConfig";

/** 与后端 asset_entity.OSSConfig 一一对应的 JSON 形状(snake_case)。secret_access_key/credential_id 由凭据层写入。 */
interface OSSConfigJSON {
  provider?: string;
  endpoint?: string;
  region?: string;
  access_key_id?: string;
  secret_access_key?: string;
  credential_id?: number;
  use_path_style?: boolean;
  use_ssl?: boolean;
  connect_timeout?: number;
}

/** 表单态:非机密字段;机密(secret/托管凭证)留在 useAssetCredential,不在此。 */
export interface OSSFormState {
  provider: string;
  endpoint: string;
  region: string;
  accessKeyId: string;
  usePathStyle: boolean;
  useSSL: boolean;
  connectTimeout: number;
}

export const OSS_DEFAULTS: OSSFormState = {
  provider: "s3",
  endpoint: "",
  region: "",
  accessKeyId: "",
  usePathStyle: false,
  useSSL: true,
  connectTimeout: 0,
};

/** 厂商枚举(与后端 OSSConfig.Provider 注释一致)。 */
export const OSS_PROVIDER_VALUES = ["s3", "aliyun-oss", "tencent-cos", "minio", "s3-compat"] as const;

/** 厂商值 → 展示标签 i18n key(表单下拉 + 详情卡共用,单一出处)。 */
export const OSS_PROVIDER_LABEL_KEYS: Record<string, string> = {
  s3: "oss.form.providerS3",
  "aliyun-oss": "oss.form.providerAliyunOSS",
  "tencent-cos": "oss.form.providerTencentCOS",
  minio: "oss.form.providerMinio",
  "s3-compat": "oss.form.providerS3Compat",
};

/** 厂商智能预填:endpoint 模板 + region 默认 + path-style 默认。s3-compat 不预填。 */
const PROVIDER_PREFILL: Record<string, { endpoint: string; region: string; usePathStyle: boolean }> = {
  s3: { endpoint: "s3.us-east-1.amazonaws.com", region: "us-east-1", usePathStyle: false },
  "aliyun-oss": { endpoint: "oss-cn-hangzhou.aliyuncs.com", region: "cn-hangzhou", usePathStyle: false },
  "tencent-cos": { endpoint: "cos.ap-guangzhou.myqcloud.com", region: "ap-guangzhou", usePathStyle: false },
  minio: { endpoint: "http://localhost:9000", region: "us-east-1", usePathStyle: true },
};

/** 纯函数:切换厂商时的 patch。已知厂商 → 覆写 endpoint/region/usePathStyle;s3-compat/未知 → 仅切 provider,保留用户已填。 */
export function providerPrefillPatch(provider: string): Partial<OSSFormState> {
  const p = PROVIDER_PREFILL[provider];
  if (!p) return { provider };
  return { provider, endpoint: p.endpoint, region: p.region, usePathStyle: p.usePathStyle };
}

/** 编辑态:把 OSS config 的 secret_access_key/credential_id 映射成通用凭据片段,喂给 useAssetCredential。 */
export function ossCredentialFragment(configJSON: string): CredentialFragment {
  try {
    const cfg: OSSConfigJSON = JSON.parse(configJSON || "{}");
    if (cfg.credential_id) return { credential_id: cfg.credential_id };
    if (cfg.secret_access_key) return { password: cfg.secret_access_key };
    return {};
  } catch {
    return {};
  }
}

/** 序列化:按后端结构体字段序写键;空/false/0 一律省略,use_ssl 例外(默认开,始终写显式布尔)。 */
export function buildOSSConfig(state: OSSFormState, cred: CredentialFragment): string {
  const cfg: OSSConfigJSON = {};
  if (state.provider) cfg.provider = state.provider;
  if (state.endpoint) cfg.endpoint = state.endpoint;
  if (state.region) cfg.region = state.region;
  if (state.accessKeyId) cfg.access_key_id = state.accessKeyId;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.secret_access_key = cred.password;
  if (state.usePathStyle) cfg.use_path_style = true;
  cfg.use_ssl = state.useSSL;
  if (state.connectTimeout > 0) cfg.connect_timeout = state.connectTimeout;
  return JSON.stringify(cfg);
}

/** 反序列化:镜像 build 字段集;secret_access_key 不进表单态(凭据独立管理);非法 JSON 回退默认。 */
export function parseOSSConfig(configJSON: string): OSSFormState {
  try {
    const cfg: OSSConfigJSON = JSON.parse(configJSON || "{}");
    return {
      provider: cfg.provider || "s3",
      endpoint: cfg.endpoint || "",
      region: cfg.region || "",
      accessKeyId: cfg.access_key_id || "",
      usePathStyle: cfg.use_path_style || false,
      useSSL: cfg.use_ssl ?? true,
      connectTimeout: cfg.connect_timeout || 0,
    };
  } catch {
    return { ...OSS_DEFAULTS };
  }
}
