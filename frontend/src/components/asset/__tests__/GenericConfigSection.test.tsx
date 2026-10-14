import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { GenericConfigSection } from "@/components/asset/GenericConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { useAssetStore } from "@/stores/assetStore";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { asset_entity, custom_type_entity, customtype } from "../../../../wailsjs/go/models";
import { GetCustomType, ListCustomTypes } from "../../../../wailsjs/go/customtype/CustomType";
import { ListCredentialsByType } from "../../../../wailsjs/go/system/System";

// 类型编辑器本身有自己的测试;这里只需要它"保存成功"这一个出口。
vi.mock("@/components/settings/CustomTypeEditorDialog", () => ({
  CustomTypeEditorDialog: ({ open, onSaved }: { open: boolean; onSaved?: () => void }) =>
    open ? <button type="button" data-testid="type-editor-saved" onClick={() => onSaved?.()} /> : null,
}));

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
  useCustomTypeStore.setState({ types: [], loaded: false, loading: false, loadError: "" });
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

  // 类型列表加载失败时配置区要把原因显示出来,而不是一直转圈(保存按钮同时是禁用的)。
  it("shows why the type could not be loaded instead of spinning forever", async () => {
    vi.mocked(ListCustomTypes).mockRejectedValue(new Error("database is locked"));
    render(<GenericConfigSection ctx={ctx} variant="grafana" onValidityChange={() => {}} />);

    expect(await screen.findByText(/database is locked/)).toBeInTheDocument();
  });

  describe("editing the type from inside the form", () => {
    it("keeps what was typed and picks up the new structure", async () => {
      const ref = createRef<AssetFormHandle>();
      render(<GenericConfigSection ref={ref} ctx={ctx} variant="grafana" onValidityChange={() => {}} />);
      await userEvent.type(await screen.findByTestId("generic-field-host"), "g.example.com");
      await userEvent.type(screen.getByTestId("generic-secret-token").querySelector("input")!, "tok");

      const withTeam = new custom_type_entity.CustomType({
        ...grafana,
        fields: [...grafana.fields, { name: "team", label: "Team", secret: false, required: false }],
      });
      vi.mocked(GetCustomType).mockImplementation(async () => withTeam);
      fireEvent.click(screen.getByTestId("generic-edit-type"));
      fireEvent.click(screen.getByTestId("type-editor-saved"));

      await screen.findByTestId("generic-field-team");
      expect(screen.getByTestId("generic-field-host")).toHaveValue("g.example.com");
      const built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON).values).toEqual({
        host: { value: "g.example.com" },
        token: { value: "enc(tok)" },
      });
    });

    // 类型里把 org 改成密钥字段:后端已把资产上的 org 重存成密文。表单拿着打开时的旧配置重建,
    // 保存就会把旧明文当成密文写回去。
    it("rebuilds from the asset as the backend rewrote it, not from the config the form was opened with", async () => {
      const stored = (org: string) =>
        new asset_entity.Asset({
          ID: 42,
          Type: "generic",
          Config: JSON.stringify({
            custom_type: "grafana",
            values: { host: { value: "g.example.com" }, token: { value: "CIPHER" }, org: { value: org } },
          }),
        });
      const ref = createRef<AssetFormHandle>();
      render(
        <GenericConfigSection
          ref={ref}
          editAsset={stored("2")}
          ctx={{ ...ctx, isEdit: true }}
          onValidityChange={() => {}}
        />
      );
      await userEvent.clear(await screen.findByTestId("generic-field-host"));
      await userEvent.type(screen.getByTestId("generic-field-host"), "new.example.com");

      const orgSecret = new custom_type_entity.CustomType({
        ...grafana,
        fields: grafana.fields.map((f) => (f.name === "org" ? { ...f, secret: true, default: "" } : f)),
      });
      vi.mocked(GetCustomType).mockImplementation(async () => orgSecret);
      useAssetStore.setState({ assets: [stored("ORG-CIPHER")] });
      fireEvent.click(screen.getByTestId("generic-edit-type"));
      fireEvent.click(screen.getByTestId("type-editor-saved"));

      await screen.findByTestId("generic-secret-org");
      const built = await ref.current!.buildConfig(ctx);
      expect(JSON.parse(built.configJSON).values).toEqual({
        host: { value: "new.example.com" },
        token: { value: "CIPHER" },
        org: { value: "ORG-CIPHER" },
      });
    });
  });

  describe("TLS certificate input on the connection tab", () => {
    const CA = "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----";
    const CERT = "-----BEGIN CERTIFICATE-----\nCERT\n-----END CERTIFICATE-----";
    const KEY = "-----BEGIN PRIVATE KEY-----\nKEY\n-----END PRIVATE KEY-----";

    async function renderEdit(tls: Record<string, unknown>) {
      const editAsset = new asset_entity.Asset({
        ID: 42,
        Type: "generic",
        Config: JSON.stringify({
          custom_type: "grafana",
          values: { host: { value: "g.example.com" }, token: { value: "CIPHER" } },
          ...tls,
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
      fireEvent.click(screen.getByTestId("config-tab-connection"));
      return ref;
    }
    const saved = async (ref: React.RefObject<AssetFormHandle | null>) =>
      JSON.parse((await ref.current!.buildConfig(ctx)).configJSON);
    const tested = async (ref: React.RefObject<AssetFormHandle | null>) =>
      JSON.parse((await ref.current!.buildTestConfig!(ctx)).configJSON);

    it("restores stored content without echoing the key, and keeps its ciphertext on save and test", async () => {
      const ref = await renderEdit({ tls_ca_pem: CA, tls_cert_pem: CERT, tls_key_pem: "KEYCIPHER" });
      expect(screen.getByTestId("tls-ca-pem")).toHaveValue(CA);
      expect(screen.getByTestId("tls-key-pem")).toHaveValue("");
      expect(await saved(ref)).toMatchObject({ tls_ca_pem: CA, tls_cert_pem: CERT, tls_key_pem: "KEYCIPHER" });
      expect(await tested(ref)).toMatchObject({ tls_cert_pem: CERT, tls_key_pem: "KEYCIPHER" });
    });

    it("encrypts a typed key before save and before test-connection", async () => {
      const ref = await renderEdit({ tls_cert_pem: CERT, tls_key_pem: "KEYCIPHER" });
      fireEvent.change(screen.getByTestId("tls-key-pem"), { target: { value: KEY } });
      expect((await saved(ref)).tls_key_pem).toBe(`enc(${KEY})`);
      expect((await tested(ref)).tls_key_pem).toBe(`enc(${KEY})`);
    });

    it("restores the file source and writes only the current source's group", async () => {
      const ref = await renderEdit({ tls_ca_file: "/ca.pem", tls_cert_file: "/c.crt", tls_key_file: "/c.key" });
      expect(screen.getByTestId("tls-ca-file")).toHaveValue("/ca.pem");
      const cfg = await saved(ref);
      expect(cfg).toMatchObject({ tls_ca_file: "/ca.pem", tls_cert_file: "/c.crt", tls_key_file: "/c.key" });
      expect(cfg.tls_ca_pem).toBeUndefined();

      fireEvent.click(screen.getByTestId("tls-cert-source-pem"));
      fireEvent.change(screen.getByTestId("tls-ca-pem"), { target: { value: CA } });
      const switched = await saved(ref);
      expect(switched.tls_ca_pem).toBe(CA);
      expect(switched.tls_ca_file).toBeUndefined();
      expect((await tested(ref)).tls_ca_file).toBeUndefined();
    });
  });
});
