import { describe, it, expect, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useAssetTypes, useAssetTypeDef, getAssetType, isKnownAssetType } from "@/lib/assetTypes";
import { registerExtensionAssetTypes, unregisterExtensionAssetTypes } from "@/extension/assetTypes";
import type { ExtManifest } from "@/extension/types";

const acme = {
  name: "acme",
  version: "1.0.0",
  icon: "cloud",
  i18n: { displayName: "assetType.acme.name", description: "" },
  assetTypes: [
    {
      type: "acme-store",
      i18n: { name: "assetType.acme.name" },
      configSchema: {
        type: "object",
        properties: { endpoint: { type: "string" }, token: { type: "string", format: "password" } },
      },
    },
  ],
  policies: { type: "ext:acme", actions: ["object.list", "object.read"], groups: [], default: [] },
} as unknown as ExtManifest;

afterEach(() => unregisterExtensionAssetTypes("acme"));

describe("asset type registry", () => {
  it("re-renders subscribers when an extension's types are added and removed", () => {
    // 注册表是运行期可变的：扩展启用/禁用要能驱动重渲染。这正是它从裸 Map 变成 store
    // 的理由——组件读的和触发更新的必须是同一份内容。
    const { result } = renderHook(() => useAssetTypes());
    const before = result.current.length;

    act(() => registerExtensionAssetTypes("acme", acme));
    expect(result.current.length).toBe(before + 1);
    expect(result.current.some((d) => d.type === "acme-store")).toBe(true);

    act(() => unregisterExtensionAssetTypes("acme"));
    expect(result.current.length).toBe(before);
  });

  it("exposes a single definition reactively", () => {
    const { result } = renderHook(() => useAssetTypeDef("acme-store"));
    expect(result.current).toBeUndefined();

    act(() => registerExtensionAssetTypes("acme", acme));
    expect(result.current?.extensionName).toBe("acme");
  });
});

describe("extension asset type definition", () => {
  it("carries everything the shared UI needs, so no consumer has to ask the extension store", () => {
    registerExtensionAssetTypes("acme", acme);
    const def = getAssetType("acme-store")!;

    expect(isKnownAssetType("acme-store")).toBe(true);
    expect(def.category).toBe("extension");
    expect(def.labelNs).toBe("ext-acme");
    expect(def.label).toBe("assetType.acme.name");
    // 表单区块与详情卡都由 configSchema 生成，接进内置类型用的同一个槽位。
    expect(def.ConfigSection).toBeTypeOf("function");
    expect(def.DetailInfoCard).toBeTypeOf("function");
    // 策略卡走同一段渲染：规则是 action 名，可用取值由 manifest 列出。
    expect(def.policy?.policyType).toBe("ext:acme");
    expect(def.policy?.fields.map((f) => f.key)).toEqual(["allow_list", "deny_list"]);
    expect(def.policy?.fields[0].placeholder).toBe("object.list, object.read");
  });

  it("cannot be connected when the manifest declares no asset.connect page", () => {
    registerExtensionAssetTypes("acme", acme);
    const def = getAssetType("acme-store")!;
    expect(def.canConnect).toBe(false);
    expect(def.pageId).toBeUndefined();
  });
});
