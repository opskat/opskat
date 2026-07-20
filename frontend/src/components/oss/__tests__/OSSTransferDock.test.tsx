import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { OSSTransferDock } from "../OSSTransferDock";
import type { OssTransfer } from "@/stores/ossTransferStore";

function tx(over: Partial<OssTransfer>): OssTransfer {
  return {
    transferId: "t1",
    tabId: "q",
    direction: "upload",
    name: "a.txt",
    bytesDone: 50,
    bytesTotal: 100,
    speed: 1024,
    status: "active",
    ...over,
  };
}

describe("OSSTransferDock", () => {
  it("renders a row with name, speed and percentage; the button cancels an active row", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(<OSSTransferDock transfers={[tx({})]} onCancel={onCancel} onClear={onClear} onClearCompleted={vi.fn()} />);
    expect(screen.getByText("a.txt")).toBeInTheDocument();
    expect(screen.getByText("1.0 KB/s")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("oss-transfer-action-t1"));
    expect(onCancel).toHaveBeenCalledWith("t1");
    expect(onClear).not.toHaveBeenCalled();
  });

  it("the row button clears a finished row instead of cancelling", () => {
    const onCancel = vi.fn();
    const onClear = vi.fn();
    render(
      <OSSTransferDock
        transfers={[tx({ transferId: "t2", status: "done" })]}
        onCancel={onCancel}
        onClear={onClear}
        onClearCompleted={vi.fn()}
      />
    );
    fireEvent.click(screen.getByTestId("oss-transfer-action-t2"));
    expect(onClear).toHaveBeenCalledWith("t2");
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("clear-completed header button fires onClearCompleted", () => {
    const onClearCompleted = vi.fn();
    render(
      <OSSTransferDock
        transfers={[tx({ status: "done" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={onClearCompleted}
      />
    );
    fireEvent.click(screen.getByTestId("oss-transfer-clear-completed"));
    expect(onClearCompleted).toHaveBeenCalledTimes(1);
  });

  it("exposes transfer progress and icon actions to assistive technology", () => {
    render(
      <OSSTransferDock
        transfers={[tx({ bytesDone: 25, bytesTotal: 100 })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
    expect(screen.getByTestId("oss-transfer-action-t1")).toHaveAttribute("aria-label", "oss.transfer.cancel");
    expect(screen.getByTestId("oss-transfer-status-t1")).toHaveClass("text-info");
  });

  it("disables clear completed when every transfer is still active", () => {
    const { rerender } = render(
      <OSSTransferDock transfers={[tx({})]} onCancel={vi.fn()} onClear={vi.fn()} onClearCompleted={vi.fn()} />
    );
    expect(screen.getByTestId("oss-transfer-clear-completed")).toBeDisabled();
    rerender(
      <OSSTransferDock
        transfers={[tx({ status: "done" })]}
        onCancel={vi.fn()}
        onClear={vi.fn()}
        onClearCompleted={vi.fn()}
      />
    );
    expect(screen.getByTestId("oss-transfer-clear-completed")).toBeEnabled();
  });
});
