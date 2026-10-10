import { describe, it, expect, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";
import { KafkaConfigSection } from "@/components/asset/KafkaConfigSection";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: true, encryptPassword: async (p) => `enc(${p})` };
const CA = "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----";
const CERT = "-----BEGIN CERTIFICATE-----\nclient\n-----END CERTIFICATE-----";
const KEY = "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----";
const PEM_KEYS = ["tls_ca_pem", "tls_cert_pem", "tls_key_pem"];
const FILE_KEYS = ["tls_ca_file", "tls_cert_file", "tls_key_file"];

// TLS 证书可以给本机路径，也可以直接填内容；内置类型的 TLS 分组共用同一套字段。
describe.each([
  { type: "etcd", Section: EtcdConfigSection, base: { endpoints: ["a:2379"] } },
  { type: "redis", Section: RedisConfigSection, base: { host: "r", port: 6379 } },
  { type: "kafka", Section: KafkaConfigSection, base: { brokers: ["k:9092"], sasl_mechanism: "none" } },
])("$type TLS certificate source", ({ type, Section, base }) => {
  function renderWith(tls: Record<string, unknown>) {
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: type, Config: JSON.stringify({ ...base, tls: true, ...tls }) });
    render(<Section ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    fireEvent.click(screen.getByTestId("config-tab-tls"));
    return ref;
  }
  const saved = async (ref: React.RefObject<AssetFormHandle | null>) =>
    JSON.parse((await ref.current!.buildConfig(ctx)).configJSON) as Record<string, unknown>;
  const tested = async (ref: React.RefObject<AssetFormHandle | null>) =>
    JSON.parse((await ref.current!.buildTestConfig!(ctx)).configJSON) as Record<string, unknown>;

  it("certificates stored as content stay content, the key kept as its stored ciphertext", async () => {
    const ref = renderWith({ tls_ca_pem: CA, tls_cert_pem: CERT, tls_key_pem: "CIPHER" });

    expect(screen.getByTestId("tls-ca-pem")).toHaveValue(CA);
    expect(screen.getByTestId("tls-key-pem")).toHaveValue("");
    const cfg = await saved(ref);
    expect(cfg).toMatchObject({ tls_ca_pem: CA, tls_cert_pem: CERT, tls_key_pem: "CIPHER" });
    for (const key of FILE_KEYS) expect(cfg).not.toHaveProperty(key);
    expect((await tested(ref)).tls_key_pem).toBe("CIPHER");
  });

  it("a newly entered key is encrypted for the save and for the connection test", async () => {
    const ref = renderWith({ tls_cert_pem: CERT, tls_key_pem: "CIPHER" });

    fireEvent.change(screen.getByTestId("tls-key-pem"), { target: { value: KEY } });

    expect((await saved(ref)).tls_key_pem).toBe(`enc(${KEY})`);
    expect((await tested(ref)).tls_key_pem).toBe(`enc(${KEY})`);
  });

  it("certificates stored as paths open as paths, and switching to content saves only the content", async () => {
    const ref = renderWith({ tls_ca_file: "/ca.pem", tls_cert_file: "/c.crt", tls_key_file: "/c.key" });

    expect(screen.getByTestId("tls-ca-file")).toHaveValue("/ca.pem");
    expect(await saved(ref)).toMatchObject({ tls_ca_file: "/ca.pem", tls_cert_file: "/c.crt", tls_key_file: "/c.key" });

    fireEvent.click(screen.getByTestId("tls-cert-source-pem"));
    fireEvent.change(screen.getByTestId("tls-ca-pem"), { target: { value: CA } });
    const cfg = await saved(ref);
    expect(cfg.tls_ca_pem).toBe(CA);
    for (const key of FILE_KEYS) expect(cfg).not.toHaveProperty(key);
  });

  it("with TLS switched off no certificate is saved", async () => {
    const ref = renderWith({ tls: false, tls_ca_pem: CA });

    const cfg = await saved(ref);
    for (const key of [...PEM_KEYS, ...FILE_KEYS]) expect(cfg).not.toHaveProperty(key);
  });
});
