import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { AssetDetail } from "@/components/asset/AssetDetail";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { asset_entity, custom_type_entity, customtype } from "../../../../wailsjs/go/models";
import { GetCustomType, GetGenericAssetView, ListCustomTypes } from "../../../../wailsjs/go/customtype/CustomType";
import { GetDefaultPolicy, UpdateAsset } from "../../../../wailsjs/go/system/System";

const asset = new asset_entity.Asset({
  ID: 42,
  Name: "grafana-prod",
  Type: "generic",
  Config: JSON.stringify({ custom_type: "grafana", values: { host: { value: "g" } } }),
  CmdPolicy: JSON.stringify({ allow_list: ["GET *"] }),
});

const grafana = new custom_type_entity.CustomType({
  id: 7,
  slug: "grafana",
  name: "Grafana",
  icon: "",
  execMode: "http",
  fields: [{ name: "host", label: "Host", secret: false, required: true }],
  http: { base_url: "https://{{host}}" },
  usage: "",
  defaultPolicy: { allow_list: ["GET *", "HEAD *"], deny_list: ["DELETE *"] },
});

beforeEach(() => {
  vi.clearAllMocks();
  useCustomTypeStore.setState({ types: [], loaded: false, loading: false });
  vi.mocked(ListCustomTypes).mockResolvedValue([
    new customtype.Summary({ id: 7, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 1 }),
  ]);
  vi.mocked(GetCustomType).mockResolvedValue(grafana);
  vi.mocked(GetGenericAssetView).mockResolvedValue(
    new customtype.GenericAssetView({
      typeId: 7,
      slug: "grafana",
      typeName: "Grafana",
      typeIcon: "",
      execMode: "http",
      usage: "",
      missing: [],
      fields: [{ name: "host", label: "Host", secret: false, required: true, value: "g", set: true, missing: false }],
    })
  );
});

function renderDetail() {
  return render(
    <TooltipProvider>
      <AssetDetail asset={asset} onEdit={vi.fn()} onDelete={vi.fn()} onConnect={vi.fn()} />
    </TooltipProvider>
  );
}

describe("AssetDetail for generic assets", () => {
  it("has no connect button and titles the asset with its custom type", async () => {
    renderDetail();
    const subtitle = await screen.findByTestId("generic-detail-subtitle");
    await waitFor(() => expect(subtitle).toHaveTextContent("Grafana"));
    expect(subtitle).toHaveTextContent("asset.generic.customTypeLabel");
    expect(screen.getByTestId("generic-detail-edit-type")).toBeInTheDocument();
    expect(screen.queryByText("ssh.connect")).toBeNull();
  });

  it("keeps the policy card but without a rule tester, and resets to the custom type's default", async () => {
    renderDetail();
    expect(await screen.findByText("asset.generic.policyTitle")).toBeInTheDocument();
    expect(screen.queryByText("asset.policyTest")).toBeNull();

    await userEvent.click(screen.getByText("asset.policyReset.default"));
    await userEvent.click(await screen.findByText("action.confirm"));

    await waitFor(() => expect(UpdateAsset).toHaveBeenCalled());
    expect(GetDefaultPolicy).not.toHaveBeenCalled();
    const saved = vi.mocked(UpdateAsset).mock.calls[0][0];
    expect(JSON.parse(saved.CmdPolicy)).toEqual({ allow_list: ["GET *", "HEAD *"], deny_list: ["DELETE *"] });
  });
});
