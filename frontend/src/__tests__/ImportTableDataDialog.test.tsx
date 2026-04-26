import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ImportTableDataDialog } from "@/components/query/ImportTableDataDialog";
import * as App from "../../wailsjs/go/app/App";

describe("ImportTableDataDialog", () => {
  async function walkCsvWizardToMode(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByLabelText("query.importTypeCsv"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    const file = new File(["id,name\n1,Alice"], "users.csv", { type: "text/csv" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
  }

  async function walkCsvWizardToSummary(user: ReturnType<typeof userEvent.setup>) {
    await walkCsvWizardToMode(user);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
  }

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

    await user.click(screen.getByLabelText("query.importTypeCsv"));
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

  it("does not show enterprise-only import format placeholders", () => {
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

    expect(screen.queryByText("query.importTypeExcel")).not.toBeInTheDocument();
    expect(screen.queryByText("query.importTypeAccess")).not.toBeInTheDocument();
    expect(screen.queryByText("Ent")).not.toBeInTheDocument();
  });

  it("rejects files that do not match the selected import type", async () => {
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

    await user.click(screen.getByLabelText("query.importTypeJson"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, {
      target: { files: [new File(["id,name\n1,Alice"], "users.csv", { type: "text/csv" })] },
    });

    await waitFor(() => expect(screen.queryByText("users.csv")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "query.importWizardNext" })).toBeDisabled();
  });

  it("does not expose Save Profile as a clickable no-op on the final import step", async () => {
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

    await walkCsvWizardToSummary(user);

    expect(screen.getByRole("button", { name: "query.importSaveProfile" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "query.importWizardStart" })).toBeEnabled();
  });

  it("shows import mode before summary and exposes advanced settings", async () => {
    const user = userEvent.setup();

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="users"
        columns={["id", "name"]}
        primaryKeys={["id"]}
        driver="mysql"
        onSuccess={vi.fn()}
      />
    );

    await walkCsvWizardToMode(user);

    expect(screen.getByText("query.importModeIntro")).toBeInTheDocument();
    expect(screen.getByLabelText("query.importModeAppend")).toBeChecked();

    await user.click(screen.getByRole("button", { name: "query.importAdvancedSettings" }));

    expect(screen.getByText("query.importAdvancedTitle")).toBeInTheDocument();
    expect(screen.getByLabelText("query.importAdvancedExtendedInsert")).toBeChecked();
    expect(screen.getByLabelText("query.importAdvancedContinueOnError")).toBeChecked();

    await user.click(screen.getByRole("button", { name: "query.importAdvancedOk" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    expect(screen.getByText("query.importSummaryIntro")).toBeInTheDocument();
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
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));

    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    expect(App.ExecuteSQL).toHaveBeenCalledWith(
      1,
      "INSERT INTO `appdb`.`users` (`id`, `name`, `email`) VALUES ('1', 'Alice', 'alice@example.test');",
      "appdb"
    );
  });

  it("shows import execution errors in the summary log", async () => {
    const user = userEvent.setup();
    vi.mocked(App.ExecuteSQL).mockRejectedValueOnce(new Error("Incorrect datetime value"));

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

    await walkCsvWizardToSummary(user);
    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    expect(await screen.findByText(/^\[ERR\].*Incorrect datetime value/)).toBeInTheDocument();
    expect(screen.getByText("query.importError")).toBeInTheDocument();
    expect(screen.getByText("[IMP] Processed: 1, Added: 0, Updated: 0, Deleted: 0, Errors: 1")).toBeInTheDocument();
  });
});
