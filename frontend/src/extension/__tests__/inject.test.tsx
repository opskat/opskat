import { describe, it, expect, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { injectExtensionAPI } from "../inject";
import type { ExtAPI } from "../types";

const fakeApi: ExtAPI = {
  callTool: async () => undefined,
};

// @opskat/host-ui is what an extension page reaches for on window.__OPSKAT_EXT__:
// the page renders these components itself, so the contract is that they are
// there and render.
describe("injectExtensionAPI — @opskat/host-ui", () => {
  afterEach(() => {
    delete (window as unknown as { __OPSKAT_EXT__?: unknown }).__OPSKAT_EXT__;
  });

  it("gives a page a versioned hostUI whose components it can render", () => {
    injectExtensionAPI(fakeApi);

    const hostUI = window.__OPSKAT_EXT__!.hostUI;
    expect(hostUI.version).toMatch(/^\d+\.\d+$/);
    expect(hostUI.CodeEditor).toBeDefined();

    render(<hostUI.JsonTreeView data={{ note: "hello" }} />);
    expect(screen.getByText("note:")).toBeInTheDocument();
  });

  it("renders a DataTable and no longer exposes the host's QueryResultTable", () => {
    injectExtensionAPI(fakeApi);

    const hostUI = window.__OPSKAT_EXT__!.hostUI;
    expect("QueryResultTable" in hostUI).toBe(false);

    render(<hostUI.DataTable columns={["key"]} rows={[{ key: "todo.md" }]} />);
    expect(screen.getByText("todo.md")).toBeInTheDocument();
  });

  it("renders the notebook page's note list as a DataTable with key / size / updatedAt columns", async () => {
    const notes = [
      { key: "todo.md", size: 12, updatedAt: "2026-10-27" },
      { key: "ideas.md", size: 340, updatedAt: "2026-10-26" },
    ];
    injectExtensionAPI({ ...fakeApi, callTool: async () => ({ notes }) });
    // @ts-expect-error the page is a plain ES module with no type declarations
    const { NotebookPage } = await import("../../../../extensions/notebook/frontend/page.js");

    render(<NotebookPage assetId={1} />);

    const table = await screen.findByTestId("notebook-table");
    await waitFor(() => expect(table).toHaveTextContent("todo.md"));
    for (const col of ["key", "size", "updatedAt"]) expect(table).toHaveTextContent(col);
    expect(table).toHaveTextContent("ideas.md");
    expect(table).toHaveTextContent("340");
  });
});
