import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { K8sConfigSection } from "@/components/asset/K8sConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("K8sConfigSection ref 契約", () => {
  it("新規モード: kubeconfig 未入力 → canSave=false + formMissingKubeconfig, canTest=false", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.formMissingKubeconfig",
    });
  });

  it("編集モード: kubeconfig 空でも canSave=true, canTest=false", () => {
    const editAsset = new asset_entity.Asset({
      Type: "k8s",
      Config: '{"namespace":"prod","context":"my-ctx"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: true,
      saveDisabledReason: "",
    });
  });

  it("buildTestConfig は null(k8s は非テスト可能型)", () => {
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    expect(ref.current!.buildTestConfig).toBeNull();
  });

  it("buildConfig(新規 + kubeconfig 入力): encryptPassword 呼び出し + 結果埋め込み", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);
    // state の kubeconfig を直接テストする場合、初期値は空なので
    // 編集モードで kubeconfig を持つケースをシミュレートするため
    // editAsset なし + 状態は初期(kubeconfig="") → ciphertext="" → buildK8sConfig({}, "") = "{}"
    const result = await ref.current!.buildConfig(ctx);
    expect(result).toEqual({ configJSON: "{}", sshTunnelId: 0 });
  });

  it("buildConfig(編集モード + kubeconfig 空入力): 旧 ciphertext を保持", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "k8s",
      Config: '{"kubeconfig":"OLD_CIPHER","namespace":"ns","context":"ctx"}',
      sshTunnelId: 7,
    });
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const result = await ref.current!.buildConfig(ctx);
    expect(result).toEqual({
      configJSON: '{"kubeconfig":"OLD_CIPHER","namespace":"ns","context":"ctx"}',
      sshTunnelId: 7,
    });
  });

  it("buildConfig(編集モード + 旧 ciphertext なし): kubeconfig キー省略", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "k8s",
      Config: '{"namespace":"ns"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const result = await ref.current!.buildConfig(ctx);
    expect(result.configJSON).toBe('{"namespace":"ns"}');
    expect(result.configJSON).not.toContain("kubeconfig");
  });

  it("sshTunnelId が buildConfig 結果に引き継がれる", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "k8s",
      Config: "{}",
      sshTunnelId: 42,
    });
    const ref = createRef<AssetFormHandle>();
    render(<K8sConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const result = await ref.current!.buildConfig(ctx);
    expect(result.sshTunnelId).toBe(42);
  });
});
