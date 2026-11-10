import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ExternalEditCompareWorkbench } from "../CompareWorkbench";

const { codeDiffViewerMock } = vi.hoisted(() => ({ codeDiffViewerMock: vi.fn() }));

vi.mock("@/components/CodeDiffViewer", () => ({
  CodeDiffViewer: (props: { original?: string; modified?: string; testId?: string }) => {
    codeDiffViewerMock(props);
    return <div data-testid={props.testId} />;
  },
}));

const compareResult = {
  documentKey: "101:/srv/app/demo.txt",
  primaryDraftSessionId: "conflict",
  fileName: "demo.txt",
  remotePath: "/srv/app/demo.txt",
  remoteContent: "remote\n",
  localContent: "local\n",
  readOnly: true,
};

describe("ExternalEditCompareWorkbench", () => {
  it("puts the local draft on the left (Monaco original side) and the remote version on the right", () => {
    render(<ExternalEditCompareWorkbench compareResult={compareResult} onDismiss={vi.fn()} />);

    expect(codeDiffViewerMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ original: "local\n", modified: "remote\n" })
    );
  });

  it("labels the left column as local and the right column as remote", () => {
    render(<ExternalEditCompareWorkbench compareResult={compareResult} onDismiss={vi.fn()} />);

    expect(codeDiffViewerMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        originalTitle: "externalEdit.side.local",
        modifiedTitle: "externalEdit.side.remote",
        toneBySide: true,
      })
    );
    expect(screen.getByText("externalEdit.compare.localLeftRemoteRight")).toBeInTheDocument();
  });
});
