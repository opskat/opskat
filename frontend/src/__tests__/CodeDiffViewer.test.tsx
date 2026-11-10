import { beforeAll, describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { buildTextDiffBlocks } from "../lib/textDiffBlocks";

const diffEditorMock = vi.fn();
const originalDecorationsMock = vi.fn((..._args: unknown[]) => ({ clear: vi.fn() }));
const modifiedDecorationsMock = vi.fn((..._args: unknown[]) => ({ clear: vi.fn() }));

vi.mock("@/lib/monaco-setup", () => ({
  setupMonaco: vi.fn(),
}));

vi.mock("@monaco-editor/react", () => ({
  DiffEditor: (props: Record<string, unknown>) => {
    diffEditorMock(props);
    const onMount = props.onMount as
      | ((editor: { getOriginalEditor: () => unknown; getModifiedEditor: () => unknown }, monaco: unknown) => void)
      | undefined;
    onMount?.(
      {
        getOriginalEditor: () => ({
          createDecorationsCollection: originalDecorationsMock,
          revealLineInCenter: vi.fn(),
          setPosition: vi.fn(),
        }),
        getModifiedEditor: () => ({
          createDecorationsCollection: modifiedDecorationsMock,
          revealLineInCenter: vi.fn(),
          setPosition: vi.fn(),
        }),
      },
      {
        Range: vi.fn(function Range(
          this: unknown,
          startLine: number,
          startColumn: number,
          endLine: number,
          endColumn: number
        ) {
          return { startLineNumber: startLine, startColumn, endLineNumber: endLine, endColumn };
        }),
        editor: { OverviewRulerLane: { Full: 7 } },
      }
    );
    return null;
  },
}));

describe("CodeDiffViewer", () => {
  // 第一次动态加载组件要编译整棵依赖，全量并发跑时会超过单个测试默认的 5 秒；单独预热。
  beforeAll(async () => {
    await import("../components/CodeDiffViewer");
  }, 60_000);

  it("forces side-by-side diff mode without inline fallback", async () => {
    const { CodeDiffViewer } = await import("../components/CodeDiffViewer");
    render(<CodeDiffViewer original="remote" modified="local" />);

    await waitFor(() => {
      expect(diffEditorMock).toHaveBeenCalled();
    });

    const calls = diffEditorMock.mock.calls as Array<[Record<string, unknown>]>;
    const props = calls[0][0] as {
      options?: {
        diffAlgorithm?: string;
        renderSideBySide?: boolean;
        useInlineViewWhenSpaceIsLimited?: boolean;
        readOnly?: boolean;
        originalEditable?: boolean;
        renderOverviewRuler?: boolean;
        glyphMargin?: boolean;
      };
      wrapperProps?: Record<string, string>;
    };

    expect(props.options?.diffAlgorithm).toBe("advanced");
    expect(props.options?.renderSideBySide).toBe(true);
    expect(props.options?.useInlineViewWhenSpaceIsLimited).toBe(false);
    expect(props.options?.readOnly).toBe(true);
    expect(props.options?.originalEditable).toBe(false);
    expect(props.options?.renderOverviewRuler).toBe(true);
    expect(props.options?.glyphMargin).toBe(true);
  });

  it("reports diff count and exposes wrapper test id for navigation", async () => {
    const onDiffStatsChange = vi.fn();
    const { CodeDiffViewer } = await import("../components/CodeDiffViewer");
    render(
      <CodeDiffViewer
        activeBlockIndex={0}
        modified={"same\nlocal\n"}
        navigationToken={1}
        original={"same\nremote\n"}
        testId="diff-editor"
        onDiffStatsChange={onDiffStatsChange}
      />
    );

    await waitFor(() => expect(diffEditorMock).toHaveBeenCalled());
    await waitFor(() => expect(onDiffStatsChange).toHaveBeenCalledWith(expect.objectContaining({ total: 1 })));

    const calls = diffEditorMock.mock.calls as Array<[Record<string, unknown>]>;
    const props = calls.at(-1)?.[0] as { wrapperProps?: Record<string, string> };
    expect(props.wrapperProps).toEqual({ "data-testid": "diff-editor" });
  });

  it("colours changes by side when asked, so a left-hand local side is never painted as deletions", async () => {
    const { CodeDiffViewer } = await import("../components/CodeDiffViewer");
    originalDecorationsMock.mockClear();
    modifiedDecorationsMock.mockClear();
    // 第 2 行只在左侧（delete），第 4 行两侧不同（modify），第 6 行只在右侧（insert），第 2 块是当前块。
    render(
      <CodeDiffViewer
        activeBlockIndex={1}
        original={"a\nlocal-only\nb\nleft\nc\n"}
        modified={"a\nb\nright\nc\nremote-only\n"}
        toneBySide
      />
    );
    await waitFor(() => expect(originalDecorationsMock).toHaveBeenCalled());
    await waitFor(() => expect(modifiedDecorationsMock).toHaveBeenCalled());

    const classes = (mock: typeof originalDecorationsMock) =>
      (mock.mock.calls.at(-1)?.[0] as { options: { className: string; glyphMarginClassName: string } }[]).map(
        (decoration) => [decoration.options.className, decoration.options.glyphMarginClassName]
      );
    const left = classes(originalDecorationsMock);
    const right = classes(modifiedDecorationsMock);
    expect(left.flat().some((name) => /delete|insert/.test(name))).toBe(false);
    expect(right.flat().some((name) => /delete|insert/.test(name))).toBe(false);
    expect(left).toContainEqual(["external-edit-compare-line-local", "external-edit-compare-gutter-local"]);
    expect(right).toContainEqual(["external-edit-compare-line-remote", "external-edit-compare-gutter-remote"]);
    expect(left).toContainEqual(["external-edit-compare-line-current", "external-edit-compare-gutter-current"]);
  });

  it("normalizes insert/delete/modify blocks for external edit navigation", () => {
    expect(buildTextDiffBlocks("a\nb\nc", "a\nB\nc")).toEqual([
      {
        id: "modify:1-1:1-1",
        kind: "modify",
        originalStartLine: 2,
        originalEndLine: 2,
        modifiedStartLine: 2,
        modifiedEndLine: 2,
      },
    ]);
    expect(buildTextDiffBlocks("a\nb", "a\nb\nc")[0]?.kind).toBe("insert");
    expect(buildTextDiffBlocks("a\nb\nc", "a\nb")[0]?.kind).toBe("delete");
  });
});
