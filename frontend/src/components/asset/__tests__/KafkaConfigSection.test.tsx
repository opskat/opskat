import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { KafkaConfigSection } from "@/components/asset/KafkaConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const encrypt = vi.fn(async (p: string) => `enc(${p})`);
const ctx: AssetFormContext = { isEdit: false, encryptPassword: encrypt };

describe("KafkaConfigSection ref 契约", () => {
  it("创建态(无 brokers):上报 canSave/canTest=false + asset.formMissingKafkaBrokers", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingKafkaBrokers",
    });
  });

  it("编辑态(有 brokers):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "kafka", Config: '{"brokers":["b:9092"]}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("编辑态 buildConfig:base→主凭据→schema_registry→connect 键序 + 伴随加密应用", async () => {
    encrypt.mockClear();
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      sshTunnelId: 4,
      Config: JSON.stringify({
        brokers: ["b1:9092", "b2:9092"],
        client_id: "opskat",
        sasl_mechanism: "plain",
        username: "admin",
        password: "MAINENC", // inline 既有密文 → 沿用,不调用 encrypt
        tls: true,
        tls_insecure: true,
        request_timeout_seconds: 30,
        message_preview_bytes: 4096,
        message_fetch_limit: 50,
        schema_registry: {
          enabled: true,
          url: "http://sr:8081",
          auth_type: "basic",
          username: "sru",
          password: "SRENC", // 既有密文 → 沿用
          tls_insecure: true,
        },
        connect: {
          enabled: true,
          clusters: [
            {
              name: "primary",
              url: "http://connect:8083",
              auth_type: "bearer",
              username: "TOKEN", // bearer 旧迁移:token 存于 username 且无 password/credential_id → 回填为明文 → 保存时加密
            },
          ],
        },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.sshTunnelId).toBe(4);
    expect(built.configJSON).toBe(
      '{"brokers":["b1:9092","b2:9092"],"client_id":"opskat","sasl_mechanism":"plain","username":"admin",' +
        '"tls":true,"tls_insecure":true,"request_timeout_seconds":30,"message_preview_bytes":4096,' +
        '"message_fetch_limit":50,"ssh_asset_id":4,"password":"MAINENC",' +
        '"schema_registry":{"enabled":true,"url":"http://sr:8081","auth_type":"basic","username":"sru",' +
        '"password":"SRENC","tls_insecure":true},' +
        '"connect":{"enabled":true,"clusters":[{"name":"primary","url":"http://connect:8083",' +
        '"auth_type":"bearer","password":"enc(TOKEN)"}]}}'
    );
    // bearer 伴随 token 经 ctx.encryptPassword 加密(主凭据/SR 用既有密文不触发)。
    expect(encrypt).toHaveBeenCalledWith("TOKEN");
    expect(encrypt).toHaveBeenCalledTimes(1);
  });

  it("buildTestConfig:不含伴随;sasl!=none 时带主凭据(既有密文),password=主明文", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({
        brokers: ["b:9092"],
        sasl_mechanism: "plain",
        username: "admin",
        password: "MAINENC",
        schema_registry: { enabled: true, url: "http://sr:8081" },
        connect: { enabled: true, clusters: [{ name: "c", url: "http://connect:8083" }] },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.assetType).toBe("kafka");
    expect(tc.password).toBe(""); // 编辑态未输入明文
    expect(tc.configJSON).not.toContain("schema_registry");
    expect(tc.configJSON).not.toContain("connect");
    expect(tc.configJSON).toContain('"password":"MAINENC"'); // 既有密文走测试 4th-arg 兜底
  });

  it("sasl=none:buildConfig 不含 credential_id/password(主凭据省略)", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({ brokers: ["b:9092"], sasl_mechanism: "none", password: "IGNORED" }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).not.toContain("credential_id");
    expect(built.configJSON).not.toContain("password");
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.configJSON).not.toContain("password");
  });

  it("schema_registry 启用但 URL 空:buildConfig reject(throw i18n key)", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({ brokers: ["b:9092"], schema_registry: { enabled: true, url: "" } }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await expect(ref.current!.buildConfig(ctx)).rejects.toThrow("asset.kafkaSchemaRegistryURLRequired");
  });

  it("schema_registry managed 凭据:写 credential_id 不写 password", async () => {
    encrypt.mockClear();
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({
        brokers: ["b:9092"],
        schema_registry: {
          enabled: true,
          url: "http://sr:8081",
          auth_type: "basic",
          username: "sru",
          credential_id: 9, // managed → passwordSource 回填 "managed",credentialId=9
        },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain(
      '"schema_registry":{"enabled":true,"url":"http://sr:8081","auth_type":"basic","username":"sru","credential_id":9}'
    );
    // managed 走 credential_id 早退,不加密、不写 password(整个 config 无 password 键)
    expect(built.configJSON).not.toContain('"password"');
    expect(encrypt).not.toHaveBeenCalled();
  });

  it("connect cluster managed 凭据:写 credential_id 不写 password", async () => {
    encrypt.mockClear();
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({
        brokers: ["b:9092"],
        connect: {
          enabled: true,
          clusters: [
            {
              name: "primary",
              url: "http://connect:8083",
              auth_type: "bearer",
              credential_id: 12, // managed bearer → credential_id 即 token,不写 password
            },
          ],
        },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain(
      '"connect":{"enabled":true,"clusters":[{"name":"primary","url":"http://connect:8083","auth_type":"bearer","credential_id":12}]}'
    );
    expect(built.configJSON).not.toContain('"password"');
    expect(encrypt).not.toHaveBeenCalled();
  });

  it("schema_registry 字符串 TLS 字段全部 write-through", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({
        brokers: ["b:9092"],
        schema_registry: {
          enabled: true,
          url: "http://sr:8081",
          tls_insecure: true,
          tls_server_name: "sr.example.com",
          tls_ca_file: "/sr/ca.pem",
          tls_cert_file: "/sr/client.crt",
          tls_key_file: "/sr/client.key",
        },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    // applyKafkaCompanionTLS 注入顺序:tls_insecure→tls_server_name→tls_ca_file→tls_cert_file→tls_key_file
    expect(built.configJSON).toContain(
      '"schema_registry":{"enabled":true,"url":"http://sr:8081","tls_insecure":true,' +
        '"tls_server_name":"sr.example.com","tls_ca_file":"/sr/ca.pem",' +
        '"tls_cert_file":"/sr/client.crt","tls_key_file":"/sr/client.key"}'
    );
  });

  it("connect cluster 字符串 TLS 字段全部 write-through", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      Config: JSON.stringify({
        brokers: ["b:9092"],
        connect: {
          enabled: true,
          clusters: [
            {
              name: "primary",
              url: "http://connect:8083",
              tls_insecure: true,
              tls_server_name: "connect.example.com",
              tls_ca_file: "/cn/ca.pem",
              tls_cert_file: "/cn/client.crt",
              tls_key_file: "/cn/client.key",
            },
          ],
        },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain(
      '"connect":{"enabled":true,"clusters":[{"name":"primary","url":"http://connect:8083",' +
        '"tls_insecure":true,"tls_server_name":"connect.example.com","tls_ca_file":"/cn/ca.pem",' +
        '"tls_cert_file":"/cn/client.crt","tls_key_file":"/cn/client.key"}]}'
    );
  });

  it("connect 启用但 0 个有效集群:buildConfig reject kafkaConnectClusterRequired", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      // connect.enabled=true 但 clusters 为空(name/url 都空亦被 filter 掉)→ 0 有效集群
      Config: JSON.stringify({ brokers: ["b:9092"], connect: { enabled: true, clusters: [] } }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await expect(ref.current!.buildConfig(ctx)).rejects.toThrow("asset.kafkaConnectClusterRequired");
  });

  it("connect cluster 缺 url:buildConfig reject kafkaConnectClusterInvalid", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      // 有 name 无 url → 通过 filter(name 非空)但 url 空 → invalid
      Config: JSON.stringify({
        brokers: ["b:9092"],
        connect: { enabled: true, clusters: [{ name: "primary", url: "" }] },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await expect(ref.current!.buildConfig(ctx)).rejects.toThrow("asset.kafkaConnectClusterInvalid");
  });

  it("connect cluster 缺 name:buildConfig reject kafkaConnectClusterInvalid", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "kafka",
      // 有 url 无 name → 通过 filter(url 非空)但 name 空 → invalid
      Config: JSON.stringify({
        brokers: ["b:9092"],
        connect: { enabled: true, clusters: [{ name: "", url: "http://connect:8083" }] },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(<KafkaConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await expect(ref.current!.buildConfig(ctx)).rejects.toThrow("asset.kafkaConnectClusterInvalid");
  });
});
