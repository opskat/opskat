import { describe, it, expect } from "vitest";
import {
  buildOSSConfig,
  parseOSSConfig,
  providerPrefillPatch,
  ossCredentialFragment,
  OSS_DEFAULTS,
  type OSSFormState,
} from "@/components/asset/OSSConfigSection.config";

const FULL: OSSFormState = {
  ...OSS_DEFAULTS,
  provider: "minio",
  endpoint: "http://localhost:9000",
  region: "us-east-1",
  accessKeyId: "AKIA",
  usePathStyle: true,
  useSSL: false,
  skipTLSVerify: true,
  connectTimeout: 30,
  partSizeMB: 16,
};

describe("buildOSSConfig(锁字段序 provider→endpoint→region→access_key_id→cred→use_path_style→use_ssl→connect_timeout)", () => {
  it("全字段 + inline 密文", () => {
    expect(buildOSSConfig(FULL, { password: "ENC" })).toBe(
      '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
        '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"skip_tls_verify":true,"connect_timeout":30,"part_size_mb":16}'
    );
  });
  it("托管凭据 → credential_id 紧跟 access_key_id,不写 secret_access_key", () => {
    const json = buildOSSConfig(FULL, { credential_id: 7 });
    expect(json).toContain('"access_key_id":"AKIA","credential_id":7,"use_path_style":true');
    expect(json).not.toContain("secret_access_key");
  });
  it("空片段不写 credential_id / secret_access_key", () => {
    const json = buildOSSConfig(FULL, {});
    expect(json).not.toContain("secret_access_key");
    expect(json).not.toContain("credential_id");
  });
  it("默认态最小输出(use_ssl 默认开始终写)", () => {
    expect(buildOSSConfig(OSS_DEFAULTS, {})).toBe('{"provider":"s3","use_ssl":true}');
  });
  it("use_path_style 关闭时省略该键;use_ssl 关闭仍写显式 false", () => {
    const json = buildOSSConfig({ ...OSS_DEFAULTS, endpoint: "e", accessKeyId: "a", useSSL: false }, {});
    expect(json).not.toContain("use_path_style");
    expect(json).toContain('"use_ssl":false');
  });
  it("connect_timeout 为 0 时省略该键", () => {
    expect(buildOSSConfig({ ...OSS_DEFAULTS, connectTimeout: 0 }, {})).not.toContain("connect_timeout");
  });
});

describe("parseOSSConfig(镜像 build 字段集;secret 不入表单态)", () => {
  it("全字段回填(忽略 secret_access_key)", () => {
    expect(
      parseOSSConfig(
        '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
          '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"skip_tls_verify":true,"connect_timeout":30,"part_size_mb":16}'
      )
    ).toEqual(FULL);
  });
  it("缺字段用默认(provider→s3,use_ssl→true)", () => {
    expect(parseOSSConfig("{}")).toEqual(OSS_DEFAULTS);
  });
  it("显式 use_ssl:false 不被默认覆盖", () => {
    expect(parseOSSConfig('{"use_ssl":false}').useSSL).toBe(false);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseOSSConfig("nope")).toEqual(OSS_DEFAULTS);
  });
  it("parse→build 往返(密文经 cred 片段回注)", () => {
    const original =
      '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
      '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"skip_tls_verify":true,"connect_timeout":30,"part_size_mb":16}';
    expect(buildOSSConfig(parseOSSConfig(original), { password: "ENC" })).toBe(original);
  });
});

describe("providerPrefillPatch(纯函数,厂商→endpoint/region/path-style 预填)", () => {
  it("s3:virtual-hosted(path-style 关)", () => {
    expect(providerPrefillPatch("s3")).toEqual({
      provider: "s3",
      endpoint: "s3.us-east-1.amazonaws.com",
      region: "us-east-1",
      usePathStyle: false,
    });
  });
  it("aliyun-oss", () => {
    expect(providerPrefillPatch("aliyun-oss")).toEqual({
      provider: "aliyun-oss",
      endpoint: "oss-cn-hangzhou.aliyuncs.com",
      region: "cn-hangzhou",
      usePathStyle: false,
    });
  });
  it("tencent-cos", () => {
    expect(providerPrefillPatch("tencent-cos")).toEqual({
      provider: "tencent-cos",
      endpoint: "cos.ap-guangzhou.myqcloud.com",
      region: "ap-guangzhou",
      usePathStyle: false,
    });
  });
  it("minio:path-style 开", () => {
    expect(providerPrefillPatch("minio")).toEqual({
      provider: "minio",
      endpoint: "http://localhost:9000",
      region: "us-east-1",
      usePathStyle: true,
    });
  });
  it.each([
    ["huawei-obs", "obs.cn-north-4.myhuaweicloud.com", "cn-north-4", false],
    ["volcengine-tos", "tos-s3-cn-beijing.volces.com", "cn-beijing", false],
    ["qiniu-kodo", "s3-cn-east-1.qiniucs.com", "cn-east-1", false],
    ["cloudflare-r2", "<account-id>.r2.cloudflarestorage.com", "auto", true],
    ["backblaze-b2", "s3.us-west-004.backblazeb2.com", "us-west-004", false],
    ["digitalocean-spaces", "nyc3.digitaloceanspaces.com", "nyc3", false],
    ["wasabi", "s3.us-east-1.wasabisys.com", "us-east-1", false],
  ])("%s 预填官方 S3 endpoint", (provider, endpoint, region, usePathStyle) => {
    expect(providerPrefillPatch(provider)).toEqual({ provider, endpoint, region, usePathStyle });
  });
  it("s3-compat:仅切 provider,不预填(保留用户已填)", () => {
    expect(providerPrefillPatch("s3-compat")).toEqual({ provider: "s3-compat" });
  });
});

describe("ossCredentialFragment(编辑态映射 secret_access_key/credential_id → 通用片段)", () => {
  it("托管 → credential_id", () => {
    expect(ossCredentialFragment('{"credential_id":9}')).toEqual({ credential_id: 9 });
  });
  it("inline 密文 → password", () => {
    expect(ossCredentialFragment('{"secret_access_key":"ENC"}')).toEqual({ password: "ENC" });
  });
  it("credential_id 优先于 secret_access_key", () => {
    expect(ossCredentialFragment('{"credential_id":9,"secret_access_key":"ENC"}')).toEqual({ credential_id: 9 });
  });
  it("都无 / 非法 JSON → 空片段", () => {
    expect(ossCredentialFragment("{}")).toEqual({});
    expect(ossCredentialFragment("nope")).toEqual({});
  });
});
