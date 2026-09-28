/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("../../wailsjs/go/customtype/CustomType", () => ({
  ListCustomTypes: vi.fn().mockResolvedValue([]),
  GetCustomType: vi.fn(),
  GetCustomTypeUsage: vi.fn(),
  SaveCustomType: vi.fn(),
  DeleteCustomType: vi.fn(),
}));

import { useCustomTypeStore } from "./customTypeStore";

describe("useCustomTypeStore", () => {
  beforeEach(async () => {
    const mod = await import("../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.ListCustomTypes).mockClear().mockResolvedValue([]);
    vi.mocked(mod.SaveCustomType).mockClear();
    vi.mocked(mod.DeleteCustomType).mockClear();
    useCustomTypeStore.setState({ types: [], loading: false, loaded: false });
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
    vi.mocked(SaveCustomType).mockResolvedValueOnce({ issues: [{ path: "name", message: "required" }] } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(ListCustomTypes).not.toHaveBeenCalled();

    // 成功:应刷新列表
    vi.mocked(SaveCustomType).mockResolvedValueOnce({ type: { id: 1, slug: "grafana" } } as any);
    await useCustomTypeStore.getState().save({} as never);
    expect(ListCustomTypes).toHaveBeenCalledTimes(1);
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
});
