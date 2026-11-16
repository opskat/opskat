import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DumpSqlFileDialog, DUMP_SQL_FILE_EVENT, type DumpSqlFileEvent } from "@/components/query/DumpSqlFileDialog";
import { CancelDumpSQLFile, SelectDumpFile, StartDumpSQLFile } from "../../wailsjs/go/query/Query";
import { OpenDirectory } from "../../wailsjs/go/system/System";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// 带插值参数的文案要能看出参数(全局 mock 只回键名)。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key} ${Object.values(opts).join(" ")}` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }), initReactI18next: { type: "3rdParty", init: vi.fn() } };
});

type Listener = (event: DumpSqlFileEvent) => void;
let listeners: Listener[] = [];
const unsubscribe = vi.fn();

/** 后端发出一个转储事件。 */
function emit(event: Partial<DumpSqlFileEvent>) {
  const full: DumpSqlFileEvent = {
    dumpId: "dump-1",
    status: "running",
    progress: { objects: 2, object: 1, name: "users", rows: 0 },
    elapsedMs: 0,
    ...event,
  };
  act(() => listeners.forEach((listener) => listener(full)));
}

const onOpenChange = vi.fn();

function renderDialog(props: Partial<React.ComponentProps<typeof DumpSqlFileDialog>> = {}) {
  return render(
    <DumpSqlFileDialog
      open
      onOpenChange={onOpenChange}
      assetId={7}
      assetName="demo-sqlite"
      database="main"
      target={{ kind: "tables", tables: ["users", "orders"] }}
      withData
      canDumpData
      {...props}
    />
  );
}

const chooseButton = () => screen.getByRole("button", { name: /query\.exportChooseFile/ });
const startButton = () => screen.getByRole("button", { name: /query\.dumpSqlFileStart/ });

async function chooseLocation(path = "/tmp/main.sql") {
  vi.mocked(SelectDumpFile).mockResolvedValue(path);
  await userEvent.click(chooseButton());
  await screen.findByText(path);
}

async function startDump() {
  await chooseLocation();
  await userEvent.click(startButton());
  await waitFor(() => expect(StartDumpSQLFile).toHaveBeenCalled());
}

beforeEach(() => {
  listeners = [];
  vi.mocked(EventsOn).mockImplementation((name: string, callback: (...data: never[]) => void) => {
    expect(name).toBe(DUMP_SQL_FILE_EVENT);
    listeners.push(callback as unknown as Listener);
    return unsubscribe;
  });
  vi.mocked(StartDumpSQLFile).mockResolvedValue("dump-1");
});

afterEach(() => {
  vi.mocked(SelectDumpFile).mockReset();
  vi.mocked(StartDumpSQLFile).mockReset();
  vi.mocked(CancelDumpSQLFile).mockReset();
  vi.mocked(OpenDirectory).mockReset();
  vi.mocked(EventsOn).mockReset();
  onOpenChange.mockReset();
  unsubscribe.mockReset();
});

describe("DumpSqlFileDialog", () => {
  it("names the source, offers <table>.sql for one table and only starts once a location is chosen", async () => {
    renderDialog({ target: { kind: "tables", tables: ["users"] }, withData: false });
    expect(screen.getByText("query.dumpSqlFileDesc demo-sqlite / main users")).toBeInTheDocument();
    expect(screen.getByText("query.dumpSqlFileNote")).toBeInTheDocument();
    expect(startButton()).toBeDisabled();

    await chooseLocation("/tmp/users.sql");
    expect(SelectDumpFile).toHaveBeenCalledWith("users.sql");
    await userEvent.click(startButton());

    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(7, {
        database: "main",
        schema: "",
        tables: ["users"],
        whole: false,
        withData: false,
        path: "/tmp/users.sql",
      })
    );
  });

  it("offers <database>_<YYYYMMDD>.sql for several tables", async () => {
    renderDialog();
    await chooseLocation();
    expect(SelectDumpFile).toHaveBeenCalledWith(expect.stringMatching(/^main_\d{8}\.sql$/));
  });

  it("dumps the whole database: names it, offers <database>_<YYYYMMDD>.sql and asks for every object", async () => {
    renderDialog({ target: { kind: "database" } });
    expect(
      screen.getByText("query.dumpSqlFileDesc demo-sqlite / main query.dumpSqlFileWholeDatabase")
    ).toBeInTheDocument();

    await chooseLocation();
    expect(SelectDumpFile).toHaveBeenCalledWith(expect.stringMatching(/^main_\d{8}\.sql$/));
    await userEvent.click(startButton());
    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(7, {
        database: "main",
        schema: "",
        tables: [],
        whole: true,
        withData: true,
        path: "/tmp/main.sql",
      })
    );
    // 转储整个库时事先不知道有几个对象:第一个事件到之前进度是 0,不是 NaN。
    expect(screen.getByText("0%")).toBeInTheDocument();
  });

  it("dumps a whole schema: names it, offers <schema>_<YYYYMMDD>.sql and passes the schema", async () => {
    renderDialog({ target: { kind: "schema", schema: "sales" }, withData: false });
    expect(
      screen.getByText("query.dumpSqlFileDesc demo-sqlite / main query.dumpSqlFileWholeSchema sales")
    ).toBeInTheDocument();

    await chooseLocation("/tmp/sales.sql");
    expect(SelectDumpFile).toHaveBeenCalledWith(expect.stringMatching(/^sales_\d{8}\.sql$/));
    await userEvent.click(startButton());
    await waitFor(() =>
      expect(StartDumpSQLFile).toHaveBeenCalledWith(7, {
        database: "main",
        schema: "sales",
        tables: [],
        whole: true,
        withData: false,
        path: "/tmp/sales.sql",
      })
    );
  });

  it("starts with the content picked from the menu and leaves out structure and data when the driver can't write rows", async () => {
    renderDialog({ withData: false, canDumpData: false });
    expect(screen.getByRole("combobox")).toHaveTextContent("query.dumpSqlFileStructureOnly");
    await userEvent.click(screen.getByRole("combobox"));
    expect(screen.queryByRole("option", { name: "query.dumpSqlFileStructureAndData" })).not.toBeInTheDocument();
  });

  it("while running: shows progress and a log line per object, can't be closed, and Cancel cancels the dump", async () => {
    renderDialog();
    await startDump();

    emit({ progress: { objects: 2, object: 1, name: "users", rows: 1200 } });
    expect(screen.getByText("query.dumpSqlFileProgress 1 2 1,200")).toBeInTheDocument();
    emit({
      progress: {
        objects: 2,
        object: 1,
        name: "users",
        rows: 1200,
        finished: { name: "users", kind: "table", data: true, rows: 1200 },
      },
    });
    expect(screen.getByText("[DMP] users: query.dumpSqlFileLogStructure")).toBeInTheDocument();
    expect(screen.getByText("[DMP] users: query.dumpSqlFileLogData 1,200")).toBeInTheDocument();

    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: /query\.dumpSqlFileStart/ })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /action\.cancel/ }));
    expect(CancelDumpSQLFile).toHaveBeenCalledWith("dump-1");
  });

  it("when done: shows the summary and offers to open the folder or the file", async () => {
    renderDialog();
    await startDump();

    emit({
      status: "done",
      progress: { objects: 2, object: 2, name: "orders", rows: 131027 },
      elapsedMs: 8200,
      result: { objects: 2, rows: 131027, bytes: 2048 },
    });
    expect(screen.getByText("131,027")).toBeInTheDocument();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("query.dumpSqlFileSeconds 8.2")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /query\.openExport/ }));
    await userEvent.click(await screen.findByText("query.openExportFolder"));
    expect(OpenDirectory).toHaveBeenCalledWith("/tmp");
    await userEvent.click(screen.getByRole("button", { name: /query\.openExport/ }));
    await userEvent.click(await screen.findByText("query.dumpSqlFileOpenFile"));
    expect(OpenDirectory).toHaveBeenCalledWith("/tmp/main.sql");

    await userEvent.click(screen.getByRole("button", { name: /action\.close/ }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("when it fails: names the object and the error, says the unfinished file was deleted, and can dump again", async () => {
    renderDialog();
    await startDump();

    emit({ status: "failed", object: "orders", error: "invalid connection" });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("query.dumpSqlFileFailedAt orders");
    expect(alert).toHaveTextContent("invalid connection");
    expect(alert).toHaveTextContent("query.dumpSqlFileDeleted");

    vi.mocked(StartDumpSQLFile).mockClear();
    await userEvent.click(screen.getByRole("button", { name: /query\.dumpSqlFileRestart/ }));
    await waitFor(() => expect(StartDumpSQLFile).toHaveBeenCalledTimes(1));
  });

  it("when canceled: says where it stopped and that nothing was left behind", async () => {
    renderDialog();
    await startDump();

    emit({ status: "canceled", object: "users" });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("query.dumpSqlFileCanceledAt users");
    expect(alert).toHaveTextContent("query.dumpSqlFileDeleted");
    expect(screen.getByRole("button", { name: /action\.close/ })).toBeEnabled();
  });

  it("cancels the running dump when the dialog goes away (the asset tab is closed)", async () => {
    const { unmount } = renderDialog();
    await startDump();

    unmount();
    expect(CancelDumpSQLFile).toHaveBeenCalledWith("dump-1");
  });
});
