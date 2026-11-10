import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TableDataStatusBar, TableEditorToolbar } from "../TableEditorToolbar";

function renderToolbar(overrides: Partial<React.ComponentProps<typeof TableEditorToolbar>> = {}) {
  return render(
    <TableEditorToolbar
      hasEdits
      submitting={false}
      canExport
      canImport
      gridEdit
      importAvailable
      sqlExportAvailable
      pageSize={1000}
      onPageSizeChange={vi.fn()}
      filterSortOpen={false}
      onToggleFilterSort={vi.fn()}
      onSubmit={vi.fn()}
      onImport={vi.fn()}
      onExport={vi.fn()}
      onPreviewSql={vi.fn()}
      {...overrides}
    />
  );
}

function renderStatusBar(overrides: Partial<React.ComponentProps<typeof TableDataStatusBar>> = {}) {
  return render(
    <TableDataStatusBar
      gridEdit
      pendingEditCount={0}
      sqlSummary=""
      totalRows={12}
      page={0}
      totalPages={3}
      pageInput="1"
      hasPrev={false}
      hasNext
      selectedRowCount={0}
      submitting={false}
      loading={false}
      refreshTitle="query.refreshTable"
      onRefresh={vi.fn()}
      onStopLoading={vi.fn()}
      onPageInputChange={vi.fn()}
      onPageInputConfirm={vi.fn()}
      onFirstPage={vi.fn()}
      onPreviousPage={vi.fn()}
      onNextPage={vi.fn()}
      onLastPage={vi.fn()}
      onAddRow={vi.fn()}
      onDeleteRows={vi.fn()}
      onApplyChanges={vi.fn()}
      onDiscardChanges={vi.fn()}
      {...overrides}
    />
  );
}

describe("TableEditorToolbar", () => {
  it("has no discard entry in the top bar", () => {
    renderToolbar();
    expect(screen.queryByTitle("query.discardEdits")).not.toBeInTheDocument();
  });

  it("changes the page size directly from the toolbar", async () => {
    const user = userEvent.setup();
    const onPageSizeChange = vi.fn();
    renderToolbar({ onPageSizeChange });

    const input = screen.getByLabelText("query.pageSize");
    await user.clear(input);
    await user.type(input, "500{Enter}");

    expect(onPageSizeChange).toHaveBeenCalledWith(500);
  });
});

describe("TableDataStatusBar pending edits", () => {
  it("shows the count with text buttons for discard-all and preview-and-commit", async () => {
    const user = userEvent.setup();
    const onApplyChanges = vi.fn();
    const onDiscardChanges = vi.fn();
    renderStatusBar({ pendingEditCount: 3, onApplyChanges, onDiscardChanges });

    expect(screen.getByText("query.pendingEditsCount")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "query.discardAll" }));
    await user.click(screen.getByRole("button", { name: "query.previewAndCommit" }));

    expect(onDiscardChanges).toHaveBeenCalledOnce();
    expect(onApplyChanges).toHaveBeenCalledOnce();
  });

  it("shows no pending-edit controls when nothing is pending", () => {
    renderStatusBar({ pendingEditCount: 0 });
    expect(screen.queryByRole("button", { name: "query.discardAll" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "query.previewAndCommit" })).not.toBeInTheDocument();
  });

  it("has no page-size control (it lives in the toolbar)", () => {
    renderStatusBar();
    expect(screen.queryByTitle("query.tableFooterSettings")).not.toBeInTheDocument();
  });
});
