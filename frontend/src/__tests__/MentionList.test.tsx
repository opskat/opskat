/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { createRef } from "react";
import { MentionList, type MentionListRef, type MentionItem } from "@/components/ai/MentionList";
import { useAssetStore } from "@/stores/assetStore";

function seed(assets: any[], groups: any[] = []) {
  useAssetStore.setState({
    assets,
    groups,
  } as any);
}

describe("MentionList", () => {
  beforeEach(() => {
    seed(
      [
        { ID: 42, Name: "prod-db", Type: "mysql", GroupID: 0 },
        { ID: 43, Name: "prod-web", Type: "ssh", GroupID: 1 },
        { ID: 44, Name: "cache-1", Type: "redis", GroupID: 0 },
      ],
      [{ ID: 1, Name: "生产", ParentID: 0 }]
    );
  });

  it("按资产名过滤", async () => {
    const selected: MentionItem[] = [];
    render(<MentionList query="prod" command={(item) => selected.push(item)} />);
    const items = screen.getAllByRole("option");
    expect(items.map((el) => el.textContent)).toEqual(
      expect.arrayContaining([expect.stringContaining("prod-db"), expect.stringContaining("prod-web")])
    );
    expect(items).toHaveLength(2);
  });

  it("按分组路径过滤", async () => {
    render(<MentionList query="生产" command={() => {}} />);
    const items = screen.getAllByRole("option");
    expect(items).toHaveLength(1);
    expect(items[0].textContent).toContain("prod-web");
  });

  it("无匹配显示未找到", () => {
    render(<MentionList query="nope" command={() => {}} />);
    expect(screen.getByText("ai.mentionNotFound")).toBeInTheDocument();
  });

  it("Enter 触发 command 提交选中项", async () => {
    const ref = createRef<MentionListRef>();
    const received: MentionItem[] = [];
    render(<MentionList ref={ref} query="prod" command={(item) => received.push(item)} />);
    ref.current?.onKeyDown({ event: { key: "Enter" } as any });
    expect(received).toHaveLength(1);
    expect(received[0].id).toBe(42); // 前缀匹配 + 排序后第一项
  });

  it("ArrowDown 移动 selectedIndex", () => {
    const ref = createRef<MentionListRef>();
    render(<MentionList ref={ref} query="prod" command={() => {}} />);
    ref.current?.onKeyDown({ event: { key: "ArrowDown" } as any });
    const items = screen.getAllByRole("option");
    expect(items[1]).toHaveAttribute("aria-selected", "true");
  });
});
