import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { DatabaseTree } from "../components/query/DatabaseTree";
import { useQueryStore } from "../stores/queryStore";
import { useTabStore } from "../stores/tabStore";
import { ListDatabases } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS, provideDrivers } from "./dbDriverFixtures";

vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({ value }: { value: string }) => <pre data-testid="code-editor">{value}</pre>,
}));

function setup() {
  provideDrivers(BUILTIN_DRIVERS);
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "test",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "test-db",
          assetIcon: "",
          assetType: "database",
          driver: "mysql",
          defaultDatabase: "",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: [],
        tables: {},
        loadingTables: {},
        expandedDbs: [],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
    redisStates: {},
    mongoStates: {},
  });
}

describe("DatabaseTree — database list load state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setup();
  });

  it("shows the failure reason with a retry instead of an empty list, and retry reloads", async () => {
    vi.mocked(ListDatabases).mockRejectedValueOnce(new Error("dial tcp: connection refused"));
    await act(async () => {
      render(<DatabaseTree tabId="query-1" />);
    });

    await screen.findByTestId("async-state-error");
    expect(screen.getByText("dial tcp: connection refused")).toBeInTheDocument();
    expect(screen.queryByTestId("async-state-empty")).not.toBeInTheDocument();

    vi.mocked(ListDatabases).mockResolvedValueOnce(["appdb"]);
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    await waitFor(() => expect(screen.getByText("appdb")).toBeInTheDocument());
    expect(screen.queryByTestId("async-state-error")).not.toBeInTheDocument();
  });

  it("shows the empty state only after a successful empty load", async () => {
    vi.mocked(ListDatabases).mockResolvedValueOnce([]);
    await act(async () => {
      render(<DatabaseTree tabId="query-1" />);
    });
    expect(await screen.findByTestId("async-state-empty")).toBeInTheDocument();
    expect(screen.queryByTestId("async-state-error")).not.toBeInTheDocument();
  });
});
