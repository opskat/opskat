import { render, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { CONNECTION_DEFAULTS, sshProxyLayer, socks5ProxyLayer } from "@/components/asset/proxyConfig";

// react-i18next is mocked in src/__tests__/setup.ts so `t(key)` returns the key verbatim.
function renderChain(over: Record<string, unknown> = {}, onChange = vi.fn()) {
  const value = { ...CONNECTION_DEFAULTS, connectionType: "jumphost" as const, ...over };
  return { onChange, ...render(<ConnectionMethodFields value={value} onChange={onChange} />) };
}

describe("ConnectionMethodFields", () => {
  it("renders the local and target endpoints plus each hop in chain mode", () => {
    const { getByText } = renderChain({ proxyChainLayers: [sshProxyLayer(42, "Bastion")] });
    expect(getByText("asset.proxyChainLocal")).toBeInTheDocument();
    expect(getByText("asset.proxyChainTargetLabel")).toBeInTheDocument();
    expect(getByText("Bastion")).toBeInTheDocument();
  });

  it("shows the empty state when chain mode has no layers", () => {
    const { getByText } = renderChain({ proxyChainLayers: [] });
    expect(getByText("asset.proxyChainEmptyTitle")).toBeInTheDocument();
  });

  it("expands a hop inline when its card is clicked", () => {
    const { getByRole, queryByDisplayValue } = renderChain({
      proxyChainLayers: [sshProxyLayer(42, "Bastion"), { ...socks5ProxyLayer(), id: "s1", name: "Proxy A" }],
    });
    // nothing selected initially -> the layer's editable name field is not mounted
    expect(queryByDisplayValue("Proxy A")).toBeNull();
    fireEvent.click(getByRole("button", { name: /Proxy A/ }));
    expect(queryByDisplayValue("Proxy A")).not.toBeNull();
  });

  it("shows the validation banner when an enabled layer is incomplete", () => {
    const { getByText } = renderChain({
      proxyChainLayers: [{ ...socks5ProxyLayer(), id: "s1", host: "" }],
    });
    expect(getByText("asset.proxyChainProblems")).toBeInTheDocument();
  });
});
