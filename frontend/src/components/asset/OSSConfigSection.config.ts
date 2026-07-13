import type { CredentialFragment } from "./credentialConfig";
import {
  buildProxyChainJSON,
  CONNECTION_DEFAULTS,
  parseConnectionFields,
  type ConnectionFormFields,
  type ProxyChainJSON,
} from "./proxyConfig";

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
  skip_tls_verify?: boolean;
  connect_timeout?: number;
  part_size_mb?: number;
  proxy_chain?: ProxyChainJSON;
}

/** 表单态:非机密字段;机密(secret/托管凭证)留在 useAssetCredential,不在此。 */
export interface OSSFormState extends ConnectionFormFields {
  provider: string;
  endpoint: string;
  region: string;
  accessKeyId: string;
  usePathStyle: boolean;
  useSSL: boolean;
  skipTLSVerify: boolean;
  connectTimeout: number;
  partSizeMB: number;
}

export const OSS_DEFAULTS: OSSFormState = {
  provider: "s3",
  endpoint: "",
  region: "",
  accessKeyId: "",
  usePathStyle: false,
  useSSL: true,
  skipTLSVerify: false,
  connectTimeout: 0,
  partSizeMB: 0,
  ...CONNECTION_DEFAULTS,
};

interface OSSProviderDefinition {
  value: string;
  labelKey: string;
  prefill?: Pick<OSSFormState, "endpoint" | "region" | "usePathStyle">;
}

/** 厂商列表、标签和连接预设的单一出处。所有厂商都使用 S3 兼容 API。 */
const OSS_PROVIDER_DEFINITIONS: readonly OSSProviderDefinition[] = [
  {
    value: "s3",
    labelKey: "oss.form.providerS3",
    prefill: { endpoint: "s3.us-east-1.amazonaws.com", region: "us-east-1", usePathStyle: false },
  },
  {
    value: "aliyun-oss",
    labelKey: "oss.form.providerAliyunOSS",
    prefill: { endpoint: "oss-cn-hangzhou.aliyuncs.com", region: "cn-hangzhou", usePathStyle: false },
  },
  {
    value: "tencent-cos",
    labelKey: "oss.form.providerTencentCOS",
    prefill: { endpoint: "cos.ap-guangzhou.myqcloud.com", region: "ap-guangzhou", usePathStyle: false },
  },
  {
    value: "huawei-obs",
    labelKey: "oss.form.providerHuaweiOBS",
    prefill: { endpoint: "obs.cn-north-4.myhuaweicloud.com", region: "cn-north-4", usePathStyle: false },
  },
  {
    value: "volcengine-tos",
    labelKey: "oss.form.providerVolcengineTOS",
    prefill: { endpoint: "tos-s3-cn-beijing.volces.com", region: "cn-beijing", usePathStyle: false },
  },
  {
    value: "qiniu-kodo",
    labelKey: "oss.form.providerQiniuKodo",
    prefill: { endpoint: "s3-cn-east-1.qiniucs.com", region: "cn-east-1", usePathStyle: false },
  },
  {
    value: "cloudflare-r2",
    labelKey: "oss.form.providerCloudflareR2",
    prefill: { endpoint: "<account-id>.r2.cloudflarestorage.com", region: "auto", usePathStyle: true },
  },
  {
    value: "backblaze-b2",
    labelKey: "oss.form.providerBackblazeB2",
    prefill: { endpoint: "s3.us-west-004.backblazeb2.com", region: "us-west-004", usePathStyle: false },
  },
  {
    value: "digitalocean-spaces",
    labelKey: "oss.form.providerDigitalOceanSpaces",
    prefill: { endpoint: "nyc3.digitaloceanspaces.com", region: "nyc3", usePathStyle: false },
  },
  {
    value: "wasabi",
    labelKey: "oss.form.providerWasabi",
    prefill: { endpoint: "s3.us-east-1.wasabisys.com", region: "us-east-1", usePathStyle: false },
  },
  {
    value: "minio",
    labelKey: "oss.form.providerMinio",
    prefill: { endpoint: "http://localhost:9000", region: "us-east-1", usePathStyle: true },
  },
  { value: "s3-compat", labelKey: "oss.form.providerS3Compat" },
];

export const OSS_PROVIDER_VALUES = OSS_PROVIDER_DEFINITIONS.map(({ value }) => value);
export const OSS_PROVIDER_LABEL_KEYS: Record<string, string> = Object.fromEntries(
  OSS_PROVIDER_DEFINITIONS.map(({ value, labelKey }) => [value, labelKey])
);
const PROVIDER_PREFILL: Record<string, OSSProviderDefinition["prefill"]> = Object.fromEntries(
  OSS_PROVIDER_DEFINITIONS.map(({ value, prefill }) => [value, prefill])
);

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
export function buildOSSConfig(
  state: OSSFormState,
  cred: CredentialFragment,
  proxyChainSecrets: Record<string, { password?: string; token?: string }> = {}
): string {
  const cfg: OSSConfigJSON = {};
  if (state.provider) cfg.provider = state.provider;
  if (state.endpoint) cfg.endpoint = state.endpoint;
  if (state.region) cfg.region = state.region;
  if (state.accessKeyId) cfg.access_key_id = state.accessKeyId;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.secret_access_key = cred.password;
  if (state.usePathStyle) cfg.use_path_style = true;
  cfg.use_ssl = state.useSSL;
  if (state.skipTLSVerify) cfg.skip_tls_verify = true;
  if (state.connectTimeout > 0) cfg.connect_timeout = state.connectTimeout;
  if (state.partSizeMB > 0) cfg.part_size_mb = state.partSizeMB;
  const proxyChain = buildProxyChainJSON(state.proxyChainLayers || [], proxyChainSecrets);
  if (proxyChain) cfg.proxy_chain = proxyChain;
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
      skipTLSVerify: cfg.skip_tls_verify || false,
      connectTimeout: cfg.connect_timeout || 0,
      partSizeMB: cfg.part_size_mb || 0,
      ...parseConnectionFields(undefined, 0, cfg.proxy_chain),
    };
  } catch {
    return { ...OSS_DEFAULTS };
  }
}
