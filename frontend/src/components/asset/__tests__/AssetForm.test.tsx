import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetForm } from "@/components/asset/AssetForm";
import { asset_entity } from "../../../../wailsjs/go/models";
import { CancelTest, TestAssetConnection } from "../../../../wailsjs/go/system/System";
import { ConnectVNCTemporary, DisconnectVNC } from "../../../../wailsjs/go/vnc/VNC";
import { startVNCClient, VNCClientError, type VNCNegotiatedSecurity } from "@/lib/vncClient";
import { notifySuccess } from "@/lib/notify";
import { toast } from "sonner";
import { EventsOff } from "../../../../wailsjs/runtime/runtime";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";

const mocks = vi.hoisted(() => ({
  notifySuccess: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
}));

vi.mock("@/lib/notify", () => ({ notifySuccess: mocks.notifySuccess }));
vi.mock("sonner", () => ({
  toast: { error: mocks.toastError, info: mocks.toastInfo, warning: vi.fn(), success: vi.fn() },
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

const vncAsset = new asset_entity.Asset({
  ID: 42,
  Name: "unsaved-vnc",
  Type: "vnc",
  Config: JSON.stringify({ host: "vnc.example.com", port: 5901, encryption: "always_on" }),
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(ConnectVNCTemporary).mockResolvedValue({
    id: "temporary-vnc",
    username: "tester",
    password: "secret",
    fileSshAssetId: 0,
    encryption: "always_on",
  } as never);
  vi.mocked(DisconnectVNC).mockResolvedValue(undefined as never);
});

describe("AssetForm custom test lifecycle", () => {
  it("cancels a VNC test on the cancel button, tears down both sides, and ignores late success", async () => {
    const negotiated = deferred<VNCNegotiatedSecurity>();
    const cleanup = vi.fn();
    vi.mocked(startVNCClient).mockImplementation((options) => ({
      rfb: {} as never,
      result: negotiated.promise,
      cleanup: () => {
        cleanup();
        options.source.close();
      },
    }));

    render(<AssetForm open editAsset={vncAsset} onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByTestId("asset-test-connection"));
    await waitFor(() => expect(startVNCClient).toHaveBeenCalledTimes(1));

    await userEvent.click(screen.getByRole("button", { name: /asset.cancelTest/ }));
    expect(cleanup).toHaveBeenCalledTimes(1);
    expect(DisconnectVNC).toHaveBeenCalledWith("temporary-vnc");
    expect(EventsOff).toHaveBeenCalledWith("vnc:data:temporary-vnc");
    expect(EventsOff).toHaveBeenCalledWith("vnc:closed:temporary-vnc");
    expect(CancelTest).not.toHaveBeenCalled();

    negotiated.resolve({
      type: 129,
      name: "RA2_256",
      authenticationEncrypted: true,
      sessionEncrypted: true,
      aesBits: 256,
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(notifySuccess).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("closes an active VNC test when the form closes and ignores late failure", async () => {
    const negotiated = deferred<VNCNegotiatedSecurity>();
    const cleanup = vi.fn();
    vi.mocked(startVNCClient).mockImplementation((options) => ({
      rfb: {} as never,
      result: negotiated.promise,
      cleanup: () => {
        cleanup();
        options.source.close();
      },
    }));

    const view = render(<AssetForm open editAsset={vncAsset} onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByTestId("asset-test-connection"));
    await waitFor(() => expect(startVNCClient).toHaveBeenCalledTimes(1));

    view.rerender(<AssetForm open={false} editAsset={vncAsset} onOpenChange={vi.fn()} />);
    await waitFor(() => expect(cleanup).toHaveBeenCalledTimes(1));
    expect(DisconnectVNC).toHaveBeenCalledWith("temporary-vnc");

    negotiated.reject(new Error("late failure"));
    await Promise.resolve();
    await Promise.resolve();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("uses the negotiated VNC result for the success copy", async () => {
    vi.mocked(startVNCClient).mockImplementation(() => ({
      rfb: {} as never,
      result: Promise.resolve({
        type: 130,
        name: "RA2ne_256",
        authenticationEncrypted: true,
        sessionEncrypted: false,
        aesBits: 256,
      }),
      cleanup: vi.fn(),
    }));

    render(<AssetForm open editAsset={vncAsset} onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByTestId("asset-test-connection"));

    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("asset.testConnectionSuccessDetail"));
    expect(DisconnectVNC).toHaveBeenCalledWith("temporary-vnc");
  });

  it("surfaces the same typed VNC failure copy as a normal session", async () => {
    vi.mocked(startVNCClient).mockImplementation(() => ({
      rfb: {} as never,
      result: Promise.reject(new VNCClientError("policy-rejected")),
      cleanup: vi.fn(),
    }));

    render(<AssetForm open editAsset={vncAsset} onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByTestId("asset-test-connection"));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("vnc.failure.policyRejected"));
    expect(notifySuccess).not.toHaveBeenCalled();
    expect(DisconnectVNC).toHaveBeenCalledWith("temporary-vnc");
  });

  it("preserves the generic TestAssetConnection path for a non-VNC section", async () => {
    vi.mocked(TestAssetConnection).mockResolvedValue(undefined as never);
    const serialAsset = new asset_entity.Asset({
      ID: 9,
      Name: "serial",
      Type: "serial",
      Config: JSON.stringify({ port_path: "/dev/ttyUSB0", baud_rate: 115200 }),
    });

    render(<AssetForm open editAsset={serialAsset} onOpenChange={vi.fn()} />);
    await userEvent.click(await screen.findByTestId("asset-test-connection"));

    await waitFor(() =>
      expect(TestAssetConnection).toHaveBeenCalledWith(
        expect.any(String),
        "serial",
        expect.stringContaining('"port_path":"/dev/ttyUSB0"'),
        ""
      )
    );
    expect(ConnectVNCTemporary).not.toHaveBeenCalled();
    expect(notifySuccess).toHaveBeenCalledWith("asset.testConnectionSuccess");
  });
});

// An extension asset type that declares a test-connection handler (describe()'s
// AssetTypeDef.TestConnection) gets the button through the very same generic
// TestAssetConnection path the "serial" case above exercises for a built-in type
// (task 10, docs/specs 2026-09-24-ext-platform-capabilities.md 测试连接): the form
// must show the outcome to the user either way, success or failure.
describe("extension asset type test connection surfaces the outcome in the form", () => {
  const testableExtManifest = {
    name: "acme",
    version: "1.0.0",
    icon: "cloud",
    i18n: { displayName: "acme", description: "" },
    assetTypes: [
      {
        type: "acme-store",
        i18n: { name: "acme" },
        configSchema: { type: "object", properties: { endpoint: { type: "string" } } },
        testConnection: true,
      },
    ],
    policies: { type: "ext:acme", actions: [], groups: [], default: [] },
  } as unknown as ExtManifest;

  const acmeAsset = new asset_entity.Asset({
    ID: 11,
    Name: "acme-1",
    Type: "acme-store",
    Config: JSON.stringify({ endpoint: "http://x" }),
  });

  beforeEach(() => registerExtensionAssetTypes("acme", testableExtManifest));
  afterEach(() => unregisterExtensionAssetTypes("acme"));

  it("a successful test connection notifies the user of success", async () => {
    vi.mocked(TestAssetConnection).mockResolvedValue(undefined as never);

    render(<AssetForm open editAsset={acmeAsset} onOpenChange={vi.fn()} />);
    const button = await screen.findByTestId("asset-test-connection");
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.click(button);

    await waitFor(() =>
      expect(TestAssetConnection).toHaveBeenCalledWith(expect.any(String), "acme-store", expect.any(String), "11")
    );
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("asset.testConnectionSuccess"));
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("a failed test connection shows the failure to the user", async () => {
    vi.mocked(TestAssetConnection).mockRejectedValue(new Error("connection refused"));

    render(<AssetForm open editAsset={acmeAsset} onOpenChange={vi.fn()} />);
    const button = await screen.findByTestId("asset-test-connection");
    await waitFor(() => expect(button).toBeEnabled());
    await userEvent.click(button);

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("connection refused")));
    expect(notifySuccess).not.toHaveBeenCalled();
  });
});
