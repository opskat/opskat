import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity, dbdriver } from "../../../../wailsjs/go/models";
import { ListDatabaseDrivers } from "../../../../wailsjs/go/query/Query";
import { refreshDatabaseDrivers } from "@/lib/dbDrivers";
import { BUILTIN_DRIVERS } from "@/__tests__/dbDriverFixtures";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
  SelectSQLiteFile: () => Promise.resolve(""),
}));

// Radix Select 在 happy-dom 无 layout/pointer-capture,补齐 user-event 驱动所需的最小桩。
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

beforeEach(() => {
  refreshDatabaseDrivers();
  vi.mocked(ListDatabaseDrivers).mockResolvedValue(BUILTIN_DRIVERS);
});

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("DatabaseConfigSection ref 契约", () => {
  it("创建态默认 mysql(network):无 host → canSave/canTest=false + asset.formMissingHost", async () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formMissingHost",
      })
    );
  });

  it("编辑态(network 有 host):上报 canSave/canTest=true,无 reason", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"mysql","host":"127.0.0.1","port":3306}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
  });

  it("编辑态(sqlite 无 path):上报 canSave/canTest=false + asset.formMissingPath", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formMissingPath",
      })
    );
  });

  it("编辑态(sqlite 有 path):上报 canSave/canTest=true,无 reason", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","path":"/tmp/x.db"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
  });

  it("编辑态(remote sqlite 有 path 但无 SSH 资产):上报缺 SSH 资产", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","sqlite_source":"remote_ssh_vfs","path":"/tmp/x.db"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formMissingSQLiteSSH",
      })
    );
  });

  it("编辑态(remote sqlite 有 SSH 资产):buildConfig 写 remote source 和顶层 sshTunnelId", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","sqlite_source":"remote_ssh_vfs","ssh_asset_id":8,"path":"/tmp/x.db"}',
    });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );

    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"driver":"sqlite","sqlite_source":"remote_ssh_vfs","ssh_asset_id":8,"path":"/tmp/x.db","query_timeout_seconds":30}',
      sshTunnelId: 8,
    });
  });

  it("编辑态(postgresql + ssl_mode,inline 既有密文):buildConfig 沿用密文 + ssh_asset_id;buildTestConfig 同形,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config:
        '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"OLD",' +
        '"ssh_asset_id":5,"ssl_mode":"require","database":"mydb"}',
      sshTunnelId: 5,
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await waitFor(() => expect(ref.current).not.toBeNull());
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"driver":"postgresql","host":"pg.example.com","port":5432,"username":"postgres","password":"OLD",' +
        '"ssh_asset_id":5,"ssl_mode":"require",' +
        '"proxy_chain":{"layers":[{"id":"legacy-ssh-5","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":5}]},' +
        '"database":"mydb","query_timeout_seconds":30}',
      sshTunnelId: 5,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "database", configJSON: built.configJSON, password: "" });
  });

  it("编辑态(sqlite):buildConfig 仅 path/database/read_only,无凭据/host/ssh", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"sqlite","path":"/tmp/x.db","database":"main","read_only":true}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await waitFor(() => expect(ref.current).not.toBeNull());
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"driver":"sqlite","path":"/tmp/x.db","database":"main","read_only":true,"query_timeout_seconds":30}',
      sshTunnelId: 0,
    });
  });

  it("编辑态(managed 凭据):buildConfig 写 credential_id,不写 password", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: '{"driver":"mysql","host":"127.0.0.1","port":3306,"username":"root","credential_id":7}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    await waitFor(() => expect(ref.current).not.toBeNull());
    const built = await ref.current!.buildConfig(ctx);
    expect(built.configJSON).toContain('"credential_id":7');
    expect(built.configJSON).not.toContain('"password"');
  });

  it("driver 切到 sqlite:触发 onIconChange('sqlite') 且切到 path 校验(formMissingPath)", async () => {
    const user = userEvent.setup();
    const onIcon = vi.fn();
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<DatabaseConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} onIconChange={onIcon} />);
    await waitFor(() => expect(ref.current).not.toBeNull());
    // 打开 driver Select(显示当前值 MySQL;页面另有密码来源 combobox),选 SQLite。
    await screen.findByTestId("database-driver-select");
    const driverSelect = screen.getAllByRole("combobox").find((el) => el.textContent?.includes("MySQL"));
    await user.click(driverSelect!);
    await user.click(await screen.findByText("SQLite"));
    expect(onIcon).toHaveBeenCalledWith("sqlite");
    await waitFor(() =>
      expect(onValidity).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formMissingPath",
      })
    );
  });
});

describe("DatabaseConfigSection 证书字段组(tls_cert)", () => {
  const CERT_DRIVER = new dbdriver.Descriptor({
    id: "certdb",
    name: "CertDB",
    icon: "database",
    order: 901,
    aliases: [],
    defaultPort: 1234,
    form: [
      { tab: "connection", fields: ["host", "port", "username", "password"] },
      { tab: "tls", fields: ["tls_cert"] },
    ],
    capabilities: [],
  });
  const STORED =
    '{"driver":"certdb","host":"h","port":1234,"tls":true,"tls_insecure":true,"tls_server_name":"srv.example.com",' +
    '"tls_ca_pem":"CA","tls_cert_pem":"CERT","tls_key_pem":"STOREDKEY"}';

  beforeEach(() => {
    vi.mocked(ListDatabaseDrivers).mockResolvedValue([...BUILTIN_DRIVERS, CERT_DRIVER]);
  });

  async function renderEdit(config: string) {
    const ref = createRef<AssetFormHandle>();
    render(
      <DatabaseConfigSection
        ref={ref}
        editAsset={new asset_entity.Asset({ Type: "database", Config: config })}
        ctx={ctx}
        onValidityChange={() => {}}
      />
    );
    await waitFor(() => expect(ref.current).not.toBeNull());
    await userEvent.setup().click(await screen.findByTestId("config-tab-tls"));
    return ref;
  }

  it("声明的驱动在 TLS Tab 显示证书字段并回填,私钥显示为已设置", async () => {
    await renderEdit(STORED);
    expect(await screen.findByTestId("tls-ca-pem")).toHaveValue("CA");
    expect(screen.getByTestId("tls-cert-pem")).toHaveValue("CERT");
    expect(screen.getByTestId("tls-key-pem")).toHaveValue("");
    expect(screen.getByTestId("tls-key-pem")).toHaveAttribute("placeholder", "asset.passwordUnchanged");
    expect(screen.getByText("asset.tlsInsecure")).toBeInTheDocument();
    expect(screen.getByText("asset.tlsServerName")).toBeInTheDocument();
  });

  it("不改动直接保存与测试连接都带着全部证书键,已存私钥密文原样保留", async () => {
    const ref = await renderEdit(STORED);
    const want = {
      driver: "certdb",
      host: "h",
      port: 1234,
      tls: true,
      tls_insecure: true,
      tls_server_name: "srv.example.com",
      tls_ca_pem: "CA",
      tls_cert_pem: "CERT",
      tls_key_pem: "STOREDKEY",
    };
    expect(JSON.parse((await ref.current!.buildConfig(ctx)).configJSON)).toMatchObject(want);
    expect(JSON.parse((await ref.current!.buildTestConfig!(ctx)).configJSON)).toMatchObject(want);
  });

  it("新输入的私钥经 encryptPassword 加密后保存与测试", async () => {
    const ref = await renderEdit(STORED);
    await userEvent.setup().type(await screen.findByTestId("tls-key-pem"), "NEWKEY");
    const save = JSON.parse((await ref.current!.buildConfig(ctx)).configJSON);
    const test = JSON.parse((await ref.current!.buildTestConfig!(ctx)).configJSON);
    expect(save.tls_key_pem).toBe("enc(NEWKEY)");
    expect(test.tls_key_pem).toBe("enc(NEWKEY)");
  });

  it("不声明证书字段组的驱动,TLS Tab 不出现证书字段,配置里的证书键不被写出", async () => {
    const ref = await renderEdit(
      '{"driver":"mysql","host":"h","port":3306,"tls":true,"tls_ca_pem":"CA","tls_key_pem":"K"}'
    );
    expect(screen.queryByTestId("tls-ca-pem")).toBeNull();
    expect((await ref.current!.buildConfig(ctx)).configJSON).not.toContain("tls_");
  });
});
