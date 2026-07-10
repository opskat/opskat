import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { VNCConfigSection } from "@/components/asset/RemoteDesktopConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: true, encryptPassword: async (password) => `enc(${password})` };

describe("RemoteDesktopConfigSection 测试连接凭据", () => {
  it("VNC 使用用户刚输入的明文密码进行测试", async () => {
    const user = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "vnc",
      Config: '{"host":"vnc.example.com","port":5900}',
    });
    render(<VNCConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    await user.type(screen.getByPlaceholderText("asset.passwordPlaceholder"), "wrong-password");
    const testConfig = await ref.current!.buildTestConfig!(ctx);

    expect(testConfig.password).toBe("wrong-password");
    expect(JSON.parse(testConfig.configJSON).password).toBe("wrong-password");
  });
});
