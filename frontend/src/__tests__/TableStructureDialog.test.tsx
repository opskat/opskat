import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { TableStructureDialog } from "@/components/query/TableStructureDialog";
import { useQueryStore } from "@/stores/queryStore";
import { useTabStore } from "@/stores/tabStore";
import { GetTableMetadata } from "../../wailsjs/go/query/Query";

function setupStores() {
  useTabStore.setState({
    tabs: [
      {
        id: "query-1",
        type: "query",
        label: "db",
        meta: {
          type: "query",
          assetId: 1,
          assetName: "db",
          assetIcon: "",
          assetType: "database",
          driver: "mysql",
        },
      },
    ],
    activeTabId: "query-1",
  });
  useQueryStore.setState({
    dbStates: {
      "query-1": {
        databases: ["appdb"],
        tables: { appdb: ["users"] },
        loadingTables: {},
        expandedDbs: ["appdb"],
        expandedSchemas: {},
        loadingDbs: false,
        innerTabs: [],
        activeInnerTabId: null,
        error: null,
      },
    },
  });
}

describe("TableStructureDialog", () => {
  beforeEach(() => {
    vi.mocked(GetTableMetadata).mockReset();
    setupStores();
  });

  it("marks unique indexes with the translated UNIQUE label", async () => {
    vi.mocked(GetTableMetadata).mockResolvedValue({
      columns: [{ name: "email", type: "varchar(255)", nullable: false, primaryKey: false, comment: "" }],
      indexes: [{ name: "uq_email", columns: ["email"], unique: true }],
      foreignKeys: [],
    } as never);

    render(<TableStructureDialog open onOpenChange={vi.fn()} tabId="query-1" database="appdb" table="users" />);

    expect(await screen.findByText(/query\.uniqueIndexLabel/)).toBeInTheDocument();
  });
});
