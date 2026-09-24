import { describe, it, expect, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { injectExtensionAPI } from "../inject";
import { CodeEditor } from "@/components/CodeEditor";
import { JsonTreeView } from "@/components/JsonTreeView";
import { QueryResultTable } from "@/components/query/QueryResultTable";
import type { ExtAPI } from "../types";

const fakeApi: ExtAPI = {
  callTool: async () => undefined,
  executeAction: async () => undefined,
};

describe("injectExtensionAPI — @opskat/host-ui", () => {
  afterEach(() => {
    delete (window as unknown as { __OPSKAT_EXT__?: unknown }).__OPSKAT_EXT__;
  });

  it("exposes hostUI with a version alongside the existing api/ui/i18n surface", () => {
    injectExtensionAPI(fakeApi);

    const injected = window.__OPSKAT_EXT__;
    expect(injected).toBeDefined();
    expect(injected!.hostUI).toBeDefined();
    expect(typeof injected!.hostUI.version).toBe("string");
    expect(injected!.hostUI.version.length).toBeGreaterThan(0);
  });

  it("hostUI.CodeEditor is the exact same component the host uses (so it stays on-theme and supports json)", () => {
    injectExtensionAPI(fakeApi);
    expect(window.__OPSKAT_EXT__!.hostUI.CodeEditor).toBe(CodeEditor);
  });

  it("hostUI.QueryResultTable is the exact same component the host uses (so sorting/copy behavior is identical)", () => {
    injectExtensionAPI(fakeApi);
    expect(window.__OPSKAT_EXT__!.hostUI.QueryResultTable).toBe(QueryResultTable);
  });

  it("hostUI.JsonTreeView is the exact same component the host uses and renders", () => {
    injectExtensionAPI(fakeApi);
    const HostJsonTreeView = window.__OPSKAT_EXT__!.hostUI.JsonTreeView;
    expect(HostJsonTreeView).toBe(JsonTreeView);

    render(<HostJsonTreeView data={{ note: "hello" }} />);
    expect(screen.getByText("note:")).toBeInTheDocument();
  });
});
