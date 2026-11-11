import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MongoDBCollectionBrowser } from "../MongoDBCollectionBrowser";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { ListMongoCollections, ListMongoDatabases } from "../../../../wailsjs/go/query/Query";

const TAB = "query-7";

function seed(databases: string[] = []) {
  useTabStore.setState({
    activeTabId: TAB,
    tabs: [
      {
        id: TAB,
        type: "query",
        label: "Mongo",
        meta: { type: "query", assetId: 7, assetName: "Mongo", assetIcon: "", assetType: "mongodb" },
      },
    ],
  });
  useQueryStore.setState({
    mongoStates: {
      [TAB]: {
        databases,
        collections: {},
        expandedDbs: [],
        activeDatabase: null,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
  });
}

describe("MongoDBCollectionBrowser load states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    seed();
  });

  it("shows the failure reason and a retry that reloads, not an empty list, when listing databases fails", async () => {
    vi.mocked(ListMongoDatabases).mockRejectedValueOnce(new Error("auth failed: bad credentials"));
    render(<MongoDBCollectionBrowser tabId={TAB} assetId={7} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("auth failed: bad credentials");
    expect(screen.queryByTestId("async-state-empty")).not.toBeInTheDocument();

    vi.mocked(ListMongoDatabases).mockResolvedValueOnce(JSON.stringify(["admin"]));
    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));
    expect(await screen.findByText("admin")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("shows a failed collection listing under its database, and retry reloads that database", async () => {
    seed(["shop"]);
    vi.mocked(ListMongoCollections).mockRejectedValue(new Error("not authorized on shop"));
    render(<MongoDBCollectionBrowser tabId={TAB} assetId={7} />);

    fireEvent.click(screen.getByText("shop"));
    expect(await screen.findByRole("alert")).toHaveTextContent("not authorized on shop");

    vi.mocked(ListMongoCollections).mockResolvedValue(JSON.stringify(["orders"]));
    fireEvent.click(screen.getByRole("button", { name: "action.retry" }));
    expect(await screen.findByText("orders")).toBeInTheDocument();
    expect(ListMongoCollections).toHaveBeenLastCalledWith(7, "shop");
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });

  it("shows the empty state only after a successful empty listing", async () => {
    vi.mocked(ListMongoDatabases).mockResolvedValueOnce(JSON.stringify([]));
    render(<MongoDBCollectionBrowser tabId={TAB} assetId={7} />);

    expect(await screen.findByTestId("async-state-empty")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
