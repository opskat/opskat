import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConnectionMethodFields } from "@/components/asset/ConnectionMethodFields";
import { CONNECTION_DEFAULTS, sshProxyLayer } from "@/components/asset/proxyConfig";

describe("ConnectionMethodFields", () => {
  it("代理层名称作为可点击选择区域显示手型光标", () => {
    const layer = sshProxyLayer(42, "Bastion");
    const { getByRole } = render(
      <ConnectionMethodFields
        value={{ ...CONNECTION_DEFAULTS, connectionType: "jumphost", proxyChainLayers: [layer] }}
        onChange={() => {}}
      />
    );

    expect(getByRole("button", { name: "Bastion" })).toHaveClass("cursor-pointer");
  });
});
