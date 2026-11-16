import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { RunSqlFileDialog, RUN_SQL_FILE_EVENT, type RunSqlFileEvent } from "@/components/query/RunSqlFileDialog";
import { SelectSQLFile, StartRunSQLFile } from "../../wailsjs/go/query/Query";
import { EventsOn } from "../../wailsjs/runtime/runtime";

// 带插值参数的文案要能看出参数(全局 mock 只回键名)。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key} ${Object.values(opts).join(" ")}` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return { useTranslation: () => ({ t, i18n }), initReactI18next: { type: "3rdParty", init: vi.fn() } };
});

type Listener = (event: RunSqlFileEvent) => void;
let listeners: Listener[] = [];
const unsubscribe = vi.fn();

/** 后端发出一个运行事件。 */
function emit(event: Partial<RunSqlFileEvent>) {
  const full: RunSqlFileEvent = {
    runId: "run-1",
    status: "running",
    progress: { succeeded: 0, failed: 0, bytesRead: 0, totalBytes: 2048 },
    elapsedMs: 0,
    ...event,
  };
  act(() => listeners.forEach((listener) => listener(full)));
}

const onOpenChange = vi.fn();
const onFinished = vi.fn();

function renderDialog() {
  return render(
    <RunSqlFileDialog
      open
      onOpenChange={onOpenChange}
      assetId={7}
      assetName="demo-sqlite"
      database="main"
      onFinished={onFinished}
    />
  );
}

async function chooseFile() {
  vi.mocked(SelectSQLFile).mockResolvedValue({ path: "/tmp/shop.sql", size: 2048 } as never);
  await userEvent.click(screen.getByRole("button", { name: /query\.runSqlFileChoose/ }));
  await screen.findByText("/tmp/shop.sql");
}

const startButton = () => screen.getByRole("button", { name: /query\.runSqlFileStart/ });

beforeEach(() => {
  listeners = [];
  vi.mocked(EventsOn).mockImplementation((name: string, callback: (...data: never[]) => void) => {
    expect(name).toBe(RUN_SQL_FILE_EVENT);
    listeners.push(callback as unknown as Listener);
    return unsubscribe;
  });
  vi.mocked(StartRunSQLFile).mockResolvedValue("run-1");
});

afterEach(() => {
  vi.mocked(SelectSQLFile).mockReset();
  vi.mocked(StartRunSQLFile).mockReset();
  vi.mocked(EventsOn).mockReset();
  onOpenChange.mockReset();
  onFinished.mockReset();
  unsubscribe.mockReset();
});

describe("RunSqlFileDialog", () => {
  it("can only start after a file is chosen, then runs it on the database with the chosen options", async () => {
    renderDialog();
    expect(screen.getByText("query.runSqlFileDesc demo-sqlite / main")).toBeInTheDocument();
    expect(screen.getByText("query.runSqlFileWarning main")).toBeInTheDocument();
    expect(startButton()).toBeDisabled();

    await chooseFile();
    expect(screen.getByText("2.0 KB")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("checkbox"));
    await userEvent.click(startButton());

    expect(StartRunSQLFile).toHaveBeenCalledWith(7, "main", { path: "/tmp/shop.sql", continueOnError: true });
  });

  it("cannot be closed while running and shows the progress", async () => {
    renderDialog();
    await chooseFile();
    await userEvent.click(startButton());
    await waitFor(() => expect(screen.getByRole("button", { name: /query\.runSqlFileRunning/ })).toBeDisabled());

    emit({ progress: { succeeded: 12345, failed: 0, bytesRead: 1024, totalBytes: 2048 } });
    expect(screen.getByText("query.runSqlFileProgress 12,345 1.0 KB 2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("[RUN] query.runSqlFileLogProgress 10,000")).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Close" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /query\.runSqlFileChoose/ })).toBeDisabled();
  });

  it("shows where the run stopped and refreshes the tree", async () => {
    renderDialog();
    await chooseFile();
    await userEvent.click(startButton());
    await waitFor(() => expect(StartRunSQLFile).toHaveBeenCalled());

    emit({
      status: "failed",
      progress: { succeeded: 3, failed: 1, bytesRead: 2048, totalBytes: 2048 },
      failure: { number: 4, line: 17, error: "UNIQUE constraint failed: t.id", excerpt: "INSERT INTO t VALUES (1)" },
    });

    expect(screen.getByText("query.runSqlFileFailedAt 4 17")).toBeInTheDocument();
    expect(screen.getByText("UNIQUE constraint failed: t.id")).toBeInTheDocument();
    expect(screen.getByText("INSERT INTO t VALUES (1)")).toBeInTheDocument();
    expect(screen.getByText("query.runSqlFileNotRolledBack 3")).toBeInTheDocument();
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: /query\.runSqlFileStart/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "action.close" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("explains a non-UTF-8 file", async () => {
    renderDialog();
    await chooseFile();
    await userEvent.click(startButton());
    await waitFor(() => expect(StartRunSQLFile).toHaveBeenCalled());

    emit({ status: "error", invalidUtf8Line: 42, error: "script is not UTF-8 encoded" });

    expect(screen.getByText("query.runSqlFileNotUtf8 42")).toBeInTheDocument();
  });

  it("does not lose events that arrive before the run id", async () => {
    vi.mocked(StartRunSQLFile).mockImplementation(async () => {
      emit({ runId: "someone-else", status: "done" });
      emit({
        status: "done",
        progress: { succeeded: 5, failed: 0, bytesRead: 2048, totalBytes: 2048 },
        elapsedMs: 1500,
      });
      return "run-1";
    });
    renderDialog();
    await chooseFile();
    await userEvent.click(startButton());

    expect(await screen.findByText("query.runSqlFileDoneSummary 5 0 1.5")).toBeInTheDocument();
    expect(onFinished).toHaveBeenCalledTimes(1);
  });

  it("reports a run that could not start and lets the user try again", async () => {
    vi.mocked(StartRunSQLFile).mockRejectedValue(new Error("connection refused"));
    renderDialog();
    await chooseFile();
    await userEvent.click(startButton());

    expect(await screen.findByText("query.runSqlFileErrorSummary connection refused")).toBeInTheDocument();
    expect(onFinished).not.toHaveBeenCalled();
    expect(unsubscribe).toHaveBeenCalled();
    expect(startButton()).toBeEnabled();
  });
});
