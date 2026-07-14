import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RemoteStatusPill } from "@/components/remote/RemoteStatusPill";

describe("RemoteStatusPill", () => {
  it("renders label and status-specific color classes", () => {
    render(<RemoteStatusPill status="connected" label="Connected" testid="x-status" />);
    const pill = screen.getByTestId("x-status");
    expect(pill).toHaveTextContent("Connected");
    expect(pill.className).toContain("text-success");
  });
  it("maps error status to destructive", () => {
    render(<RemoteStatusPill status="error" label="Error" testid="x-status" />);
    expect(screen.getByTestId("x-status").className).toContain("text-destructive");
  });
});
