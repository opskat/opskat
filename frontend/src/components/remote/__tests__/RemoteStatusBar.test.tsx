import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RemoteStatusBar } from "@/components/remote/RemoteStatusBar";

describe("RemoteStatusBar", () => {
  it("shows resolution, fit badge and uptime when connected", () => {
    render(<RemoteStatusBar width={1920} height={1080} showFit fitLabel="Auto-fit" connected elapsed={3661} />);
    expect(screen.getByText("1920 × 1080")).toBeInTheDocument();
    expect(screen.getByText("Auto-fit")).toBeInTheDocument();
    expect(screen.getByText("01:01:01")).toBeInTheDocument();
  });
  it("shows a dash when resolution is unknown and hides fit badge/uptime", () => {
    render(<RemoteStatusBar width={0} height={0} showFit={false} fitLabel="Auto-fit" connected={false} elapsed={0} />);
    expect(screen.getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("Auto-fit")).not.toBeInTheDocument();
    expect(screen.queryByText("00:00:00")).not.toBeInTheDocument();
  });
});
