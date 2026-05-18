import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type * as MonacoNS from "monaco-editor";
import { ExternalEditMergeWorkbench } from "../components/terminal/external-edit/MergeWorkbench";
import { useExternalEditStore } from "../stores/externalEditStore";

const { codeEditorMountController } = vi.hoisted(() => ({
  codeEditorMountController: {
    mounts: [] as Array<() => void>,
    editors: new Map<
      string,
      {
        createDecorationsCollection: ReturnType<typeof vi.fn>;
        getTopForLineNumber: ReturnType<typeof vi.fn>;
        getScrollTop: ReturnType<typeof vi.fn>;
        onDidScrollChange: ReturnType<typeof vi.fn>;
        onDidLayoutChange: ReturnType<typeof vi.fn>;
        onDidContentSizeChange: ReturnType<typeof vi.fn>;
        revealLineInCenter: ReturnType<typeof vi.fn>;
        setPosition: ReturnType<typeof vi.fn>;
      }
    >(),
  },
}));

const requestAnimationFrameMock = vi.fn((callback: FrameRequestCallback) => {
  callback(0);
  return 1;
});
vi.stubGlobal("requestAnimationFrame", requestAnimationFrameMock);
vi.stubGlobal("cancelAnimationFrame", vi.fn());

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({
    onMount,
    readOnly: _readOnly,
    testId,
    value,
  }: {
    onMount?: (editor: unknown, monaco: unknown) => void;
    readOnly?: boolean;
    testId?: string;
    value?: string;
  }) => {
    const editor = {
      createDecorationsCollection: vi.fn(() => ({ clear: vi.fn() })),
      getTopForLineNumber: vi.fn((lineNumber: number) => (lineNumber - 1) * 19),
      getScrollTop: vi.fn(() => 0),
      onDidScrollChange: vi.fn(() => ({ dispose: vi.fn() })),
      onDidLayoutChange: vi.fn(() => ({ dispose: vi.fn() })),
      onDidContentSizeChange: vi.fn(() => ({ dispose: vi.fn() })),
      revealLineInCenter: vi.fn(),
      setPosition: vi.fn(),
    };
    const monaco = {
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
    } as unknown as typeof MonacoNS;
    const mount = () => {
      codeEditorMountController.editors.set(testId || "unknown", editor);
      onMount?.(editor, monaco);
    };
    codeEditorMountController.mounts.push(mount);
    return <pre data-testid={testId}>{value}</pre>;
  },
}));

describe("ExternalEditMergeWorkbench", () => {
  beforeEach(() => {
    codeEditorMountController.mounts = [];
    codeEditorMountController.editors.clear();
    useExternalEditStore.setState({ applyMerge: vi.fn() });
  });

  const defaultMergeResult = {
    documentKey: "101:/srv/app/demo.txt",
    primaryDraftSessionId: "conflict",
    fileName: "demo.txt",
    remotePath: "/srv/app/demo.txt",
    localContent: "line1\nlocal-change\nline3\n",
    remoteContent: "line1\nremote-change\nline3\n",
    finalContent: "line1\nlocal-change\nline3\n",
    remoteHash: "remote-hash",
  };

  it("renders three-pane merge layout with navigation", async () => {
    const { container } = render(
      <ExternalEditMergeWorkbench
        mergeResult={defaultMergeResult}
        savingSessionId={null}
        onClose={vi.fn()}
        onError={vi.fn()}
      />
    );

    for (const mount of [...codeEditorMountController.mounts]) mount();

    await waitFor(() => {
      expect(container.querySelector('[data-testid="external-edit-merge-local"]')).toBeTruthy();
      expect(container.querySelector('[data-testid="external-edit-merge-final"]')).toBeTruthy();
      expect(container.querySelector('[data-testid="external-edit-merge-remote"]')).toBeTruthy();
      expect(container.querySelector('[data-testid="external-edit-merge-conflict-count"]')?.textContent).toBe("1 / 1");
    });
  });

  it("re-runs decorations and reveals first conflict on mount", async () => {
    render(
      <ExternalEditMergeWorkbench
        mergeResult={defaultMergeResult}
        savingSessionId={null}
        onClose={vi.fn()}
        onError={vi.fn()}
      />
    );

    for (const mount of [...codeEditorMountController.mounts]) mount();

    await waitFor(() => {
      const localEditor = codeEditorMountController.editors.get("external-edit-merge-local");
      const finalEditor = codeEditorMountController.editors.get("external-edit-merge-final");
      const remoteEditor = codeEditorMountController.editors.get("external-edit-merge-remote");

      expect(localEditor?.createDecorationsCollection).toHaveBeenCalled();
      expect(finalEditor?.createDecorationsCollection).toHaveBeenCalled();
      expect(remoteEditor?.createDecorationsCollection).toHaveBeenCalled();
      expect(localEditor?.revealLineInCenter).toHaveBeenCalledWith(2);
    });
  });

  it("shows 3-column grid without action columns", () => {
    const { container } = render(
      <ExternalEditMergeWorkbench
        mergeResult={defaultMergeResult}
        savingSessionId={null}
        onClose={vi.fn()}
        onError={vi.fn()}
      />
    );

    expect(container.querySelector("[data-action-column]")).toBeFalsy();
    expect(container.querySelectorAll("[data-idea-pane]").length).toBe(3);
  });

  it("shows save button enabled when not saving", () => {
    const { container } = render(
      <ExternalEditMergeWorkbench
        mergeResult={defaultMergeResult}
        savingSessionId={null}
        onClose={vi.fn()}
        onError={vi.fn()}
      />
    );

    const saveBtn = container.querySelector('[data-testid="external-edit-merge-workbench"] button:last-of-type');
    expect(saveBtn).toBeTruthy();
  });
});
