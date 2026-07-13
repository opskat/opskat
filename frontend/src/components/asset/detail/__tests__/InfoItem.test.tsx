import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProxyChainDetailSection } from "@/components/asset/detail/InfoItem";
import type { ProxyChainJSON } from "@/components/asset/proxyConfig";

describe("ProxyChainDetailSection", () => {
  it("renders nothing without layers", () => {
    const { container } = render(<ProxyChainDetailSection chain={null} resolveSshName={() => null} />);
    expect(container.firstChild).toBeNull();
  });

  it("renders the ssh hop name and socks5 host:port ordered by `order`", () => {
    const chain: ProxyChainJSON = {
      layers: [
        { type: "socks5", order: 2, name: "内网代理", host: "127.0.0.1", port: 1080 },
        { type: "ssh", order: 1, ssh_asset_id: 42 },
      ],
    };
    const { getByText } = render(
      <ProxyChainDetailSection chain={chain} resolveSshName={(id) => (id === 42 ? "bastion-01" : null)} />
    );
    expect(getByText("bastion-01")).toBeInTheDocument();
    expect(getByText("内网代理")).toBeInTheDocument();
    expect(getByText(/127\.0\.0\.1:1080/)).toBeInTheDocument();
    expect(getByText("SOCKS5")).toBeInTheDocument();
    expect(getByText("SSH")).toBeInTheDocument();
  });
});
