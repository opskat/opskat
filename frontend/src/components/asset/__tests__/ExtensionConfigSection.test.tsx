import { describe, it, expect, beforeEach, vi } from "vitest";
import { createRef } from "react";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { makeExtensionConfigSection } from "@/components/asset/ExtensionConfigSection";
import type { AssetFormHandle } from "@/lib/assetTypes/formContract";
import { toast } from "sonner";
import { GetDecryptedExtensionConfig } from "../../../../wailsjs/go/extension/Extension";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

const Section = makeExtensionConfigSection({
  extensionName: "demo",
  assetType: "demo-type",
  hasBackend: false,
  schema: {
    type: "object",
    properties: {
      endpoint: { type: "string", title: "Endpoint" },
      secret: { type: "string", format: "password", title: "Secret" },
    },
  },
});

const CIPHERTEXT = "ENC(v1:deadbeef)";

function editAsset() {
  return new asset_entity.Asset({
    ID: 3,
    Name: "demo",
    Type: "demo-type",
    Config: JSON.stringify({ endpoint: "https://x", secret: CIPHERTEXT }),
  });
}

const ctx = { isEdit: true, encryptPassword: (p: string) => Promise.resolve(`ENC(${p})`) };

describe("ExtensionConfigSection edit mode", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("blocks saving until the decrypted config is loaded, then shows plaintext", async () => {
    let resolve!: (v: string) => void;
    vi.mocked(GetDecryptedExtensionConfig).mockReturnValue(new Promise<string>((r) => (resolve = r)));
    const onValidity = vi.fn();

    render(<Section editAsset={editAsset()} ctx={ctx} onValidityChange={onValidity} />);

    // 解密还没回来：不可保存，也不把密文塞进密码框
    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: false }));
    expect(screen.queryByDisplayValue(CIPHERTEXT)).not.toBeInTheDocument();

    await act(async () => {
      resolve(JSON.stringify({ endpoint: "https://x", secret: "plain" }));
    });

    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: true }));
    expect(screen.getByDisplayValue("plain")).toBeInTheDocument();
  });

  it("surfaces a decrypt failure and never re-encrypts the ciphertext", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockRejectedValue(new Error("bad key"));
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();

    render(<Section ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={onValidity} />);
    await act(async () => {});

    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("bad key"));
    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: false }));
    expect(screen.queryByDisplayValue(CIPHERTEXT)).not.toBeInTheDocument();
    await expect(ref.current!.buildConfig(ctx)).rejects.toThrow();
  });

  it("create mode is savable immediately without decrypting anything", () => {
    const onValidity = vi.fn();
    render(<Section ctx={{ ...ctx, isEdit: false }} onValidityChange={onValidity} />);

    expect(GetDecryptedExtensionConfig).not.toHaveBeenCalled();
    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: true }));
  });
});

describe("ExtensionConfigSection connection settings", () => {
  const schema = {
    type: "object",
    properties: { endpoint: { type: "string", title: "Endpoint" } },
  } as const;
  const Tunneled = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    hasBackend: false,
    schema,
    connection: { sshTunnel: true },
  });
  const Plain = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    hasBackend: false,
    schema,
  });

  function tunneledAsset(sshTunnelId: number) {
    return new asset_entity.Asset({
      ID: 4,
      Name: "es",
      Type: "demo-type",
      Config: JSON.stringify({ endpoint: "http://es.internal:9200" }),
      sshTunnelId,
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "http://es.internal:9200" }));
  });

  it("an undeclared tunnel shows no picker and saves no tunnel", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Plain ref={ref} editAsset={tunneledAsset(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.queryByRole("radiogroup", { name: "asset.connectionType" })).not.toBeInTheDocument();
    expect((await ref.current!.buildConfig(ctx)).sshTunnelId).toBe(0);
  });

  it("a declared tunnel keeps the asset's SSH tunnel and saves it on the asset", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Tunneled ref={ref} editAsset={tunneledAsset(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.getByTestId("extension-ssh-tunnel-select")).toBeInTheDocument();
    const built = await ref.current!.buildConfig(ctx);
    expect(built.sshTunnelId).toBe(9);
    // 隧道是资产列，不进扩展看得到的配置
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
  });

  it("switching to direct drops the tunnel", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Tunneled ref={ref} editAsset={tunneledAsset(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionDirect" }));

    expect(screen.queryByTestId("extension-ssh-tunnel-select")).not.toBeInTheDocument();
    expect((await ref.current!.buildConfig(ctx)).sshTunnelId).toBe(0);
  });

  it("choosing the tunnel without an SSH asset blocks saving", async () => {
    const onValidity = vi.fn();
    render(<Tunneled ctx={{ ...ctx, isEdit: false }} onValidityChange={onValidity} />);

    fireEvent.click(screen.getByRole("radio", { name: "asset.sshTunnel" }));

    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, saveDisabledReason: "asset.formMissingSSHTunnel" })
    );
  });
});
