import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { makeExtensionDetailInfoCard } from "@/components/asset/detail/ExtensionDetailInfoCard";
import { asset_entity } from "../../../../wailsjs/go/models";
import { HOST_CONNECTION_CONFIG_KEY } from "@/extension/connectionConfig";

const schema = { type: "object", properties: { endpoint: { type: "string", title: "Endpoint" } } } as const;
const asset = new asset_entity.Asset({
  ID: 4,
  Name: "es",
  Type: "demo-type",
  Config: JSON.stringify({ endpoint: "http://es.internal:9200" }),
  sshTunnelId: 9,
});
const sshTunnelName = (id?: number) => (id === 9 ? "bastion" : "");

describe("ExtensionDetailInfoCard SSH tunnel", () => {
  it("shows the asset's SSH tunnel when the type declares it", () => {
    const Card = makeExtensionDetailInfoCard({
      displayNameKey: "demo",
      ns: "ext-demo",
      assetType: "demo-type",
      schema,
      connection: { sshTunnel: true },
    });
    render(<Card asset={asset} sshTunnelName={sshTunnelName} />);

    expect(screen.getByText("asset.sshTunnel")).toBeInTheDocument();
    expect(screen.getByText("bastion")).toBeInTheDocument();
  });

  it("shows no tunnel for a type that does not declare it", () => {
    const Card = makeExtensionDetailInfoCard({
      displayNameKey: "demo",
      ns: "ext-demo",
      assetType: "demo-type",
      schema,
    });
    render(<Card asset={asset} sshTunnelName={sshTunnelName} />);

    expect(screen.queryByText("bastion")).not.toBeInTheDocument();
  });
});

describe("ExtensionDetailInfoCard proxy chain and TLS", () => {
  const chainAsset = new asset_entity.Asset({
    ID: 5,
    Name: "es",
    Type: "demo-type",
    Config: JSON.stringify({
      endpoint: "http://es.internal:9200",
      [HOST_CONNECTION_CONFIG_KEY]: {
        proxyChain: {
          layers: [{ id: "hop1", name: "Bastion Hop", type: "socks5", order: 1, host: "10.0.0.5", port: 1080 }],
        },
        tls: { enabled: true, insecure: true },
      },
    }),
  });

  it("shows the proxy chain and TLS status when the type declares them", () => {
    const Card = makeExtensionDetailInfoCard({
      displayNameKey: "demo",
      ns: "ext-demo",
      assetType: "demo-type",
      schema,
      connection: { proxyChain: true, tls: true },
    });
    render(<Card asset={chainAsset} sshTunnelName={sshTunnelName} />);

    expect(screen.getByText("Bastion Hop")).toBeInTheDocument();
    expect(screen.getByText("asset.tls")).toBeInTheDocument();
  });

  it("shows neither when the type does not declare them", () => {
    const Card = makeExtensionDetailInfoCard({
      displayNameKey: "demo",
      ns: "ext-demo",
      assetType: "demo-type",
      schema,
    });
    render(<Card asset={chainAsset} sshTunnelName={sshTunnelName} />);

    expect(screen.queryByText("Bastion Hop")).not.toBeInTheDocument();
    expect(screen.queryByText("asset.tls")).not.toBeInTheDocument();
  });
});

// 详情卡与表单看同一份字段：换过认证方式后留在配置里的旧字段不再展示。
describe("ExtensionDetailInfoCard auth method fields", () => {
  it("shows only the fields of the asset's selected auth method", () => {
    const Card = makeExtensionDetailInfoCard({
      displayNameKey: "demo",
      ns: "ext-demo",
      assetType: "demo-type",
      schema: {
        type: "object",
        properties: {
          endpoint: { type: "string", title: "Endpoint" },
          authType: { type: "string", title: "Auth", enum: ["basic", "apiKey"] },
          username: { type: "string", title: "Username" },
          apiKey: { type: "string", format: "password", title: "API key" },
        },
      },
      auth: {
        selector: "authType",
        groups: [
          { when: "basic", fields: ["username"] },
          { when: "apiKey", fields: ["apiKey"] },
        ],
      },
    });
    const stale = new asset_entity.Asset({
      ID: 5,
      Name: "es",
      Type: "demo-type",
      Config: JSON.stringify({ endpoint: "http://es:9200", authType: "apiKey", username: "elastic", apiKey: "ENC" }),
    });
    render(<Card asset={stale} sshTunnelName={sshTunnelName} />);

    expect(screen.getByText("API key")).toBeInTheDocument();
    expect(screen.queryByText("Username")).not.toBeInTheDocument();
    expect(screen.queryByText("elastic")).not.toBeInTheDocument();
  });
});
