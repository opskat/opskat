import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Server } from "lucide-react";
import { AssetIcon, EntityIcon } from "../AssetIcon";
import { getAssetType } from "@/lib/assetTypes";
import { asset_entity } from "../../../../wailsjs/go/models";

// 注意:注册的资产类型键是 ssh/database/redis/... —— 数据库统一为 "database"(没有 "mysql")。
const assets = [
  new asset_entity.Asset({ ID: 1, Name: "web", Type: "ssh", Icon: "server#ff0000" }),
  new asset_entity.Asset({ ID: 2, Name: "db", Type: "database", Icon: "" }),
];

describe("AssetIcon", () => {
  it("uses the asset's own Icon + color when present", () => {
    render(<AssetIcon assets={assets} assetId={1} fallbackType="ssh" className="asset-icon" data-testid="icon" />);

    const icon = screen.getByTestId("icon");
    expect(icon.classList.contains("asset-icon")).toBe(true);
    expect(icon.getAttribute("style") ?? "").toContain("color");
  });

  it("falls back to the registered asset-type icon when the asset has no Icon", () => {
    const DatabaseIcon = getAssetType("database")?.icon;
    expect(DatabaseIcon).toBeDefined();

    render(<AssetIcon assets={assets} assetId={2} fallbackType="database" data-testid="icon" />);

    expect(screen.getByTestId("icon").tagName.toLowerCase()).toBe("svg");
  });

  it("falls back to Server when the asset is absent and the type is unregistered", () => {
    expect(getAssetType("no-such-type")).toBeUndefined();

    render(<AssetIcon assets={assets} assetId={999} fallbackType="no-such-type" data-testid="icon" />);

    expect(screen.getByTestId("icon").tagName.toLowerCase()).toBe("svg");
  });
});

describe("EntityIcon", () => {
  it("renders the provided fallback when no icon value is configured", () => {
    render(<EntityIcon fallback={Server} style={{ opacity: 0.5 }} data-testid="icon" />);

    const icon = screen.getByTestId("icon");
    expect(icon.tagName.toLowerCase()).toBe("svg");
    expect(icon).toHaveStyle({ opacity: "0.5" });
  });

  it("merges the configured icon color with caller-provided styles", () => {
    render(<EntityIcon icon="server#ff0000" style={{ opacity: 0.5 }} data-testid="icon" />);

    expect(screen.getByTestId("icon")).toHaveStyle({ color: "#ff0000", opacity: "0.5" });
  });
});
