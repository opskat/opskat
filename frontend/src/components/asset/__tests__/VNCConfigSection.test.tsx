import { createRef } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { VNCConfigSection } from "@/components/asset/VNCConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: true, encryptPassword: async (password) => `enc(${password})` };

describe("VNCConfigSection encryption policy", () => {
  it("renders encryption in a fourth Advanced tab and persists the selected policy", async () => {
    const user = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    render(<VNCConfigSection ref={ref} ctx={ctx} onValidityChange={() => {}} />);

    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((tab) => tab.getAttribute("data-testid"))).toEqual([
      "config-tab-connection",
      "config-tab-tunnel",
      "config-tab-files",
      "config-tab-advanced",
    ]);
    expect(screen.queryByTestId("vnc-encryption-select")).not.toBeInTheDocument();

    await user.click(screen.getByTestId("config-tab-advanced"));
    expect(screen.getByTestId("vnc-encryption-select")).toHaveTextContent("vnc.encryptionServer");
    await user.click(screen.getByTestId("vnc-encryption-select"));
    const options = await screen.findAllByRole("option");
    expect(options).toHaveLength(5);
    expect(options.map((option) => option.textContent)).toEqual([
      "vnc.encryptionServer",
      "vnc.encryptionAlwaysMaximum",
      "vnc.encryptionAlwaysOn",
      "vnc.encryptionPreferOn",
      "vnc.encryptionPreferOff",
    ]);
    expect(options.map((option) => option.textContent).join(" ")).not.toMatch(/RA2/i);

    await user.click(screen.getByRole("option", { name: "vnc.encryptionPreferOn" }));
    expect(screen.getByText("vnc.encryptionPreferOnHint")).toBeInTheDocument();
    const config = JSON.parse((await ref.current!.buildConfig(ctx)).configJSON);
    expect(config.encryption).toBe("prefer_on");
  });
});

describe("VNCConfigSection 测试连接凭据", () => {
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
