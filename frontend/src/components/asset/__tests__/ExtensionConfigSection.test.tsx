import { describe, it, expect, beforeEach, vi } from "vitest";
import { createRef } from "react";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { makeExtensionConfigSection } from "@/components/asset/ExtensionConfigSection";
import type { AssetFormHandle } from "@/lib/assetTypes/formContract";
import { toast } from "sonner";
import { GetDecryptedExtensionConfig } from "../../../../wailsjs/go/extension/Extension";
import { asset_entity } from "../../../../wailsjs/go/models";
import { HOST_CONNECTION_CONFIG_KEY } from "@/extension/connectionConfig";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

const Section = makeExtensionConfigSection({
  extensionName: "demo",
  assetType: "demo-type",
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
    schema,
    connection: { sshTunnel: true },
  });
  const Plain = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
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

describe("ExtensionConfigSection proxy chain", () => {
  const schema = {
    type: "object",
    properties: { endpoint: { type: "string", title: "Endpoint" } },
  } as const;
  const Chained = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
    connection: { proxyChain: true },
  });
  const Plain = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
  });

  function chainAsset(layer?: Record<string, unknown>) {
    const config: Record<string, unknown> = { endpoint: "http://es.internal:9200" };
    if (layer) {
      config[HOST_CONNECTION_CONFIG_KEY] = { proxyChain: { layers: [layer] } };
    }
    return new asset_entity.Asset({ ID: 5, Name: "es", Type: "demo-type", Config: JSON.stringify(config) });
  }

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("an undeclared proxy chain shows no chain control", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "http://es.internal:9200" }));
    render(<Plain editAsset={chainAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.queryByRole("radio", { name: "asset.connectionTunnelProxy" })).not.toBeInTheDocument();
  });

  it("a declared chain is hidden in direct mode and never reaches the reserved key on save", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "http://es.internal:9200" }));
    const ref = createRef<AssetFormHandle>();
    render(<Chained ref={ref} editAsset={chainAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.getByRole("radio", { name: "asset.connectionTunnelProxy" })).toBeInTheDocument();
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
  });

  it("a saved chain loads into the UI and round-trips through the reserved key on save", async () => {
    const savedLayer = {
      id: "hop1",
      name: "Existing Hop",
      enabled: true,
      type: "socks5",
      order: 1,
      host: "10.0.0.5",
      port: 1080,
      password: "ENC(prev)",
    };
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
      JSON.stringify({
        endpoint: "http://es.internal:9200",
        [HOST_CONNECTION_CONFIG_KEY]: { proxyChain: { layers: [savedLayer] } },
      })
    );
    const ref = createRef<AssetFormHandle>();
    render(<Chained ref={ref} editAsset={chainAsset(savedLayer)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.getByText("Existing Hop")).toBeInTheDocument();

    const built = await ref.current!.buildConfig(ctx);
    const parsed = JSON.parse(built.configJSON);
    expect(parsed.endpoint).toBe("http://es.internal:9200");
    expect(parsed[HOST_CONNECTION_CONFIG_KEY].proxyChain.layers).toEqual([
      expect.objectContaining({ id: "hop1", type: "socks5", host: "10.0.0.5", port: 1080, password: "ENC(prev)" }),
    ]);
  });

  it("switching a saved chain back to direct drops it from the reserved key", async () => {
    const savedLayer = {
      id: "hop1",
      name: "Existing Hop",
      enabled: true,
      type: "socks5",
      host: "10.0.0.5",
      port: 1080,
    };
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
      JSON.stringify({
        endpoint: "http://es.internal:9200",
        [HOST_CONNECTION_CONFIG_KEY]: { proxyChain: { layers: [savedLayer] } },
      })
    );
    const ref = createRef<AssetFormHandle>();
    render(<Chained ref={ref} editAsset={chainAsset(savedLayer)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionDirect" }));

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
  });
});

describe("ExtensionConfigSection TLS", () => {
  const schema = {
    type: "object",
    properties: { endpoint: { type: "string", title: "Endpoint" } },
  } as const;
  const TLSAware = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
    connection: { tls: true },
  });
  const Plain = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
  });

  function tlsAsset(tls?: Record<string, unknown>) {
    const config: Record<string, unknown> = { endpoint: "http://es.internal:9200" };
    if (tls) config[HOST_CONNECTION_CONFIG_KEY] = { tls };
    return new asset_entity.Asset({ ID: 6, Name: "es", Type: "demo-type", Config: JSON.stringify(config) });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "http://es.internal:9200" }));
  });

  it("an undeclared TLS setting shows no TLS toggle", async () => {
    render(<Plain editAsset={tlsAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.queryByRole("switch", { name: "asset.tls" })).not.toBeInTheDocument();
  });

  it("a declared TLS setting left off saves no reserved TLS key", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<TLSAware ref={ref} editAsset={tlsAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.getByRole("switch", { name: "asset.tls" })).toBeInTheDocument();
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
  });

  it("enabling TLS and filling fields saves them into the reserved key", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<TLSAware ref={ref} editAsset={tlsAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    fireEvent.click(screen.getByRole("switch", { name: "asset.tls" }));
    fireEvent.change(screen.getByLabelText("asset.tlsServerName"), { target: { value: "es.example.com" } });
    fireEvent.change(screen.getByLabelText("asset.tlsCAFile"), { target: { value: "/etc/ca.pem" } });

    const built = await ref.current!.buildConfig(ctx);
    const parsed = JSON.parse(built.configJSON);
    expect(parsed[HOST_CONNECTION_CONFIG_KEY].tls).toEqual(
      expect.objectContaining({ enabled: true, serverName: "es.example.com", caFile: "/etc/ca.pem" })
    );
  });

  it("a saved TLS config loads into the UI", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
      JSON.stringify({
        endpoint: "http://es.internal:9200",
        [HOST_CONNECTION_CONFIG_KEY]: { tls: { enabled: true, insecure: true, serverName: "es.example.com" } },
      })
    );
    render(
      <TLSAware
        editAsset={tlsAsset({ enabled: true, insecure: true, serverName: "es.example.com" })}
        ctx={ctx}
        onValidityChange={() => {}}
      />
    );
    await act(async () => {});

    expect(screen.getByRole("switch", { name: "asset.tls" })).toBeChecked();
    expect(screen.getByDisplayValue("es.example.com")).toBeInTheDocument();
  });
});

describe("ExtensionConfigSection test connection", () => {
  const schema = {
    type: "object",
    properties: {
      endpoint: { type: "string", title: "Endpoint" },
      secret: { type: "string", format: "password", title: "Secret" },
    },
  } as const;

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("no handler declared: the section never reports canTest, and exposes no buildTestConfig", () => {
    const NotTestable = makeExtensionConfigSection({ extensionName: "demo", assetType: "demo-type", schema });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<NotTestable ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={onValidity} />);

    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canTest: false }));
    expect(ref.current!.buildTestConfig).toBeNull();
  });

  it("handler declared, new asset: canTest follows canSave and the test config carries no asset id", async () => {
    const Testable = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema,
      testConnection: true,
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<Testable ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={onValidity} />);

    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canTest: true }));
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.assetType).toBe("demo-type");
    expect(tc.password).toBe("");
    expect(JSON.parse(tc.configJSON)).toEqual({});
  });

  it("handler declared, editing: an untouched password field is dropped, a retyped one is sent, and the asset id rides in password", async () => {
    const Testable = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema,
      testConnection: true,
    });
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
      JSON.stringify({ endpoint: "https://x", secret: "stored-plain" })
    );
    const ref = createRef<AssetFormHandle>();
    render(<Testable ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const untouched = await ref.current!.buildTestConfig!(ctx);
    expect(untouched.password).toBe("3");
    expect(JSON.parse(untouched.configJSON)).toEqual({ endpoint: "https://x" });

    fireEvent.change(screen.getByDisplayValue("stored-plain"), { target: { value: "new-plain" } });
    const touched = await ref.current!.buildTestConfig!(ctx);
    expect(JSON.parse(touched.configJSON)).toEqual({ endpoint: "https://x", secret: "new-plain" });
  });

  it("connection settings ride the test config as the reserved key, including an ad-hoc SSH tunnel id", async () => {
    const Testable = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema: { type: "object", properties: { endpoint: { type: "string", title: "Endpoint" } } },
      connection: { sshTunnel: true },
      testConnection: true,
    });
    const asset = new asset_entity.Asset({
      ID: 9,
      Name: "es",
      Type: "demo-type",
      Config: JSON.stringify({ endpoint: "http://es.internal:9200" }),
      sshTunnelId: 7,
    });
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "http://es.internal:9200" }));
    const ref = createRef<AssetFormHandle>();
    render(<Testable ref={ref} editAsset={asset} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const tc = await ref.current!.buildTestConfig!(ctx);
    const parsed = JSON.parse(tc.configJSON);
    expect(parsed[HOST_CONNECTION_CONFIG_KEY]).toEqual(expect.objectContaining({ sshTunnelId: 7 }));
  });
});
