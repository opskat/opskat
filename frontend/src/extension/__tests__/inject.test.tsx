import { describe, it, expect, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { injectExtensionAPI } from "../inject";
import type { ExtAPI } from "../types";

const fakeApi: ExtAPI = {
  callTool: async () => undefined,
  executeAction: async () => undefined,
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
    expect(hostUI.version).toBe("1.0");
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
});
