import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { DatabaseConfigSection } from "@/components/asset/DatabaseConfigSection";
import { refreshDatabaseDrivers } from "@/lib/dbDrivers";
import { ListDatabaseDrivers } from "../../wailsjs/go/query/Query";
import { BUILTIN_DRIVERS } from "./dbDriverFixtures";

beforeEach(() => {
  refreshDatabaseDrivers();
  vi.mocked(ListDatabaseDrivers).mockResolvedValue(BUILTIN_DRIVERS);
});

const ctx = { isEdit: false, encryptPassword: vi.fn() };

describe("DatabaseConfigSection tabs", () => {
  it("renders connection / tunnel / tls / advanced tabs", async () => {
    render(<DatabaseConfigSection ctx={ctx} onValidityChange={vi.fn()} onIconChange={vi.fn()} />);
    expect(await screen.findByTestId("config-tab-connection")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tunnel")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-tls")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
  });
});
