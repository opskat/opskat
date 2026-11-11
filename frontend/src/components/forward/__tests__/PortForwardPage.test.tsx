import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { PortForwardPage } from "../PortForwardPage";
import {
  ListForwardConfigs,
  CreateForwardConfig,
  UpdateForwardConfig,
  StartForwardConfig,
  DeleteForwardConfig,
} from "../../../../wailsjs/go/ssh/SSH";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));
vi.mock("@/components/asset/AssetSelect", () => ({
  AssetSelect: ({ onValueChange }: { onValueChange: (v: number) => void }) => (
    <button type="button" data-testid="asset-select" onClick={() => onValueChange(7)}>
      pick
    </button>
  ),
}));

const cfg = (over: Record<string, unknown> = {}) => ({
  id: 1,
  name: "dev",
  assetId: 7,
  status: "stopped",
  rules: [
    {
      id: 1,
      type: "local",
      localHost: "127.0.0.1",
      localPort: 8080,
      remoteHost: "db",
      remotePort: 5432,
      status: "stopped",
    },
    {
      id: 2,
      type: "dynamic",
      localHost: "127.0.0.1",
      localPort: 1080,
      remoteHost: "",
      remotePort: 0,
      status: "stopped",
    },
  ],
  ...over,
});

const mockList = (v: unknown) => vi.mocked(ListForwardConfigs).mockResolvedValue(v as never);

describe("PortForwardPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockList([cfg()]);
    vi.mocked(StartForwardConfig).mockResolvedValue(undefined as never);
  });

  it("keeps dynamic rules when saving an edited config", async () => {
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByTitle("forward.edit"));
    fireEvent.click(screen.getByText("forward.save"));
    await waitFor(() => expect(UpdateForwardConfig).toHaveBeenCalled());
    const rules: Parameters<typeof UpdateForwardConfig>[3] = vi.mocked(UpdateForwardConfig).mock.calls[0][3];
    expect(rules.map((r) => [r.type, r.localPort, r.remotePort])).toEqual([
      ["local", 8080, 5432],
      ["dynamic", 1080, 0],
    ]);
  });

  it("reports an invalid rule inline and neither saves nor drops it", async () => {
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByText("forward.create"));
    fireEvent.change(screen.getByPlaceholderText("forward.namePlaceholder"), { target: { value: "x" } });
    fireEvent.click(screen.getAllByTestId("asset-select").slice(-1)[0]);
    fireEvent.change(screen.getAllByPlaceholderText("forward.port")[0], { target: { value: "70000" } });
    fireEvent.click(screen.getByText("forward.save"));
    expect(await screen.findByText("forward.ruleError.localPort")).toBeInTheDocument();
    expect(CreateForwardConfig).not.toHaveBeenCalled();
    expect(screen.getByPlaceholderText("forward.namePlaceholder")).toBeInTheDocument();
  });

  it("requires remote host and port for local forwards", async () => {
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByText("forward.create"));
    fireEvent.change(screen.getByPlaceholderText("forward.namePlaceholder"), { target: { value: "x" } });
    fireEvent.click(screen.getAllByTestId("asset-select").slice(-1)[0]);
    fireEvent.change(screen.getAllByPlaceholderText("forward.port")[0], { target: { value: "8080" } });
    fireEvent.click(screen.getByText("forward.save"));
    expect(await screen.findByText("forward.ruleError.remote")).toBeInTheDocument();
    expect(CreateForwardConfig).not.toHaveBeenCalled();
  });

  it("shows the cause when saving fails and keeps the dialog open", async () => {
    vi.mocked(UpdateForwardConfig).mockRejectedValue(new Error("port in use"));
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByTitle("forward.edit"));
    fireEvent.click(screen.getByText("forward.save"));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("port in use")));
    expect(screen.getByPlaceholderText("forward.namePlaceholder")).toBeInTheDocument();
  });

  it("ignores a second save click while saving", async () => {
    let resolve!: () => void;
    vi.mocked(UpdateForwardConfig).mockReturnValue(new Promise<void>((r) => (resolve = r)) as never);
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByTitle("forward.edit"));
    const save = screen.getByText("forward.save");
    fireEvent.click(save);
    fireEvent.click(save);
    expect(UpdateForwardConfig).toHaveBeenCalledTimes(1);
    resolve();
    await waitFor(() => expect(screen.queryByPlaceholderText("forward.namePlaceholder")).not.toBeInTheDocument());
  });

  it("shows the cause when start fails", async () => {
    vi.mocked(StartForwardConfig).mockRejectedValue(new Error("ssh down"));
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByTitle("forward.start"));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("ssh down")));
  });

  it("shows each rule's cause when start succeeds but rules fail to listen", async () => {
    render(<PortForwardPage />);
    const start = await screen.findByTitle("forward.start");
    const started = cfg({ status: "partial" });
    started.rules[0] = { ...started.rules[0], status: "error", error: "bind: address already in use" } as never;
    started.rules[1] = { ...started.rules[1], status: "running" } as never;
    mockList([started]);
    fireEvent.click(start);
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("bind: address already in use"))
    );
    expect(vi.mocked(toast.error).mock.calls[0][0]).toContain("8080");
  });

  it("does not report anything when every rule starts", async () => {
    render(<PortForwardPage />);
    const start = await screen.findByTitle("forward.start");
    const started = cfg({ status: "running" });
    started.rules = started.rules.map((r) => ({ ...r, status: "running" }));
    mockList([started]);
    fireEvent.click(start);
    await waitFor(() => expect(StartForwardConfig).toHaveBeenCalled());
    expect(await screen.findByText("forward.running")).toBeInTheDocument();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("keeps the list on screen while it reloads after an action", async () => {
    render(<PortForwardPage />);
    const start = await screen.findByTitle("forward.start");
    vi.mocked(ListForwardConfigs).mockReturnValue(new Promise(() => {}) as never);
    fireEvent.click(start);
    await waitFor(() => expect(ListForwardConfigs).toHaveBeenCalledTimes(2));
    expect(screen.getByText("dev")).toBeInTheDocument();
  });

  it("keeps an inline rule error on its own rule after another rule is removed", async () => {
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByText("forward.create"));
    fireEvent.change(screen.getByPlaceholderText("forward.namePlaceholder"), { target: { value: "x" } });
    fireEvent.click(screen.getAllByTestId("asset-select").slice(-1)[0]);
    fireEvent.click(screen.getByText("forward.addRule"));
    const ports = () => screen.getAllByPlaceholderText("forward.port");
    // rule 0 valid, rule 1 has a bad local port
    fireEvent.change(ports()[0], { target: { value: "8080" } });
    fireEvent.change(ports()[1], { target: { value: "5432" } });
    fireEvent.change(ports()[2], { target: { value: "70000" } });
    fireEvent.click(screen.getByText("forward.save"));
    expect(await screen.findByText("forward.ruleError.localPort")).toBeInTheDocument();

    fireEvent.click(screen.getAllByTitle("forward.removeRule")[0]);
    // the remaining rule is the invalid one and still shows its error
    expect(ports()[0]).toHaveValue("70000");
    expect(screen.getByText("forward.ruleError.localPort")).toBeInTheDocument();
  });

  it("shows the cause when delete fails", async () => {
    vi.mocked(DeleteForwardConfig).mockRejectedValue(new Error("locked"));
    render(<PortForwardPage />);
    fireEvent.click(await screen.findByTitle("forward.delete"));
    fireEvent.click(await screen.findByText("action.delete"));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("locked")));
  });

  it("shows a failed list load with retry instead of the empty state", async () => {
    vi.mocked(ListForwardConfigs).mockRejectedValueOnce(new Error("db gone"));
    render(<PortForwardPage />);
    expect(await screen.findByText("db gone")).toBeInTheDocument();
    expect(screen.queryByText("forward.empty")).not.toBeInTheDocument();
    mockList([]);
    fireEvent.click(screen.getByTestId("async-state-retry"));
    expect(await screen.findByText("forward.empty")).toBeInTheDocument();
  });

  it("labels rule types with full names in the list", async () => {
    render(<PortForwardPage />);
    expect(await screen.findByText(/forward\.type\.dynamic/)).toBeInTheDocument();
    expect(screen.getByText(/forward\.type\.local/)).toBeInTheDocument();
  });
});
