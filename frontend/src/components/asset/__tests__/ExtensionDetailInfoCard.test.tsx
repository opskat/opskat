import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { makeExtensionDetailInfoCard } from "@/components/asset/detail/ExtensionDetailInfoCard";
import { asset_entity } from "../../../../wailsjs/go/models";

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
