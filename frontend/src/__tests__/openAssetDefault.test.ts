import { describe, it, expect, vi, beforeEach } from "vitest";
import { openAssetDefault } from "../lib/openAssetDefault";
import { asset_entity } from "../../wailsjs/go/models";

// Mock the dependencies
vi.mock("../lib/assetTypes", () => ({
  getAssetType: vi.fn(),
}));

vi.mock("../lib/openAssetInfoTab", () => ({
  openAssetInfoTab: vi.fn(),
}));

import { getAssetType } from "../lib/assetTypes";
import { openAssetInfoTab } from "../lib/openAssetInfoTab";

describe("openAssetDefault", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls onConnectAsset when asset type canConnect is true", () => {
    const mockGetAssetType = getAssetType as typeof getAssetType;
    const mockOnConnect = vi.fn();
    const asset: asset_entity.Asset = {
      ID: 1,
      Name: "test",
      Type: "ssh",
      Host: "",
      Port: 0,
      Username: "",
      Password: "",
      PrivateKey: "",
      Icon: "",
      Remark: "",
      GroupID: 0,
      Color: "",
      Status: 1,
      CreatedAt: "",
      UpdatedAt: "",
    };

    mockGetAssetType.mockReturnValue({ canConnect: true });

    openAssetDefault(asset, mockOnConnect);

    expect(mockOnConnect).toHaveBeenCalledWith(asset);
    expect(openAssetInfoTab).not.toHaveBeenCalled();
  });

  it("calls openAssetInfoTab when asset type canConnect is false", () => {
    const mockGetAssetType = getAssetType as typeof getAssetType;
    const mockOnConnect = vi.fn();
    const asset: asset_entity.Asset = {
      ID: 42,
      Name: "test",
      Type: "unknown",
      Host: "",
      Port: 0,
      Username: "",
      Password: "",
      PrivateKey: "",
      Icon: "",
      Remark: "",
      GroupID: 0,
      Color: "",
      Status: 1,
      CreatedAt: "",
      UpdatedAt: "",
    };

    mockGetAssetType.mockReturnValue({ canConnect: false });

    openAssetDefault(asset, mockOnConnect);

    expect(openAssetInfoTab).toHaveBeenCalledWith(42);
    expect(mockOnConnect).not.toHaveBeenCalled();
  });

  it("calls openAssetInfoTab when getAssetType returns undefined", () => {
    const mockGetAssetType = getAssetType as typeof getAssetType;
    const mockOnConnect = vi.fn();
    const asset: asset_entity.Asset = {
      ID: 99,
      Name: "test",
      Type: "nonexistent",
      Host: "",
      Port: 0,
      Username: "",
      Password: "",
      PrivateKey: "",
      Icon: "",
      Remark: "",
      GroupID: 0,
      Color: "",
      Status: 1,
      CreatedAt: "",
      UpdatedAt: "",
    };

    mockGetAssetType.mockReturnValue(undefined);

    openAssetDefault(asset, mockOnConnect);

    expect(openAssetInfoTab).toHaveBeenCalledWith(99);
    expect(mockOnConnect).not.toHaveBeenCalled();
  });
});
