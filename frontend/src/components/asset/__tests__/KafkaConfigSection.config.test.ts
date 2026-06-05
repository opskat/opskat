import { describe, it, expect } from "vitest";
import {
  appendKafkaCredential,
  buildKafkaBaseConfig,
  kafkaBrokers,
  kafkaCompanionPlainSecretFromConfig,
  kafkaCompanionUsernameFromConfig,
  KAFKA_DEFAULTS,
  parseKafkaConfig,
  type KafkaFormState,
} from "@/components/asset/KafkaConfigSection.config";

const FULL: KafkaFormState = {
  brokersText: "b1:9092\nb2:9092",
  clientId: "opskat",
  saslMechanism: "scram-sha-512",
  username: "admin",
  tls: true,
  tlsInsecure: true,
  tlsServerName: "kafka.x",
  tlsCAFile: "/ca.pem",
  tlsCertFile: "/c.crt",
  tlsKeyFile: "/c.key",
  requestTimeoutSeconds: 30,
  messagePreviewBytes: 4096,
  messageFetchLimit: 50,
  sshTunnelId: 3,
};

describe("kafkaBrokers (逗号/换行分隔 + trim + 去空)", () => {
  it("混合分隔", () => {
    expect(kafkaBrokers("b1:9092, b2:9092\n b3:9092 ,, \n")).toEqual(["b1:9092", "b2:9092", "b3:9092"]);
  });
  it("空文本 → 空数组", () => {
    expect(kafkaBrokers("")).toEqual([]);
  });
});

describe("buildKafkaBaseConfig (锁旧 buildKafkaConfig 键序:brokers→client_id→sasl_mechanism→username→tls…→timeouts→ssh_asset_id;无凭据/伴随)", () => {
  it("全字段 + scram(username 紧跟 sasl_mechanism)", () => {
    expect(JSON.stringify(buildKafkaBaseConfig(FULL))).toBe(
      '{"brokers":["b1:9092","b2:9092"],"client_id":"opskat","sasl_mechanism":"scram-sha-512",' +
        '"username":"admin","tls":true,"tls_insecure":true,"tls_server_name":"kafka.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key","request_timeout_seconds":30,' +
        '"message_preview_bytes":4096,"message_fetch_limit":50,"ssh_asset_id":3}'
    );
  });
  it("最小态(仅 brokers,默认 client_id/timeouts 仍写,sasl=none)", () => {
    expect(JSON.stringify(buildKafkaBaseConfig({ ...KAFKA_DEFAULTS, brokersText: "127.0.0.1:9092" }))).toBe(
      '{"brokers":["127.0.0.1:9092"],"client_id":"opskat","sasl_mechanism":"none",' +
        '"request_timeout_seconds":30,"message_preview_bytes":4096,"message_fetch_limit":50}'
    );
  });
  it("sasl=none 不写 username(即便 state.username 非空)", () => {
    const json = JSON.stringify(buildKafkaBaseConfig({ ...KAFKA_DEFAULTS, brokersText: "h:9092", username: "admin" }));
    expect(json).not.toContain('"username"');
    expect(json).toContain('"sasl_mechanism":"none"');
  });
  it("sasl=plain 且 username 空 → 写 sasl_mechanism 但省 username", () => {
    const json = JSON.stringify(
      buildKafkaBaseConfig({ ...KAFKA_DEFAULTS, brokersText: "h:9092", saslMechanism: "plain", username: "" })
    );
    expect(json).toContain('"sasl_mechanism":"plain"');
    expect(json).not.toContain('"username"');
  });
  it("tls=false 时省略全部 tls_* 子键", () => {
    const json = JSON.stringify(buildKafkaBaseConfig({ ...FULL, tls: false }));
    expect(json).not.toContain("tls_insecure");
    expect(json).not.toContain("tls_server_name");
    expect(json).not.toContain('"tls":');
  });
  it("client_id 为空时省略该键", () => {
    const json = JSON.stringify(buildKafkaBaseConfig({ ...KAFKA_DEFAULTS, brokersText: "h:9092", clientId: "  " }));
    expect(json).not.toContain("client_id");
  });
  it("timeouts/preview/fetch=0 与 sshTunnelId=0 时省略对应键", () => {
    const json = JSON.stringify(
      buildKafkaBaseConfig({
        ...KAFKA_DEFAULTS,
        brokersText: "h:9092",
        clientId: "",
        requestTimeoutSeconds: 0,
        messagePreviewBytes: 0,
        messageFetchLimit: 0,
        sshTunnelId: 0,
      })
    );
    expect(json).toBe('{"brokers":["h:9092"],"sasl_mechanism":"none"}');
  });
  it("base 不含 credential_id/password/schema_registry/connect 键", () => {
    const json = JSON.stringify(buildKafkaBaseConfig(FULL));
    expect(json).not.toContain("credential_id");
    expect(json).not.toContain("password");
    expect(json).not.toContain("schema_registry");
    expect(json).not.toContain("connect");
  });
});

describe("appendKafkaCredential (键序:base → credential_id|password)", () => {
  it("managed → credential_id 追加在 username 之后(base 末尾前)", () => {
    const base = buildKafkaBaseConfig({
      ...KAFKA_DEFAULTS,
      brokersText: "h:9092",
      saslMechanism: "plain",
      username: "u",
    });
    appendKafkaCredential(base, { credential_id: 7 });
    expect(JSON.stringify(base)).toContain('"sasl_mechanism":"plain","username":"u"');
    // credential_id 插在 base 全部键之后(末键 message_fetch_limit 之后)
    expect(JSON.stringify(base)).toContain('"message_fetch_limit":50,"credential_id":7}');
    expect(JSON.stringify(base)).not.toContain("password");
  });
  it("inline 密文 → password", () => {
    const base = buildKafkaBaseConfig({
      ...KAFKA_DEFAULTS,
      brokersText: "h:9092",
      saslMechanism: "plain",
      username: "u",
    });
    appendKafkaCredential(base, { password: "ENC" });
    expect(JSON.stringify(base)).toContain('"password":"ENC"');
    expect(JSON.stringify(base)).not.toContain("credential_id");
  });
  it("空片段 → 都不写", () => {
    const base = buildKafkaBaseConfig({ ...KAFKA_DEFAULTS, brokersText: "h:9092" });
    appendKafkaCredential(base, {});
    expect(JSON.stringify(base)).not.toContain("password");
    expect(JSON.stringify(base)).not.toContain("credential_id");
  });
});

describe("parseKafkaConfig (锁旧 loadKafkaConfig 非凭据/非伴随字段)", () => {
  it("全字段回填(brokers join 换行;ssh_asset_id 仅来自 config)", () => {
    expect(
      parseKafkaConfig(
        '{"brokers":["b1:9092","b2:9092"],"client_id":"c","sasl_mechanism":"plain","username":"u",' +
          '"tls":true,"tls_insecure":true,"tls_server_name":"sn","tls_ca_file":"/ca","tls_cert_file":"/cc",' +
          '"tls_key_file":"/ck","request_timeout_seconds":60,"message_preview_bytes":8192,' +
          '"message_fetch_limit":100,"ssh_asset_id":5}'
      )
    ).toEqual({
      brokersText: "b1:9092\nb2:9092",
      clientId: "c",
      saslMechanism: "plain",
      username: "u",
      tls: true,
      tlsInsecure: true,
      tlsServerName: "sn",
      tlsCAFile: "/ca",
      tlsCertFile: "/cc",
      tlsKeyFile: "/ck",
      requestTimeoutSeconds: 60,
      messagePreviewBytes: 8192,
      messageFetchLimit: 100,
      sshTunnelId: 5,
    });
  });
  it("缺字段用默认", () => {
    expect(parseKafkaConfig("{}")).toEqual(KAFKA_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseKafkaConfig("nope")).toEqual(KAFKA_DEFAULTS);
  });
  it("round-trip:build→parse 还原非凭据字段", () => {
    const round = parseKafkaConfig(JSON.stringify(buildKafkaBaseConfig(FULL)));
    expect(round).toEqual({
      brokersText: "b1:9092\nb2:9092",
      clientId: "opskat",
      saslMechanism: "scram-sha-512",
      username: "admin",
      tls: true,
      tlsInsecure: true,
      tlsServerName: "kafka.x",
      tlsCAFile: "/ca.pem",
      tlsCertFile: "/c.crt",
      tlsKeyFile: "/c.key",
      requestTimeoutSeconds: 30,
      messagePreviewBytes: 4096,
      messageFetchLimit: 50,
      sshTunnelId: 3,
    });
  });
});

describe("companion 伴随回填 leaf 转换", () => {
  it("kafkaCompanionUsernameFromConfig:bearer → 空;basic → username", () => {
    expect(kafkaCompanionUsernameFromConfig({ auth_type: "bearer", username: "tok" })).toBe("");
    expect(kafkaCompanionUsernameFromConfig({ auth_type: "basic", username: "u" })).toBe("u");
    expect(kafkaCompanionUsernameFromConfig(undefined)).toBe("");
  });
  it("kafkaCompanionPlainSecretFromConfig:bearer 且无 password/credential_id → 旧 username 当 token", () => {
    expect(kafkaCompanionPlainSecretFromConfig({ auth_type: "bearer", username: "tok" })).toBe("tok");
  });
  it("kafkaCompanionPlainSecretFromConfig:bearer 但已有 password → 空", () => {
    expect(kafkaCompanionPlainSecretFromConfig({ auth_type: "bearer", username: "tok", password: "ENC" })).toBe("");
  });
  it("kafkaCompanionPlainSecretFromConfig:bearer 但已有 credential_id → 空", () => {
    expect(kafkaCompanionPlainSecretFromConfig({ auth_type: "bearer", username: "tok", credential_id: 4 })).toBe("");
  });
  it("kafkaCompanionPlainSecretFromConfig:非 bearer → 空", () => {
    expect(kafkaCompanionPlainSecretFromConfig({ auth_type: "basic", username: "u" })).toBe("");
  });
});
