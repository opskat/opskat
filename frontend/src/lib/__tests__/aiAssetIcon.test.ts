import { describe, it, expect } from "vitest";
import { Server } from "lucide-react";
import { resolveAssetIcon } from "../aiAssetIcon";
import { getIconComponent } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";

// 注意:注册的资产类型键是 ssh/database/redis/... —— 数据库统一为 "database"(没有 "mysql")。
const assets = [
  { ID: 1, Name: "web", Type: "ssh", Icon: "server#ff0000" } as any,
  { ID: 2, Name: "db", Type: "database", Icon: "" } as any,
];

describe("resolveAssetIcon", () => {
  it("uses the asset's own Icon + color when present", () => {
    const r = resolveAssetIcon(assets, 1, "ssh");
    expect(r.Icon).toBe(getIconComponent("server#ff0000"));
    expect(r.color).toBe("#ff0000");
  });

  it("falls back to the registered asset-type icon (no color) when the asset has no Icon", () => {
    expect(getAssetType("database")).toBeDefined();
    const r = resolveAssetIcon(assets, 2, "database");
    expect(r.Icon).toBe(getAssetType("database")?.icon);
    expect(r.color).toBeUndefined();
  });

  it("falls back to Server when the asset is absent and the type is unregistered", () => {
    const r = resolveAssetIcon(assets, 999, "no-such-type");
    expect(r.Icon).toBe(Server);
    expect(r.color).toBeUndefined();
  });
});
