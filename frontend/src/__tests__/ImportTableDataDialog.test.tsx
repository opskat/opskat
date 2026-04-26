import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ImportTableDataDialog } from "@/components/query/ImportTableDataDialog";
import * as App from "../../wailsjs/go/app/App";

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

    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    const file = new File(["external_id,full_name\n1,Alice"], "users.csv", { type: "text/csv" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    expect(await screen.findByText("query.importNoMappedColumns")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "query.importWizardNext" })).toBeDisabled();
  });

  it("walks through JSON import wizard and starts importing mapped rows", async () => {
    const user = userEvent.setup();
    vi.mocked(App.ExecuteSQL).mockResolvedValue(JSON.stringify({ affected_rows: 1 }));

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="users"
        columns={["id", "name", "email"]}
        driver="mysql"
        onSuccess={vi.fn()}
      />
    );

    await user.click(screen.getByLabelText("query.importTypeJson"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    const file = new File([JSON.stringify([{ id: 1, name: "Alice", email: "alice@example.test" }])], "users.json", {
      type: "application/json",
    });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    expect(screen.getByText("query.importOptionsTitle")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    expect(await screen.findByText("query.importMappingIntro")).toBeInTheDocument();
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    expect(screen.getAllByText("email").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    expect(App.ExecuteSQL).toHaveBeenCalledWith(
      1,
      "INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');",
      "appdb"
    );
  });
});
