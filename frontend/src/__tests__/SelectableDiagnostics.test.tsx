import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { SystemStatusSection } from "@/components/settings/SystemStatusSection";
import { GetSystemStatus } from "../../wailsjs/go/system/System";
import type { ComponentType } from "react";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
  withTranslation: () => (Component: ComponentType<Record<string, unknown>>) => (props: Record<string, unknown>) => (
    <Component {...props} t={(key: string) => key} i18n={{}} tReady />
  ),
}));

function CrashingChild(): never {
  throw new Error("renderer diagnostics failed");
}

describe("selectable diagnostic content", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("allows users to select an application crash message", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    render(
      <ErrorBoundary>
        <CrashingChild />
      </ErrorBoundary>
    );

    expect(screen.getByText("renderer diagnostics failed")).toHaveClass("select-text");
    consoleError.mockRestore();
  });

  it("allows users to select system status summaries and expanded details", async () => {
    vi.mocked(GetSystemStatus).mockResolvedValue([
      {
        level: "error",
        source: "sshpool",
        message: "connection health check failed",
        detail: "dial tcp 192.0.2.1:22: timeout",
        time: "2026-08-28T12:00:00Z",
      },
    ] as never);
    render(<SystemStatusSection />);

    const summary = await screen.findByText("connection health check failed");
    expect(summary.closest("div")?.parentElement).toHaveClass("select-text");
    await userEvent.click(summary);
    await waitFor(() => expect(screen.getByText("dial tcp 192.0.2.1:22: timeout")).toBeVisible());
    expect(screen.getByText("dial tcp 192.0.2.1:22: timeout")).toHaveClass("select-text");
  });
});
