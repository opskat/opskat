import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import type { AssetFormContext, AssetFormHandle } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, EXT_DRIVER, provideDrivers } from "./dbDriverFixtures";

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
  provideDrivers([EXT_DRIVER, ...BUILTIN_DRIVERS]);
});

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

function renderSection(editAsset?: asset_entity.Asset) {
  const ref = createRef<AssetFormHandle>();
  const onValidityChange = vi.fn();
  render(<DatabaseConfigSection ref={ref} ctx={ctx} editAsset={editAsset} onValidityChange={onValidityChange} />);
  return { ref, onValidityChange };
}

async function savedConfig(ref: React.RefObject<AssetFormHandle | null>) {
  const built = await ref.current!.buildConfig(ctx);
  return JSON.parse(built.configJSON);
}

function precedes(a: Element, b: Element) {
  return !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
}

describe("DatabaseConfigSection 驱动专属字段", () => {
  it("显示在声明的 Tab 里、该 Tab 的内置字段之后;标签与说明取自描述", async () => {
    const user = userEvent.setup();
    renderSection();

    const serviceName = await screen.findByLabelText("Service name *");
    expect(precedes(screen.getByTestId("database-name-input"), serviceName)).toBe(true);
    expect(screen.getByText("Listener service")).toBeInTheDocument();
    expect(screen.getByLabelText("Wallet password")).toHaveAttribute("type", "password");

    // 表单本身没有声明高级 / TLS Tab:专属字段把它们带出来,默认值取自描述
    await user.click(screen.getByTestId("config-tab-advanced"));
    expect(screen.getByLabelText("Fetch size")).toHaveValue(100);
    expect(screen.getByText("Role")).toBeInTheDocument();
    await user.click(screen.getByTestId("config-tab-tls"));
    expect(screen.getByRole("switch")).not.toBeChecked();
    expect(screen.getByText("Native encryption")).toBeInTheDocument();
  });

  it("必填的专属字段未填时不能保存,提示与内置字段一样落在保存按钮旁", async () => {
    const user = userEvent.setup();
    const { onValidityChange } = renderSection();

    await user.type(await screen.findByTestId("database-host-input"), "db.internal");
    await waitFor(() =>
      expect(onValidityChange).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formMissingDriverField",
      })
    );

    await user.type(screen.getByLabelText("Service name *"), "ORCL");
    await waitFor(() =>
      expect(onValidityChange).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
  });

  it("数字类专属字段只收整数:填了小数不能保存,改回整数即可", async () => {
    const user = userEvent.setup();
    const { onValidityChange } = renderSection();

    await user.type(await screen.findByTestId("database-host-input"), "db.internal");
    await user.type(screen.getByLabelText("Service name *"), "ORCL");
    await user.click(screen.getByTestId("config-tab-advanced"));
    const fetchSize = screen.getByLabelText("Fetch size");
    await user.clear(fetchSize);
    await user.type(fetchSize, "1.5");
    await waitFor(() =>
      expect(onValidityChange).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "asset.formInvalidDriverField",
      })
    );

    await user.clear(fetchSize);
    await user.type(fetchSize, "200");
    await waitFor(() =>
      expect(onValidityChange).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );
  });

  it("保存时写进 driver_options:口令类加密,未改动的取描述的默认值", async () => {
    const user = userEvent.setup();
    const { ref } = renderSection();

    await user.type(await screen.findByTestId("database-host-input"), "db.internal");
    await user.type(screen.getByLabelText("Service name *"), "ORCL");
    await user.type(screen.getByLabelText("Wallet password"), "s3cret");

    const cfg = await savedConfig(ref);
    expect(cfg.driver).toBe("extdb");
    expect(cfg.driver_options).toEqual({
      service_name: "ORCL",
      wallet_password: "enc(s3cret)",
      role: "normal",
      fetch_size: 100,
      native_encryption: false,
    });
  });

  it("切换到别的驱动:专属字段不再显示,保存时不写入", async () => {
    const user = userEvent.setup();
    const { ref } = renderSection();

    await user.type(await screen.findByLabelText("Service name *"), "ORCL");
    await user.click(screen.getByTestId("database-driver-select"));
    await user.click(await screen.findByTestId("database-driver-option-mysql"));

    expect(screen.queryByText("Service name")).not.toBeInTheDocument();
    expect(screen.queryByText("Wallet password")).not.toBeInTheDocument();
    const cfg = await savedConfig(ref);
    expect(cfg.driver).toBe("mysql");
    expect(cfg).not.toHaveProperty("driver_options");
  });

  it("编辑态:回填已存的取值,已设置的口令留空即沿用已存的密文", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "database",
      Config: JSON.stringify({
        driver: "extdb",
        host: "db.internal",
        port: 7357,
        driver_options: { service_name: "ORCL", wallet_password: "STORED-CIPHERTEXT", role: "sysdba", fetch_size: 50 },
      }),
    });
    const { ref, onValidityChange } = renderSection(editAsset);

    expect(await screen.findByLabelText("Service name *")).toHaveValue("ORCL");
    const wallet = screen.getByLabelText("Wallet password");
    expect(wallet).toHaveValue("");
    expect(wallet).toHaveAttribute("placeholder", "asset.passwordUnchanged");
    await waitFor(() =>
      expect(onValidityChange).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" })
    );

    const cfg = await savedConfig(ref);
    expect(cfg.driver_options).toEqual({
      service_name: "ORCL",
      wallet_password: "STORED-CIPHERTEXT",
      role: "sysdba",
      fetch_size: 50,
      native_encryption: false,
    });
  });
});
