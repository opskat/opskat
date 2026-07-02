import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { ConnectionSection } from "@/components/settings/ConnectionSection";
import { GetSSHConnectionSettings, SetSSHConnectionSettings } from "../../wailsjs/go/system/System";

// App 全局包了 TooltipProvider(App.tsx);独立渲染 section 时需自备,否则 keepalive 标签旁的 ⓘ Tooltip 抛错。
const renderSection = () =>
  render(
    <TooltipProvider>
      <ConnectionSection />
    </TooltipProvider>
  );

describe("ConnectionSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetSSHConnectionSettings).mockResolvedValue({ keepAliveIntervalSeconds: 60 });
    vi.mocked(SetSSHConnectionSettings).mockResolvedValue();
  });

  it("loads and displays the current keepalive interval", async () => {
    renderSection();
    expect(await screen.findByDisplayValue("60")).toBeInTheDocument();
    expect(GetSSHConnectionSettings).toHaveBeenCalledTimes(1);
  });

  it("persists an edited keepalive interval on blur", async () => {
    renderSection();
    const intervalInput = await screen.findByDisplayValue("60");

    await userEvent.clear(intervalInput);
    await userEvent.type(intervalInput, "120");
    await userEvent.tab();

    expect(SetSSHConnectionSettings).toHaveBeenCalledWith({ keepAliveIntervalSeconds: 120 });
  });

  it("rejects an out-of-range value without calling the backend", async () => {
    renderSection();
    const intervalInput = await screen.findByDisplayValue("60");

    await userEvent.clear(intervalInput);
    await userEvent.type(intervalInput, "1");
    await userEvent.tab();

    expect(SetSSHConnectionSettings).not.toHaveBeenCalled();
  });
});
