import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { buildMentionXml } from "@/lib/mentionXml";
import { ReferencesRow } from "../ReferencesRow";
import { useAIStore } from "@/stores/aiStore";

const { jumpToAsset } = vi.hoisted(() => ({ jumpToAsset: vi.fn().mockResolvedValue(undefined) }));
vi.mock("@/lib/aiReferences", async (orig) => {
  const actual = await orig<typeof import("@/lib/aiReferences")>();
  return { ...actual, jumpToAsset, isAssetTabOpen: (id: number) => id === 5 };
});

describe("ReferencesRow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAIStore.setState({
      conversationMessages: {
        1: [
          { role: "user", content: `${buildMentionXml({ assetId: 5, name: "web", type: "ssh" })} ${buildMentionXml({ assetId: 9, name: "cache", type: "redis" })}`, blocks: [] } as any,
        ],
      },
    });
  });

  it("renders referenced assets excluding the bound one and jumps on click", () => {
    render(<ReferencesRow conversationId={1} boundAssetId={5} />);
    // 5 被排除（是绑定资产），只剩 cache(9)
    expect(screen.queryByText("web")).toBeNull();
    fireEvent.click(screen.getByText("cache"));
    expect(jumpToAsset).toHaveBeenCalledWith(9);
  });

  it("renders nothing when there are no references", () => {
    const { container } = render(<ReferencesRow conversationId={999} boundAssetId={null} />);
    expect(container).toBeEmptyDOMElement();
  });
});
