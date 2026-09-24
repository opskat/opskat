import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, act, fireEvent } from "@testing-library/react";
import { TooltipProvider } from "@opskat/ui";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { registerAssetType, unregisterAssetType } from "@/lib/assetTypes";
import type { AssetTypeDefinition } from "@/lib/assetTypes/types";
import { useAssetStore } from "@/stores/assetStore";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";
import { asset_entity } from "../../../../wailsjs/go/models";

const TYPE = "late-ext-type";

// 扩展类型在 ext:ready 之后才注册：详情页可能先于定义打开。
const lateDef = {
  type: TYPE,
  icon: () => null,
  aliases: [TYPE],
  label: TYPE,
  category: "extension",
  canConnect: false,
  canConnectInNewTab: false,
  DetailInfoCard: () => null,
  ConfigSection: () => null,
  testable: false,
  policy: {
    policyType: TYPE,
    titleKey: "title",
    fields: [
      { key: "allow_list", labelKey: "asset.cmdPolicyAllowList", placeholder: "allow-input", variant: "allow" },
      { key: "deny_list", labelKey: "asset.cmdPolicyDenyList", placeholder: "deny-input", variant: "deny" },
    ],
  },
} as unknown as AssetTypeDefinition;

function makeAsset(): asset_entity.Asset {
  return new asset_entity.Asset({
    ID: 7,
    Name: "ext-asset",
    Type: TYPE,
    Config: "{}",
    CmdPolicy: JSON.stringify({ allow_list: ["list"], deny_list: ["delete"] }),
  });
}

describe("AssetDetail policy when the type definition registers late", () => {
  const updateAsset = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => {
    updateAsset.mockClear();
    useAssetStore.setState({ updateAsset });
  });

  afterEach(() => {
    unregisterAssetType(TYPE);
  });

  it("shows the stored rules and keeps them when a rule is added after registration", async () => {
    const noop = () => {};
    render(
      <TooltipProvider>
        <AssetDetail asset={makeAsset()} onEdit={noop} onDelete={noop} onConnect={noop} />
      </TooltipProvider>
    );

    await act(async () => {
      registerAssetType(lateDef);
    });

    // 已存规则必须在定义到位后显示出来
    expect(screen.getByText("list")).toBeInTheDocument();
    expect(screen.getByText("delete")).toBeInTheDocument();

    const input = screen.getByPlaceholderText("allow-input");
    fireEvent.change(input, { target: { value: "get" } });
    await act(async () => {
      fireEvent.keyDown(input, { key: "Enter" });
    });

    expect(updateAsset).toHaveBeenCalledTimes(1);
    const saved = JSON.parse(updateAsset.mock.calls[0][0].CmdPolicy);
    // 不能因为 policyFields 停在 {} 而把已有 allow/deny 覆盖掉
    expect(saved).toEqual({ allow_list: ["list", "get"], deny_list: ["delete"] });
  });
});

// 扩展资产的规则与 opsctl / 运行期同一份：存储形态是 ext:<policyType>:<action>[:<glob>]，
// 卡片上显示去掉前缀的 <action>[:<glob>]。
describe("AssetDetail policy card for an extension asset type", () => {
  const updateAsset = vi.fn().mockResolvedValue(undefined);
  const manifest = {
    name: "acme",
    version: "1.0.0",
    icon: "cloud",
    i18n: { displayName: "acme", description: "" },
    assetTypes: [
      {
        type: "acme-store",
        i18n: { name: "acme" },
        configSchema: { type: "object", properties: { endpoint: { type: "string" } } },
      },
    ],
    policies: { type: "acme", actions: ["delete", "read", "write"], groups: [], default: [] },
  } as unknown as ExtManifest;

  beforeEach(() => {
    updateAsset.mockClear();
    useAssetStore.setState({ updateAsset });
    registerExtensionAssetTypes("acme", manifest);
  });

  afterEach(() => {
    unregisterExtensionAssetTypes("acme");
  });

  it("shows stored rules without the namespace and writes card-entered rules canonically", async () => {
    const noop = () => {};
    const asset = new asset_entity.Asset({
      ID: 8,
      Name: "acme-1",
      Type: "acme-store",
      Config: "{}",
      // 这两条是 opsctl policy allow/deny 写下的形态。
      CmdPolicy: JSON.stringify({ allow_list: ["ext:acme:write:runbook/*"], deny_list: ["ext:acme:delete"] }),
    });
    render(
      <TooltipProvider>
        <AssetDetail asset={asset} onEdit={noop} onDelete={noop} onConnect={noop} />
      </TooltipProvider>
    );

    expect(screen.getByText("write:runbook/*")).toBeInTheDocument();
    expect(screen.getByText("delete")).toBeInTheDocument();
    expect(screen.queryByText("ext:acme:delete")).not.toBeInTheDocument();

    const input = screen.getAllByPlaceholderText("delete, read, write")[0];
    fireEvent.change(input, { target: { value: "read:logs/*" } });
    await act(async () => {
      fireEvent.keyDown(input, { key: "Enter" });
    });

    expect(updateAsset).toHaveBeenCalledTimes(1);
    const saved = JSON.parse(updateAsset.mock.calls[0][0].CmdPolicy);
    expect(saved).toEqual({
      allow_list: ["ext:acme:write:runbook/*", "ext:acme:read:logs/*"],
      deny_list: ["ext:acme:delete"],
    });
  });
});
