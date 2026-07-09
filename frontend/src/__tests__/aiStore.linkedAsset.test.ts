import { describe, it, expect, vi } from "vitest";

vi.mock("../i18n", () => ({ default: { t: (k: string, f?: string) => f || k } }));

import { sanitizeSidebarTab } from "../stores/aiStore";

describe("sanitizeSidebarTab linked asset", () => {
  it("round-trips valid linked asset fields", () => {
    const tab = sanitizeSidebarTab({
      id: "sidebar-1",
      conversationId: 7,
      title: "t",
      createdAt: 1,
      linkedAssetId: 42,
      linkedAssetName: "prod-web-01",
      linkedAssetType: "ssh",
    });
    expect(tab?.linkedAssetId).toBe(42);
    expect(tab?.linkedAssetName).toBe("prod-web-01");
    expect(tab?.linkedAssetType).toBe("ssh");
  });

  it("drops non-number linkedAssetId to undefined", () => {
    const tab = sanitizeSidebarTab({ id: "s2", linkedAssetId: "nope" });
    expect(tab?.linkedAssetId).toBeUndefined();
  });
});
