/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../../wailsjs/go/customtype/CustomType", () => ({
  ListCustomTypes: vi.fn().mockResolvedValue([]),
  GetCustomType: vi.fn(),
  GetCustomTypeUsage: vi.fn(),
  SaveCustomType: vi.fn(),
  DeleteCustomType: vi.fn(),
}));

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

import { useCustomTypeStore } from "./customTypeStore";
import { useAssetStore } from "./assetStore";

describe("useCustomTypeStore", () => {
  beforeEach(async () => {
    const mod = await import("../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.ListCustomTypes).mockClear().mockResolvedValue([]);
    vi.mocked(mod.SaveCustomType).mockClear();
    vi.mocked(mod.DeleteCustomType).mockClear();
    useCustomTypeStore.setState({ types: [], loading: false, loaded: false });
    useAssetStore.setState({ assets: [] });
    toastError.mockClear();
  });

  it("load() populates the type list", async () => {
    const { ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 2 },
    ]);
    await useCustomTypeStore.getState().load();
    expect(useCustomTypeStore.getState().types).toHaveLength(1);
    expect(useCustomTypeStore.getState().loaded).toBe(true);
  });

  it("save() refreshes the list on success but not when the backend returns validation issues", async () => {
    const { SaveCustomType, ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");

    // 失败:校验错误——不应刷新列表
    vi.mocked(SaveCustomType).mockResolvedValueOnce({ issues: [{ path: "name", code: "name_required" }] } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(ListCustomTypes).not.toHaveBeenCalled();

    // 成功:应刷新列表
    vi.mocked(SaveCustomType).mockResolvedValueOnce({ type: { id: 1, slug: "grafana" } } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(ListCustomTypes).toHaveBeenCalledTimes(1);
  });

  // 更新类型时后端会改写该类型的资产(删除字段的值、密钥属性变了的值重存)。资产快照不跟着换,
  // 下一次保存资产(如在详情页加一条策略规则)就会把旧 Config 整行写回去。
  it("save() re-fetches assets on success, because the backend rewrites the type's assets", async () => {
    const { SaveCustomType } = await import("../../wailsjs/go/customtype/CustomType");
    const refresh = vi.fn().mockResolvedValue(undefined);
    useAssetStore.setState({ refresh });

    vi.mocked(SaveCustomType).mockResolvedValueOnce({ issues: [{ path: "name", code: "name_required" }] } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(refresh).not.toHaveBeenCalled();

    vi.mocked(SaveCustomType).mockResolvedValueOnce({ type: { id: 1, slug: "grafana" } } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  // 类型已经存上了:之后刷新列表失败不是"保存失败",否则编辑器停在未保存状态,再点保存就是标识重名。
  it("save() still resolves with the saved type when the follow-up reload fails, and toasts the reload error", async () => {
    const { SaveCustomType, ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");
    useAssetStore.setState({ refresh: vi.fn().mockResolvedValue(undefined) });
    vi.mocked(SaveCustomType).mockResolvedValueOnce({ type: { id: 1, slug: "grafana" } } as any);
    vi.mocked(ListCustomTypes).mockRejectedValueOnce(new Error("list failed"));

    const res = await useCustomTypeStore.getState().save({} as never);

    expect(res.type).toMatchObject({ id: 1 });
    expect(toastError).toHaveBeenCalledWith(expect.stringContaining("list failed"));
  });

  it("remove() refreshes the list only when the type was actually deleted", async () => {
    const { DeleteCustomType, ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");

    // 占用拒绝——不应刷新列表
    vi.mocked(DeleteCustomType).mockResolvedValueOnce({ deleted: false, assets: ["grafana-prod"] });
    const refusal = await useCustomTypeStore.getState().remove(1);
    expect(refusal.assets).toEqual(["grafana-prod"]);
    expect(ListCustomTypes).not.toHaveBeenCalled();

    // 成功——应刷新列表
    vi.mocked(DeleteCustomType).mockResolvedValueOnce({ deleted: true });
    await useCustomTypeStore.getState().remove(1);
    expect(ListCustomTypes).toHaveBeenCalledTimes(1);
  });

  it("importing the module has no side effect on useAssetStore", async () => {
    vi.resetModules();
    vi.doMock("./assetStore", () => ({ useAssetStore: () => [] }));
    await expect(import("./customTypeStore")).resolves.toBeDefined();
    vi.doUnmock("./assetStore");
  });

  describe("reacting to asset changes", () => {
    const summary = (assetCount: number) => ({
      id: 1,
      slug: "grafana",
      name: "Grafana",
      icon: "",
      execMode: "http",
      assetCount,
    });

    it("reloads the list and counts when assets change after the first load", async () => {
      const { ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");
      vi.mocked(ListCustomTypes).mockResolvedValueOnce([summary(1)]);
      await useCustomTypeStore.getState().load();

      vi.mocked(ListCustomTypes).mockResolvedValueOnce([summary(2)]);
      useAssetStore.setState({ assets: [{ ID: 9 } as never] });

      await vi.waitFor(() => expect(useCustomTypeStore.getState().types[0].assetCount).toBe(2));
      expect(ListCustomTypes).toHaveBeenCalledTimes(2);
    });

    it("does not load on asset changes before the list was ever loaded", async () => {
      const { ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");
      useAssetStore.setState({ assets: [{ ID: 9 } as never] });
      await Promise.resolve();
      expect(ListCustomTypes).not.toHaveBeenCalled();
    });

    it("surfaces a failed reload as an error toast", async () => {
      const { ListCustomTypes } = await import("../../wailsjs/go/customtype/CustomType");
      await useCustomTypeStore.getState().load();
      vi.mocked(ListCustomTypes).mockRejectedValueOnce(new Error("boom"));
      useAssetStore.setState({ assets: [{ ID: 9 } as never] });
      await vi.waitFor(() => expect(toastError).toHaveBeenCalledWith(expect.stringContaining("boom")));
    });
  });
});
