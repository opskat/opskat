import { describe, it, expect } from "vitest";
import {
  buildK8sConfig,
  parseK8sConfig,
  K8S_DEFAULTS,
  type K8sFormState,
} from "@/components/asset/K8sConfigSection.config";

const FULL: K8sFormState = {
  kubeconfig: "",
  showKubeconfig: false,
  namespace: "production",
  context: "my-context",
  sshTunnelId: 5,
};

describe("buildK8sConfig (锁旧 save 键序: kubeconfig → namespace → context, 无 ssh_asset_id)", () => {
  it("全字段(ciphertext + namespace + context)", () => {
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

  it("全空(无 ciphertext/namespace/context) → {}", () => {
    expect(buildK8sConfig({ ...K8S_DEFAULTS }, "")).toBe("{}");
  });

  it("不含 ssh_asset_id 键(隧道走 asset 顶层)", () => {
    const json = buildK8sConfig(FULL, "ENC");
    expect(json).not.toContain("ssh_asset_id");
  });
});

describe("parseK8sConfig (锁旧 loadK8sConfig: 只解 namespace/context)", () => {
  it("全字段回填", () => {
    expect(parseK8sConfig('{"kubeconfig":"ENC","namespace":"ns","context":"ctx"}')).toEqual({
      namespace: "ns",
      context: "ctx",
    });
  });

  it("缺字段用空串", () => {
    expect(parseK8sConfig("{}")).toEqual({ namespace: "", context: "" });
  });

  it("非法 JSON 回退空", () => {
    expect(parseK8sConfig("not-json")).toEqual({ namespace: "", context: "" });
  });

  it("空字符串回退空", () => {
    expect(parseK8sConfig("")).toEqual({ namespace: "", context: "" });
  });
});
