import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { RemoteConnectionOverlay } from "@/components/remote/RemoteConnectionOverlay";

const labels = {
  connecting: "Connecting...",
  error: "Failed",
  closed: "Disconnected",
  reconnect: "Reconnect",
  edit: "Edit",
};

describe("RemoteConnectionOverlay", () => {
  it("returns null when connected", () => {
    const { container } = render(
      <RemoteConnectionOverlay status="connected" error="" host="h" port={1} labels={labels} onReconnect={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });
  it("shows host:port while connecting", () => {
    render(
      <RemoteConnectionOverlay
        status="connecting"
        error=""
        host="10.0.0.1"
        port={5901}
        labels={labels}
        onReconnect={() => {}}
      />
    );
    expect(screen.getByText("Connecting...")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.1:5901")).toBeInTheDocument();
  });
  it("shows error + reconnect/edit with testids and fires callbacks", () => {
    const onReconnect = vi.fn();
    const onEdit = vi.fn();
    render(
      <RemoteConnectionOverlay
        status="error"
        error="boom"
        host="h"
        port={1}
        labels={labels}
        onReconnect={onReconnect}
        onEdit={onEdit}
        reconnectTestId="x-reconnect"
        editTestId="x-edit"
      />
    );
    expect(screen.getByText("boom")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("x-reconnect"));
    fireEvent.click(screen.getByTestId("x-edit"));
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledTimes(1);
  });
});
