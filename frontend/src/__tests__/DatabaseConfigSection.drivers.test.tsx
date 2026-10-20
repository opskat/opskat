import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import { refreshDatabaseDrivers } from "@/lib/dbDrivers";
import { ListDatabaseDrivers } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, FAKE_DRIVER } from "./dbDriverFixtures";

vi.mock("../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
  SelectSQLiteFile: () => Promise.resolve(""),
}));

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

beforeEach(() => {
  refreshDatabaseDrivers();
});

const ctx = { isEdit: false, encryptPassword: vi.fn() };

function renderSection(onIconChange = vi.fn(), onValidityChange = vi.fn()) {
  render(<DatabaseConfigSection ctx={ctx} onValidityChange={onValidityChange} onIconChange={onIconChange} />);
  return { onIconChange, onValidityChange };
}

describe("DatabaseConfigSection 跟随后端驱动描述", () => {
  it("下拉按后端顺序列出所有驱动,字段、默认值与图标随所选驱动变化(假驱动无需改共享代码)", async () => {
    vi.mocked(ListDatabaseDrivers).mockResolvedValue([FAKE_DRIVER, ...BUILTIN_DRIVERS]);
    const user = userEvent.setup();
    const { onIconChange } = renderSection();

    // 新建态默认选中后端返回的第一个驱动,默认端口与图标取自它的描述
    await screen.findByTestId("database-driver-select");
    expect(screen.getByTestId("database-driver-select")).toHaveTextContent("FakeDB");
    expect(onIconChange).toHaveBeenCalledWith("fake-icon");
    expect(screen.getByTestId("database-port-input")).toHaveValue(9042);
    // 连接 Tab:描述声明了的字段才出现
    expect(screen.getByTestId("database-host-input")).toBeInTheDocument();
    expect(screen.getByTestId("database-username-input")).toBeInTheDocument();
    expect(screen.getByTestId("database-name-input")).toBeInTheDocument();
    expect(screen.queryByText("asset.password")).not.toBeInTheDocument();
    // Tab 集合也以描述为准:没有声明隧道 Tab 的驱动不出现隧道 Tab
    expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
    expect(screen.queryByTestId("config-tab-tunnel")).not.toBeInTheDocument();
    // TLS Tab 里是它声明的 ssl_mode,而不是 tls 开关
    await user.click(screen.getByTestId("config-tab-tls"));
    expect(screen.getByText("asset.sslMode")).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();

    // 下拉列出全部驱动,顺序与后端一致
    await user.click(screen.getByTestId("config-tab-connection"));
    await user.click(screen.getByTestId("database-driver-select"));
    const options = await screen.findAllByRole("option");
    expect(options.map((o) => o.textContent)).toEqual(["FakeDB", "MySQL", "PostgreSQL", "SQL Server", "SQLite"]);
    expect(options.map((o) => o.getAttribute("data-testid"))).toEqual([
      "database-driver-option-fakedb",
      "database-driver-option-mysql",
      "database-driver-option-postgresql",
      "database-driver-option-mssql",
      "database-driver-option-sqlite",
    ]);

    // 切到 SQL Server:图标、默认端口、TLS 开关都换成它的描述
    await user.click(screen.getByTestId("database-driver-option-mssql"));
    expect(onIconChange).toHaveBeenLastCalledWith("sqlserver");
    expect(screen.getByTestId("database-port-input")).toHaveValue(1433);
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    await user.click(screen.getByTestId("config-tab-tls"));
    expect(screen.getByRole("switch", { name: "TLS" })).toBeInTheDocument();
    expect(screen.queryByText("asset.sslMode")).not.toBeInTheDocument();
  });

  it("取描述失败:表单显示该错误,不出现任何内置驱动列表", async () => {
    vi.mocked(ListDatabaseDrivers).mockRejectedValue(new Error("driver registry unavailable"));
    const { onValidityChange } = renderSection();

    expect(await screen.findByText("driver registry unavailable")).toBeInTheDocument();
    expect(screen.queryByTestId("database-driver-select")).not.toBeInTheDocument();
    expect(screen.queryByText("MySQL")).not.toBeInTheDocument();
    expect(onValidityChange).not.toHaveBeenCalledWith(expect.objectContaining({ canSave: true }));
  });

  it("编辑态的驱动不在注册表里:显示未知驱动错误,配置原样保留,仍可保存与测试", async () => {
    vi.mocked(ListDatabaseDrivers).mockResolvedValue(BUILTIN_DRIVERS);
    const { asset_entity } = await import("../../wailsjs/go/models");
    const editAsset = new asset_entity.Asset({ Type: "database", Config: '{"driver":"gone","host":"h","port":1}' });
    const onValidityChange = vi.fn();
    render(<DatabaseConfigSection ctx={ctx} editAsset={editAsset} onValidityChange={onValidityChange} />);

    expect(await screen.findByText("asset.driverUnknown")).toBeInTheDocument();
    await waitFor(() => expect(onValidityChange).toHaveBeenCalled());
    expect(onValidityChange).toHaveBeenLastCalledWith({ canTest: true, canSave: true });
  });
});
