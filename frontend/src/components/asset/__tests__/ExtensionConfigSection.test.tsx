import { describe, it, expect, beforeEach, vi } from "vitest";
import { createRef } from "react";
import { render, screen, act } from "@testing-library/react";
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
