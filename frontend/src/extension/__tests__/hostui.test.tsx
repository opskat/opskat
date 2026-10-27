import { describe, it, expect, vi, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { CodeEditor, DataTable, JsonTreeView } from "../hostui";

// The real Monaco is not loadable in jsdom: stand in for @monaco-editor/react and
// hand out a fake editor + monaco so the wrapper's onMount contract is observable.
const fakeEditor = { getModel: () => null, addAction: vi.fn() };
const fakeMonaco = { languages: { register: vi.fn() }, editor: {} };
const editorProps = vi.hoisted(() => ({ last: null as null | Record<string, unknown> }));

vi.mock("@monaco-editor/react", async () => {
  const React = await import("react");
  const FakeEditor = (
    props: { onMount?: (e: unknown, m: unknown) => void; value?: string } & Record<string, unknown>
  ) => {
    editorProps.last = props;
    React.useEffect(() => props.onMount?.(fakeEditor, fakeMonaco), []); // eslint-disable-line react-hooks/exhaustive-deps
    return <div data-testid="monaco">{props.value}</div>;
  };
  return { default: FakeEditor };
});
vi.mock("@/lib/monaco-setup", () => ({ setupMonaco: () => {} }));

afterEach(cleanup);

describe("hostUI.CodeEditor", () => {
  it("hands the editor and the monaco instance to onMount", async () => {
    const onMount = vi.fn();
    render(<CodeEditor value="GET /" language="json" onMount={onMount} />);
    await waitFor(() => expect(onMount).toHaveBeenCalledTimes(1));
    const [editor, monaco] = onMount.mock.calls[0];
    expect(editor).toBe(fakeEditor);
    expect(monaco.languages).toBeDefined();
    expect(screen.getByTestId("monaco")).toHaveTextContent("GET /");
  });

  it("forwards fontSize, readOnly and options into the editor options", async () => {
    render(<CodeEditor value="" fontSize={15} readOnly options={{ lineNumbers: "off" }} />);
    await waitFor(() => expect(editorProps.last).not.toBeNull());
    expect(editorProps.last!.options).toMatchObject({ fontSize: 15, readOnly: true, lineNumbers: "off" });
  });

  it("reports edits through onChange", async () => {
    const onChange = vi.fn();
    render(<CodeEditor value="a" onChange={onChange} />);
    await waitFor(() => expect(editorProps.last).not.toBeNull());
    (editorProps.last!.onChange as (v?: string) => void)("ab");
    expect(onChange).toHaveBeenCalledWith("ab");
  });
});

describe("hostUI.DataTable", () => {
  const rows = [{ name: "alpha" }, { name: "beta" }];

  it("forwards cell and row selection to the callbacks", () => {
    const onSelectedCellChange = vi.fn();
    const onSelectedRowsChange = vi.fn();
    render(
      <DataTable
        columns={["name"]}
        rows={rows}
        showRowNumber
        onSelectedCellChange={onSelectedCellChange}
        onSelectedRowsChange={onSelectedRowsChange}
      />
    );
    fireEvent.click(document.querySelector('[data-cell-key="1:name"]')!);
    expect(onSelectedCellChange).toHaveBeenLastCalledWith({ rowIdx: 1, col: "name" });

    fireEvent.click(screen.getByText("1"));
    expect(onSelectedRowsChange).toHaveBeenLastCalledWith([0]);
  });

  it("starts the row numbers at rowNumberOffset", () => {
    render(<DataTable columns={["name"]} rows={rows} showRowNumber rowNumberOffset={40} />);
    expect(screen.getByText("41")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("sorts through onSortChange when the page controls sorting", () => {
    const onSortChange = vi.fn();
    render(<DataTable columns={["name"]} rows={rows} sortColumn={null} sortDir={null} onSortChange={onSortChange} />);
    fireEvent.click(screen.getByTitle("query.columnActions:name"));
    fireEvent.click(screen.getByText("query.sortAsc"));
    expect(onSortChange).toHaveBeenCalledWith("name", "asc");
  });
});

describe("hostUI.JsonTreeView", () => {
  it("renders a JSON value read-only", () => {
    render(<JsonTreeView data={{ note: "hello" }} />);
    expect(screen.getByText("note:")).toBeInTheDocument();
  });
});
