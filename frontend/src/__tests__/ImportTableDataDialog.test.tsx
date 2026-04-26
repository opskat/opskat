import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ImportTableDataDialog } from "@/components/query/ImportTableDataDialog";

describe("ImportTableDataDialog", () => {
  it("shows an explicit warning when no uploaded columns map to table columns", async () => {
    const user = userEvent.setup();

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="users"
        columns={["id", "name"]}
        driver="mysql"
        onSuccess={vi.fn()}
      />
    );

    const file = new File(["external_id,full_name\n1,Alice"], "users.csv", { type: "text/csv" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);

    expect(await screen.findByText("query.importNoMappedColumns")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "query.designTablePreviewChanges" })).toBeDisabled();
  });
});
