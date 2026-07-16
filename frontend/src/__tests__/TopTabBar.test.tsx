import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TopTabBar } from "@/components/layout/TopTabBar";
import { useTabStore } from "@/stores/tabStore";
import { Environment } from "../../wailsjs/runtime/runtime";

describe("TopTabBar window controls inset", () => {
  beforeEach(() => {
    vi.mocked(Environment).mockResolvedValue({ platform: "windows", buildType: "test", arch: "amd64" });
    useTabStore.setState({
      tabs: [
        {
          id: "settings",
          type: "page",
          label: "Settings",
          meta: { type: "page", pageId: "settings" },
        },
      ],
      activeTabId: "settings",
    });
  });

  it("keeps tab actions clear of Windows window controls when it is the topmost bar", async () => {
    const { container } = render(<TopTabBar topmost />);

    await waitFor(() => expect(container.querySelector("[data-top-tabbar]")).toHaveClass("pr-[140px]"));
  });
});
