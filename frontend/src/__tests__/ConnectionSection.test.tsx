import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ConnectionSection } from "@/components/settings/ConnectionSection";
import { GetSSHConnectionSettings, SetSSHConnectionSettings } from "../../wailsjs/go/system/System";

describe("ConnectionSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetSSHConnectionSettings).mockResolvedValue({
      tcpNoDelay: true,
      tcpKeepAlive: true,
      keepAliveIntervalSeconds: 60,
      connectTimeoutSeconds: 30,
    });
    vi.mocked(SetSSHConnectionSettings).mockResolvedValue();
  });

  it("loads and displays the current settings", async () => {
    render(<ConnectionSection />);
    expect(await screen.findByDisplayValue("60")).toBeInTheDocument();
    expect(screen.getByDisplayValue("30")).toBeInTheDocument();
    expect(GetSSHConnectionSettings).toHaveBeenCalledTimes(1);
  });

  it("persists a toggled TCP_NODELAY switch", async () => {
    render(<ConnectionSection />);
    await screen.findByDisplayValue("60");

    const switches = screen.getAllByRole("switch");
    await userEvent.click(switches[0]);

    expect(SetSSHConnectionSettings).toHaveBeenCalledWith(
      expect.objectContaining({ tcpNoDelay: false, tcpKeepAlive: true })
    );
  });

  it("persists an edited keepalive interval on blur", async () => {
    render(<ConnectionSection />);
    const intervalInput = await screen.findByDisplayValue("60");

    await userEvent.clear(intervalInput);
    await userEvent.type(intervalInput, "120");
    await userEvent.tab();

    expect(SetSSHConnectionSettings).toHaveBeenCalledWith(expect.objectContaining({ keepAliveIntervalSeconds: 120 }));
  });

  it("rejects an out-of-range value without calling the backend", async () => {
    render(<ConnectionSection />);
    const intervalInput = await screen.findByDisplayValue("60");

    await userEvent.clear(intervalInput);
    await userEvent.type(intervalInput, "1");
    await userEvent.tab();

    expect(SetSSHConnectionSettings).not.toHaveBeenCalled();
  });
});
