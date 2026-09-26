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

// 扩展未声明 credentials:read：宿主不把已存密码交给表单（扩展页面与表单同一个
// webview，能直接调这个绑定），GetDecryptedExtensionConfig 里没有该字段。表单按内置
// 资产的"已设置，留空则不修改"呈现，保存时沿用已存密文。
describe("ExtensionConfigSection stored password withheld", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ endpoint: "https://x" }));
  });

  it("shows the password as set-but-hidden, never as a value", async () => {
    render(<Section editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const input = screen.getByLabelText("Secret");
    expect(input).toHaveValue("");
    expect(input).toHaveAttribute("placeholder", "asset.passwordUnchanged");
    expect(screen.queryByDisplayValue("[object Object]")).not.toBeInTheDocument();
    expect(screen.queryByDisplayValue(CIPHERTEXT)).not.toBeInTheDocument();
  });

  it("saving untouched keeps the stored ciphertext as is", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Section ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "https://x", secret: CIPHERTEXT });
  });

  it("a newly typed password replaces the stored one", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Section ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    fireEvent.change(screen.getByLabelText("Secret"), { target: { value: "rotated" } });

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "https://x", secret: "ENC(rotated)" });
  });

  it("test connection leaves the untouched password for the host to fill from the stored value", async () => {
    const Testable = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema: {
        type: "object",
        properties: {
          endpoint: { type: "string", title: "Endpoint" },
          secret: { type: "string", format: "password", title: "Secret" },
        },
      },
      testConnection: true,
    });
    const ref = createRef<AssetFormHandle>();
    render(<Testable ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.password).toBe("3");
    expect(JSON.parse(tc.configJSON)).toEqual({ endpoint: "https://x" });
  });
});

// 复制资产：表单拿到的是源资产的完整存储配置（ID 为 0，密码字段是密文）。密文不能进
// 输入框，也不能被当成明文再加密一遍——那会把复制出来的资产的密钥毁掉。
describe("ExtensionConfigSection copying an asset", () => {
  function copiedAsset() {
    return new asset_entity.Asset({
      ID: 0,
      Name: "demo - copy",
      Type: "demo-type",
      Config: JSON.stringify({ endpoint: "https://x", secret: CIPHERTEXT }),
    });
  }

  it("keeps the source's stored secret as is instead of encrypting its ciphertext again", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Section ref={ref} editAsset={copiedAsset()} ctx={ctx} onValidityChange={() => {}} />);

    expect(screen.queryByDisplayValue(CIPHERTEXT)).not.toBeInTheDocument();
    expect(screen.getByLabelText("Secret")).toHaveAttribute("placeholder", "asset.passwordUnchanged");
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "https://x", secret: CIPHERTEXT });
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

  // 宿主拨号时非空代理链优先于隧道列：两者都设会静默跳过用户选的 SSH 隧道，只能拦在保存前。
  it("a type declaring both blocks saving while a tunnel and a chain are both set", async () => {
    const Both = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema,
      connection: { sshTunnel: true, proxyChain: true },
    });
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
    const asset = chainAsset(savedLayer);
    asset.sshTunnelId = 9;
    const onValidity = vi.fn();
    render(<Both editAsset={asset} ctx={ctx} onValidityChange={onValidity} />);
    await act(async () => {});

    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, saveDisabledReason: "asset.formTunnelWithProxyChain" })
    );
  });

  // 同一个"连接方式"选择器里选中 SSH 隧道，代理链就不再生效：与切到直连一样不再持久化它
  // （本次会话里切回代理链时恢复），而不是让看不见的链路把保存拦下来、或随测试连接一起发出去。
  it("choosing the tunnel in the selector drops a configured chain from save and test, and restores it on switching back", async () => {
    const Both = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema,
      connection: { sshTunnel: true, proxyChain: true },
      testConnection: true,
    });
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
    const asset = chainAsset(savedLayer);
    asset.sshTunnelId = 9;
    const ref = createRef<AssetFormHandle>();
    const onValidity = vi.fn();
    render(<Both ref={ref} editAsset={asset} ctx={ctx} onValidityChange={onValidity} />);
    await act(async () => {});

    // 先看到链路，再选隧道。
    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionTunnelProxy" }));
    expect(screen.getByText("Existing Hop")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("radio", { name: "asset.sshTunnel" }));

    expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: true }));
    const built = await ref.current!.buildConfig(ctx);
    expect(built.sshTunnelId).toBe(9);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
    const tested = await ref.current!.buildTestConfig!(ctx);
    expect(JSON.parse(tested.configJSON)[HOST_CONNECTION_CONFIG_KEY]).toEqual({ sshTunnelId: 9 });

    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionTunnelProxy" }));
    expect(screen.getByText("Existing Hop")).toBeInTheDocument();
  });
});

// 声明了 sshTunnel + proxyChain 两项时,历史实现各画一组 Segmented,都以 "asset.connectionType"
// 为 aria-label,渲染成两个并列的"连接方式"单选组。这里验证合并成一组、且切换方式不丢已选值。
describe("ExtensionConfigSection connection settings — unified selector", () => {
  const schema = {
    type: "object",
    properties: { endpoint: { type: "string", title: "Endpoint" } },
  } as const;
  const Both = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
    connection: { sshTunnel: true, proxyChain: true },
  });
  const Tunneled = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
    connection: { sshTunnel: true },
  });

  function assetWithTunnel(sshTunnelId: number) {
    return new asset_entity.Asset({
      ID: 11,
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

  it("declaring both sshTunnel and proxyChain renders exactly one connection-method selector", async () => {
    render(<Both editAsset={assetWithTunnel(0)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    expect(screen.getAllByRole("radiogroup", { name: "asset.connectionType" })).toHaveLength(1);
    expect(screen.getByRole("radio", { name: "asset.connectionDirect" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "asset.sshTunnel" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "asset.connectionTunnelProxy" })).toBeInTheDocument();
  });

  it("switching the tunnel off and back on keeps the previously selected SSH asset", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Tunneled ref={ref} editAsset={assetWithTunnel(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionDirect" }));
    fireEvent.click(screen.getByRole("radio", { name: "asset.sshTunnel" }));

    expect((await ref.current!.buildConfig(ctx)).sshTunnelId).toBe(9);
  });

  it("declaring both, choosing the tunnel saves only the tunnel — not the reserved proxy-chain key", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Both ref={ref} editAsset={assetWithTunnel(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const built = await ref.current!.buildConfig(ctx);
    expect(built.sshTunnelId).toBe(9);
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
