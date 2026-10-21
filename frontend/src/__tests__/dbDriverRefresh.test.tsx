import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import type { AssetFormHandle } from "@/lib/assetTypes/formContract";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import { DatabasePanel } from "@/components/query/DatabasePanel";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ListInstalledExtensions } from "../../wailsjs/go/extension/Extension";
import { ListDatabaseDrivers, ListDatabases } from "../../wailsjs/go/query/Query";
import { dbdriver } from "../../wailsjs/go/models";
import i18n from "../i18n";
import { BUILTIN_DRIVERS, EXT_DRIVER, provideDrivers } from "./dbDriverFixtures";

vi.mock("../extension/inject", () => ({ injectExtensionAPI: vi.fn() }));
vi.mock("../extension/api", () => ({ createExtensionAPI: vi.fn() }));
vi.mock("../extension/loader", () => ({ clearExtensionCache: vi.fn() }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

import { bootstrapExtensions, _resetForTesting } from "../extension/init";

beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

beforeEach(async () => {
  vi.clearAllMocks();
  _resetForTesting();
  vi.mocked(ListInstalledExtensions).mockResolvedValue([]);
  await bootstrapExtensions();
});

/** 后端发出的扩展事件(扩展加载完成、启用 / 停用 / 安装 / 升级 / 卸载之后)。 */
async function emit(event: "ext:reload" | "ext:ready") {
  const handler = vi.mocked(EventsOn).mock.calls.find(([name]) => name === event)?.[1];
  if (!handler) throw new Error(`no ${event} listener`);
  await act(async () => {
    handler();
  });
}

/** 后端注册表此刻的驱动:下一次取描述时得到它。 */
function registry(...drivers: typeof BUILTIN_DRIVERS) {
  vi.mocked(ListDatabaseDrivers).mockResolvedValue(drivers);
}

async function driverOptions(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByTestId("database-driver-select"));
  const options = (await screen.findAllByRole("option")).map((o) => o.textContent);
  await user.keyboard("{Escape}");
  return options;
}

describe("驱动注册或移除后不重启即反映", () => {
  it.each(["ext:reload", "ext:ready"] as const)(
    "资产表单的驱动下拉在 %s 后列出新注册的驱动,已填的内容不丢",
    async (event) => {
      provideDrivers(BUILTIN_DRIVERS);
      const user = userEvent.setup();
      render(<DatabaseConfigSection ctx={{ isEdit: false, encryptPassword: vi.fn() }} onValidityChange={vi.fn()} />);
      await user.type(await screen.findByTestId("database-host-input"), "db.internal");
      expect(await driverOptions(user)).not.toContain("ExtDB");

      registry(...BUILTIN_DRIVERS, EXT_DRIVER);
      await emit(event);

      await waitFor(async () => expect(await driverOptions(user)).toContain("ExtDB"));
      expect(screen.getByTestId("database-host-input")).toHaveValue("db.internal");
    }
  );

  it("驱动描述更新后保存:按新描述写 driver_options(新增字段取它的默认值)", async () => {
    provideDrivers([EXT_DRIVER, ...BUILTIN_DRIVERS]);
    const user = userEvent.setup();
    const ref = createRef<AssetFormHandle>();
    const ctx = { isEdit: false, encryptPassword: async (p: string) => `enc(${p})` };
    render(<DatabaseConfigSection ref={ref} ctx={ctx} onValidityChange={vi.fn()} />);
    await user.type(await screen.findByLabelText("Service name *"), "ORCL");

    const upgraded = new dbdriver.Descriptor({
      ...EXT_DRIVER,
      driverFields: [...EXT_DRIVER.driverFields, { key: "pool_size", type: "number", tab: "advanced", default: 8 }],
    });
    registry(upgraded, ...BUILTIN_DRIVERS);
    await emit("ext:reload");
    await user.click(await screen.findByTestId("config-tab-advanced"));
    await screen.findByLabelText("pool_size");

    const cfg = JSON.parse((await ref.current!.buildConfig(ctx)).configJSON);
    expect(cfg.driver_options).toMatchObject({ service_name: "ORCL", pool_size: 8 });
  });

  it("新建资产时所选驱动被移除:不能保存,驱动下拉仍在,可改选别的驱动", async () => {
    provideDrivers([EXT_DRIVER, ...BUILTIN_DRIVERS]);
    const user = userEvent.setup();
    const onValidityChange = vi.fn();
    render(
      <DatabaseConfigSection ctx={{ isEdit: false, encryptPassword: vi.fn() }} onValidityChange={onValidityChange} />
    );
    await user.type(await screen.findByLabelText("Service name *"), "ORCL");
    await user.type(screen.getByTestId("database-host-input"), "db.internal");

    registry(...BUILTIN_DRIVERS);
    await emit("ext:reload");
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("asset.driverUnknown"));
    expect(onValidityChange).toHaveBeenLastCalledWith(expect.objectContaining({ canSave: false, canTest: false }));

    await user.click(screen.getByTestId("database-driver-select"));
    await user.click(await screen.findByTestId("database-driver-option-mysql"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByTestId("database-host-input")).toBeInTheDocument();
  });

  it("切换界面语言后,专属字段的标签换成后端按新语言翻译的描述", async () => {
    provideDrivers([EXT_DRIVER, ...BUILTIN_DRIVERS]);
    render(<DatabaseConfigSection ctx={{ isEdit: false, encryptPassword: vi.fn() }} onValidityChange={vi.fn()} />);
    expect(await screen.findByLabelText("Service name *")).toBeInTheDocument();

    const translated = new dbdriver.Descriptor({
      ...EXT_DRIVER,
      driverFields: EXT_DRIVER.driverFields.map((f) => (f.key === "service_name" ? { ...f, label: "服务名" } : f)),
    });
    registry(translated, ...BUILTIN_DRIVERS);
    const before = i18n.language;
    const next = before === "zh-CN" ? "en" : "zh-CN";
    try {
      await act(async () => {
        await i18n.changeLanguage(next);
      });
      expect(await screen.findByLabelText("服务名 *")).toBeInTheDocument();
      // 语言由前端点名,不依赖后端镜像的语言已经切过去。
      expect(vi.mocked(ListDatabaseDrivers)).toHaveBeenLastCalledWith(next);
    } finally {
      await act(async () => {
        await i18n.changeLanguage(before);
      });
    }
  });

  it("已打开的查询标签页:驱动移除后显示「驱动不可用」,恢复后重新可用", async () => {
    provideDrivers([...BUILTIN_DRIVERS, EXT_DRIVER]);
    vi.mocked(ListDatabases).mockResolvedValue(["app"]);
    useTabStore.setState({
      tabs: [
        {
          id: "query-1",
          type: "query",
          label: "ext",
          meta: { type: "query", assetId: 1, assetName: "ext", assetIcon: "", assetType: "database", driver: "extdb" },
        },
      ],
      activeTabId: "query-1",
    });
    useQueryStore.setState({
      dbStates: {
        "query-1": {
          databases: [],
          tables: {},
          loadingTables: {},
          expandedDbs: [],
          expandedSchemas: {},
          loadingDbs: false,
          innerTabs: [],
          activeInnerTabId: null,
          error: null,
        },
      },
    });
    render(<DatabasePanel tabId="query-1" />);
    expect(await screen.findByText("app")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    registry(...BUILTIN_DRIVERS);
    await emit("ext:reload");
    await waitFor(() => expect(screen.getAllByRole("alert")).toHaveLength(2));
    for (const notice of screen.getAllByRole("alert")) expect(notice).toHaveTextContent("query.driverUnavailable");

    registry(...BUILTIN_DRIVERS, EXT_DRIVER);
    await emit("ext:reload");
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(screen.getByText("app")).toBeInTheDocument();
    expect(screen.getByText("query.openTable")).toBeInTheDocument();
  });
});
