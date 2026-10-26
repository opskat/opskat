import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import { refreshDatabaseDrivers } from "@/lib/dbDrivers";
import { asset_entity } from "../../wailsjs/go/models";
import { ListDatabaseDrivers } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS } from "./dbDriverFixtures";

vi.mock("../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
  SelectSQLiteFile: () => Promise.resolve(""),
}));

beforeEach(() => {
  refreshDatabaseDrivers();
  vi.mocked(ListDatabaseDrivers).mockResolvedValue(BUILTIN_DRIVERS);
});

// 改动前表单对四个内置驱动恒为 连接/隧道/TLS/高级 四个 Tab;现在没有声明任何字段的 Tab 不显示,
// 内置驱动里只影响 SQLite 的 TLS Tab(规格「有意的行为变化」第 4 项)。
const EXPECTED_TABS: Record<string, string[]> = {
  mysql: ["connection", "tunnel", "tls", "advanced"],
  postgresql: ["connection", "tunnel", "tls", "advanced"],
  mssql: ["connection", "tunnel", "tls", "advanced"],
  sqlite: ["connection", "tunnel", "advanced"],
};

describe("内置驱动的表单 Tab 条", () => {
  it.each(Object.entries(EXPECTED_TABS))("%s 的 Tab 及顺序", async (id, tabs) => {
    const editAsset = new asset_entity.Asset({ Type: "database", Config: JSON.stringify({ driver: id }) });
    render(
      <DatabaseConfigSection
        ctx={{ isEdit: true, encryptPassword: vi.fn() }}
        editAsset={editAsset}
        onValidityChange={vi.fn()}
      />
    );
    await screen.findByTestId("config-tab-connection");
    const rendered = screen.getAllByRole("tab").map((el) => el.getAttribute("data-testid")?.replace("config-tab-", ""));
    expect(rendered).toEqual(tabs);
  });
});
