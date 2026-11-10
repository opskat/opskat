import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { RDPConfigSection } from "@/components/asset/RDPConfigSection";
import type { AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";
import { renderForSave } from "./rejectedSave";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: true, encryptPassword: async (p) => `enc(${p})` };
const rdp = (config: Record<string, unknown>) =>
  new asset_entity.Asset({ ID: 3, Type: "rdp", Config: JSON.stringify(config) });

describe("RDPConfigSection on a rejected save", () => {
  it("reports an invalid port as a port problem, not a missing host", () => {
    const onValidity = vi.fn();
    render(
      <RDPConfigSection
        editAsset={rdp({ host: "10.0.0.5", port: 70000, username: "admin" })}
        ctx={ctx}
        onValidityChange={onValidity}
      />
    );
    expect(onValidity).toHaveBeenLastCalledWith(
      expect.objectContaining({ canSave: false, saveDisabledReason: "network.proxy.error.portRange" })
    );
  });

  it("shows the port and username problems under their fields", () => {
    const { rejectSave, reportShown } = renderForSave(
      <RDPConfigSection
        editAsset={rdp({ host: "10.0.0.5", port: 70000, username: " " })}
        ctx={ctx}
        onValidityChange={vi.fn()}
      />
    );
    expect(screen.getByTestId("rdp-port-input")).not.toHaveAttribute("aria-invalid");

    rejectSave();

    expect(screen.getByTestId("rdp-port-input")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("network.proxy.error.portRange")).toBeInTheDocument();
    expect(screen.getByTestId("rdp-username-input")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByTestId("config-tab-error-connection")).toBeInTheDocument();
    expect(reportShown).toHaveBeenCalled();
  });
});
