import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { GenericConfigSection } from "@/components/asset/GenericConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { asset_entity, custom_type_entity, customtype } from "../../../../wailsjs/go/models";
import { GetCustomType, ListCustomTypes } from "../../../../wailsjs/go/customtype/CustomType";
import { ListCredentialsByType } from "../../../../wailsjs/go/system/System";

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

const grafana = new custom_type_entity.CustomType({
  id: 7,
  slug: "grafana",
  name: "Grafana",
  icon: "chart-column",
  execMode: "http",
  fields: [
    { name: "host", label: "主机", placeholder: "grafana.internal:3000", secret: false, required: true },
    { name: "org", label: "组织 ID", secret: false, required: false, default: "1" },
    { name: "token", label: "Token", secret: true, required: true },
  ],
  http: { base_url: "https://{{host}}" },
  usage: "",
});

const awsCli = new custom_type_entity.CustomType({
  id: 8,
  slug: "aws-cli",
  name: "AWS CLI",
  icon: "",
  execMode: "command",
  fields: [{ name: "profile", label: "Profile", secret: false, required: false }],
  command: { template: "aws --profile {{profile}}" },
  usage: "",
});

function summary(ct: custom_type_entity.CustomType) {
  return new customtype.Summary({
    id: ct.id,
    slug: ct.slug,
    name: ct.name,
    icon: ct.icon,
    execMode: ct.execMode,
    assetCount: 0,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  useCustomTypeStore.setState({ types: [], loaded: false, loading: false });
  vi.mocked(ListCustomTypes).mockResolvedValue([summary(grafana), summary(awsCli)]);
  vi.mocked(GetCustomType).mockImplementation(async (id: number) => (id === grafana.id ? grafana : awsCli));
  vi.mocked(ListCredentialsByType).mockResolvedValue([{ id: 3, name: "grafana-sa", type: "password" } as never]);
});

describe("GenericConfigSection", () => {
  it("generates inputs from the type's field structure: label, placeholder, required mark, default", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<GenericConfigSection ref={ref} ctx={ctx} variant="grafana" onValidityChange={() => {}} />);

    const host = await screen.findByTestId("generic-field-host");
    expect(host).toHaveAttribute("placeholder", "grafana.internal:3000");
    expect(screen.getByText("主机").parentElement?.textContent).toContain("*");
    expect(screen.getByTestId("generic-field-org")).toHaveValue("1");
    expect(screen.getByTestId("generic-secret-token")).toBeInTheDocument();
    expect(screen.getByTestId("generic-type-hint")).toHaveTextContent("Grafana");
  });

  it("shows the connection tab only for HTTP types", async () => {
    const { unmount } = render(<GenericConfigSection ctx={ctx} variant="grafana" onValidityChange={() => {}} />);
    await screen.findByTestId("generic-field-host");
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    unmount();

    render(<GenericConfigSection ctx={ctx} variant="aws-cli" onValidityChange={() => {}} />);
    await screen.findByTestId("generic-field-profile");
    expect(screen.queryByTestId("config-tab-connection")).toBeNull();
  });

  it("blocks save and test while a required field is empty and marks it 'required' under the field", async () => {
    const onValidity = vi.fn();
    render(<GenericConfigSection ctx={ctx} variant="grafana" onValidityChange={onValidity} />);
    const host = await screen.findByTestId("generic-field-host");
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith(
        expect.objectContaining({ canSave: false, canTest: false, saveDisabledReason: "asset.generic.missingRequired" })
      )
    );

    await userEvent.type(host, "g.example.com");
    await userEvent.clear(host);
    expect(screen.getByTestId("generic-required-host")).toHaveTextContent("asset.generic.required");

    await userEvent.type(host, "g.example.com");
    await userEvent.type(screen.getByTestId("generic-secret-token").querySelector("input")!, "tok");
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith(
        expect.objectContaining({ canSave: true, canTest: true, testable: true })
      )
    );
    expect(screen.queryByTestId("generic-required-host")).toBeNull();
  });

  it("reports command types as not testable", async () => {
    const onValidity = vi.fn();
    render(<GenericConfigSection ctx={ctx} variant="aws-cli" onValidityChange={onValidity} />);
    await screen.findByTestId("generic-field-profile");
    await waitFor(() => expect(onValidity).toHaveBeenLastCalledWith(expect.objectContaining({ testable: false })));
  });

  it("serializes values: plain text, encrypted inline secret, untouched default omitted", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<GenericConfigSection ref={ref} ctx={ctx} variant="grafana" onValidityChange={() => {}} />);
    await userEvent.type(await screen.findByTestId("generic-field-host"), "g.example.com");
    await userEvent.type(screen.getByTestId("generic-secret-token").querySelector("input")!, "tok");

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({
      custom_type: "grafana",
      values: { host: { value: "g.example.com" }, token: { value: "enc(tok)" } },
    });
    expect(built.sshTunnelId).toBe(0);

    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc.assetType).toBe("generic");
    expect(JSON.parse(tc.configJSON)).toEqual({
      custom_type: "grafana",
      values: { host: "g.example.com", token: "tok" },
    });
  });

  it("editing keeps an unchanged secret's stored value and leaves it out of the test input", async () => {
    const editAsset = new asset_entity.Asset({
      ID: 42,
      Type: "generic",
      sshTunnelId: 5,
      Config: JSON.stringify({
        custom_type: "grafana",
        values: { host: { value: "g.example.com" }, token: { value: "CIPHER" }, org: { value: "2" } },
        tls_insecure: true,
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(
      <GenericConfigSection
        ref={ref}
        editAsset={editAsset}
        ctx={{ ...ctx, isEdit: true }}
        onValidityChange={() => {}}
      />
    );
    expect(await screen.findByTestId("generic-field-host")).toHaveValue("g.example.com");

    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON)).toEqual({
      custom_type: "grafana",
      values: { host: { value: "g.example.com" }, org: { value: "2" }, token: { value: "CIPHER" } },
      proxy_chain: {
        layers: [{ id: "legacy-ssh-5", name: "SSH Tunnel", enabled: true, type: "ssh", order: 1, ssh_asset_id: 5 }],
      },
      tls_insecure: true,
    });
    expect(built.sshTunnelId).toBe(5);

    const tc = await ref.current!.buildTestConfig!(ctx);
    const input = JSON.parse(tc.configJSON);
    expect(input.asset_id).toBe(42);
    expect(input.ssh_tunnel_id).toBe(5);
    expect(input.values).toEqual({ host: "g.example.com", org: "2" });
    expect(input.tls_insecure).toBe(true);
  });

  it("stores a managed credential reference for a secret field", async () => {
    const editAsset = new asset_entity.Asset({
      ID: 42,
      Type: "generic",
      Config: JSON.stringify({
        custom_type: "grafana",
        values: { host: { value: "g.example.com" }, token: { credential_id: 3 } },
      }),
    });
    const ref = createRef<AssetFormHandle>();
    render(
      <GenericConfigSection
        ref={ref}
        editAsset={editAsset}
        ctx={{ ...ctx, isEdit: true }}
        onValidityChange={() => {}}
      />
    );
    await screen.findByTestId("generic-field-host");
    const built = await ref.current!.buildConfig(ctx);
    expect(JSON.parse(built.configJSON).values.token).toEqual({ credential_id: 3 });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(JSON.parse(tc.configJSON).values.token).toEqual({ credential_id: 3 });
  });
});
