import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ImportTableDataDialog } from "@/components/query/ImportTableDataDialog";
import * as App from "../../wailsjs/go/query/Query";
import { dbdriver, query_svc } from "../../wailsjs/go/models";
import { BUILTIN_DRIVERS, fakeDriverWith, provideDrivers } from "./dbDriverFixtures";

describe("ImportTableDataDialog", () => {
  function mockExecuteTableImport(
    result: Partial<query_svc.TableImportBatchResult> = { processed: 1, added: 1, updated: 0, deleted: 0, error: 0 }
  ) {
    vi.mocked(App.ExecuteTableImport).mockResolvedValue(
      new query_svc.TableImportBatchResult({ processed: 0, added: 0, updated: 0, deleted: 0, error: 0, ...result })
    );
    return vi.mocked(App.ExecuteTableImport);
  }

  beforeEach(() => {
    provideDrivers(BUILTIN_DRIVERS);
  });

  afterEach(() => {
    vi.mocked(App.ExecuteSQL).mockReset();
    vi.mocked(App.ExecuteTableImport).mockReset();
  });

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

  async function walkCsvWizardWithFileToMode(user: ReturnType<typeof userEvent.setup>, content: string) {
    await user.click(screen.getByLabelText("query.importTypeCsv"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    const file = new File([content], "users.csv", { type: "text/csv" });
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, file);
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
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

    const errorLine = await screen.findByText(/^\[ERR\].*Incorrect datetime value/);
    expect(errorLine).toBeInTheDocument();
    expect(errorLine.parentElement).toHaveClass("select-text");
    expect(screen.getByText("query.importError")).toBeInTheDocument();
    expect(screen.getByText("[IMP] Processed: 1, Added: 0, Updated: 0, Deleted: 0, Errors: 1")).toBeInTheDocument();
  });

  it("keeps simple append imports cancellable between statements", async () => {
    const user = userEvent.setup();
    let cancelled = false;
    vi.mocked(App.ExecuteSQL).mockImplementation(async () => {
      cancelled = true;
      return JSON.stringify({ affected_rows: 1 });
    });

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="users"
        columns={["id", "name"]}
        driver="mysql"
        isSubmitCancelled={() => cancelled}
        onSuccess={vi.fn()}
      />
    );

    await walkCsvWizardWithFileToMode(user, "id,name\n1,Alice\n2,Bob");
    await user.click(screen.getByRole("button", { name: "query.importAdvancedSettings" }));
    await user.click(screen.getByLabelText("query.importAdvancedExtendedInsert"));
    await user.click(screen.getByRole("button", { name: "query.importAdvancedOk" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    await waitFor(() => expect(App.ExecuteSQL).toHaveBeenCalledTimes(1));
    expect(App.ExecuteTableImport).not.toHaveBeenCalled();
  });

  async function openAdvancedSettings(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "query.importAdvancedSettings" }));
    expect(await screen.findByText("query.importAdvancedTitle")).toBeInTheDocument();
  }

  async function closeAdvancedSettingsAndStart(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "query.importAdvancedOk" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));
  }

  it("sends copy imports with ignored foreign keys as one batch through the Query binding", async () => {
    const user = userEvent.setup();
    const executeTableImport = mockExecuteTableImport({ processed: 2, added: 1, deleted: 1 });
    const onSuccess = vi.fn();

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="users"
        columns={["id", "name"]}
        driver="mysql"
        onSuccess={onSuccess}
      />
    );

    await walkCsvWizardToMode(user);
    await user.click(screen.getByLabelText("query.importModeCopy"));
    await openAdvancedSettings(user);
    await user.click(await screen.findByLabelText("query.importAdvancedIgnoreForeignKey"));
    await closeAdvancedSettingsAndStart(user);

    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
    expect(executeTableImport).toHaveBeenCalledWith(1, "appdb", {
      statements: ["DELETE FROM `appdb`.`users`;", "INSERT INTO `appdb`.`users` (`id`, `name`) VALUES ('1', 'Alice');"],
      mode: "copy",
      continueOnError: true,
      disableForeignKeyChecks: true,
      table: "users",
    });
    expect(App.ExecuteSQL).not.toHaveBeenCalled();
    expect(screen.queryByText(/binding unavailable/)).not.toBeInTheDocument();
  });

  it("sends imports that stop on the first error as one batch through the Query binding", async () => {
    const user = userEvent.setup();
    const executeTableImport = mockExecuteTableImport();

    render(
      <ImportTableDataDialog
        open
        onOpenChange={vi.fn()}
        assetId={1}
        database="appdb"
        table="dbo.users"
        columns={["id", "name"]}
        driver="mssql"
        onSuccess={vi.fn()}
      />
    );

    await walkCsvWizardToMode(user);
    await openAdvancedSettings(user);
    await user.click(screen.getByLabelText("query.importAdvancedContinueOnError"));
    await closeAdvancedSettingsAndStart(user);

    await waitFor(() => expect(executeTableImport).toHaveBeenCalledTimes(1));
    expect(executeTableImport).toHaveBeenCalledWith(
      1,
      "appdb",
      expect.objectContaining({
        mode: "append",
        continueOnError: false,
        disableForeignKeyChecks: false,
        table: "dbo.users",
      })
    );
    expect(App.ExecuteSQL).not.toHaveBeenCalled();
  });

  it("shows a rolled back batch and its failed statement in the import log", async () => {
    const user = userEvent.setup();
    mockExecuteTableImport({
      processed: 2,
      error: 1,
      rolledBack: true,
      errors: [new query_svc.TableImportBatchError({ index: 1, statement: "INSERT bad", message: "Duplicate entry" })],
    });

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

    await walkCsvWizardToMode(user);
    await user.click(screen.getByLabelText("query.importModeCopy"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    expect(await screen.findByText("[ERR] Duplicate entry")).toBeInTheDocument();
    expect(screen.getByText("[ERR] INSERT bad")).toBeInTheDocument();
    expect(screen.getByText("[IMP] Processed: 2, Added: 0, Updated: 0, Deleted: 0, Errors: 1")).toBeInTheDocument();
    expect(screen.getByText("[IMP] Rolled back")).toBeInTheDocument();
  });

  it("shows a failure of the batch itself in the import log", async () => {
    const user = userEvent.setup();
    vi.mocked(App.ExecuteTableImport).mockRejectedValue(new Error("begin transaction: connection reset"));

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

    await walkCsvWizardToMode(user);
    await user.click(screen.getByLabelText("query.importModeCopy"));
    await user.click(screen.getByRole("button", { name: "query.importWizardNext" }));
    await user.click(screen.getByRole("button", { name: "query.importWizardStart" }));

    expect(await screen.findByText("[ERR] begin transaction: connection reset")).toBeInTheDocument();
    expect(screen.getByText("[IMP] Finished with error")).toBeInTheDocument();
  });

  it("offers ignoring foreign keys only when the driver declares it can disable them", async () => {
    const user = userEvent.setup();
    provideDrivers([
      ...BUILTIN_DRIVERS,
      new dbdriver.Descriptor({ ...fakeDriverWith(["import"], "fk_toggle"), canDisableForeignKeyChecks: true }),
      fakeDriverWith(["import"], "no_fk_toggle"),
    ]);

    for (const [driver, offered] of [
      ["fk_toggle", true],
      ["no_fk_toggle", false],
      ["postgresql", false],
    ] as const) {
      const { unmount } = render(
        <ImportTableDataDialog
          open
          onOpenChange={vi.fn()}
          assetId={1}
          database="appdb"
          table="users"
          columns={["id", "name"]}
          driver={driver}
          onSuccess={vi.fn()}
        />
      );
      await walkCsvWizardToMode(user);
      await openAdvancedSettings(user);
      await waitFor(() =>
        expect(screen.queryByLabelText("query.importAdvancedIgnoreForeignKey") !== null).toBe(offered)
      );
      expect(screen.getByLabelText("query.importAdvancedContinueOnError")).toBeInTheDocument();
      unmount();
    }
  });
});
