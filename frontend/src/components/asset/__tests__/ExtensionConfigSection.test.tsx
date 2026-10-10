import { describe, it, expect, beforeEach, vi } from "vitest";
import { createRef } from "react";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { makeExtensionConfigSection } from "@/components/asset/ExtensionConfigSection";
import type { AssetFormHandle } from "@/lib/assetTypes/formContract";
import { toast } from "sonner";
import { GetDecryptedExtensionConfig, ValidateExtensionConfig } from "../../../../wailsjs/go/extension/Extension";
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

// The guest's validator accepts unless a test says otherwise.
beforeEach(() => {
  vi.mocked(ValidateExtensionConfig).mockResolvedValue([]);
});

function editAsset() {
  return new asset_entity.Asset({
    ID: 3,
    Name: "demo",
    Type: "demo-type",
    Config: JSON.stringify({ endpoint: "https://x", secret: CIPHERTEXT }),
  });
}

const ctx = { isEdit: true, encryptPassword: (p: string) => Promise.resolve(`ENC(${p})`) };

// 宿主的连接方式与 TLS 各在自己的标签里，操作前先切过去。
function openTab(key: "connection" | "tunnel" | "tls") {
  fireEvent.click(screen.getByTestId(`config-tab-${key}`));
}

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

  // A copy has no asset id for the host to fill the kept secret from: testing it
  // without the secret would test a different configuration than Save stores.
  it("test connection asks for the kept secret instead of testing without it", async () => {
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
    render(<Testable ref={ref} editAsset={copiedAsset()} ctx={ctx} onValidityChange={() => {}} />);

    await expect(ref.current!.buildTestConfig!(ctx)).rejects.toThrow("asset.extTestCopiedSecret");

    fireEvent.change(screen.getByLabelText("Secret"), { target: { value: "typed" } });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.password).toBe("");
    expect(JSON.parse(tc.configJSON)).toEqual({ endpoint: "https://x", secret: "typed" });
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
    openTab("tunnel");

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
    openTab("tunnel");

    fireEvent.click(screen.getByRole("radio", { name: "asset.connectionDirect" }));

    expect(screen.queryByTestId("extension-ssh-tunnel-select")).not.toBeInTheDocument();
    expect((await ref.current!.buildConfig(ctx)).sshTunnelId).toBe(0);
  });

  it("choosing the tunnel without an SSH asset blocks saving", async () => {
    const onValidity = vi.fn();
    render(<Tunneled ctx={{ ...ctx, isEdit: false }} onValidityChange={onValidity} />);
    openTab("tunnel");

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
    openTab("tunnel");

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
    openTab("tunnel");

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
    openTab("tunnel");

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
    openTab("tunnel");

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
    openTab("tunnel");

    expect(screen.getAllByRole("radiogroup", { name: "asset.connectionType" })).toHaveLength(1);
    expect(screen.getByRole("radio", { name: "asset.connectionDirect" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "asset.sshTunnel" })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "asset.connectionTunnelProxy" })).toBeInTheDocument();
  });

  it("switching the tunnel off and back on keeps the previously selected SSH asset", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Tunneled ref={ref} editAsset={assetWithTunnel(9)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});
    openTab("tunnel");

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
  const TLSTestable = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
    connection: { tls: true },
    testConnection: true,
  });
  const Plain = makeExtensionConfigSection({
    extensionName: "demo",
    assetType: "demo-type",
    schema,
  });
  const CA_PEM = "-----BEGIN CERTIFICATE-----\nca\n-----END CERTIFICATE-----";
  const CERT_PEM = "-----BEGIN CERTIFICATE-----\nclient\n-----END CERTIFICATE-----";
  const KEY_PEM = "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----";

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
    openTab("tls");

    expect(screen.getByRole("switch", { name: "asset.tls" })).toBeInTheDocument();
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ endpoint: "http://es.internal:9200" });
  });

  // 证书可以直接填内容（PEM），也可以给本机路径；私钥内容是其中唯一的密钥，加密后才落盘。
  it("certificates entered as content are saved as content, the client key encrypted", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<TLSAware ref={ref} editAsset={tlsAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});
    openTab("tls");

    fireEvent.click(screen.getByRole("switch", { name: "asset.tls" }));
    fireEvent.change(screen.getByTestId("tls-server-name"), { target: { value: "es.example.com" } });
    fireEvent.change(screen.getByTestId("tls-ca-pem"), { target: { value: CA_PEM } });
    fireEvent.change(screen.getByTestId("tls-cert-pem"), { target: { value: `${CERT_PEM}\n` } });
    fireEvent.change(screen.getByTestId("tls-key-pem"), { target: { value: KEY_PEM } });

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls).toEqual({
      enabled: true,
      serverName: "es.example.com",
      caCert: CA_PEM,
      clientCert: CERT_PEM,
      clientKey: `ENC(${KEY_PEM})`,
    });
  });

  it("certificates given as paths are saved as paths", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<TLSAware ref={ref} editAsset={tlsAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});
    openTab("tls");

    fireEvent.click(screen.getByRole("switch", { name: "asset.tls" }));
    fireEvent.click(screen.getByTestId("tls-cert-source-file"));
    fireEvent.change(screen.getByTestId("tls-ca-file"), { target: { value: "/etc/ca.pem" } });
    fireEvent.change(screen.getByTestId("tls-cert-file"), { target: { value: "/etc/client.crt" } });
    fireEvent.change(screen.getByTestId("tls-key-file"), { target: { value: "/etc/client.key" } });

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls).toEqual({
      enabled: true,
      caFile: "/etc/ca.pem",
      certFile: "/etc/client.crt",
      keyFile: "/etc/client.key",
    });
  });

  it("a config saved with paths opens on the path source", async () => {
    const stored = { enabled: true, caFile: "/etc/ca.pem" };
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
      JSON.stringify({ endpoint: "http://es.internal:9200", [HOST_CONNECTION_CONFIG_KEY]: { tls: stored } })
    );
    render(<TLSAware editAsset={tlsAsset(stored)} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});
    openTab("tls");

    expect(screen.getByTestId("tls-ca-file")).toHaveValue("/etc/ca.pem");
    expect(screen.queryByTestId("tls-ca-pem")).not.toBeInTheDocument();
  });

  describe("with a client key already stored", () => {
    const storedTLS = { enabled: true, clientCert: CERT_PEM, clientKey: CIPHERTEXT };

    function renderStored(ref: React.RefObject<AssetFormHandle | null>, asset = tlsAsset(storedTLS)) {
      vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(
        JSON.stringify({ endpoint: "http://es.internal:9200", [HOST_CONNECTION_CONFIG_KEY]: { tls: storedTLS } })
      );
      render(<TLSTestable ref={ref} editAsset={asset} ctx={ctx} onValidityChange={() => {}} />);
    }

    it("the key is shown as set, never as its stored value, and saved untouched as it was stored", async () => {
      const ref = createRef<AssetFormHandle>();
      renderStored(ref);
      await act(async () => {});
      openTab("tls");

      const key = screen.getByTestId("tls-key-pem");
      expect(key).toHaveValue("");
      expect(key).toHaveAttribute("placeholder", "asset.passwordUnchanged");
      expect(screen.queryByDisplayValue(CIPHERTEXT)).not.toBeInTheDocument();

      const built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls).toEqual(storedTLS);
    });

    it("a retyped key replaces the stored one", async () => {
      const ref = createRef<AssetFormHandle>();
      renderStored(ref);
      await act(async () => {});
      openTab("tls");

      fireEvent.change(screen.getByTestId("tls-key-pem"), { target: { value: KEY_PEM } });

      const built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls.clientKey).toBe(`ENC(${KEY_PEM})`);
    });

    it("removing the client certificate drops its stored key", async () => {
      const ref = createRef<AssetFormHandle>();
      renderStored(ref);
      await act(async () => {});
      openTab("tls");

      fireEvent.change(screen.getByTestId("tls-cert-pem"), { target: { value: "" } });

      const built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls).toEqual({ enabled: true });
    });

    // 宿主只认私钥密文：测试连接送出的与保存的是同一份，没碰过就是已存密文，重填的先加密。
    it("test connection sends the key as ciphertext: the stored one untouched, a retyped one encrypted", async () => {
      const ref = createRef<AssetFormHandle>();
      renderStored(ref);
      await act(async () => {});
      openTab("tls");

      const untouched = await ref.current!.buildTestConfig!(ctx);
      expect(JSON.parse(untouched.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls).toEqual(storedTLS);

      fireEvent.change(screen.getByTestId("tls-key-pem"), { target: { value: KEY_PEM } });
      const retyped = await ref.current!.buildTestConfig!(ctx);
      expect(JSON.parse(retyped.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls.clientKey).toBe(`ENC(${KEY_PEM})`);
    });

    // 复制出来的资产带着源资产的私钥密文，测试连接照样用得上它。
    it("test connection on a copy uses the key kept from the source asset", async () => {
      const ref = createRef<AssetFormHandle>();
      const copy = tlsAsset(storedTLS);
      copy.ID = 0;
      render(<TLSTestable ref={ref} editAsset={copy} ctx={ctx} onValidityChange={() => {}} />);

      const tc = await ref.current!.buildTestConfig!(ctx);
      expect(JSON.parse(tc.configJSON)[HOST_CONNECTION_CONFIG_KEY].tls.clientKey).toBe(CIPHERTEXT);
    });
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
    openTab("tls");

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

// 保存前把配置交给扩展的校验器：逐字段错误显示在对应字段上，且不放行保存。
describe("ExtensionConfigSection validation errors", () => {
  const authSchema = {
    type: "object",
    properties: {
      username: { type: "string", title: "Username" },
      secret: { type: "string", format: "password", title: "Secret" },
      auth: {
        type: "string",
        title: "Auth",
        enum: ["none", "basic"],
        enumLabels: ["No auth", "Basic auth"],
        default: "none",
      },
    },
  };
  const Validated = makeExtensionConfigSection({ extensionName: "demo", assetType: "demo-type", schema: authSchema });

  it("shows a field error on its field and refuses the save", async () => {
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([
      { field: "username", message: "username is required for basic authentication" },
    ] as never);
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    await expect(ref.current!.buildConfig({ ...ctx, isEdit: false })).rejects.toThrow();

    await screen.findByText("username is required for basic authentication");
    expect(screen.getByLabelText("Username")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Secret")).not.toHaveAttribute("aria-invalid", "true");
  });

  it("clears a field's error once the user edits that field", async () => {
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([
      { field: "username", message: "username is required" },
    ] as never);
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);
    await expect(ref.current!.buildConfig({ ...ctx, isEdit: false })).rejects.toThrow();
    await screen.findByText("username is required");

    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "elastic" } });

    expect(screen.queryByText("username is required")).not.toBeInTheDocument();
  });

  it("an error naming no field of the form is carried by the refusal instead", async () => {
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([{ field: "", message: "config is inconsistent" }] as never);
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    await expect(ref.current!.buildConfig({ ...ctx, isEdit: false })).rejects.toThrow("config is inconsistent");
  });

  // A field the form does not show (left out of propertyOrder, or no declared field
  // at all, even one named like an Object.prototype member) has nowhere to show its
  // error, so the refusal carries it.
  // 没选中的认证方式，其字段不在表单里：落在它上面的错误同样没处显示。
  it("an error on a field of an auth method that is not selected is carried by the refusal", async () => {
    const WithAuth = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema: authSchema,
      auth: { selector: "auth", groups: [{ when: "basic", fields: ["username", "secret"] }] },
    });
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([{ field: "username", message: "stale username" }] as never);
    const ref = createRef<AssetFormHandle>();
    render(<WithAuth ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
    await expect(ref.current!.buildConfig({ ...ctx, isEdit: false })).rejects.toThrow("username: stale username");
  });

  it("an error on a field the form does not show is carried by the refusal", async () => {
    const Ordered = makeExtensionConfigSection({
      extensionName: "demo",
      assetType: "demo-type",
      schema: { ...authSchema, propertyOrder: ["username", "secret"] },
    });
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([
      { field: "auth", message: "unknown authentication type" },
      { field: "constructor", message: "not a field" },
    ] as never);
    const ref = createRef<AssetFormHandle>();
    render(<Ordered ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    await expect(ref.current!.buildConfig({ ...ctx, isEdit: false })).rejects.toThrow(
      "auth: unknown authentication type; constructor: not a field"
    );
  });

  // Go hands a nil error list over IPC as null; the host's own save check reads it as valid.
  it("a validator answering no error list at all lets the save through", async () => {
    vi.mocked(ValidateExtensionConfig).mockResolvedValue(null as never);
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    const built = await ref.current!.buildConfig({ ...ctx, isEdit: false });
    expect(JSON.parse(built.configJSON)).toEqual({ auth: "none" });
  });

  it("validates the config that would be saved: an untouched stored secret counts as filled", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ username: "u", auth: "basic" }));
    const ref = createRef<AssetFormHandle>();
    const asset = new asset_entity.Asset({
      ID: 3,
      Type: "demo-type",
      Config: JSON.stringify({ username: "u", auth: "basic", secret: CIPHERTEXT }),
    });
    render(<Validated ref={ref} editAsset={asset} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    await ref.current!.buildConfig(ctx);

    expect(ValidateExtensionConfig).toHaveBeenCalledWith("demo", "demo-type", expect.any(String));
    const sent = JSON.parse(vi.mocked(ValidateExtensionConfig).mock.calls.at(-1)![2]);
    expect(sent).toEqual({ username: "u", auth: "basic", secret: CIPHERTEXT });
  });

  it("a new asset starts with the declared default selected and saved", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} ctx={{ ...ctx, isEdit: false }} onValidityChange={() => {}} />);

    expect(screen.getByRole("combobox")).toHaveTextContent("No auth");
    const built = await ref.current!.buildConfig({ ...ctx, isEdit: false });
    expect(JSON.parse(built.configJSON)).toEqual({ auth: "none" });
  });

  it("an asset saved without the field is not given the default behind the user's back", async () => {
    vi.mocked(GetDecryptedExtensionConfig).mockResolvedValue(JSON.stringify({ username: "u" }));
    const ref = createRef<AssetFormHandle>();
    render(<Validated ref={ref} editAsset={editAsset()} ctx={ctx} onValidityChange={() => {}} />);
    await act(async () => {});

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({ username: "u", secret: CIPHERTEXT });
  });
});

// 扩展类型与内置类型用同一套标签：schema 字段在"连接"，宿主的连接方式在"隧道/代理"，
// 宿主的 TLS 在"TLS/证书"；只有 describe() 声明了的项才出标签。
describe("ExtensionConfigSection tabs", () => {
  const schema = {
    type: "object",
    properties: { endpoint: { type: "string", title: "Endpoint" } },
  } as const;
  const base = { extensionName: "demo", assetType: "demo-type", schema };
  const Full = makeExtensionConfigSection({ ...base, connection: { sshTunnel: true, proxyChain: true, tls: true } });
  const Tunneled = makeExtensionConfigSection({ ...base, connection: { sshTunnel: true } });
  const Plain = makeExtensionConfigSection(base);
  const createCtx = { ...ctx, isEdit: false };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([]);
  });

  it("splits the schema fields, the connection method and TLS into their own tabs", () => {
    render(<Full ctx={createCtx} onValidityChange={() => {}} />);

    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      "asset.tabConnection",
      "asset.tabTunnel",
      "asset.tabTls",
    ]);
    expect(screen.getByLabelText("Endpoint")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "asset.connectionType" })).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "asset.tls" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("config-tab-tunnel"));
    expect(screen.getByRole("radiogroup", { name: "asset.connectionType" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Endpoint")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("config-tab-tls"));
    expect(screen.getByRole("switch", { name: "asset.tls" })).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup", { name: "asset.connectionType" })).not.toBeInTheDocument();
  });

  it("gives a tab only to the connection settings the type declared", () => {
    render(<Tunneled ctx={createCtx} onValidityChange={() => {}} />);

    expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
      "asset.tabConnection",
      "asset.tabTunnel",
    ]);
  });

  it("a type declaring no connection settings keeps a plain form without a tab bar", () => {
    render(<Plain ctx={createCtx} onValidityChange={() => {}} />);

    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Endpoint")).toBeInTheDocument();
  });

  it("values typed on one tab survive visiting another and are saved", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Full ref={ref} ctx={createCtx} onValidityChange={() => {}} />);

    fireEvent.change(screen.getByLabelText("Endpoint"), { target: { value: "https://es.internal:9200" } });
    fireEvent.click(screen.getByTestId("config-tab-tls"));
    fireEvent.click(screen.getByRole("switch", { name: "asset.tls" }));
    fireEvent.click(screen.getByTestId("config-tab-connection"));

    expect(screen.getByLabelText("Endpoint")).toHaveValue("https://es.internal:9200");
    const built = await ref.current!.buildConfig(createCtx);
    expect(JSON.parse(built.configJSON)).toEqual({
      endpoint: "https://es.internal:9200",
      [HOST_CONNECTION_CONFIG_KEY]: { tls: { enabled: true } },
    });
  });

  // 字段错误只画在"连接"标签里：停在别的标签上保存被拒时，得把用户带回能看到它的地方。
  it("a save refused for a field error brings back the tab showing that field", async () => {
    vi.mocked(ValidateExtensionConfig).mockResolvedValue([
      { field: "endpoint", message: "endpoint must be a URL" },
    ] as never);
    const ref = createRef<AssetFormHandle>();
    render(<Full ref={ref} ctx={createCtx} onValidityChange={() => {}} />);
    fireEvent.click(screen.getByTestId("config-tab-tls"));

    await expect(ref.current!.buildConfig(createCtx)).rejects.toThrow();

    await screen.findByText("endpoint must be a URL");
    expect(screen.getByTestId("config-tab-connection")).toHaveAttribute("aria-selected", "true");
  });
});
