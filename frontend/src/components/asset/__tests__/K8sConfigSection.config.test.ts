import { describe, it, expect } from "vitest";
import {
  buildK8sConfig,
  parseK8sConfig,
  K8S_DEFAULTS,
  type K8sFormState,
} from "@/components/asset/K8sConfigSection.config";
import { CONNECTION_DEFAULTS } from "@/components/asset/proxyConfig";

const FULL: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "production",
  context: "my-context",
  ...CONNECTION_DEFAULTS,
};

describe("buildK8sConfig (锁旧 save 键序: kubeconfig → namespace → context → proxy, 无 ssh_asset_id)", () => {
  it("全字段(ciphertext + namespace + context, 直连无 proxy)", () => {
    expect(buildK8sConfig(FULL, "ENC_KUBECONFIG")).toBe(
      '{"kubeconfig":"ENC_KUBECONFIG","namespace":"production","context":"my-context"}'
    );
  });

  it("仅 kubeconfig(无 namespace/context)", () => {
    expect(buildK8sConfig({ ...K8S_DEFAULTS }, "CIPHER")).toBe('{"kubeconfig":"CIPHER"}');
  });

  it("空 ciphertext 省略 kubeconfig 键", () => {
    const json = buildK8sConfig({ ...FULL }, "");
    expect(json).not.toContain("kubeconfig");
    expect(json).toBe('{"namespace":"production","context":"my-context"}');
  });

  it("namespace 为空时省略 namespace 键", () => {
    const json = buildK8sConfig({ ...FULL, namespace: "" }, "ENC");
    expect(json).not.toContain("namespace");
    expect(json).toContain('"kubeconfig":"ENC"');
  });

  it("context 为空时省略 context 键", () => {
    const json = buildK8sConfig({ ...FULL, context: "" }, "ENC");
    expect(json).not.toContain("context");
  });

  it("全空(无 ciphertext/namespace/context, 直连) → {}", () => {
    expect(buildK8sConfig({ ...K8S_DEFAULTS }, "")).toBe("{}");
  });

  it("不含 ssh_asset_id 键(隧道走 asset 顶层)", () => {
    const json = buildK8sConfig({ ...FULL, connectionType: "jumphost", sshTunnelId: 5 }, "ENC");
    expect(json).not.toContain("ssh_asset_id");
  });

  it("proxy 模式 + host:写 proxy 块为尾键(明文密码经预解析)", () => {
    const state: K8sFormState = {
      ...FULL,
      connectionType: "proxy",
      proxyType: "socks5",
      proxyHost: "proxy.example.com",
      proxyPort: 1080,
      proxyUsername: "pu",
    };
    expect(buildK8sConfig(state, "ENC", "PROXY_CIPHER")).toBe(
      '{"kubeconfig":"ENC","namespace":"production","context":"my-context",' +
        '"proxy":{"type":"socks5","host":"proxy.example.com","port":1080,"username":"pu","password":"PROXY_CIPHER"}}'
    );
  });

  it("proxy 模式但无 host:不写 proxy 块", () => {
    const json = buildK8sConfig({ ...FULL, connectionType: "proxy", proxyHost: "" }, "ENC", "X");
    expect(json).not.toContain("proxy");
  });

  it("非 proxy 模式(jumphost):即便填了 proxyHost 也不写 proxy 块", () => {
    const json = buildK8sConfig(
      { ...FULL, connectionType: "jumphost", sshTunnelId: 7, proxyHost: "proxy.example.com" },
      "ENC",
      "X"
    );
    expect(json).not.toContain("proxy");
  });
});

describe("parseK8sConfig (解 namespace/context + 派生连接方式)", () => {
  it("直连:无 proxy 无 tunnel → connectionType direct", () => {
    const s = parseK8sConfig('{"kubeconfig":"ENC","namespace":"ns","context":"ctx"}');
    expect(s.namespace).toBe("ns");
    expect(s.context).toBe("ctx");
    expect(s.kubeconfig).toBe("");
    expect(s.showKubeconfig).toBe(false);
    expect(s.connectionType).toBe("direct");
  });

  it("assetTunnelId 派生 jumphost", () => {
    const s = parseK8sConfig('{"namespace":"ns"}', 9);
    expect(s.connectionType).toBe("jumphost");
    expect(s.sshTunnelId).toBe(9);
  });

  it("proxy round-trip:派生 proxy 模式 + 回填字段(密文不回显为明文)", () => {
    const s = parseK8sConfig(
      '{"namespace":"ns","proxy":{"type":"socks5","host":"h","port":1080,"username":"pu","password":"CIPHER"}}'
    );
    expect(s.connectionType).toBe("proxy");
    expect(s.proxyHost).toBe("h");
    expect(s.proxyPort).toBe(1080);
    expect(s.proxyUsername).toBe("pu");
    expect(s.proxyPassword).toBe("");
    expect(s.encryptedProxyPassword).toBe("CIPHER");
  });

  it("非法 JSON 回退 K8S_DEFAULTS", () => {
    expect(parseK8sConfig("not-json")).toEqual({ ...K8S_DEFAULTS });
  });

  it("空字符串回退 K8S_DEFAULTS", () => {
    expect(parseK8sConfig("")).toEqual({ ...K8S_DEFAULTS });
  });
});
