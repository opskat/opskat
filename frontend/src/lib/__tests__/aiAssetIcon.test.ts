import { describe, it, expect } from "vitest";
import { Server } from "lucide-react";
import { resolveAssetIcon } from "../aiAssetIcon";
import { getIconComponent } from "@/components/asset/IconPicker";
import { getAssetType } from "@/lib/assetTypes";

const assets = [
  { ID: 1, Name: "web", Type: "ssh", Icon: "server#ff0000" } as any,
  { ID: 2, Name: "db", Type: "mysql", Icon: "" } as any,
];

describe("resolveAssetIcon", () => {
  it("uses the asset's own Icon + color when present", () => {
    const r = resolveAssetIcon(assets, 1, "ssh");
    expect(r.Icon).toBe(getIconComponent("server#ff0000"));
    expect(r.color).toBe("#ff0000");
  });

  it("falls back to the asset-type icon (no color) when the asset has no Icon", () => {
    const r = resolveAssetIcon(assets, 2, "mysql");
    expect(r.Icon).toBe(getAssetType("mysql")?.icon);
    expect(r.color).toBeUndefined();
  });

  it("falls back to Server when the asset is not in the store", () => {
    const r = resolveAssetIcon(assets, 999, "unknown-type");
    expect(r.Icon).toBe(Server);
    expect(r.color).toBeUndefined();
  });
});
