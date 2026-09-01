import { createRef } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { VNCConfigSection } from "@/components/asset/VNCConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";
import {
  CheckVNCServerKey,
  ConnectVNCTemporary,
  DisconnectVNC,
  StartVNCStream,
  TrustVNCServerKey,
} from "../../../../wailsjs/go/vnc/VNC";
import { startVNCClient, type StartVNCClientOptions, type VNCNegotiatedSecurity } from "@/lib/vncClient";
import { securityPolicyForVNCEncryption } from "@/lib/vncSecurity";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));
vi.mock("../../../../wailsjs/go/vnc/VNC", () => ({
  CheckVNCServerKey: vi.fn(),
  ConnectVNCTemporary: vi.fn(),
  DisconnectVNC: vi.fn(),
  StartVNCStream: vi.fn(),
  TrustVNCServerKey: vi.fn(),
  WriteVNC: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("@/lib/vncClient", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/vncClient")>();
  return { ...actual, startVNCClient: vi.fn() };
});

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(ConnectVNCTemporary).mockResolvedValue({
    id: "temporary-session",
    username: "tester",
    password: "secret",
    fileSshAssetId: 0,
    encryption: "always_on",
  } as never);
  vi.mocked(StartVNCStream).mockResolvedValue(undefined as never);
  vi.mocked(DisconnectVNC).mockResolvedValue(undefined as never);
});

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

  it("creates a temporary transport and runs the shared client through ServerInit with the selected policy", async () => {
    const negotiated = deferred<VNCNegotiatedSecurity>();
    let options: StartVNCClientOptions | undefined;
    const cleanup = vi.fn();
    vi.mocked(startVNCClient).mockImplementation((nextOptions) => {
      options = nextOptions;
      return { rfb: {} as never, result: negotiated.promise, cleanup };
    });
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "vnc",
      Config: '{"host":"vnc.example.com","port":5901,"encryption":"always_on"}',
    });
    render(<VNCConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);

    const attempt = ref.current!.startTest!(ctx);
    await waitFor(() => expect(startVNCClient).toHaveBeenCalledTimes(1));
    expect(ConnectVNCTemporary).toHaveBeenCalledWith(expect.stringContaining('"encryption":"always_on"'), "");
    expect(options?.securityPolicy).toEqual(securityPolicyForVNCEncryption("always_on"));
    expect(options?.checkServerKey).toBe(CheckVNCServerKey);
    expect(options?.trustServerKey).toBe(TrustVNCServerKey);
    expect(options?.startTransport).toEqual(expect.any(Function));
    await options!.startTransport();
    expect(StartVNCStream).toHaveBeenCalledWith("temporary-session");

    negotiated.resolve({
      type: 129,
      name: "RA2_256",
      authenticationEncrypted: true,
      sessionEncrypted: true,
      aesBits: 256,
    });
    await expect(attempt.result).resolves.toEqual({ successDetail: "RA2_256 — vnc.security.sessionEncrypted" });
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(DisconnectVNC).toHaveBeenCalledWith("temporary-session");
  });

  it("uses the shared durable server-identity prompt callbacks", async () => {
    const negotiated = deferred<VNCNegotiatedSecurity>();
    let options: StartVNCClientOptions | undefined;
    vi.mocked(startVNCClient).mockImplementation((nextOptions) => {
      options = nextOptions;
      return { rfb: {} as never, result: negotiated.promise, cleanup: vi.fn() };
    });
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "vnc",
      Config: '{"host":"vnc.example.com","port":5901,"encryption":"always_on"}',
    });
    render(<VNCConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const attempt = ref.current!.startTest!(ctx);
    await waitFor(() => expect(options).toBeDefined());

    const decision = options!.requestServerTrust({
      state: "changed",
      host: "vnc.example.com",
      port: 5901,
      oldFingerprint: "SHA256:old",
      newFingerprint: "SHA256:new",
    });
    expect(await screen.findByText("SHA256:new")).toBeInTheDocument();
    expect(screen.getByText("SHA256:old")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("vnc-test-verify-trust"));
    await expect(decision).resolves.toBe(true);

    attempt.cancel();
    await expect(attempt.result).rejects.toMatchObject({ code: "cancelled" });
  });
});
